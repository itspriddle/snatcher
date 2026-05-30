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

// runQueue executes "queue <verb> <args...>" against a fresh NewQueueCmd, with a
// *bytes.Buffer stdout so the piped/plaintext path is exercised.
func runQueue(t *testing.T, verb string, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewQueueCmd()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(append([]string{verb}, args...))

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

func TestQueueTopByID(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runQueue(t, "top", "--config", cfgPath, "1")
	require.NoError(t, err)

	assert.Equal(t, "Moved to top 1 torrent: ubuntu-24.04.iso\n", out)
}

func TestQueueUpByID(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runQueue(t, "up", "--config", cfgPath, "1")
	require.NoError(t, err)

	assert.Equal(t, "Moved up 1 torrent: ubuntu-24.04.iso\n", out)
}

func TestQueueDownConfirmation(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runQueue(t, "down", "--config", cfgPath, "1")
	require.NoError(t, err)

	assert.Equal(t, "Moved down 1 torrent: ubuntu-24.04.iso\n", out)
}

func TestQueueBottomConfirmation(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runQueue(t, "bottom", "--config", cfgPath, "1")
	require.NoError(t, err)

	assert.Equal(t, "Moved to bottom 1 torrent: ubuntu-24.04.iso\n", out)
}

func TestQueueTopByHash(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runQueue(t, "top", "--config", cfgPath, fullHashTwo)
	require.NoError(t, err)

	assert.Equal(t, "Moved to top 1 torrent: debian.iso\n", out)
}

func TestQueueNoSelectionErrors(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	for _, verb := range []string{"top", "up", "down", "bottom"} {
		_, _, err := runQueue(t, verb, "--config", cfgPath)
		require.Error(t, err, "queue %s with no selector must error", verb)
		assert.Contains(t, err.Error(), "specify torrent ids or hashes")
	}
}

// TestQueueRejectsAllFlag guards the deliberate omission of --all: it must not be
// a registered flag on any subcommand.
func TestQueueRejectsAllFlag(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	_, _, err := runQueue(t, "top", "--config", cfgPath, "--all")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown flag: --all")
}

func TestQueueNoMatchErrors(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	_, _, err := runQueue(t, "top", "--config", cfgPath, "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no torrents matched: 99")
}

func TestQueuePartialMatchSurfacesUnmatched(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runQueue(t, "top", "--config", cfgPath, "1", "99")
	require.NoError(t, err)
	assert.Contains(t, out, "Moved to top 1 torrent: ubuntu-24.04.iso")
	assert.Contains(t, out, "No torrents matched: 99")
}

func TestQueueJSON(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runQueue(t, "top", "--config", cfgPath, "--json", "1")
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

	assert.Equal(t, "queue-top", got.Action)
	assert.Equal(t, 1, got.Count)
	require.Len(t, got.Torrents, 1)
	assert.Equal(t, int64(1), got.Torrents[0].ID)
	assert.Equal(t, fullHashOne, got.Torrents[0].Hash)
	assert.Equal(t, "ubuntu-24.04.iso", got.Torrents[0].Name)
	assert.Nil(t, got.Now, "queue omits the now field")
	assert.Nil(t, got.DeleteData, "queue omits the delete_data field")
	assert.Empty(t, got.Unmatched, "no unmatched tokens when every selector matched")
}

func TestQueueJQ(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runQueue(t, "bottom", "--config", cfgPath, "--jq", ".action", "1")
	require.NoError(t, err)

	assert.Equal(t, `"queue-bottom"`, strings.TrimSpace(out))
}

func TestQueueNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runQueue(t, "top", "--config", cfgPath, "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// TestQueueSanitizesNameInHumanButNotJSON guards the security boundary: a torrent
// name carrying an ANSI escape is neutralized in the human confirmation but
// preserved verbatim in the structured --json contract.
func TestQueueSanitizesNameInHumanButNotJSON(t *testing.T) {
	const body = `{
		"arguments": {"torrents": [
			{"id": 1, "hashString": "0123456789abcdef0123456789abcdef01234567", "name": "\u001b[2Jmalicious", "status": 0}
		]},
		"result": "success",
		"tag": 1
	}`

	cfgPath, closeSrv := mutationConfigPath(t, body)
	defer closeSrv()

	human, _, err := runQueue(t, "top", "--config", cfgPath, "1")
	require.NoError(t, err)
	assert.NotContains(t, human, "\x1b", "raw ESC must not reach the human confirmation")
	assert.Contains(t, human, "[2Jmalicious", "visible text survives sanitization")

	jsonOut, _, err := runQueue(t, "top", "--config", cfgPath, "--json", "1")
	require.NoError(t, err)
	assert.Contains(t, jsonOut, "\\u001b", "the raw escape is preserved in the JSON contract")
}

// TestQueueSendsMethodBespoke asserts the bespoke wire request for top/up carries
// the kebab-case queue-move-* method and the requested ids.
func TestQueueSendsMethodBespoke(t *testing.T) {
	cases := []struct {
		verb   string
		method string
	}{
		{"top", "queue-move-top"},
		{"up", "queue-move-up"},
	}

	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
			defer closeSrv()

			_, _, err := runQueue(t, tc.verb, "--config", cfgPath, "1")
			require.NoError(t, err)

			reqBody, ok := stub.request(tc.method)
			require.True(t, ok, "the bespoke %s method was called", tc.method)
			assert.Contains(t, reqBody, `"ids":[1]`)
		})
	}
}

// TestQueueSendsMethodJSONRPC asserts the JSON-RPC wire request for top/up carries
// the snake_case queue_move_* method and the requested ids.
func TestQueueSendsMethodJSONRPC(t *testing.T) {
	cases := []struct {
		verb   string
		method string
	}{
		{"top", "queue_move_top"},
		{"up", "queue_move_up"},
	}

	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			cfgPath, stub, closeSrv := removeConfigPath(t, "6.0.0", jsonrpcListResponse)
			defer closeSrv()

			_, _, err := runQueue(t, tc.verb, "--config", cfgPath, "1")
			require.NoError(t, err)

			reqBody, ok := stub.request(tc.method)
			require.True(t, ok, "the JSON-RPC %s method was called", tc.method)
			assert.Contains(t, reqBody, `"ids":[1]`)
		})
	}
}
