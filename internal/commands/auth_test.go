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

	require.NoError(t, config.Save(config.Config{ServerURL: oldURL, Username: "alice"}, cfgPath))
	require.NoError(t, secrets.Set(oldURL, "oldpw"))

	_, _, err := runAuth(t, "newpw\n",
		"set",
		"--config", cfgPath,
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
	assert.Equal(t, newURL, cfg.ServerURL)
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
	assert.Equal(t, "http://host:9091/rpc", result.ServerURL)
	assert.Equal(t, "bob", result.Username)
	assert.Equal(t, cfgPath, result.ConfigPath)
	assert.False(t, result.SecretStored)
	assert.True(t, result.Configured)
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

func TestAuthRemoveClearsConfigAndKeyring(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{ServerURL: "http://host:9091/rpc", Username: "bob"}, cfgPath))
	require.NoError(t, secrets.Set("http://host:9091/rpc", "pw"))

	out, _, err := runAuth(t, "", "remove", "--config", cfgPath)
	require.NoError(t, err)
	assert.Contains(t, out, "Removed stored credentials")

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Empty(t, cfg.ServerURL)
	assert.Empty(t, cfg.Username)

	_, getErr := secrets.Get("http://host:9091/rpc")
	require.ErrorIs(t, getErr, secrets.ErrNotFound)
}

func TestAuthRemoveWhenNothingStored(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runAuth(t, "", "remove", "--config", cfgPath)
	require.NoError(t, err)
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

	for _, want := range []string{"set", "show", "test", "remove"} {
		assert.Truef(t, names[want], "missing subcommand %q", want)
	}
}
