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

// renameTorrentName is the fixture torrent's starting name. It is deliberately
// unlike a file name so the root path ("Ubuntu 24.04") and a path inside the
// torrent ("Ubuntu 24.04/disk.iso") cannot be confused in an assertion.
const renameTorrentName = "Ubuntu 24.04"

// renameStub emulates a Transmission server for the rename command on either
// wire scheme. It serves torrent-get from a single mutable torrent (filtered by
// the request's ids, like a real server), answers a rename by echoing the
// requested path/name, and applies a root rename to the torrent's own name — so
// the command's post-rename re-read observes the new name the way it would in
// production. Every non-handshake request body is recorded per method.
type renameStub struct {
	// rpcVersion is reported as the CSRF version header; a non-empty value
	// selects the JSON-RPC 2.0 scheme, empty selects the legacy bespoke one.
	rpcVersion string

	// emptyEcho answers a successful rename with blank path/name, exercising the
	// contract's fallback to the request arguments.
	emptyEcho bool

	// echoPath and echoName, when set, replace the corresponding value in the
	// rename response so the server's echo can be made to differ from the
	// request — the only way to tell a reported echo from a replayed argument.
	echoPath string
	echoName string

	mu       sync.Mutex
	name     string
	requests map[string][]string
}

func newRenameStub(rpcVersion, name string) *renameStub {
	return &renameStub{rpcVersion: rpcVersion, name: name, requests: map[string][]string{}}
}

func (s *renameStub) jsonrpc() bool { return s.rpcVersion != "" }

func (s *renameStub) handler() http.HandlerFunc {
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

		// The two schemes wrap their arguments differently (params vs
		// arguments); decoding both and preferring the populated one keeps the
		// stub scheme-agnostic.
		var req struct {
			Method string `json:"method"`
			Params struct {
				IDs  []json.RawMessage `json:"ids"`
				Path string            `json:"path"`
				Name string            `json:"name"`
			} `json:"params"`
			Arguments struct {
				IDs  []json.RawMessage `json:"ids"`
				Path string            `json:"path"`
				Name string            `json:"name"`
			} `json:"arguments"`
		}
		_ = json.Unmarshal(body, &req)

		args := req.Arguments
		if s.jsonrpc() {
			args = req.Params
		}

		s.mu.Lock()
		defer s.mu.Unlock()

		s.requests[req.Method] = append(s.requests[req.Method], string(body))

		switch req.Method {
		case "torrent-get", "torrent_get":
			_, _ = w.Write(s.torrentsBody(args.IDs))
		case "torrent-rename-path", "torrent_rename_path":
			if strings.TrimSuffix(args.Path, "/") == s.name {
				s.name = args.Name
			}
			_, _ = w.Write(s.renameBody(args.Path, args.Name))
		default:
			_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": "success", "arguments": {}, "id": 1, "tag": 1}`))
		}
	}
}

// torrentsBody renders the fixture torrent in the active scheme's spelling,
// filtered by the requested ids so a selector matching nothing comes back empty
// (a real server never over-returns). It runs under the stub's lock.
func (s *renameStub) torrentsBody(ids []json.RawMessage) []byte {
	torrent := map[string]any{"id": 1, "name": s.name, "status": 4}
	if s.jsonrpc() {
		torrent["hash_string"] = infoHash1
	} else {
		torrent["hashString"] = infoHash1
	}

	torrents := []any{torrent}
	if len(ids) > 0 && !renameStubMatches(ids) {
		torrents = []any{}
	}

	return s.envelope(map[string]any{"torrents": torrents})
}

// renameBody renders the server's echo of a completed rename: the requested
// path/name by default, the stub's overrides when set, or a blank echo when the
// stub is configured for one. It runs under the stub's lock.
func (s *renameStub) renameBody(path, name string) []byte {
	if s.echoPath != "" {
		path = s.echoPath
	}

	if s.echoName != "" {
		name = s.echoName
	}

	if s.emptyEcho {
		path, name = "", ""
	}

	return s.envelope(map[string]any{"id": 1, "path": path, "name": name})
}

