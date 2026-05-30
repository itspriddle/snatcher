package commands

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// runReannounce executes the reannounce command with args, returning stdout,
// stderr, and the Execute error. The command's output is a *bytes.Buffer, so the
// piped/plaintext path is exercised.
func runReannounce(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewReannounceCmd()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

func TestReannounceByID(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runReannounce(t, "--config", cfgPath, "1")
	require.NoError(t, err)

	assert.Equal(t, "Reannounced 1 torrent: ubuntu-24.04.iso\n", out)
}

func TestReannounceByHash(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runReannounce(t, "--config", cfgPath, fullHashTwo)
	require.NoError(t, err)

	assert.Equal(t, "Reannounced 1 torrent: debian.iso\n", out)
}

func TestReannounceAll(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runReannounce(t, "--config", cfgPath, "--all")
	require.NoError(t, err)

	assert.Equal(t, "Reannounced all 2 torrents.\n", out)
}

func TestReannounceNoSelectionErrors(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	_, _, err := runReannounce(t, "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "specify torrent ids or hashes, or use --all")
}

func TestReannounceSelectorsAndAllErrors(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	_, _, err := runReannounce(t, "--config", cfgPath, "1", "--all")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot combine torrent selectors with --all")
}

func TestReannounceNoMatchErrors(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	_, _, err := runReannounce(t, "--config", cfgPath, "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no torrents matched: 99")
}

func TestReannouncePartialMatchSurfacesUnmatched(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runReannounce(t, "--config", cfgPath, "1", "99")
	require.NoError(t, err)
	assert.Contains(t, out, "Reannounced 1 torrent: ubuntu-24.04.iso")
	assert.Contains(t, out, "No torrents matched: 99")
}

func TestReannounceJSON(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runReannounce(t, "--config", cfgPath, "--json", "1")
	require.NoError(t, err)

	var got struct {
		Action   string `json:"action"`
		Count    int    `json:"count"`
		Torrents []struct {
			ID   int64  `json:"id"`
			Hash string `json:"hash"`
			Name string `json:"name"`
		} `json:"torrents"`
		Now        *bool    `json:"now"`
		DeleteData *bool    `json:"delete_data"`
		Unmatched  []string `json:"unmatched"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	assert.Equal(t, "reannounce", got.Action)
	assert.Equal(t, 1, got.Count)
	require.Len(t, got.Torrents, 1)
	assert.Equal(t, int64(1), got.Torrents[0].ID)
	assert.Equal(t, fullHashOne, got.Torrents[0].Hash)
	assert.Equal(t, "ubuntu-24.04.iso", got.Torrents[0].Name)
	assert.Nil(t, got.Now, "reannounce omits the now field")
	assert.Nil(t, got.DeleteData, "reannounce omits the delete_data field")
	assert.Empty(t, got.Unmatched, "no unmatched tokens when every selector matched")
}

func TestReannounceJSONIncludesUnmatched(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runReannounce(t, "--config", cfgPath, "--json", "1", "99")
	require.NoError(t, err)

	var got struct {
		Count     int      `json:"count"`
		Unmatched []string `json:"unmatched"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, 1, got.Count)
	assert.Equal(t, []string{"99"}, got.Unmatched)
}

func TestReannounceJQ(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runReannounce(t, "--config", cfgPath, "--jq", ".torrents[].name", "--all")
	require.NoError(t, err)

	assert.Equal(t, `"ubuntu-24.04.iso"`+"\n"+`"debian.iso"`, strings.TrimSpace(out))
}

func TestReannounceNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runReannounce(t, "--config", cfgPath, "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// TestReannounceSanitizesNameInHumanButNotJSON guards the security boundary: a
// torrent name carrying an ANSI escape is neutralized in the human confirmation
// but preserved verbatim in the structured --json contract.
func TestReannounceSanitizesNameInHumanButNotJSON(t *testing.T) {
	const body = `{
		"arguments": {"torrents": [
			{"id": 1, "hashString": "0123456789abcdef0123456789abcdef01234567", "name": "\u001b[2Jmalicious", "status": 0}
		]},
		"result": "success",
		"tag": 1
	}`

	cfgPath, closeSrv := mutationConfigPath(t, body)
	defer closeSrv()

	human, _, err := runReannounce(t, "--config", cfgPath, "1")
	require.NoError(t, err)
	assert.NotContains(t, human, "\x1b", "raw ESC must not reach the human confirmation")
	assert.Contains(t, human, "[2Jmalicious", "visible text survives sanitization")

	jsonOut, _, err := runReannounce(t, "--config", cfgPath, "--json", "1")
	require.NoError(t, err)
	assert.Contains(t, jsonOut, "\\u001b", "the raw escape is preserved in the JSON contract")
}
