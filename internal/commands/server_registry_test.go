package commands

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
	"github.com/itspriddle/snatcher/internal/secrets"
)

// errDBusLaunchMissing is the failure a headless Linux box produces on every
// keyring call: go-keyring asks D-Bus for a session bus, D-Bus shells out to
// dbus-launch, and the lookup fails. Reproduced as the real *exec.Error rather
// than a message string, since that is what internal/secrets classifies on, and
// because its rendered text is what must never reach a user.
var errDBusLaunchMissing = &exec.Error{Name: "dbus-launch", Err: exec.ErrNotFound}

// mockStoreUnavailable points the keyring provider at a mock that fails every
// call, simulating a box with no reachable secret store, and restores the
// working in-memory mock afterwards.
func mockStoreUnavailable(t *testing.T) {
	t.Helper()

	keyring.MockInitWithError(errDBusLaunchMissing)
	t.Cleanup(keyring.MockInit)
}

// resetStoreUnavailableWarning clears dial's once-per-process warning guard so
// a test observes the warning independently of whichever tests ran before it.
func resetStoreUnavailableWarning(t *testing.T) {
	t.Helper()

	storeUnavailableWarned.Store(false)
	t.Cleanup(func() { storeUnavailableWarned.Store(false) })
}

// sessionIDHeader and rpcVersionHeader mirror the CSRF/version headers the rpc
// transport handles. They are duplicated here (the rpc constants are
// unexported) so the stub can emulate a real Transmission server.
const (
	sessionIDHeader  = "X-Transmission-Session-Id"
	rpcVersionHeader = "X-Transmission-Rpc-Version"
)

// transmissionStub emulates a Transmission server: it answers the CSRF probe
// with a 409 carrying the session id (and optionally the rpc-version header),
// then dispatches later requests on the requested method to a canned response.
type transmissionStub struct {
	rpcVersionHeader string
	responses        map[string]string
}