// envelope wraps a payload in the active scheme's response envelope.
func (s *renameStub) envelope(payload map[string]any) []byte {
	if s.jsonrpc() {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "result": payload, "id": 1})

		return body
	}

	body, _ := json.Marshal(map[string]any{"result": "success", "arguments": payload, "tag": 1})

	return body
}

// renameStubMatches reports whether the requested ids name the fixture torrent
// (id 1 or its info-hash).
func renameStubMatches(ids []json.RawMessage) bool {
	for _, id := range ids {
		switch strings.Trim(string(id), `"`) {
		case "1", infoHash1:
			return true
		}
	}

	return false
}

func (s *renameStub) requestsFor(method string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.requests[method]
}

// renameRequest returns the body of the rename call issued on either scheme,
// and whether one was made at all.
func (s *renameStub) renameRequest() (string, bool) {
	for _, method := range []string{"torrent-rename-path", "torrent_rename_path"} {
		if bodies := s.requestsFor(method); len(bodies) > 0 {
			return bodies[0], true
		}
	}

	return "", false
}

// renameConfigPath stands up a rename stub on the given wire scheme and writes a
// config pointing at it, returning the config path, the stub for assertions, and
// a cleanup func.
func renameConfigPath(t *testing.T, rpcVersion string) (string, *renameStub, func()) {
	t.Helper()

	return renameConfigPathNamed(t, rpcVersion, renameTorrentName)
}

// renameConfigPathNamed is renameConfigPath with the fixture torrent's starting
// name spelled out, for the sanitization fixtures.
func renameConfigPathNamed(t *testing.T, rpcVersion, name string) (string, *renameStub, func()) {
	t.Helper()

	keyring.MockInit()

	stub := newRenameStub(rpcVersion, name)
	srv := httptest.NewServer(stub.handler())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	return cfgPath, stub, srv.Close
}

// runRename executes the rename command with args, returning stdout, stderr, and
// the Execute error. Stdin is an empty (non-terminal) reader, so the root gate's
// non-interactive branch is what a bare invocation exercises.
func runRename(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewRenameCmd()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// TestRenameNonRootBothSchemes covers the end-to-end happy path on both wire
// schemes: the scheme's method name is called with the one id and the path/name
// arguments, and a non-root rename never prompts.
func TestRenameNonRootBothSchemes(t *testing.T) {
	cases := []struct {
		name       string
		rpcVersion string
		method     string
	}{
		{name: "bespoke", rpcVersion: "", method: "torrent-rename-path"},
		{name: "jsonrpc", rpcVersion: "6.0.0", method: "torrent_rename_path"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := renameConfigPath(t, tc.rpcVersion)
			defer closeSrv()

			out, errOut, err := runRename(t, "--config", cfgPath, "1", "Ubuntu 24.04/disk.iso", "ubuntu.iso")
			require.NoError(t, err)
			assert.Empty(t, errOut)

			assert.Equal(t, "Renamed Ubuntu 24.04/disk.iso to ubuntu.iso in torrent Ubuntu 24.04\n", out)

			bodies := stub.requestsFor(tc.method)
			require.Len(t, bodies, 1, "the scheme's rename method was called exactly once")
			assert.Contains(t, bodies[0], `"ids":[1]`)
			assert.Contains(t, bodies[0], `"path":"Ubuntu 24.04/disk.iso"`)
			assert.Contains(t, bodies[0], `"name":"ubuntu.iso"`)
		})
	}
}

// TestRenameByHashResolvesID guards that a hash selector is resolved to the
// numeric id before the rename, so the wire always carries the id.
func TestRenameByHashResolvesID(t *testing.T) {
	cfgPath, stub, closeSrv := renameConfigPath(t, "")
	defer closeSrv()

	out, _, err := runRename(t, "--config", cfgPath, infoHash1, "Ubuntu 24.04/disk.iso", "ubuntu.iso")
	require.NoError(t, err)
	assert.Equal(t, "Renamed Ubuntu 24.04/disk.iso to ubuntu.iso in torrent Ubuntu 24.04\n", out)

	body, ok := stub.renameRequest()
	require.True(t, ok, "the rename method was called")
	assert.Contains(t, body, `"ids":[1]`)
}

