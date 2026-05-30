package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
)

// captureStub emulates a Transmission server and records the body of every
// non-handshake request keyed by method, so tests can assert what the remove
// command sent (or that it sent nothing). It answers torrent-get with the given
// list body and every other method with a generic success envelope.
type captureStub struct {
	rpcVersion string
	listBody   string

	mu       sync.Mutex
	requests map[string]string
}

func (s *captureStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			if s.rpcVersion != "" {
				w.Header().Set(rpcVersionHeader, s.rpcVersion)
			}
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		body, _ := io.ReadAll(r.Body)

		var envelope struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &envelope)

		s.mu.Lock()
		if s.requests == nil {
			s.requests = map[string]string{}
		}
		s.requests[envelope.Method] = string(body)
		s.mu.Unlock()

		if envelope.Method == "torrent-get" || envelope.Method == "torrent_get" {
			_, _ = w.Write([]byte(s.listBody))
			return
		}

		_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": "success", "arguments": {}, "id": 1, "tag": 1}`))
	}
}

func (s *captureStub) request(method string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.requests[method]
	return body, ok
}

// removeConfigPath stands up a capturing transmission stub (using the given rpc
// version) and writes a config pointing at it, returning the config path, the
// stub for assertions, and a cleanup func.
func removeConfigPath(t *testing.T, rpcVersion, listBody string) (string, *captureStub, func()) {
	t.Helper()

	keyring.MockInit()

	stub := &captureStub{rpcVersion: rpcVersion, listBody: listBody}
	srv := httptest.NewServer(stub.handler())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{ServerURL: srv.URL}, cfgPath))

	return cfgPath, stub, srv.Close
}

// runRemove executes the remove command with args, returning stdout, stderr, and
// the Execute error. Stdin is an empty (non-terminal) reader, so the piped path
// is exercised.
func runRemove(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewRemoveCmd()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

func TestRemoveByID(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runRemove(t, "--config", cfgPath, "--yes", "1")
	require.NoError(t, err)

	assert.Equal(t, "Removed 1 torrent: ubuntu-24.04.iso\n", out)
}

func TestRemoveByHash(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runRemove(t, "--config", cfgPath, "--yes", fullHashTwo)
	require.NoError(t, err)

	assert.Equal(t, "Removed 1 torrent: debian.iso\n", out)
}

func TestRemoveMultipleByID(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runRemove(t, "--config", cfgPath, "--yes", "1", "2")
	require.NoError(t, err)

	assert.Equal(t, "Removed 2 torrents: ubuntu-24.04.iso, debian.iso\n", out)
}

func TestRemoveAll(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runRemove(t, "--config", cfgPath, "--yes", "--all")
	require.NoError(t, err)

	assert.Equal(t, "Removed all 2 torrents.\n", out)
}

func TestRemoveNoSelectionErrors(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runRemove(t, "--config", cfgPath, "--yes")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "specify torrent ids or hashes, or use --all")
}

func TestRemoveSelectorsAndAllErrors(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runRemove(t, "--config", cfgPath, "--yes", "1", "--all")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot combine torrent selectors with --all")
}

func TestRemoveNoMatchErrors(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runRemove(t, "--config", cfgPath, "--yes", "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no torrents matched: 99")
}

func TestRemovePartialMatchSurfacesUnmatched(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runRemove(t, "--config", cfgPath, "--yes", "1", "99")
	require.NoError(t, err)
	assert.Contains(t, out, "Removed 1 torrent: ubuntu-24.04.iso")
	assert.Contains(t, out, "No torrents matched: 99")
}

func TestRemoveNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runRemove(t, "--config", cfgPath, "--yes", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestRemoveJSON(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runRemove(t, "--config", cfgPath, "--yes", "--json", "1")
	require.NoError(t, err)

	var got struct {
		Action   string `json:"action"`
		Count    int    `json:"count"`
		Torrents []struct {
			ID   int64  `json:"id"`
			Hash string `json:"hash"`
			Name string `json:"name"`
		} `json:"torrents"`
		DeleteData *bool    `json:"delete_data"`
		Unmatched  []string `json:"unmatched"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	assert.Equal(t, "remove", got.Action)
	assert.Equal(t, 1, got.Count)
	require.Len(t, got.Torrents, 1)
	assert.Equal(t, int64(1), got.Torrents[0].ID)
	assert.Equal(t, fullHashOne, got.Torrents[0].Hash)
	assert.Equal(t, "ubuntu-24.04.iso", got.Torrents[0].Name)
	require.NotNil(t, got.DeleteData)
	assert.False(t, *got.DeleteData, "delete_data reflects the --delete-data flag")
	assert.Empty(t, got.Unmatched)
}

func TestRemoveJSONWithDeleteData(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runRemove(t, "--config", cfgPath, "--yes", "--delete-data", "--json", "1")
	require.NoError(t, err)

	var got struct {
		DeleteData *bool `json:"delete_data"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.NotNil(t, got.DeleteData)
	assert.True(t, *got.DeleteData)
}

func TestRemoveJSONIncludesUnmatched(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runRemove(t, "--config", cfgPath, "--yes", "--json", "1", "99")
	require.NoError(t, err)

	var got struct {
		Count     int      `json:"count"`
		Unmatched []string `json:"unmatched"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, 1, got.Count)
	assert.Equal(t, []string{"99"}, got.Unmatched)
}

func TestRemoveJQ(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runRemove(t, "--config", cfgPath, "--yes", "--jq", ".torrents[].name", "--all")
	require.NoError(t, err)

	assert.Equal(t, `"ubuntu-24.04.iso"`+"\n"+`"debian.iso"`, strings.TrimSpace(out))
}

// TestRemoveSendsDeleteFlagBespoke asserts the bespoke remove request carries the
// torrent-remove method with delete-local-data set per the --delete-data flag.
func TestRemoveSendsDeleteFlagBespoke(t *testing.T) {
	t.Run("delete-data set", func(t *testing.T) {
		cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
		defer closeSrv()

		_, _, err := runRemove(t, "--config", cfgPath, "--yes", "--delete-data", "1")
		require.NoError(t, err)

		body, ok := stub.request("torrent-remove")
		require.True(t, ok, "the bespoke torrent-remove method was called")
		assert.Contains(t, body, `"delete-local-data":true`)
		assert.Contains(t, body, `"ids":[1]`)
	})

	t.Run("delete-data unset", func(t *testing.T) {
		cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
		defer closeSrv()

		_, _, err := runRemove(t, "--config", cfgPath, "--yes", "1")
		require.NoError(t, err)

		body, ok := stub.request("torrent-remove")
		require.True(t, ok, "the bespoke torrent-remove method was called")
		assert.Contains(t, body, `"delete-local-data":false`)
	})
}

// jsonrpcListResponse is a JSON-RPC torrent_get reply (snake_case hash_string,
// result-wrapped) used to drive the confirmation lookup on the JSON-RPC path.
const jsonrpcListResponse = `{
	"jsonrpc": "2.0",
	"result": {"torrents": [
		{"id": 1, "hash_string": "0123456789abcdef0123456789abcdef01234567", "name": "ubuntu-24.04.iso", "status": 0}
	]},
	"id": 1
}`

// TestRemoveSendsDeleteFlagJSONRPC asserts the JSON-RPC remove request carries the
// torrent_remove method with delete_local_data set per the --delete-data flag.
func TestRemoveSendsDeleteFlagJSONRPC(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "6.0.0", jsonrpcListResponse)
	defer closeSrv()

	_, _, err := runRemove(t, "--config", cfgPath, "--yes", "--delete-data", "1")
	require.NoError(t, err)

	body, ok := stub.request("torrent_remove")
	require.True(t, ok, "the JSON-RPC torrent_remove method was called")
	assert.Contains(t, body, `"delete_local_data":true`)
}

// TestRemoveWithoutYesErrorsAndDoesNotRemove guards the destructive gate: without
// --yes the command errors and never calls the remove method (verified via the
// capturing stub).
func TestRemoveWithoutYesErrorsAndDoesNotRemove(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runRemove(t, "--config", cfgPath, "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to remove 1 torrent without confirmation")
	assert.Contains(t, err.Error(), "pass --yes to proceed")

	_, ok := stub.request("torrent-remove")
	assert.False(t, ok, "the remove method must not be called without --yes")
}

// TestRemoveYesBypassesPrompt is a regression guard: with --yes the command never
// prompts and removes without reading stdin.
func TestRemoveYesBypassesPrompt(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runRemove(t, "--config", cfgPath, "--yes", "1")
	require.NoError(t, err)

	assert.NotContains(t, out, "[y/N]", "no prompt is shown when --yes is given")
	assert.Equal(t, "Removed 1 torrent: ubuntu-24.04.iso\n", out)

	_, ok := stub.request("torrent-remove")
	assert.True(t, ok, "the remove method runs with --yes")
}

func TestParseAffirmative(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"y", true},
		{"Y", true},
		{"yes", true},
		{"YES", true},
		{" y \n", true},
		{"yes\r\n", true},
		{"n", false},
		{"N", false},
		{"no", false},
		{"", false},
		{"\n", false},
		{"  ", false},
		{"yep", false},
		{"yeah", false},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, parseAffirmative(tc.in))
		})
	}
}

func TestPromptConfirm(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"affirmative", "y\n", true},
		{"affirmative word", "yes\n", true},
		{"decline", "n\n", false},
		{"empty line declines", "\n", false},
		{"eof declines", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRemoveCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)

			got, err := promptConfirm(cmd, strings.NewReader(tc.input), "Remove 1 torrent? [y/N] ")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, "Remove 1 torrent? [y/N] ", out.String(), "the prompt is written verbatim")
		})
	}
}

func TestConfirmPrompt(t *testing.T) {
	cases := []struct {
		name       string
		result     mutationResult
		deleteData bool
		want       string
	}{
		{
			name:   "single torrent",
			result: mutationResult{Count: 1},
			want:   "Remove 1 torrent? [y/N] ",
		},
		{
			name:   "multiple torrents",
			result: mutationResult{Count: 3},
			want:   "Remove 3 torrents? [y/N] ",
		},
		{
			name:   "all",
			result: mutationResult{Count: 2, all: true},
			want:   "Remove all 2 torrents? [y/N] ",
		},
		{
			name:       "delete data",
			result:     mutationResult{Count: 1},
			deleteData: true,
			want:       "Remove 1 torrent and delete downloaded data? [y/N] ",
		},
		{
			name:       "all with delete data",
			result:     mutationResult{Count: 2, all: true},
			deleteData: true,
			want:       "Remove all 2 torrents and delete downloaded data? [y/N] ",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, confirmPrompt(tc.result, tc.deleteData))
		})
	}
}

// TestRemoveSanitizesNameInHumanButNotJSON guards the security boundary: a torrent
// name carrying an ANSI escape is neutralized in the human confirmation but
// preserved verbatim in the structured --json contract.
func TestRemoveSanitizesNameInHumanButNotJSON(t *testing.T) {
	const body = `{
		"arguments": {"torrents": [
			{"id": 1, "hashString": "0123456789abcdef0123456789abcdef01234567", "name": "\u001b[2Jmalicious", "status": 0}
		]},
		"result": "success",
		"tag": 1
	}`

	cfgPath, _, closeSrv := removeConfigPath(t, "", body)
	defer closeSrv()

	human, _, err := runRemove(t, "--config", cfgPath, "--yes", "1")
	require.NoError(t, err)
	assert.NotContains(t, human, "\x1b", "raw ESC must not reach the human confirmation")
	assert.Contains(t, human, "[2Jmalicious", "visible text survives sanitization")

	jsonOut, _, err := runRemove(t, "--config", cfgPath, "--yes", "--json", "1")
	require.NoError(t, err)
	assert.Contains(t, jsonOut, "\\u001b", "the raw escape is preserved in the JSON contract")
}
