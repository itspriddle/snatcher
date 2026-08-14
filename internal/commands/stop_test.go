package commands

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
)

// fullHashOne is a valid 40-character SHA1 info-hash, used to exercise selection
// by hash (the bespokeListResponse fixture uses short, non-selectable hashes).
const (
	fullHashOne = "0123456789abcdef0123456789abcdef01234567"
	fullHashTwo = "fedcba9876543210fedcba9876543210fedcba98"
)

// mutationListResponse is a torrent-get payload with two torrents carrying full
// 40-char hashes, used by the start/stop confirmation lookup.
const mutationListResponse = `{
	"arguments": {"torrents": [
		{"id": 1, "hashString": "0123456789abcdef0123456789abcdef01234567", "name": "ubuntu-24.04.iso", "status": 0},
		{"id": 2, "hashString": "fedcba9876543210fedcba9876543210fedcba98", "name": "debian.iso", "status": 0}
	]},
	"result": "success",
	"tag": 1
}`

// runStop executes the stop command with args, returning stdout, stderr, and the
// Execute error. The command's output is a *bytes.Buffer, so the piped/plaintext
// path is exercised.
func runStop(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewStopCmd()
	AddGlobalFlags(cmd)

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// mutationConfigPath stands up a transmission stub serving the given torrent-get
// payload (for the confirmation lookup) and a default-success answer for every
// other method (the action itself), then writes a config pointing at it.
func mutationConfigPath(t *testing.T, listBody string) (string, func()) {
	t.Helper()

	keyring.MockInit()

	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // bespoke path
		responses: map[string]string{
			"torrent-get": listBody,
		},
	}.handler())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	return cfgPath, srv.Close
}

func TestStopByID(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runStop(t, "--config", cfgPath, "1")
	require.NoError(t, err)

	assert.Equal(t, "Stopped 1 torrent: ubuntu-24.04.iso\n", out)
}

func TestStopMultipleByID(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runStop(t, "--config", cfgPath, "1", "2")
	require.NoError(t, err)

	assert.Equal(t, "Stopped 2 torrents: ubuntu-24.04.iso, debian.iso\n", out)
}

func TestStopByHash(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runStop(t, "--config", cfgPath, fullHashTwo)
	require.NoError(t, err)

	assert.Equal(t, "Stopped 1 torrent: debian.iso\n", out)
}

func TestStopAll(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runStop(t, "--config", cfgPath, "--all")
	require.NoError(t, err)

	assert.Equal(t, "Stopped all 2 torrents.\n", out)
}

func TestStopNoSelectionErrors(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	_, _, err := runStop(t, "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "specify torrent ids or hashes, or use --all")
}

func TestStopSelectorsAndAllErrors(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	_, _, err := runStop(t, "--config", cfgPath, "1", "--all")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot combine torrent selectors with --all")
}

func TestStopNoMatchErrors(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	_, _, err := runStop(t, "--config", cfgPath, "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no torrents matched: 99")
}

func TestStopPartialMatchSurfacesUnmatched(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	// One id matches, one does not: the command proceeds and reports the
	// unmatched token.
	out, _, err := runStop(t, "--config", cfgPath, "1", "99")
	require.NoError(t, err)
	assert.Contains(t, out, "Stopped 1 torrent: ubuntu-24.04.iso")
	assert.Contains(t, out, "No torrents matched: 99")
}

func TestStopJSON(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runStop(t, "--config", cfgPath, "--json", "1")
	require.NoError(t, err)

	var got struct {
		Action   string `json:"action"`
		Count    int    `json:"count"`
		Torrents []struct {
			ID   int64  `json:"id"`
			Hash string `json:"hash"`
			Name string `json:"name"`
		} `json:"torrents"`
		Now       *bool    `json:"now"`
		Unmatched []string `json:"unmatched"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	assert.Equal(t, "stop", got.Action)
	assert.Equal(t, 1, got.Count)
	require.Len(t, got.Torrents, 1)
	assert.Equal(t, int64(1), got.Torrents[0].ID)
	assert.Equal(t, fullHashOne, got.Torrents[0].Hash)
	assert.Equal(t, "ubuntu-24.04.iso", got.Torrents[0].Name)
	assert.Nil(t, got.Now, "stop omits the now field")
	assert.Empty(t, got.Unmatched, "no unmatched tokens when every selector matched")
}

func TestStopJSONIncludesUnmatched(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runStop(t, "--config", cfgPath, "--json", "1", "99")
	require.NoError(t, err)

	var got struct {
		Count     int      `json:"count"`
		Unmatched []string `json:"unmatched"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, 1, got.Count)
	assert.Equal(t, []string{"99"}, got.Unmatched)
}

func TestStopJQ(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runStop(t, "--config", cfgPath, "--jq", ".torrents[].name", "--all")
	require.NoError(t, err)

	assert.Equal(t, `"ubuntu-24.04.iso"`+"\n"+`"debian.iso"`, strings.TrimSpace(out))
}

func TestStopNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runStop(t, "--config", cfgPath, "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// TestStopSanitizesNameInHumanButNotJSON guards the security boundary: a torrent
// name carrying an ANSI escape is neutralized in the human confirmation but
// preserved verbatim in the structured --json contract.
func TestStopSanitizesNameInHumanButNotJSON(t *testing.T) {
	const body = `{
		"arguments": {"torrents": [
			{"id": 1, "hashString": "0123456789abcdef0123456789abcdef01234567", "name": "\u001b[2Jmalicious", "status": 0}
		]},
		"result": "success",
		"tag": 1
	}`

	cfgPath, closeSrv := mutationConfigPath(t, body)
	defer closeSrv()

	human, _, err := runStop(t, "--config", cfgPath, "1")
	require.NoError(t, err)
	assert.NotContains(t, human, "\x1b", "raw ESC must not reach the human confirmation")
	assert.Contains(t, human, "[2Jmalicious", "visible text survives sanitization")

	jsonOut, _, err := runStop(t, "--config", cfgPath, "--json", "1")
	require.NoError(t, err)
	assert.Contains(t, jsonOut, "\\u001b", "the raw escape is preserved in the JSON contract")
}