func TestRenameJSON(t *testing.T) {
	cfgPath, _, closeSrv := renameConfigPath(t, "")
	defer closeSrv()

	out, _, err := runRename(t, "--config", cfgPath, "--json", "1", "Ubuntu 24.04/disk.iso", "ubuntu.iso")
	require.NoError(t, err)

	got := decodeRenameResult(t, out)

	assert.Equal(t, "rename", got.Action)
	assert.Equal(t, "Ubuntu 24.04/disk.iso", got.Path)
	assert.Equal(t, "ubuntu.iso", got.Name)
	assert.False(t, got.Root, "a path inside the torrent is not the root")
	assert.Equal(t, int64(1), got.Torrent.ID)
	assert.Equal(t, infoHash1, got.Torrent.Hash)
	assert.Equal(t, "Ubuntu 24.04", got.Torrent.Name)
}

func TestRenameJQ(t *testing.T) {
	cfgPath, _, closeSrv := renameConfigPath(t, "")
	defer closeSrv()

	out, _, err := runRename(t, "--config", cfgPath, "--jq", ".name", "1", "Ubuntu 24.04/disk.iso", "ubuntu.iso")
	require.NoError(t, err)

	assert.Equal(t, `"ubuntu.iso"`, strings.TrimSpace(out))
}

// TestRenameReportsServerEcho pins the authoritative half of the contract: the
// reported path and name are the server's echo of what it actually renamed, not
// a replay of the request arguments. The stub is made to answer with values that
// differ from what was asked for, so an implementation that rebuilt the contract
// from the positional arguments fails here.
func TestRenameReportsServerEcho(t *testing.T) {
	cfgPath, stub, closeSrv := renameConfigPath(t, "")
	defer closeSrv()

	stub.echoPath = "Ubuntu 24.04/server-echo.iso"
	stub.echoName = "server-echo.iso"

	out, _, err := runRename(t, "--config", cfgPath, "--json", "1", "Ubuntu 24.04/disk.iso", "ubuntu.iso")
	require.NoError(t, err)

	got := decodeRenameResult(t, out)
	assert.Equal(t, "Ubuntu 24.04/server-echo.iso", got.Path, "the reported path is the server's echo")
	assert.Equal(t, "server-echo.iso", got.Name, "the reported name is the server's echo")

	body, ok := stub.renameRequest()
	require.True(t, ok, "the rename method was called")
	assert.Contains(t, body, `"path":"Ubuntu 24.04/disk.iso"`, "the request carried the user's path")
	assert.Contains(t, body, `"name":"ubuntu.iso"`, "the request carried the user's name")
}

// TestRenameEmptyEchoFallsBackToArguments guards that a server answering a
// successful rename with a blank echo still yields a populated contract.
func TestRenameEmptyEchoFallsBackToArguments(t *testing.T) {
	cfgPath, stub, closeSrv := renameConfigPath(t, "")
	defer closeSrv()

	stub.emptyEcho = true

	out, _, err := runRename(t, "--config", cfgPath, "--json", "1", "Ubuntu 24.04/disk.iso", "ubuntu.iso")
	require.NoError(t, err)

	got := decodeRenameResult(t, out)
	assert.Equal(t, "Ubuntu 24.04/disk.iso", got.Path)
	assert.Equal(t, "ubuntu.iso", got.Name)
}

// TestRenameRootWithYes covers the root rename: --yes skips the gate, the
// torrent itself is renamed, and the re-read reports the post-rename name.
func TestRenameRootWithYes(t *testing.T) {
	cases := []struct {
		name       string
		rpcVersion string
		method     string
	}{
		{name: "bespoke", rpcVersion: "", method: "torrent-rename-path"},
		{name: "jsonrpc", rpcVersion: "6.0.0", method: "torrent_rename_path"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := renameConfigPath(t, tc.rpcVersion)
			defer closeSrv()

			out, _, err := runRename(t, "--config", cfgPath, "--yes", "1", "Ubuntu 24.04", "ubuntu-24.04")
			require.NoError(t, err)

			assert.NotContains(t, out, "[y/N]", "no prompt is shown when --yes is given")
			assert.Equal(t, "Renamed torrent Ubuntu 24.04 to ubuntu-24.04\n", out)

			require.Len(t, stub.requestsFor(tc.method), 1, "the rename ran")
		})
	}
}

