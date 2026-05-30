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

// runMove executes the move command with args, returning stdout, stderr, and the
// Execute error. The command's output is a *bytes.Buffer, so the piped/plaintext
// path is exercised.
func runMove(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewMoveCmd()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

func TestMoveByID(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runMove(t, "--config", cfgPath, "1", "/data/media")
	require.NoError(t, err)

	assert.Equal(t, "Moved 1 torrent to /data/media: ubuntu-24.04.iso\n", out)
}

func TestMoveByHash(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runMove(t, "--config", cfgPath, fullHashTwo, "/data/media")
	require.NoError(t, err)

	assert.Equal(t, "Moved 1 torrent to /data/media: debian.iso\n", out)
}

func TestMoveInPlaceWording(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runMove(t, "--config", cfgPath, "--in-place", "1", "/data/media")
	require.NoError(t, err)

	assert.Equal(t, "Set location of 1 torrent to /data/media: ubuntu-24.04.iso\n", out)
}

// TestMoveSendsMoveFlagBespoke asserts the bespoke torrent-set-location request
// carries the location and the move flag per the --in-place flag.
func TestMoveSendsMoveFlagBespoke(t *testing.T) {
	t.Run("default relocates (move true)", func(t *testing.T) {
		cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
		defer closeSrv()

		_, _, err := runMove(t, "--config", cfgPath, "1", "/data/media")
		require.NoError(t, err)

		body, ok := stub.request("torrent-set-location")
		require.True(t, ok, "the bespoke torrent-set-location method was called")
		assert.Contains(t, body, `"location":"/data/media"`)
		assert.Contains(t, body, `"move":true`)
		assert.Contains(t, body, `"ids":[1]`)
	})

	t.Run("in-place repoints (move false)", func(t *testing.T) {
		cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
		defer closeSrv()

		_, _, err := runMove(t, "--config", cfgPath, "--in-place", "1", "/data/media")
		require.NoError(t, err)

		body, ok := stub.request("torrent-set-location")
		require.True(t, ok, "the bespoke torrent-set-location method was called")
		assert.Contains(t, body, `"location":"/data/media"`)
		assert.Contains(t, body, `"move":false`)
	})
}

// TestMoveSendsMoveFlagJSONRPC asserts the JSON-RPC torrent_set_location request
// carries the location and move flag.
func TestMoveSendsMoveFlagJSONRPC(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "6.0.0", jsonrpcListResponse)
	defer closeSrv()

	_, _, err := runMove(t, "--config", cfgPath, "1", "/data/media")
	require.NoError(t, err)

	body, ok := stub.request("torrent_set_location")
	require.True(t, ok, "the JSON-RPC torrent_set_location method was called")
	assert.Contains(t, body, `"location":"/data/media"`)
	assert.Contains(t, body, `"move":true`)
}

func TestMoveJSON(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runMove(t, "--config", cfgPath, "--json", "1", "/data/media")
	require.NoError(t, err)

	var got struct {
		Action   string `json:"action"`
		Count    int    `json:"count"`
		Torrents []struct {
			ID   int64  `json:"id"`
			Hash string `json:"hash"`
			Name string `json:"name"`
		} `json:"torrents"`
		Location string `json:"location"`
		Move     *bool  `json:"move"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	assert.Equal(t, "move", got.Action)
	assert.Equal(t, 1, got.Count)
	require.Len(t, got.Torrents, 1)
	assert.Equal(t, int64(1), got.Torrents[0].ID)
	assert.Equal(t, fullHashOne, got.Torrents[0].Hash)
	assert.Equal(t, "ubuntu-24.04.iso", got.Torrents[0].Name)
	assert.Equal(t, "/data/media", got.Location)
	require.NotNil(t, got.Move)
	assert.True(t, *got.Move, "move reflects the default (relocate) mode")
}

func TestMoveJSONInPlace(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runMove(t, "--config", cfgPath, "--in-place", "--json", "1", "/data/media")
	require.NoError(t, err)

	var got struct {
		Move *bool `json:"move"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.NotNil(t, got.Move)
	assert.False(t, *got.Move, "move is false under --in-place")
}

func TestMoveJQ(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runMove(t, "--config", cfgPath, "--jq", ".location", "1", "/data/media")
	require.NoError(t, err)

	assert.Equal(t, `"/data/media"`, strings.TrimSpace(out))
}

// TestMoveEmptyLocationErrors guards that an empty/whitespace-only location is
// rejected before any backend call.
func TestMoveEmptyLocationErrors(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runMove(t, "--config", cfgPath, "1", "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "location must not be empty")

	_, ok := stub.request("torrent-set-location")
	assert.False(t, ok, "the move method must not be called with an empty location")
}

// TestMoveMalformedSelectorErrors guards that an invalid selector token surfaces
// the ParseSelector error without contacting the server.
func TestMoveMalformedSelectorErrors(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runMove(t, "--config", cfgPath, "not-a-hash", "/data/media")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid torrent selector")

	_, ok := stub.request("torrent-set-location")
	assert.False(t, ok, "the move method must not be called for a malformed selector")
}

// TestMoveNoMatchErrors guards the standard "no torrents matched" error for a
// selector matching nothing.
func TestMoveNoMatchErrors(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runMove(t, "--config", cfgPath, "99", "/data/media")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no torrents matched: 99")
}

func TestMoveRequiresTwoArgs(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runMove(t, "--config", cfgPath, "1")
	require.Error(t, err)
}

func TestMoveNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runMove(t, "--config", cfgPath, "1", "/data/media")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// TestMoveSanitizesNameInHumanButNotJSON guards the security boundary: a torrent
// name carrying an ANSI escape is neutralized in the human confirmation but
// preserved verbatim in the structured --json contract.
func TestMoveSanitizesNameInHumanButNotJSON(t *testing.T) {
	const body = `{
		"arguments": {"torrents": [
			{"id": 1, "hashString": "0123456789abcdef0123456789abcdef01234567", "name": "\u001b[2Jmalicious", "status": 0}
		]},
		"result": "success",
		"tag": 1
	}`

	cfgPath, _, closeSrv := removeConfigPath(t, "", body)
	defer closeSrv()

	human, _, err := runMove(t, "--config", cfgPath, "1", "/data/media")
	require.NoError(t, err)
	assert.NotContains(t, human, "\x1b", "raw ESC must not reach the human confirmation")
	assert.Contains(t, human, "[2Jmalicious", "visible text survives sanitization")

	jsonOut, _, err := runMove(t, "--config", cfgPath, "--json", "1", "/data/media")
	require.NoError(t, err)
	assert.Contains(t, jsonOut, "\\u001b", "the raw escape is preserved in the JSON contract")
}
