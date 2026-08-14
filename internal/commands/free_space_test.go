package commands

import (
	"bytes"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
)

// runFreeSpace executes the free-space command with args, returning stdout,
// stderr, and the Execute error. The command's output is a *bytes.Buffer, so
// isTTY reports false where it matters.
func runFreeSpace(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewFreeSpaceCmd()
	AddGlobalFlags(cmd)

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// freeSpaceConfigPath stands up a transmission stub answering the free-space
// query and (for the default-path flow) session-get, then writes a config
// pointing at it. A non-empty rpcVersion selects the JSON-RPC 2.0 path (method
// free_space); an empty one selects the bespoke path (method free-space).
func freeSpaceConfigPath(t *testing.T, rpcVersion, method, body string) (string, func()) {
	t.Helper()

	keyring.MockInit()

	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: rpcVersion,
		responses: map[string]string{
			method:        body,
			"session-get": bespokeSessionResponse,
		},
	}.handler())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	return cfgPath, srv.Close
}

func TestFreeSpacePlaintextWithPath(t *testing.T) {
	cfgPath, closeSrv := freeSpaceConfigPath(t, "", "free-space",
		`{"arguments": {"path": "/downloads", "size-bytes": 1024, "total_size": 4096}, "result": "success", "tag": 1}`)
	defer closeSrv()

	out, _, err := runFreeSpace(t, "/downloads", "--config", cfgPath)
	require.NoError(t, err)

	assert.Contains(t, out, "Path:")
	assert.Contains(t, out, "/downloads")
	assert.Contains(t, out, "Free:")
	assert.Contains(t, out, "Total:")
}

func TestFreeSpaceJSONRPCSurfacesTotal(t *testing.T) {
	cfgPath, closeSrv := freeSpaceConfigPath(t, "6.0.0", "free_space",
		`{"jsonrpc": "2.0", "result": {"path": "/downloads", "size_bytes": 1024, "total_size": 4096}, "id": 1}`)
	defer closeSrv()

	out, _, err := runFreeSpace(t, "/downloads", "--config", cfgPath, "--json")
	require.NoError(t, err)
	// A 4.1+ (JSON-RPC) server reports the total capacity.
	assert.JSONEq(t, `{"path": "/downloads", "size_bytes": 1024, "total_bytes": 4096}`, out)
}

func TestFreeSpaceBespokeOmitsTotal(t *testing.T) {
	cfgPath, closeSrv := freeSpaceConfigPath(t, "", "free-space",
		`{"arguments": {"path": "/downloads", "size-bytes": 2048}, "result": "success", "tag": 1}`)
	defer closeSrv()

	out, _, err := runFreeSpace(t, "/downloads", "--config", cfgPath, "--json")
	require.NoError(t, err)
	// A bespoke server too old to report total_size omits total_bytes (omitempty).
	assert.JSONEq(t, `{"path": "/downloads", "size_bytes": 2048}`, out)

	pout, _, err := runFreeSpace(t, "/downloads", "--config", cfgPath)
	require.NoError(t, err)
	assert.NotContains(t, pout, "Total:", "no total capacity means no Total line")
}

func TestFreeSpaceJQ(t *testing.T) {
	cfgPath, closeSrv := freeSpaceConfigPath(t, "", "free-space",
		`{"arguments": {"path": "/downloads", "size-bytes": 1024, "total_size": 4096}, "result": "success", "tag": 1}`)
	defer closeSrv()

	out, _, err := runFreeSpace(t, "/downloads", "--config", cfgPath, "--jq", ".size_bytes")
	require.NoError(t, err)
	assert.Equal(t, "1024", strings.TrimSpace(out))
}

func TestFreeSpaceDefaultsToDownloadDir(t *testing.T) {
	// With no path argument, the command resolves the server's download-dir from
	// session-get and queries that. The free-space stub echoes back a fixed path
	// regardless; this asserts the no-arg flow still produces a result.
	cfgPath, closeSrv := freeSpaceConfigPath(t, "", "free-space",
		`{"arguments": {"path": "/data/downloads", "size-bytes": 512}, "result": "success", "tag": 1}`)
	defer closeSrv()

	out, _, err := runFreeSpace(t, "--config", cfgPath, "--json")
	require.NoError(t, err)
	assert.JSONEq(t, `{"path": "/data/downloads", "size_bytes": 512}`, out)
}

func TestFreeSpaceSanitizesPathInPlaintext(t *testing.T) {
	// Plaintext passes the server-derived path through SanitizeTerminal, stripping
	// the embedded ESC (0x1B) control byte; --json must not. The echoed path
	// carries an ESC before "[2J", written as the JSON escape \u001b so it decodes
	// to a real control byte (mirroring the session fixture).
	const body = `{"arguments": {"path": "/data\u001b[2Jdownloads", "size-bytes": 512}, "result": "success", "tag": 1}`

	cfgPath, closeSrv := freeSpaceConfigPath(t, "", "free-space", body)
	defer closeSrv()

	out, _, err := runFreeSpace(t, "--config", cfgPath)
	require.NoError(t, err)
	// Only the ESC byte is removed; the rest of the path stays, so the dangerous
	// control byte cannot reach the terminal.
	assert.NotContains(t, out, "\x1b", "the ESC control byte is stripped from human output")
	assert.Contains(t, out, "/data[2Jdownloads", "the rest of the path is preserved")

	// --json keeps the path intact (control byte not stripped): the structured
	// contract is the consumer's to handle. The JSON encoder emits the ESC as the
	// six-char \u001b escape, not a raw byte, but the character is preserved.
	jout, _, err := runFreeSpace(t, "--config", cfgPath, "--json")
	require.NoError(t, err)
	assert.Contains(t, jout, `\u001b`, "--json keeps the path; the encoder emits the ESC as its escape")
}

func TestFreeSpaceNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runFreeSpace(t, "/downloads", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestFreeSpaceTooManyArgs(t *testing.T) {
	cfgPath, closeSrv := freeSpaceConfigPath(t, "", "free-space",
		`{"arguments": {"path": "/downloads", "size-bytes": 1024}, "result": "success", "tag": 1}`)
	defer closeSrv()

	_, _, err := runFreeSpace(t, "/a", "/b", "--config", cfgPath)
	require.Error(t, err, "more than one positional argument is an error")
}