func (s transmissionStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			if s.rpcVersionHeader != "" {
				w.Header().Set(rpcVersionHeader, s.rpcVersionHeader)
			}
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		var envelope struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&envelope)

		if resp, ok := s.responses[envelope.Method]; ok {
			_, _ = w.Write([]byte(resp))
			return
		}

		_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "id": 1}`))
	}
}

// runServer executes the server command tree with args, returning its stdout,
// the combined stderr, and the error from Execute.
func runServer(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()

	root := NewServerCmd()
	AddGlobalFlags(root)

	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)

	err := root.Execute()

	return out.String(), errOut.String(), err
}

func TestServerAddStoresConfigAndKeyring(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	out, _, err := runServer(t, "hunter2\n",
		"add",
		"--config", cfgPath,
		"--url", "http://localhost:9091/transmission/rpc",
		"--username", "alice",
		"--password-stdin",
	)
	require.NoError(t, err)
	assert.Contains(t, out, "Saved credentials")

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	srv := cfg.Servers["localhost:9091"]
	assert.Equal(t, "http://localhost:9091/transmission/rpc", srv.URL)
	assert.Equal(t, "alice", srv.Username)

	// Password lands in the keyring, never in config.
	pw, getErr := secrets.Get(srv.URL)
	require.NoError(t, getErr)
	assert.Equal(t, "hunter2", pw)
}

func TestServerAddTrimsTrailingNewline(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runServer(t, "secret\r\n",
		"add",
		"--config", cfgPath,
		"--url", "http://host:9091/transmission/rpc",
		"--password-stdin",
	)
	require.NoError(t, err)

	pw, getErr := secrets.Get("http://host:9091/transmission/rpc")
	require.NoError(t, getErr)
	assert.Equal(t, "secret", pw)
}

func TestServerAddRequiresURL(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runServer(t, "pw\n", "add", "--config", cfgPath, "--password-stdin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--url is required")
}

func TestServerAddRejectsInvalidURL(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runServer(t, "pw\n",
		"add",
		"--config", cfgPath,
		"--url", "ftp://nope",
		"--password-stdin",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scheme must be http or https")
}

func TestServerAddRejectsEmbeddedCredentials(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runServer(t, "pw\n",
		"add",
		"--config", cfgPath,
		"--url", "http://user:pass@host:9091/transmission/rpc",
		"--password-stdin",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedded credentials")

	// Nothing should have been persisted to config when the URL is rejected.
	_, loadErr := config.Load(cfgPath)
	require.ErrorIs(t, loadErr, config.ErrNotConfigured)
}

func TestServerAddRekeyRemovesOldSecret(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const oldURL = "http://old:9091/transmission/rpc"
	const newURL = "http://new:9091/transmission/rpc"

	// Register a server under an explicit alias, then re-set that same alias to a
	// new URL. The rekey (old URL's secret deletion) is scoped to the alias being
	// changed.
	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"seedbox": {URL: oldURL, Username: "alice"}},
		DefaultServer: "seedbox",
	}, cfgPath))
	require.NoError(t, secrets.Set(oldURL, "oldpw"))

	_, _, err := runServer(t, "newpw\n",
		"add",
		"--config", cfgPath,
		"--name", "seedbox",
		"--url", newURL,
		"--username", "bob",
		"--password-stdin",
	)
	require.NoError(t, err)

	// New URL's secret is stored.
	pw, getErr := secrets.Get(newURL)
	require.NoError(t, getErr)
	assert.Equal(t, "newpw", pw)

	// Old URL's secret is deleted (no orphan).
	_, oldErr := secrets.Get(oldURL)
	require.ErrorIs(t, oldErr, secrets.ErrNotFound)

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, newURL, cfg.Servers["seedbox"].URL)
	assert.Equal(t, "bob", cfg.Servers["seedbox"].Username)
}

func TestServerAddDifferentNameKeepsOtherSecret(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const localURL = "http://local:9091/transmission/rpc"
	const remoteURL = "http://remote:9091/transmission/rpc"

	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"local": {URL: localURL, Username: "alice"}},
		DefaultServer: "local",
	}, cfgPath))
	require.NoError(t, secrets.Set(localURL, "localpw"))

	_, _, err := runServer(t, "remotepw\n",
		"add",
		"--config", cfgPath,
		"--name", "remote",
		"--url", remoteURL,
		"--password-stdin",
	)
	require.NoError(t, err)

	// Registering a second, distinct server leaves the first server's secret and
	// config entry untouched.
	localPW, getErr := secrets.Get(localURL)
	require.NoError(t, getErr)
	assert.Equal(t, "localpw", localPW)

	remotePW, remoteErr := secrets.Get(remoteURL)
	require.NoError(t, remoteErr)
	assert.Equal(t, "remotepw", remotePW)

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, localURL, cfg.Servers["local"].URL)
	assert.Equal(t, remoteURL, cfg.Servers["remote"].URL)
	// The existing default is preserved; only the first server claims it.
	assert.Equal(t, "local", cfg.DefaultServer)
}

func TestServerAddFirstServerBecomesDefault(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runServer(t, "pw\n",
		"add",
		"--config", cfgPath,
		"--name", "seedbox",
		"--url", "http://host:9091/transmission/rpc",
		"--password-stdin",
	)
	require.NoError(t, err)

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, "seedbox", cfg.DefaultServer)
}

func TestServerAddNameDefaultsToHost(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	out, _, err := runServer(t, "pw\n",
		"add",
		"--config", cfgPath,
		"--url", "http://Host:9091/transmission/rpc",
		"--password-stdin",
	)
	require.NoError(t, err)
	// The derived alias is the lowercased host (with port).
	assert.Contains(t, out, `"host:9091"`)

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	_, ok := cfg.Servers["host:9091"]
	assert.True(t, ok)
	assert.Equal(t, "host:9091", cfg.DefaultServer)
}

func TestServerAddSameURLKeepsSecret(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const url = "http://host:9091/transmission/rpc"

	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{config.HostAlias(url): {URL: url, Username: "alice"}},
		DefaultServer: config.HostAlias(url),
	}, cfgPath))
	require.NoError(t, secrets.Set(url, "oldpw"))

	_, _, err := runServer(t, "newpw\n",
		"add",
		"--config", cfgPath,
		"--url", url,
		"--password-stdin",
	)
	require.NoError(t, err)

	// Re-setting the same URL overwrites the password in place.
	pw, getErr := secrets.Get(url)
	require.NoError(t, getErr)
	assert.Equal(t, "newpw", pw)
}

func TestServerAddEmptyPasswordStoresNoSecret(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	out, _, err := runServer(t, "",
		"add",
		"--config", cfgPath,
		"--url", "http://host:9091/transmission/rpc",
		"--username", "alice",
		"--password-stdin",
	)
	require.NoError(t, err)
	assert.Contains(t, out, "no password stored")

	// Config is saved, but no keyring secret is written for an empty password.
	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	srv := cfg.Servers["host:9091"]
	assert.Equal(t, "http://host:9091/transmission/rpc", srv.URL)
	assert.Equal(t, "alice", srv.Username)

	_, getErr := secrets.Get(srv.URL)
	require.ErrorIs(t, getErr, secrets.ErrNotFound)
}

func TestServerAddEmptyPasswordDropsExistingSecret(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const url = "http://host:9091/transmission/rpc"
	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{config.HostAlias(url): {URL: url, Username: "alice"}},
		DefaultServer: config.HostAlias(url),
	}, cfgPath))
	require.NoError(t, secrets.Set(url, "oldpw"))

	_, _, err := runServer(t, "",
		"add",
		"--config", cfgPath,
		"--url", url,
		"--password-stdin",
	)
	require.NoError(t, err)

	// Clearing the password removes the stored secret rather than reusing it.
	_, getErr := secrets.Get(url)
	require.ErrorIs(t, getErr, secrets.ErrNotFound)
}

func TestServerListPlaintext(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const localURL = "http://local:9091/transmission/rpc"
	const seedboxURL = "http://seedbox:9091/transmission/rpc"

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"seedbox": {URL: seedboxURL, Username: "bob"},
			"local":   {URL: localURL, Username: "alice"},
		},
		DefaultServer: "local",
	}, cfgPath))
	require.NoError(t, secrets.Set(localURL, "pw"))

	out, _, err := runServer(t, "", "list", "--config", cfgPath)
	require.NoError(t, err)

	// Not a TTY in tests: tab-separated, header-less rows sorted by alias.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2)

	assert.Equal(t, "local\t"+localURL+"\talice\tyes\tyes", lines[0])
	assert.Equal(t, "seedbox\t"+seedboxURL+"\tbob\tno\tno", lines[1])
}

func TestServerListJSON(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const localURL = "http://local:9091/transmission/rpc"
	const seedboxURL = "http://seedbox:9091/transmission/rpc"

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"seedbox": {URL: seedboxURL, Username: "bob"},
			"local":   {URL: localURL, Username: "alice"},
		},
		DefaultServer: "seedbox",
	}, cfgPath))
	require.NoError(t, secrets.Set(seedboxURL, "pw"))

	out, _, err := runServer(t, "", "list", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var results []serverListResult
	require.NoError(t, json.Unmarshal([]byte(out), &results))
	require.Len(t, results, 2)

	// Sorted by alias.
	assert.Equal(t, "local", results[0].Name)
	assert.Equal(t, localURL, results[0].ServerURL)
	assert.Equal(t, "alice", results[0].Username)
	assert.False(t, results[0].SecretStored)
	assert.False(t, results[0].IsDefault)

	assert.Equal(t, "seedbox", results[1].Name)
	assert.True(t, results[1].SecretStored)
	assert.True(t, results[1].IsDefault)
}

func TestServerListEmpty(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	out, _, err := runServer(t, "", "list", "--config", cfgPath, "--json")
	require.NoError(t, err)

	// An empty inventory serializes as "[]", never "null".
	assert.Equal(t, "[]\n", out)
}

func TestWriteServerListTableEmpty(t *testing.T) {
	var buf bytes.Buffer
	writeServerListTable(&buf, nil)

	assert.Equal(t, "No servers configured.", strings.TrimSpace(buf.String()))
}

func TestWriteServerListTableHeader(t *testing.T) {
	rows := [][]string{
		{"local", "http://local:9091/rpc", "alice", "yes", "yes"},
	}

	var buf bytes.Buffer
	writeServerListTable(&buf, rows)

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2, "header row plus one row per server")

	// The header row carries the documented columns in order.
	header := lines[0]
	assert.True(t, strings.HasPrefix(header, "NAME"))
	for _, col := range []string{"NAME", "URL", "USERNAME", "SECRET", "DEFAULT"} {
		assert.Contains(t, header, col)
	}

	assert.Contains(t, out, "local")
	assert.Contains(t, out, "http://local:9091/rpc")
	assert.Contains(t, out, "alice")
}

func TestWriteServerListPlaintextSanitizesControlChars(t *testing.T) {
	results := []serverListResult{
		{
			Name:         "\x1b[2Jevil",
			ServerURL:    "http://h\x1bost:9091/rpc",
			Username:     "ali\x07ce",
			SecretStored: true,
			IsDefault:    true,
		},
	}

	// A bytes.Buffer is not a TTY, so this exercises the piped tab-separated path.
	cmd := NewServerCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	writeServerListPlaintext(cmd, results)

	out := buf.String()
	assert.NotContains(t, out, "\x1b", "raw ESC must not reach the plaintext output")
	assert.NotContains(t, out, "\x07", "raw BEL must not reach the plaintext output")
	assert.Contains(t, out, "[2Jevil", "visible text survives sanitization")
	assert.Contains(t, out, "http://host:9091/rpc")
	assert.Contains(t, out, "alice")
}

func TestServerListJSONPreservesControlChars(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const url = "http://host:9091/rpc"

	// A server alias carrying a control sequence: the plaintext path strips it,
	// but the structured --json/--jq output is a consumer-owned contract and
	// preserves it verbatim.
	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"\x1b[2Jevil": {URL: url, Username: "alice"}},
		DefaultServer: "\x1b[2Jevil",
	}, cfgPath))

	jsonOut, _, err := runServer(t, "", "list", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var results []serverListResult
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &results))
	require.Len(t, results, 1)
	assert.Equal(t, "\x1b[2Jevil", results[0].Name, "JSON output preserves the raw name verbatim")

	// --json JSON-encodes the string, so the raw ESC byte is escaped rather than
	// emitted literally (and never stripped).
	assert.NotContains(t, jsonOut, "\x1b", "the raw ESC byte is JSON-escaped, not emitted literally")
	assert.Contains(t, jsonOut, "\\u001b", "the control char is preserved as a JSON escape, not dropped")

	jqOut, _, err := runServer(t, "", "list", "--config", cfgPath, "--jq", ".[].name")
	require.NoError(t, err)
	var jqName string
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(jqOut)), &jqName))
	assert.Equal(t, "\x1b[2Jevil", jqName, "jq output preserves the raw name verbatim")

	// The plaintext (piped) path strips the escape from the same alias.
	plainOut, _, err := runServer(t, "", "list", "--config", cfgPath)
	require.NoError(t, err)
	assert.NotContains(t, plainOut, "\x1b", "plaintext path strips the control sequence")
	assert.Contains(t, plainOut, "[2Jevil", "visible text survives on the plaintext path")
}

func TestServerDefaultSwitches(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"local":   {URL: "http://local:9091/rpc", Username: "alice"},
			"seedbox": {URL: "http://seedbox:9091/rpc", Username: "bob"},
		},
		DefaultServer: "local",
	}, cfgPath))

	out, _, err := runServer(t, "", "default", "seedbox", "--config", cfgPath)
	require.NoError(t, err)
	assert.Contains(t, out, "seedbox")

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, "seedbox", cfg.DefaultServer)
}

func TestServerDefaultUnknownAlias(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"local": {URL: "http://local:9091/rpc"}},
		DefaultServer: "local",
	}, cfgPath))

	_, _, err := runServer(t, "", "default", "nope", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown server "nope"`)
	assert.Contains(t, err.Error(), "local")

	// The existing default is left unchanged.
	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, "local", cfg.DefaultServer)
}