// TestRenameRootJSONReportsPostRenameName pins the post-rename re-read: the
// reported torrent name is the new one, not the name the root check compared
// against.
func TestRenameRootJSONReportsPostRenameName(t *testing.T) {
	cfgPath, _, closeSrv := renameConfigPath(t, "")
	defer closeSrv()

	out, _, err := runRename(t, "--config", cfgPath, "--yes", "--json", "1", "Ubuntu 24.04", "ubuntu-24.04")
	require.NoError(t, err)

	got := decodeRenameResult(t, out)
	assert.True(t, got.Root, "the rename targeted the torrent root")
	assert.Equal(t, "ubuntu-24.04", got.Torrent.Name, "the torrent name is post-rename truth")
}

// TestRenameRootTrailingSlashIsStillRoot guards that a shell-completed directory
// path does not slip past the root gate.
func TestRenameRootTrailingSlashIsStillRoot(t *testing.T) {
	cfgPath, stub, closeSrv := renameConfigPath(t, "")
	defer closeSrv()

	_, _, err := runRename(t, "--config", cfgPath, "1", "Ubuntu 24.04/", "ubuntu-24.04")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to rename the torrent root without confirmation")

	_, ok := stub.renameRequest()
	assert.False(t, ok, "the rename method must not be called behind the gate")
}

// TestRenameRootTrailingSlashIsTrimmedOnTheWire guards that the slash a shell
// completes a directory path with is trimmed before the call, not just before
// the root check. Transmission matches <path> exactly against the torrent name
// or a file subpath, so a stray slash would make every confirmed rename fail
// server-side, and would leak into the reported path as well.
func TestRenameRootTrailingSlashIsTrimmedOnTheWire(t *testing.T) {
	t.Run("plaintext", func(t *testing.T) {
		cfgPath, stub, closeSrv := renameConfigPath(t, "")
		defer closeSrv()

		out, _, err := runRename(t, "--config", cfgPath, "--yes", "1", "Ubuntu 24.04/", "ubuntu-24.04")
		require.NoError(t, err)
		assert.Equal(t, "Renamed torrent Ubuntu 24.04 to ubuntu-24.04\n", out,
			"the plaintext line reports the trimmed path")

		body, ok := stub.renameRequest()
		require.True(t, ok, "the rename method was called")
		assert.Contains(t, body, `"path":"Ubuntu 24.04"`, "the wire carries the trimmed path")
		assert.Contains(t, body, `"name":"ubuntu-24.04"`)
	})

	t.Run("json", func(t *testing.T) {
		cfgPath, stub, closeSrv := renameConfigPath(t, "")
		defer closeSrv()

		out, _, err := runRename(t, "--config", cfgPath, "--yes", "--json", "1", "Ubuntu 24.04/", "ubuntu-24.04")
		require.NoError(t, err)

		body, ok := stub.renameRequest()
		require.True(t, ok, "the rename method was called")
		assert.Contains(t, body, `"path":"Ubuntu 24.04"`, "the wire carries the trimmed path")

		got := decodeRenameResult(t, out)
		assert.True(t, got.Root, "the rename targeted the torrent root")
		assert.Equal(t, "Ubuntu 24.04", got.Path, "the reported path is the trimmed one")
		assert.Equal(t, "ubuntu-24.04", got.Torrent.Name, "the torrent name is post-rename truth")
	})
}

// TestRenameRootWithoutYesRefusesWhenNotATTY guards the gate's non-interactive
// branch: with stdin not a terminal the command refuses rather than prompting,
// and renames nothing.
func TestRenameRootWithoutYesRefusesWhenNotATTY(t *testing.T) {
	cfgPath, stub, closeSrv := renameConfigPath(t, "")
	defer closeSrv()

	_, _, err := runRename(t, "--config", cfgPath, "1", "Ubuntu 24.04", "ubuntu-24.04")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to rename the torrent root without confirmation")
	assert.Contains(t, err.Error(), "pass --yes to proceed")

	_, ok := stub.renameRequest()
	assert.False(t, ok, "the rename method must not be called without confirmation")
}

