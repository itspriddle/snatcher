package commands

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
	"github.com/itspriddle/snatcher/internal/secrets"
)

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

// runAuth executes the auth command tree with args, returning its stdout, the
// combined stderr, and the error from Execute.
func runAuth(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()

	root := NewAuthCmd()

	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)

	err := root.Execute()

	return out.String(), errOut.String(), err
}

func TestAuthSetStoresConfigAndKeyring(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	out, _, err := runAuth(t, "hunter2\n",
		"set",
		"--config", cfgPath,
		"--url", "http://localhost:9091/transmission/rpc",
		"--username", "alice",
		"--password-stdin",
	)
	require.NoError(t, err)
	assert.Contains(t, out, "Saved credentials")

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, "http://localhost:9091/transmission/rpc", cfg.ServerURL)
	assert.Equal(t, "alice", cfg.Username)

	// Password lands in the keyring, never in config.
	pw, getErr := secrets.Get(cfg.ServerURL)
	require.NoError(t, getErr)
	assert.Equal(t, "hunter2", pw)
}

func TestAuthSetTrimsTrailingNewline(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runAuth(t, "secret\r\n",
		"set",
		"--config", cfgPath,
		"--url", "http://host:9091/transmission/rpc",
		"--password-stdin",
	)
	require.NoError(t, err)

	pw, getErr := secrets.Get("http://host:9091/transmission/rpc")
	require.NoError(t, getErr)
	assert.Equal(t, "secret", pw)
}

func TestAuthSetRequiresURL(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runAuth(t, "pw\n", "set", "--config", cfgPath, "--password-stdin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--url is required")
}