func TestServerPingJSONRPC(t *testing.T) {
	keyring.MockInit()
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "6.0.0",
		responses: map[string]string{
			"session_get": `{
				"jsonrpc": "2.0",
				"result": {"version": "4.1.0 (ae226418eb)", "rpc_version_semver": "6.0.0"},
				"id": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL, Username: "alice"}}, DefaultServer: "test"}, cfgPath))
	require.NoError(t, secrets.Set(srv.URL, "pw"))

	out, _, err := runServer(t, "", "ping", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var result serverPingResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.True(t, result.Connected)
	assert.Equal(t, "4.1.0 (ae226418eb)", result.Version)
	assert.Equal(t, "6.0.0", result.RPCVersionSemver)
	assert.Equal(t, "jsonrpc-2.0", result.Protocol)
}

func TestServerPingNamedServer(t *testing.T) {
	keyring.MockInit()

	// Two servers; the alias picks which one is dialed. The default (local) is a
	// dead address, so a successful round-trip proves the positional alias reached
	// the named seedbox.
	seedbox := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "6.0.0",
		responses: map[string]string{
			"session_get": `{
				"jsonrpc": "2.0",
				"result": {"version": "4.1.0 (ae226418eb)", "rpc_version_semver": "6.0.0"},
				"id": 1
			}`,
		},
	}.handler())
	defer seedbox.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"local":   {URL: "http://127.0.0.1:1/rpc"},
			"seedbox": {URL: seedbox.URL},
		},
		DefaultServer: "local",
	}, cfgPath))

	out, _, err := runServer(t, "", "ping", "seedbox", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var result serverPingResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.True(t, result.Connected)
	assert.Equal(t, seedbox.URL, result.ServerURL)
	assert.Equal(t, "4.1.0 (ae226418eb)", result.Version)
}

func TestServerPingUnknownAlias(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"local": {URL: "http://local:9091/rpc"}},
		DefaultServer: "local",
	}, cfgPath))

	_, _, err := runServer(t, "", "ping", "nope", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown server "nope"`)
	assert.Contains(t, err.Error(), "server list")
}