// TestRenameNonRootNeverPrompts guards that only the root path is gated: a path
// inside the torrent renames with no --yes and no prompt, even though stdin is
// not a terminal.
func TestRenameNonRootNeverPrompts(t *testing.T) {
	cfgPath, stub, closeSrv := renameConfigPath(t, "")
	defer closeSrv()

	out, _, err := runRename(t, "--config", cfgPath, "1", "Ubuntu 24.04/disk.iso", "ubuntu.iso")
	require.NoError(t, err)
	assert.NotContains(t, out, "[y/N]")

	_, ok := stub.renameRequest()
	assert.True(t, ok, "a non-root rename runs unprompted")
}

// TestRenameRootConfirmation covers the interactive branch of the gate at the
// prompt seam, the way remove_test covers its own: an affirmative answer
// proceeds and everything else declines. The command path itself cannot be
// driven interactively without a real TTY, which confirmRootRename requires by
// design.
func TestRenameRootConfirmation(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "confirmed", input: "y\n", want: true},
		{name: "confirmed word", input: "yes\n", want: true},
		{name: "declined", input: "n\n", want: false},
		{name: "empty line declines", input: "\n", want: false},
		{name: "eof declines", input: "", want: false},
	}

	prompt := rootRenamePrompt("Ubuntu 24.04", "ubuntu-24.04")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRenameCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)

			got, err := promptConfirm(cmd, strings.NewReader(tc.input), prompt)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, prompt, out.String(), "the prompt is written verbatim")
		})
	}
}

// TestRootRenamePrompt pins the prompt copy, which names both the torrent and
// what it becomes, and sanitizes the server-derived torrent name.
func TestRootRenamePrompt(t *testing.T) {
	assert.Equal(t,
		`Rename torrent "Ubuntu 24.04" to "ubuntu-24.04"? [y/N] `,
		rootRenamePrompt("Ubuntu 24.04", "ubuntu-24.04"),
	)

	assert.NotContains(t, rootRenamePrompt("\x1b[2Jmalicious", "safe"), "\x1b",
		"raw ESC must not reach the prompt")
}

// TestRenameRejectsEmptyArguments guards that an empty or whitespace-only path
// or new name is rejected before any network call.
func TestRenameRejectsEmptyArguments(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		newName string
		want    string
	}{
		{name: "empty path", path: "", newName: "ubuntu.iso", want: "path must not be empty"},
		{name: "blank path", path: "   ", newName: "ubuntu.iso", want: "path must not be empty"},
		{name: "empty name", path: "Ubuntu 24.04/disk.iso", newName: "", want: "new name must not be empty"},
		{name: "blank name", path: "Ubuntu 24.04/disk.iso", newName: "\t ", want: "new name must not be empty"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := renameConfigPath(t, "")
			defer closeSrv()

			_, _, err := runRename(t, "--config", cfgPath, "1", tc.path, tc.newName)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)

			_, ok := stub.renameRequest()
			assert.False(t, ok, "nothing is renamed after a rejected argument")
		})
	}
}

// TestRenameNoMatchErrors guards that a selector matching nothing errors without
// renaming anything, for both selector forms.
func TestRenameNoMatchErrors(t *testing.T) {
	t.Run("id", func(t *testing.T) {
		cfgPath, stub, closeSrv := renameConfigPath(t, "")
		defer closeSrv()

		_, _, err := runRename(t, "--config", cfgPath, "99", "Ubuntu 24.04/disk.iso", "ubuntu.iso")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no torrent matched id 99")

		_, ok := stub.renameRequest()
		assert.False(t, ok, "nothing is renamed for an unmatched id")
	})

	t.Run("hash", func(t *testing.T) {
		cfgPath, stub, closeSrv := renameConfigPath(t, "")
		defer closeSrv()

		_, _, err := runRename(t, "--config", cfgPath, infoHash2, "Ubuntu 24.04/disk.iso", "ubuntu.iso")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no torrent matched: "+infoHash2)

		_, ok := stub.renameRequest()
		assert.False(t, ok, "nothing is renamed for an unmatched hash")
	})
}