func TestAuthSetRejectsInvalidURL(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runAuth(t, "pw\n",
		"set",
		"--config", cfgPath,
		"--url", "ftp://nope",
		"--password-stdin",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scheme must be http or https")
}

func TestAuthSetRejectsEmbeddedCredentials(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runAuth(t, "pw\n",
		"set",
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

func TestAuthSetRekeyRemovesOldSecret(t *testing.T) {
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

	_, _, err := runAuth(t, "newpw\n",
		"set",
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

func TestAuthSetDifferentNameKeepsOtherSecret(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const localURL = "http://local:9091/transmission/rpc"
	const remoteURL = "http://remote:9091/transmission/rpc"

	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"local": {URL: localURL, Username: "alice"}},
		DefaultServer: "local",
	}, cfgPath))
	require.NoError(t, secrets.Set(localURL, "localpw"))

	_, _, err := runAuth(t, "remotepw\n",
		"set",
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

func TestAuthSetFirstServerBecomesDefault(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runAuth(t, "pw\n",
		"set",
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

func TestAuthSetNameDefaultsToHost(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	out, _, err := runAuth(t, "pw\n",
		"set",
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

func TestAuthSetSameURLKeepsSecret(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const url = "http://host:9091/transmission/rpc"

	require.NoError(t, config.Save(config.Config{ServerURL: url, Username: "alice"}, cfgPath))
	require.NoError(t, secrets.Set(url, "oldpw"))

	_, _, err := runAuth(t, "newpw\n",
		"set",
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

func TestAuthSetEmptyPasswordStoresNoSecret(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	out, _, err := runAuth(t, "",
		"set",
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
	assert.Equal(t, "http://host:9091/transmission/rpc", cfg.ServerURL)
	assert.Equal(t, "alice", cfg.Username)

	_, getErr := secrets.Get(cfg.ServerURL)
	require.ErrorIs(t, getErr, secrets.ErrNotFound)
}

func TestAuthSetEmptyPasswordDropsExistingSecret(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const url = "http://host:9091/transmission/rpc"
	require.NoError(t, config.Save(config.Config{ServerURL: url, Username: "alice"}, cfgPath))
	require.NoError(t, secrets.Set(url, "oldpw"))

	_, _, err := runAuth(t, "",
		"set",
		"--config", cfgPath,
		"--url", url,
		"--password-stdin",
	)
	require.NoError(t, err)

	// Clearing the password removes the stored secret rather than reusing it.
	_, getErr := secrets.Get(url)
	require.ErrorIs(t, getErr, secrets.ErrNotFound)
}

func TestAuthListPlaintext(t *testing.T) {
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

	out, _, err := runAuth(t, "", "list", "--config", cfgPath)
	require.NoError(t, err)

	// Not a TTY in tests: tab-separated, header-less rows sorted by alias.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2)

	assert.Equal(t, "local\t"+localURL+"\talice\tyes\tyes", lines[0])
	assert.Equal(t, "seedbox\t"+seedboxURL+"\tbob\tno\tno", lines[1])
}

func TestAuthListJSON(t *testing.T) {
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

	out, _, err := runAuth(t, "", "list", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var results []authListResult
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

func TestAuthListEmpty(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	out, _, err := runAuth(t, "", "list", "--config", cfgPath, "--json")
	require.NoError(t, err)

	// An empty inventory serializes as "[]", never "null".
	assert.Equal(t, "[]\n", out)
}

func TestWriteAuthListTableEmpty(t *testing.T) {
	var buf bytes.Buffer
	writeAuthListTable(&buf, nil)

	assert.Equal(t, "No servers configured.", strings.TrimSpace(buf.String()))
}

func TestWriteAuthListTableHeader(t *testing.T) {
	rows := [][]string{
		{"local", "http://local:9091/rpc", "alice", "yes", "yes"},
	}

	var buf bytes.Buffer
	writeAuthListTable(&buf, rows)

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

func TestWriteAuthListPlaintextSanitizesControlChars(t *testing.T) {
	results := []authListResult{
		{
			Name:         "\x1b[2Jevil",
			ServerURL:    "http://h\x1bost:9091/rpc",
			Username:     "ali\x07ce",
			SecretStored: true,
			IsDefault:    true,
		},
	}

	// A bytes.Buffer is not a TTY, so this exercises the piped tab-separated path.
	cmd := NewAuthCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	writeAuthListPlaintext(cmd, results)

	out := buf.String()
	assert.NotContains(t, out, "\x1b", "raw ESC must not reach the plaintext output")
	assert.NotContains(t, out, "\x07", "raw BEL must not reach the plaintext output")
	assert.Contains(t, out, "[2Jevil", "visible text survives sanitization")
	assert.Contains(t, out, "http://host:9091/rpc")
	assert.Contains(t, out, "alice")
}

func TestAuthListJSONPreservesControlChars(t *testing.T) {
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

	jsonOut, _, err := runAuth(t, "", "list", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var results []authListResult
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &results))
	require.Len(t, results, 1)
	assert.Equal(t, "\x1b[2Jevil", results[0].Name, "JSON output preserves the raw name verbatim")

	// --json JSON-encodes the string, so the raw ESC byte is escaped rather than
	// emitted literally (and never stripped).
	assert.NotContains(t, jsonOut, "\x1b", "the raw ESC byte is JSON-escaped, not emitted literally")
	assert.Contains(t, jsonOut, "\\u001b", "the control char is preserved as a JSON escape, not dropped")

	jqOut, _, err := runAuth(t, "", "list", "--config", cfgPath, "--jq", ".[].name")
	require.NoError(t, err)
	var jqName string
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(jqOut)), &jqName))
	assert.Equal(t, "\x1b[2Jevil", jqName, "jq output preserves the raw name verbatim")

	// The plaintext (piped) path strips the escape from the same alias.
	plainOut, _, err := runAuth(t, "", "list", "--config", cfgPath)
	require.NoError(t, err)
	assert.NotContains(t, plainOut, "\x1b", "plaintext path strips the control sequence")
	assert.Contains(t, plainOut, "[2Jevil", "visible text survives on the plaintext path")
}

func TestAuthDefaultSwitches(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"local":   {URL: "http://local:9091/rpc", Username: "alice"},
			"seedbox": {URL: "http://seedbox:9091/rpc", Username: "bob"},
		},
		DefaultServer: "local",
	}, cfgPath))

	out, _, err := runAuth(t, "", "default", "seedbox", "--config", cfgPath)
	require.NoError(t, err)
	assert.Contains(t, out, "seedbox")

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, "seedbox", cfg.DefaultServer)
}

func TestAuthDefaultUnknownAlias(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"local": {URL: "http://local:9091/rpc"}},
		DefaultServer: "local",
	}, cfgPath))

	_, _, err := runAuth(t, "", "default", "nope", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown server "nope"`)
	assert.Contains(t, err.Error(), "local")

	// The existing default is left unchanged.
	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, "local", cfg.DefaultServer)
}

func TestAuthShowPlaintext(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{ServerURL: "http://host:9091/rpc", Username: "bob"}, cfgPath))
	require.NoError(t, secrets.Set("http://host:9091/rpc", "pw"))

	out, _, err := runAuth(t, "", "show", "--config", cfgPath)
	require.NoError(t, err)

	assert.Contains(t, out, "http://host:9091/rpc")
	assert.Contains(t, out, "bob")
	assert.Contains(t, out, cfgPath)
	assert.Contains(t, out, "Secret stored: yes")
	// Never leaks the secret value.
	assert.NotContains(t, out, "pw\n")
}

func TestAuthShowJSON(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{ServerURL: "http://host:9091/rpc", Username: "bob"}, cfgPath))

	out, _, err := runAuth(t, "", "show", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var result authShowResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, "host:9091", result.Name)
	assert.Equal(t, "http://host:9091/rpc", result.ServerURL)
	assert.Equal(t, "bob", result.Username)
	assert.Equal(t, cfgPath, result.ConfigPath)
	assert.False(t, result.SecretStored)
	assert.True(t, result.IsDefault)
	assert.True(t, result.Configured)
}

func TestAuthShowNamedServer(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"local":   {URL: "http://local:9091/rpc", Username: "alice"},
			"seedbox": {URL: "http://seedbox:9091/rpc", Username: "bob"},
		},
		DefaultServer: "local",
	}, cfgPath))

	out, _, err := runAuth(t, "", "show", "seedbox", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var result authShowResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, "seedbox", result.Name)
	assert.Equal(t, "http://seedbox:9091/rpc", result.ServerURL)
	assert.Equal(t, "bob", result.Username)
	// Naming a non-default server reports is_default false.
	assert.False(t, result.IsDefault)
	assert.True(t, result.Configured)
}

func TestAuthShowUnknownAlias(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"local": {URL: "http://local:9091/rpc"}},
		DefaultServer: "local",
	}, cfgPath))

	_, _, err := runAuth(t, "", "show", "nope", "--config", cfgPath, "--json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown server "nope"`)
	assert.Contains(t, err.Error(), "auth list")
}

func TestAuthShowNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	out, _, err := runAuth(t, "", "show", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var result authShowResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Empty(t, result.ServerURL)
	assert.False(t, result.Configured)
	assert.False(t, result.SecretStored)
}

func TestAuthTestJSONRPC(t *testing.T) {
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
	require.NoError(t, config.Save(config.Config{ServerURL: srv.URL, Username: "alice"}, cfgPath))
	require.NoError(t, secrets.Set(srv.URL, "pw"))

	out, _, err := runAuth(t, "", "test", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var result authTestResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.True(t, result.Connected)
	assert.Equal(t, "4.1.0 (ae226418eb)", result.Version)
	assert.Equal(t, "6.0.0", result.RPCVersionSemver)
	assert.Equal(t, "jsonrpc-2.0", result.Protocol)
}

func TestAuthTestNamedServer(t *testing.T) {
	keyring.MockInit()

	// Two servers; the alias picks which one is dialed. The default (local) is a
	// dead address, so a successful round-trip proves --positional alias reached
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

	out, _, err := runAuth(t, "", "test", "seedbox", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var result authTestResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.True(t, result.Connected)
	assert.Equal(t, seedbox.URL, result.ServerURL)
	assert.Equal(t, "4.1.0 (ae226418eb)", result.Version)
}

func TestAuthTestUnknownAlias(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"local": {URL: "http://local:9091/rpc"}},
		DefaultServer: "local",
	}, cfgPath))

	_, _, err := runAuth(t, "", "test", "nope", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown server "nope"`)
	assert.Contains(t, err.Error(), "auth list")
}

func TestAuthTestBespokePlaintext(t *testing.T) {
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
	require.NoError(t, config.Save(config.Config{ServerURL: srv.URL}, cfgPath))

	out, _, err := runAuth(t, "", "test", "--config", cfgPath)
	require.NoError(t, err)

	assert.Contains(t, out, "Connected to "+srv.URL)
	assert.Contains(t, out, "4.0.6 (38c164933e)")
	assert.Contains(t, out, "5.4.0")
	assert.Contains(t, out, "bespoke")
}

func TestAuthTestNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runAuth(t, "", "test", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestAuthTestConnectionFailure(t *testing.T) {
	keyring.MockInit()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{ServerURL: srv.URL}, cfgPath))

	_, _, err := runAuth(t, "", "test", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), srv.URL)
}

func TestAuthTestHTMLResponseHint(t *testing.T) {
	keyring.MockInit()
	srv := httptest.NewServer(transmissionStub{
		responses: map[string]string{
			"session-get": "<!DOCTYPE html><html><body>login</body></html>",
		},
	}.handler())
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{ServerURL: srv.URL}, cfgPath))

	_, _, err := runAuth(t, "", "test", "--config", cfgPath)
	require.Error(t, err)
	// A non-RPC endpoint that answers with HTML yields an actionable hint, not a
	// raw json syntax error.
	assert.Contains(t, err.Error(), "HTML page")
	assert.Contains(t, err.Error(), "/transmission/rpc")
}

func TestAuthRemoveDefaultClearsConfigAndKeyring(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{ServerURL: "http://host:9091/rpc", Username: "bob"}, cfgPath))
	require.NoError(t, secrets.Set("http://host:9091/rpc", "pw"))

	// Omitting the alias removes the configured default.
	out, _, err := runAuth(t, "", "remove", "--config", cfgPath)
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

func TestAuthRemoveNamedServer(t *testing.T) {
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
	out, _, err := runAuth(t, "", "remove", "seedbox", "--config", cfgPath)
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

func TestAuthRemoveDefaultRepointsToSoleRemaining(t *testing.T) {
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
	_, _, err := runAuth(t, "", "remove", "local", "--config", cfgPath)
	require.NoError(t, err)

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, "seedbox", cfg.DefaultServer)
}

func TestAuthRemoveDefaultClearsWhenMultipleRemain(t *testing.T) {
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
	_, _, err := runAuth(t, "", "remove", "local", "--config", cfgPath)
	require.NoError(t, err)

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Empty(t, cfg.DefaultServer)
}

func TestAuthRemoveUnknownAlias(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"local": {URL: "http://local:9091/rpc"}},
		DefaultServer: "local",
	}, cfgPath))

	_, _, err := runAuth(t, "", "remove", "nope", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown server "nope"`)
	assert.Contains(t, err.Error(), "auth list")

	// The configured server is left untouched.
	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	_, ok := cfg.Servers["local"]
	assert.True(t, ok)
}

func TestAuthRemoveWhenNothingStored(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runAuth(t, "", "remove", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no servers configured")
}

// compile-time guard that NewAuthCmd wires the documented subcommands.
func TestAuthSubcommands(t *testing.T) {
	cmd := NewAuthCmd()

	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
		// Every auth subcommand must accept --config.
		assert.NotNilf(t, c.Flags().Lookup("config"), "%s missing --config", c.Name())
	}

	for _, want := range []string{"set", "list", "default", "show", "test", "remove"} {
		assert.Truef(t, names[want], "missing subcommand %q", want)
	}
}