func TestServerPingBespokePlaintext(t *testing.T) {
	keyring.MockInit()
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "",
		responses: map[string]string{
			"session-get": `{
				"arguments": {"version": "4.0.6 (38c164933e)", "rpc-version-semver": "5.4.0"},
				"result": "success",
				"tag": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	out, _, err := runServer(t, "", "ping", "--config", cfgPath)
	require.NoError(t, err)

	assert.Contains(t, out, "Connected to "+srv.URL)
	assert.Contains(t, out, "4.0.6 (38c164933e)")
	assert.Contains(t, out, "5.4.0")
	assert.Contains(t, out, "bespoke")
}

func TestServerPingNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runServer(t, "", "ping", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestServerPingConnectionFailure(t *testing.T) {
	keyring.MockInit()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	_, _, err := runServer(t, "", "ping", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), srv.URL)
}

func TestServerPingHTMLResponseHint(t *testing.T) {
	keyring.MockInit()
	srv := httptest.NewServer(transmissionStub{
		responses: map[string]string{
			"session-get": "<!DOCTYPE html><html><body>login</body></html>",
		},
	}.handler())
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	_, _, err := runServer(t, "", "ping", "--config", cfgPath)
	require.Error(t, err)
	// A non-RPC endpoint that answers with HTML yields an actionable hint, not a
	// raw json syntax error.
	assert.Contains(t, err.Error(), "HTML page")
	assert.Contains(t, err.Error(), "/transmission/rpc")
}

func TestServerRemoveDefaultClearsConfigAndKeyring(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"host:9091": {URL: "http://host:9091/rpc", Username: "bob"}}, DefaultServer: "host:9091"}, cfgPath))
	require.NoError(t, secrets.Set("http://host:9091/rpc", "pw"))

	// Omitting the alias removes the configured default.
	out, _, err := runServer(t, "", "remove", "--config", cfgPath)
	require.NoError(t, err)
	assert.Contains(t, out, "Removed server")
	assert.Contains(t, out, "http://host:9091/rpc")

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Empty(t, cfg.Servers)
	assert.Empty(t, cfg.DefaultServer)

	_, getErr := secrets.Get("http://host:9091/rpc")
	require.ErrorIs(t, getErr, secrets.ErrNotFound)
}

func TestServerRemoveNamedServer(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const localURL = "http://local:9091/rpc"
	const seedboxURL = "http://seedbox:9091/rpc"

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"local":   {URL: localURL, Username: "alice"},
			"seedbox": {URL: seedboxURL, Username: "bob"},
		},
		DefaultServer: "local",
	}, cfgPath))
	require.NoError(t, secrets.Set(localURL, "localpw"))
	require.NoError(t, secrets.Set(seedboxURL, "seedboxpw"))

	// Removing a named non-default server leaves the default and other servers
	// untouched.
	out, _, err := runServer(t, "", "remove", "seedbox", "--config", cfgPath)
	require.NoError(t, err)
	assert.Contains(t, out, "seedbox")

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	_, ok := cfg.Servers["seedbox"]
	assert.False(t, ok)
	assert.Equal(t, localURL, cfg.Servers["local"].URL)
	assert.Equal(t, "local", cfg.DefaultServer)

	// The removed server's secret is gone; the other server's secret remains.
	_, gone := secrets.Get(seedboxURL)
	require.ErrorIs(t, gone, secrets.ErrNotFound)
	localPW, getErr := secrets.Get(localURL)
	require.NoError(t, getErr)
	assert.Equal(t, "localpw", localPW)
}

func TestServerRemoveDefaultRepointsToSoleRemaining(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"local":   {URL: "http://local:9091/rpc"},
			"seedbox": {URL: "http://seedbox:9091/rpc"},
		},
		DefaultServer: "local",
	}, cfgPath))

	// Removing the default with exactly one server remaining repoints the default
	// to that survivor.
	_, _, err := runServer(t, "", "remove", "local", "--config", cfgPath)
	require.NoError(t, err)

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, "seedbox", cfg.DefaultServer)
}

func TestServerRemoveDefaultClearsWhenMultipleRemain(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"local": {URL: "http://local:9091/rpc"},
			"a":     {URL: "http://a:9091/rpc"},
			"b":     {URL: "http://b:9091/rpc"},
		},
		DefaultServer: "local",
	}, cfgPath))

	// Removing the default with more than one server remaining clears the default
	// (the user must pick a new one).
	_, _, err := runServer(t, "", "remove", "local", "--config", cfgPath)
	require.NoError(t, err)

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Empty(t, cfg.DefaultServer)
}

func TestServerRemoveUnknownAlias(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"local": {URL: "http://local:9091/rpc"}},
		DefaultServer: "local",
	}, cfgPath))

	_, _, err := runServer(t, "", "remove", "nope", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown server "nope"`)
	assert.Contains(t, err.Error(), "server list")

	// The configured server is left untouched.
	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	_, ok := cfg.Servers["local"]
	assert.True(t, ok)
}