func TestRenameMalformedSelectorErrors(t *testing.T) {
	cfgPath, stub, closeSrv := renameConfigPath(t, "")
	defer closeSrv()

	_, _, err := runRename(t, "--config", cfgPath, "not-a-hash", "Ubuntu 24.04/disk.iso", "ubuntu.iso")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid torrent selector")

	_, ok := stub.renameRequest()
	assert.False(t, ok, "nothing is renamed for a malformed selector")
}

func TestRenameRequiresThreeArgs(t *testing.T) {
	cfgPath, _, closeSrv := renameConfigPath(t, "")
	defer closeSrv()

	_, _, err := runRename(t, "--config", cfgPath, "1", "Ubuntu 24.04/disk.iso")
	require.Error(t, err)
}

func TestRenameNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runRename(t, "--config", cfgPath, "1", "Ubuntu 24.04/disk.iso", "ubuntu.iso")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// TestRenameSanitizesNameInHumanButNotJSON guards the security boundary: a
// torrent name carrying terminal control sequences is neutralized in the human
// confirmation but preserved verbatim in the structured --json contract.
func TestRenameSanitizesNameInHumanButNotJSON(t *testing.T) {
	const hostile = "\x1b[2Jmali\u202ecious"

	cfgPath, _, closeSrv := renameConfigPathNamed(t, "", hostile)
	defer closeSrv()

	human, _, err := runRename(t, "--config", cfgPath, "1", hostile+"/disk.iso", "ubuntu.iso")
	require.NoError(t, err)
	assert.NotContains(t, human, "\x1b", "raw ESC must not reach the human confirmation")
	assert.NotContains(t, human, "\u202e", "the bidi override must not reach the human confirmation")
	assert.Contains(t, human, "[2Jmalicious", "visible text survives sanitization")

	cfgPath, _, closeSrv = renameConfigPathNamed(t, "", hostile)
	defer closeSrv()

	jsonOut, _, err := runRename(t, "--config", cfgPath, "--json", "1", hostile+"/disk.iso", "ubuntu.iso")
	require.NoError(t, err)
	assert.Contains(t, jsonOut, "\\u001b", "the raw escape is preserved in the JSON contract")

	got := decodeRenameResult(t, jsonOut)
	assert.Equal(t, hostile, got.Torrent.Name, "the JSON contract carries the name verbatim")
	assert.Equal(t, hostile+"/disk.iso", got.Path, "the echoed path is not sanitized in JSON")
}

// TestRenameRoutesThroughTheTorrentGroup exercises the command through the
// parent group, so registration and routing are covered rather than just the
// constructor.
func TestRenameRoutesThroughTheTorrentGroup(t *testing.T) {
	cfgPath, stub, closeSrv := renameConfigPath(t, "")
	defer closeSrv()

	out, errOut, err := runTorrent(t, "rename", "1", "Ubuntu 24.04/disk.iso", "ubuntu.iso", "--config", cfgPath)
	require.NoError(t, err)
	assert.Empty(t, errOut, "nothing is written to stderr")
	assert.Equal(t, "Renamed Ubuntu 24.04/disk.iso to ubuntu.iso in torrent Ubuntu 24.04\n", out)

	_, ok := stub.renameRequest()
	assert.True(t, ok, "the subcommand issued the rename")
}

// decodeRenameResult unmarshals the command's --json output into the documented
// contract shape, spelled out here rather than reusing renameResult so the json
// tags are asserted against an independent declaration.
func decodeRenameResult(t *testing.T, out string) struct {
	Action  string `json:"action"`
	Path    string `json:"path"`
	Name    string `json:"name"`
	Root    bool   `json:"root"`
	Torrent struct {
		ID   int64  `json:"id"`
		Hash string `json:"hash"`
		Name string `json:"name"`
	} `json:"torrent"`
} {
	t.Helper()

	var got struct {
		Action  string `json:"action"`
		Path    string `json:"path"`
		Name    string `json:"name"`
		Root    bool   `json:"root"`
		Torrent struct {
			ID   int64  `json:"id"`
			Hash string `json:"hash"`
			Name string `json:"name"`
		} `json:"torrent"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	return got
}