func TestServerRemoveWhenNothingStored(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runServer(t, "", "remove", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no servers configured")
}

func TestServerAddEmptyPasswordSucceedsWhenStoreUnavailable(t *testing.T) {
	mockStoreUnavailable(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	// The reported bug: the empty-password pre-clear aborted the whole command on
	// a box with no reachable secret store. There is no password to persist, so
	// the server must still be registered.
	out, errOut, err := runServer(t, "",
		"add",
		"--config", cfgPath,
		"--url", "http://host:9091/transmission/rpc",
		"--username", "alice",
		"--password-stdin",
	)
	require.NoError(t, err)
	assert.Contains(t, out, "no password stored")
	assert.NotContains(t, out+errOut, "dbus-launch")

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	srv := cfg.Servers["host:9091"]
	assert.Equal(t, "http://host:9091/transmission/rpc", srv.URL)
	assert.Equal(t, "alice", srv.Username)
	assert.Equal(t, "host:9091", cfg.DefaultServer)
}

func TestServerAddRekeyToleratesUnavailableStore(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const oldURL = "http://old:9091/transmission/rpc"
	const newURL = "http://new:9091/transmission/rpc"

	keyring.MockInit()
	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"seedbox": {URL: oldURL, Username: "alice"}},
		DefaultServer: "seedbox",
	}, cfgPath))

	// Repointing an alias at a new URL runs the old URL's pre-clear too; an
	// unreachable store must not abort that path either.
	mockStoreUnavailable(t)

	_, errOut, err := runServer(t, "",
		"add",
		"--config", cfgPath,
		"--name", "seedbox",
		"--url", newURL,
		"--password-stdin",
	)
	require.NoError(t, err)
	assert.NotContains(t, errOut, "dbus-launch")

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, newURL, cfg.Servers["seedbox"].URL)
}

func TestServerRemoveToleratesUnavailableStore(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const url = "http://host:9091/rpc"

	keyring.MockInit()
	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"host:9091": {URL: url, Username: "bob"}},
		DefaultServer: "host:9091",
	}, cfgPath))

	mockStoreUnavailable(t)

	out, errOut, err := runServer(t, "", "remove", "--config", cfgPath)
	require.NoError(t, err)
	assert.Contains(t, out, "Removed server")

	// The config entry is gone even though the secret could not be deleted, and
	// the note says so without naming a provider internal.
	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Empty(t, cfg.Servers)
	assert.Empty(t, cfg.DefaultServer)

	assert.Contains(t, errOut, "secret store could not be reached")
	assert.Contains(t, errOut, "may remain")
	assert.Contains(t, errOut, "OS secret service")
	assert.Contains(t, errOut, "--store=file")
	assert.Contains(t, errOut, "SNATCHER_SECRET_STORE=file")
	assert.NotContains(t, errOut, "dbus-launch")
}

func TestServerListToleratesUnavailableStore(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const localURL = "http://local:9091/transmission/rpc"
	const seedboxURL = "http://seedbox:9091/transmission/rpc"

	keyring.MockInit()
	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"local":   {URL: localURL, Username: "alice"},
			"seedbox": {URL: seedboxURL, Username: "bob"},
		},
		DefaultServer: "local",
	}, cfgPath))

	mockStoreUnavailable(t)

	out, errOut, err := runServer(t, "", "list", "--config", cfgPath)
	require.NoError(t, err)

	// The column shape is unchanged: unknown secret status renders as "no".
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "local\t"+localURL+"\talice\tno\tyes", lines[0])
	assert.Equal(t, "seedbox\t"+seedboxURL+"\tbob\tno\tno", lines[1])

	// One note for the whole listing, not one per server.
	assert.Equal(t, 1, strings.Count(errOut, "secret store could not be reached"))
	assert.Contains(t, errOut, "could not be determined")
	assert.Contains(t, errOut, "--store=file")
	assert.Contains(t, errOut, "SNATCHER_SECRET_STORE=file")
	assert.NotContains(t, errOut, "dbus-launch")
}

func TestServerListJSONToleratesUnavailableStore(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const url = "http://local:9091/transmission/rpc"

	keyring.MockInit()
	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"local": {URL: url, Username: "alice"}},
		DefaultServer: "local",
	}, cfgPath))

	mockStoreUnavailable(t)

	out, errOut, err := runServer(t, "", "list", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var results []serverListResult
	require.NoError(t, json.Unmarshal([]byte(out), &results))
	require.Len(t, results, 1)
	assert.False(t, results[0].SecretStored)

	// The note is stderr-only, so --json stdout stays a clean structured payload.
	assert.NotContains(t, out, "secret store")
	assert.Contains(t, errOut, "secret store could not be reached")
}

func TestServerShowToleratesUnavailableStore(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const url = "http://host:9091/rpc"

	keyring.MockInit()
	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"host": {URL: url, Username: "alice"}},
		DefaultServer: "host",
	}, cfgPath))

	mockStoreUnavailable(t)

	out, errOut, err := runServer(t, "", "show", "--config", cfgPath, "--local", "--json")
	require.NoError(t, err)

	var result serverShowResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, url, result.ServerURL)
	assert.False(t, result.SecretStored)

	assert.Contains(t, errOut, "secret store could not be reached")
	assert.NotContains(t, errOut, "dbus-launch")
}

func TestDialToleratesUnavailableStore(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const url = "http://host:9091/rpc"

	keyring.MockInit()
	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"host": {URL: url, Username: "alice"}},
		DefaultServer: "host",
	}, cfgPath))

	mockStoreUnavailable(t)
	resetStoreUnavailableWarning(t)

	cmd := dialCmd(t, cfgPath)
	var errOut bytes.Buffer
	cmd.SetErr(&errOut)

	// dial must hand back a usable client rather than aborting: the server may
	// not require auth at all, and if it does the request fails later with the
	// existing auth error.
	backend, dialedURL, err := dial(cmd, "")
	require.NoError(t, err)
	require.NotNil(t, backend)
	assert.Equal(t, url, dialedURL)

	assert.Contains(t, errOut.String(), "secret store could not be reached")
	assert.Contains(t, errOut.String(), "OS secret service")
	assert.Contains(t, errOut.String(), "--store=file")
	assert.Contains(t, errOut.String(), "SNATCHER_SECRET_STORE=file")
	assert.NotContains(t, errOut.String(), "dbus-launch")
}

func TestDialWarnsOnceWhenStoreUnavailable(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	keyring.MockInit()
	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"host": {URL: "http://host:9091/rpc"}},
		DefaultServer: "host",
	}, cfgPath))

	mockStoreUnavailable(t)
	resetStoreUnavailableWarning(t)

	cmd := dialCmd(t, cfgPath)
	var errOut bytes.Buffer
	cmd.SetErr(&errOut)

	for range 3 {
		_, _, err := dial(cmd, "")
		require.NoError(t, err)
	}

	// A future polling client dials on every tick; the warning must not repeat.
	assert.Equal(t, 1, strings.Count(errOut.String(), "secret store could not be reached"))
}

func TestServerPingReachesTransportWhenStoreUnavailable(t *testing.T) {
	keyring.MockInit()
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "6.0.0",
		responses: map[string]string{
			"session_get": `{
				"jsonrpc": "2.0",
				"result": {"version": "4.1.0 (ae226418eb)", "rpc_version_semver": "6.0.0"},
				"id": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"test": {URL: srv.URL, Username: "alice"}},
		DefaultServer: "test",
	}, cfgPath))

	mockStoreUnavailable(t)
	resetStoreUnavailableWarning(t)

	// A completed round-trip proves ping got past dial and reached the transport
	// instead of failing on the unreadable store.
	out, errOut, err := runServer(t, "", "ping", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var result serverPingResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.True(t, result.Connected)
	assert.Equal(t, "4.1.0 (ae226418eb)", result.Version)

	assert.Contains(t, errOut, "secret store could not be reached")
	assert.NotContains(t, out+errOut, "dbus-launch")
}

// TestServerRegistrySubcommands is a compile-time guard that NewServerCmd wires
// the documented registry subcommands and each accepts --config.
func TestServerRegistrySubcommands(t *testing.T) {
	cmd := NewServerCmd()
	AddGlobalFlags(cmd)

	found := map[string]bool{
		"add": false, "list": false, "default": false,
		"show": false, "ping": false, "remove": false,
	}
	for _, c := range cmd.Commands() {
		if _, ok := found[c.Name()]; ok {
			found[c.Name()] = true
			// Every registry subcommand reads or writes local config. --config is
			// a persistent root flag, so Flag climbs the tree to find it rather
			// than each subcommand declaring its own.
			assert.NotNilf(t, c.Flag("config"), "%s missing --config", c.Name())
		}
	}

	for name, ok := range found {
		assert.Truef(t, ok, "missing subcommand %q", name)
	}
}

// prowlarrStub emulates a Prowlarr instance answering the system status probe,
// recording the API key header it was sent (the empty string when none was) so a
// test can assert exactly which credential reached the wire.
type prowlarrStub struct {
	apiKey *string
}

func (s prowlarrStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*s.apiKey = r.Header.Get("X-Api-Key")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"appName":"Prowlarr","instanceName":"Prowlarr","version":"1.30.2.4939"}`))
	}
}

// newProwlarrStub starts a Prowlarr stub and returns its URL alongside a pointer
// to the API key header of the most recent request.
func newProwlarrStub(t *testing.T) (string, *string) {
	t.Helper()

	sent := new(string)
	srv := httptest.NewServer(prowlarrStub{apiKey: sent}.handler())
	t.Cleanup(srv.Close)

	return srv.URL, sent
}

func TestServerAddProwlarrStoresTypeAndAPIKey(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const prowlarrURL = "http://prowlarr:9696"

	out, errOut, err := runServer(t, "abc123\n",
		"add",
		"--config", cfgPath,
		"--type", "prowlarr",
		"--url", prowlarrURL,
		"--api-key-stdin",
	)
	require.NoError(t, err)
	assert.Contains(t, out, "Saved credentials")
	assert.Empty(t, errOut)

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)

	srv := cfg.Servers["prowlarr:9696"]
	assert.Equal(t, prowlarrURL, srv.URL)
	assert.Equal(t, config.TypeProwlarr, srv.ResolvedType())
	assert.Empty(t, srv.Username)

	// The key lands in the secret store keyed by URL, never in config.
	key, getErr := secrets.Get(srv.URL)
	require.NoError(t, getErr)
	assert.Equal(t, "abc123", key)

	raw, readErr := os.ReadFile(cfgPath)
	require.NoError(t, readErr)
	assert.NotContains(t, string(raw), "abc123")
}

func TestServerAddProwlarrTypeIsCaseInsensitive(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runServer(t, "k\n",
		"add",
		"--config", cfgPath,
		"--type", "  PROWLARR ",
		"--url", "http://prowlarr:9696",
		"--api-key-stdin",
	)
	require.NoError(t, err)

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, config.TypeProwlarr, cfg.Servers["prowlarr:9696"].ResolvedType())
}

func TestServerAddRejectsUnknownType(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runServer(t, "pw\n",
		"add",
		"--config", cfgPath,
		"--type", "sonarr",
		"--url", "http://host:9091/transmission/rpc",
		"--password-stdin",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid --type "sonarr"`)
	assert.Contains(t, err.Error(), `"transmission", "prowlarr"`)

	// Nothing is written when the type is rejected.
	_, loadErr := config.Load(cfgPath)
	require.ErrorIs(t, loadErr, config.ErrNotConfigured)
}

func TestServerAddTransmissionOmitsTheTypeKey(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runServer(t, "pw\n",
		"add",
		"--config", cfgPath,
		"--url", "http://host:9091/transmission/rpc",
		"--password-stdin",
	)
	require.NoError(t, err)

	// A Transmission server records no type at all, so a config written by a
	// version that predates the field stays byte-identical through a re-add.
	raw, readErr := os.ReadFile(cfgPath)
	require.NoError(t, readErr)
	assert.NotContains(t, string(raw), `"type"`)
}

func TestServerAddRejectsAPIKeyStdinForATransmissionServer(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runServer(t, "k\n",
		"add",
		"--config", cfgPath,
		"--url", "http://host:9091/transmission/rpc",
		"--api-key-stdin",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--api-key-stdin only applies to a Prowlarr server")
	assert.Contains(t, err.Error(), "--password-stdin")
}

func TestServerAddRejectsPasswordStdinForAProwlarrServer(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runServer(t, "pw\n",
		"add",
		"--config", cfgPath,
		"--type", "prowlarr",
		"--url", "http://prowlarr:9696",
		"--password-stdin",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--password-stdin does not apply to a Prowlarr server")
	assert.Contains(t, err.Error(), "--api-key-stdin")
}

func TestServerAddRejectsUsernameForAProwlarrServer(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runServer(t, "k\n",
		"add",
		"--config", cfgPath,
		"--type", "prowlarr",
		"--url", "http://prowlarr:9696",
		"--username", "alice",
		"--api-key-stdin",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--username does not apply to a Prowlarr server")
	assert.Contains(t, err.Error(), "API key")
}

// TestServerAddValidatesFlagsBeforeReadingTheSecret proves the combination check
// runs ahead of everything else: the run is rejected with a URL that would also
// have failed, so the flag error is the one that surfaces and nothing is read
// from stdin or written to disk.
func TestServerAddValidatesFlagsBeforeReadingTheSecret(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runServer(t, "k\n",
		"add",
		"--config", cfgPath,
		"--type", "prowlarr",
		"--password-stdin",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--password-stdin does not apply to a Prowlarr server")
	assert.NotContains(t, err.Error(), "--url is required")

	_, loadErr := config.Load(cfgPath)
	require.ErrorIs(t, loadErr, config.ErrNotConfigured)
}

func TestServerAddProwlarrEmptyAPIKeyWarns(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	out, errOut, err := runServer(t, "\n",
		"add",
		"--config", cfgPath,
		"--type", "prowlarr",
		"--url", "http://prowlarr:9696",
		"--api-key-stdin",
	)
	require.NoError(t, err)
	assert.Contains(t, out, "no API key stored")
	assert.Contains(t, errOut, "search commands will fail")
	assert.Contains(t, errOut, envAPIKey)

	// The server is registered even so; only the secret is missing.
	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, config.TypeProwlarr, cfg.Servers["prowlarr:9696"].ResolvedType())

	_, getErr := secrets.Get("http://prowlarr:9696")
	require.ErrorIs(t, getErr, secrets.ErrNotFound)
}

func TestServerAddProwlarrRejectsEmbeddedCredentials(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runServer(t, "k\n",
		"add",
		"--config", cfgPath,
		"--type", "prowlarr",
		"--url", "http://user:pass@prowlarr:9696",
		"--api-key-stdin",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedded credentials")
}

func TestServerAddRefusesToChangeAnAliasTypeImplicitly(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"indexers": {URL: "http://prowlarr:9696", Type: config.TypeProwlarr},
		},
		DefaultProwlarrServer: "indexers",
	}, cfgPath))

	_, _, err := runServer(t, "pw\n",
		"add",
		"--config", cfgPath,
		"--name", "indexers",
		"--url", "http://prowlarr:9696",
		"--password-stdin",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `server "indexers" is already a Prowlarr server`)
	assert.Contains(t, err.Error(), "--type prowlarr")

	// The registered type survives the refusal.
	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, config.TypeProwlarr, cfg.Servers["indexers"].ResolvedType())
}

func TestServerAddChangesAnAliasTypeWhenAsked(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"indexers": {URL: "http://prowlarr:9696", Type: config.TypeProwlarr},
		},
		DefaultProwlarrServer: "indexers",
	}, cfgPath))

	_, _, err := runServer(t, "pw\n",
		"add",
		"--config", cfgPath,
		"--name", "indexers",
		"--type", "transmission",
		"--url", "http://host:9091/transmission/rpc",
		"--password-stdin",
	)
	require.NoError(t, err)

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, config.TypeTransmission, cfg.Servers["indexers"].ResolvedType())
}

// TestSecretFromEnvIsScopedToServerType proves the two read overrides never
// cross: a Transmission password must not become the credential offered to a
// Prowlarr instance, or the reverse.
func TestSecretFromEnvIsScopedToServerType(t *testing.T) {
	t.Setenv(envPassword, "pw")
	t.Setenv(envAPIKey, "key")

	password, ok := secretFromEnv(config.Server{})
	require.True(t, ok)
	assert.Equal(t, "pw", password)

	apiKey, ok := secretFromEnv(config.Server{Type: config.TypeProwlarr})
	require.True(t, ok)
	assert.Equal(t, "key", apiKey)
}

func TestSecretFromEnvIgnoresTheOtherTypesVariable(t *testing.T) {
	t.Setenv(envPassword, "pw")
	t.Setenv(envAPIKey, "")

	_, ok := secretFromEnv(config.Server{Type: config.TypeProwlarr})
	assert.False(t, ok)

	t.Setenv(envPassword, "")
	t.Setenv(envAPIKey, "key")

	_, ok = secretFromEnv(config.Server{})
	assert.False(t, ok)
}

func TestDialProwlarrSendsTheStoredAPIKey(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	prowlarrURL, sentKey := newProwlarrStub(t)

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"indexers": {URL: prowlarrURL, Type: config.TypeProwlarr},
		},
		DefaultProwlarrServer: "indexers",
	}, cfgPath))
	require.NoError(t, secrets.Set(prowlarrURL, "stored-key"))

	client, dialedURL, err := dialProwlarr(dialCmd(t, cfgPath), "")
	require.NoError(t, err)
	assert.Equal(t, prowlarrURL, dialedURL)

	status, err := client.SystemStatus(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "Prowlarr", status.AppName)
	assert.Equal(t, "stored-key", *sentKey)
}

func TestDialProwlarrPrefersTheAPIKeyVariable(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	prowlarrURL, sentKey := newProwlarrStub(t)

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"indexers": {URL: prowlarrURL, Type: config.TypeProwlarr},
		},
		DefaultProwlarrServer: "indexers",
	}, cfgPath))
	require.NoError(t, secrets.Set(prowlarrURL, "stored-key"))

	t.Setenv(envAPIKey, "env-key")

	client, _, err := dialProwlarr(dialCmd(t, cfgPath), "")
	require.NoError(t, err)

	_, err = client.SystemStatus(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "env-key", *sentKey)
}

// TestDialProwlarrIgnoresThePasswordVariable is the read-path half of the
// scoping rule: an exported SNATCHER_PASSWORD, set for a Transmission server,
// must never be sent to a Prowlarr instance as its API key.
func TestDialProwlarrIgnoresThePasswordVariable(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	prowlarrURL, sentKey := newProwlarrStub(t)

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"indexers": {URL: prowlarrURL, Type: config.TypeProwlarr},
		},
		DefaultProwlarrServer: "indexers",
	}, cfgPath))

	t.Setenv(envPassword, "transmission-password")

	client, _, err := dialProwlarr(dialCmd(t, cfgPath), "")
	require.NoError(t, err)

	_, err = client.SystemStatus(t.Context())
	require.NoError(t, err)
	assert.Empty(t, *sentKey)
}

func TestDialProwlarrHonorsTheServerEnvironmentVariable(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	prowlarrURL, _ := newProwlarrStub(t)

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"other":    {URL: "http://other:9696", Type: config.TypeProwlarr},
			"indexers": {URL: prowlarrURL, Type: config.TypeProwlarr},
		},
		DefaultProwlarrServer: "other",
	}, cfgPath))

	t.Setenv(envServer, "indexers")

	_, dialedURL, err := dialProwlarr(dialCmd(t, cfgPath), "")
	require.NoError(t, err)
	assert.Equal(t, prowlarrURL, dialedURL)
}

func TestDialProwlarrWithNoProwlarrServerConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"local": {URL: "http://local:9091/rpc"}},
		DefaultServer: "local",
	}, cfgPath))

	_, _, err := dialProwlarr(dialCmd(t, cfgPath), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no Prowlarr server configured")
	assert.Contains(t, err.Error(), "--type prowlarr")
}

func TestDialProwlarrWithNoDefaultProwlarrServer(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"local": {URL: "http://local:9091/rpc"},
			"one":   {URL: "http://one:9696", Type: config.TypeProwlarr},
			"two":   {URL: "http://two:9696", Type: config.TypeProwlarr},
		},
		DefaultServer: "local",
	}, cfgPath))

	_, _, err := dialProwlarr(dialCmd(t, cfgPath), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no default Prowlarr server set")
	assert.Contains(t, err.Error(), "snatcher server default <alias>")

	// Only the Prowlarr aliases are offered, not every configured server.
	assert.Contains(t, err.Error(), "one, two")
	assert.NotContains(t, err.Error(), "local")
}

func TestDialProwlarrRejectsATransmissionAlias(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"local": {URL: "http://local:9091/rpc"}},
		DefaultServer: "local",
	}, cfgPath))

	_, _, err := dialProwlarr(dialCmd(t, cfgPath), "local")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `server "local" is a Transmission server`)
	assert.Contains(t, err.Error(), "needs a Prowlarr server")
}

func TestDialRejectsAProwlarrAlias(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"indexers": {URL: "http://prowlarr:9696", Type: config.TypeProwlarr},
		},
		DefaultProwlarrServer: "indexers",
	}, cfgPath))

	_, _, err := dial(dialCmd(t, cfgPath), "indexers")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `server "indexers" is a Prowlarr server`)
	assert.Contains(t, err.Error(), "needs a Transmission server")
	assert.Contains(t, err.Error(), "pass --server <alias>")
}

// TestDialTypeMismatchNamesTheEnvironmentVariable covers the case an operator is
// least likely to guess: the offending alias came from an exported
// SNATCHER_SERVER, not from anything on the command line.
func TestDialTypeMismatchNamesTheEnvironmentVariable(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"local":    {URL: "http://local:9091/rpc"},
			"indexers": {URL: "http://prowlarr:9696", Type: config.TypeProwlarr},
		},
		DefaultServer: "local",
	}, cfgPath))

	t.Setenv(envServer, "indexers")

	_, _, err := dial(dialCmd(t, cfgPath), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `server "indexers" is a Prowlarr server`)
	assert.Contains(t, err.Error(), envServer)
}

func TestDialProwlarrNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := dialProwlarr(dialCmd(t, cfgPath), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}
