package commands

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
	"github.com/itspriddle/snatcher/internal/secrets"
)

// stateDir points the file store at a temp directory for the duration of the
// test and returns the secrets file path it resolves to, so no test can write
// to the real XDG state directory.
func stateDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	return filepath.Join(dir, "snatcher", "secrets.json")
}

func TestParseStoreMode(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    storeMode
		wantErr bool
	}{
		{name: "keyring", raw: "keyring", want: storeKeyring},
		{name: "auto is a synonym for keyring", raw: "auto", want: storeKeyring},
		{name: "file", raw: "file", want: storeFile},
		{name: "none", raw: "none", want: storeNone},
		{name: "case insensitive", raw: "FILE", want: storeFile},
		{name: "surrounding whitespace", raw: "  none  ", want: storeNone},
		{name: "unknown value", raw: "vault", wantErr: true},
		{name: "typo is not a silent fallback", raw: "flie", wantErr: true},
		{name: "empty", raw: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseStoreMode(tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), `"keyring", "file", "none"`)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolveStoreModePrecedence(t *testing.T) {
	tests := []struct {
		name   string
		env    string
		field  string
		want   storeMode
		errHas string
	}{
		{name: "neither set defaults to the keyring", want: storeKeyring},
		{name: "config field alone", field: "file", want: storeFile},
		{name: "env var alone", env: "file", want: storeFile},
		{name: "env var overrides the config field", env: "none", field: "file", want: storeNone},
		{name: "env var overrides an implicit default", env: "file", want: storeFile},
		{name: "invalid env var", env: "vault", errHas: envSecretStore},
		{name: "invalid config field", field: "vault", errHas: "config secret_store"},
		{name: "env var is checked before the config field", env: "vault", field: "file", errHas: envSecretStore},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envSecretStore, tc.env)

			got, err := resolveStoreMode(config.Server{URL: "http://host:9091/rpc", SecretStore: tc.field})
			if tc.errHas != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errHas)
				assert.Contains(t, err.Error(), `"keyring", "file", "none"`)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolveStoreModeWithoutSecretStoreKeyInConfig(t *testing.T) {
	t.Setenv(envSecretStore, "")
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	// A config file written before the field existed must load and resolve to
	// the keyring exactly as it always did.
	legacy := `{"servers":{"local":{"url":"http://host:9091/rpc","username":"alice"}},"default_server":"local"}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(legacy), 0o600))

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)

	srv := cfg.Servers["local"]
	assert.Empty(t, srv.SecretStore)

	mode, err := resolveStoreMode(srv)
	require.NoError(t, err)
	assert.Equal(t, storeKeyring, mode)
}

func TestOpenStore(t *testing.T) {
	stateDir(t)

	keyringStore, err := openStore(storeKeyring)
	require.NoError(t, err)
	assert.IsType(t, keyringSecretStore{}, keyringStore)

	fileStore, err := openStore(storeFile)
	require.NoError(t, err)
	assert.IsType(t, &secrets.FileStore{}, fileStore)

	noneStore, err := openStore(storeNone)
	require.NoError(t, err)
	assert.IsType(t, noSecretStore{}, noneStore)

	_, err = openStore(storeMode("vault"))
	require.Error(t, err)
}

func TestNoSecretStoreHoldsNothing(t *testing.T) {
	var store secretStore = noSecretStore{}

	_, err := store.Get("http://host:9091/rpc")
	require.ErrorIs(t, err, secrets.ErrNotFound)

	require.ErrorIs(t, store.Delete("http://host:9091/rpc"), secrets.ErrNotFound)

	// A write is refused rather than accepted and dropped, so a password is
	// never silently discarded.
	err = store.Set("http://host:9091/rpc", "hunter2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be stored")
}

func TestServerAddPersistsStoreFlagAndWritesFileStore(t *testing.T) {
	t.Setenv(envSecretStore, "")
	secretsPath := stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	// No OS keyring at all: --store=file is the whole point of the flag.
	mockStoreUnavailable(t)

	out, errOut, err := runServer(t, "hunter2\n",
		"add",
		"--config", cfgPath,
		"--url", "http://host:9091/transmission/rpc",
		"--username", "alice",
		"--password-stdin",
		"--store", "file",
	)
	require.NoError(t, err)
	assert.Contains(t, out, "Saved credentials")
	assert.NotContains(t, out+errOut, "dbus-launch")

	// The choice is persisted, so later commands resolve the same store without
	// re-passing the flag.
	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, "file", cfg.Servers["host:9091"].SecretStore)

	store, storeErr := secrets.NewFileStore("")
	require.NoError(t, storeErr)
	password, getErr := store.Get("http://host:9091/transmission/rpc")
	require.NoError(t, getErr)
	assert.Equal(t, "hunter2", password)
	assert.FileExists(t, secretsPath)
}

func TestServerAddKeepsStoreWhenFlagOmitted(t *testing.T) {
	t.Setenv(envSecretStore, "")
	stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	keyring.MockInit()
	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"host:9091": {URL: "http://host:9091/rpc", Username: "alice", SecretStore: "file"}},
		DefaultServer: "host:9091",
	}, cfgPath))

	// Re-running "server add" to change the username must not silently move the
	// password to a different store.
	_, _, err := runServer(t, "",
		"add",
		"--config", cfgPath,
		"--url", "http://host:9091/rpc",
		"--username", "bob",
		"--password-stdin",
	)
	require.NoError(t, err)

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, "bob", cfg.Servers["host:9091"].Username)
	assert.Equal(t, "file", cfg.Servers["host:9091"].SecretStore)
}

func TestServerAddRejectsUnknownStore(t *testing.T) {
	t.Setenv(envSecretStore, "")
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	keyring.MockInit()

	_, _, err := runServer(t, "",
		"add",
		"--config", cfgPath,
		"--url", "http://host:9091/rpc",
		"--password-stdin",
		"--store", "vault",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"keyring", "file", "none"`)
	assert.NoFileExists(t, cfgPath)
}

func TestServerAddStoreNoneStoresNothing(t *testing.T) {
	t.Setenv(envSecretStore, "")
	secretsPath := stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	keyring.MockInit()

	out, _, err := runServer(t, "",
		"add",
		"--config", cfgPath,
		"--url", "http://host:9091/rpc",
		"--username", "alice",
		"--password-stdin",
		"--store", "none",
	)
	require.NoError(t, err)
	assert.Contains(t, out, "no password stored")

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, "none", cfg.Servers["host:9091"].SecretStore)
	assert.NoFileExists(t, secretsPath)
}

func TestServerAddStoreNoneRefusesAPassword(t *testing.T) {
	t.Setenv(envSecretStore, "")
	stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	keyring.MockInit()

	_, _, err := runServer(t, "hunter2\n",
		"add",
		"--config", cfgPath,
		"--url", "http://host:9091/rpc",
		"--password-stdin",
		"--store", "none",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be stored")

	// The refusal is atomic: a password that would be discarded leaves no
	// half-saved, silently password-less server behind.
	assert.NoFileExists(t, cfgPath)
}

// TestServerAddStoreMatrix walks every combination of store mode, password, and
// keyring reachability, asserting the exit status, whether config.json was
// written, and whether the plaintext file store was created. "keyring
// unreachable" is the headless-Linux box: the file and none modes must be
// indifferent to it, and only the keyring mode with a password to store may
// fail.
func TestServerAddStoreMatrix(t *testing.T) {
	const serverURL = "http://host:9091/transmission/rpc"

	tests := []struct {
		name string

		store     string
		password  string
		keyringUp bool

		wantErrHas      string
		wantOutHas      string
		wantSecretsFile bool
	}{
		{name: "keyring/no password/keyring up", store: "keyring", keyringUp: true, wantOutHas: "no password stored"},
		{name: "keyring/no password/keyring down", store: "keyring", wantOutHas: "no password stored"},
		{name: "keyring/password/keyring up", store: "keyring", password: "hunter2", keyringUp: true, wantOutHas: "Saved credentials"},
		{name: "keyring/password/keyring down", store: "keyring", password: "hunter2", wantErrHas: "--store=file"},

		{name: "file/no password/keyring up", store: "file", keyringUp: true, wantOutHas: "no password stored"},
		{name: "file/no password/keyring down", store: "file", wantOutHas: "no password stored"},
		{name: "file/password/keyring up", store: "file", password: "hunter2", keyringUp: true, wantOutHas: "plaintext", wantSecretsFile: true},
		{name: "file/password/keyring down", store: "file", password: "hunter2", wantOutHas: "plaintext", wantSecretsFile: true},

		{name: "none/no password/keyring up", store: "none", keyringUp: true, wantOutHas: "no password stored"},
		{name: "none/no password/keyring down", store: "none", wantOutHas: "no password stored"},
		{name: "none/password/keyring up", store: "none", password: "hunter2", keyringUp: true, wantErrHas: "would be discarded"},
		{name: "none/password/keyring down", store: "none", password: "hunter2", wantErrHas: "would be discarded"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envSecretStore, "")
			secretsPath := stateDir(t)
			cfgPath := filepath.Join(t.TempDir(), "config.json")

			if tc.keyringUp {
				keyring.MockInit()
			} else {
				mockStoreUnavailable(t)
			}
			resetStoreUnavailableWarning(t)

			stdin := tc.password
			if stdin != "" {
				stdin += "\n"
			}

			out, errOut, err := runServer(t, stdin,
				"add",
				"--config", cfgPath,
				"--url", serverURL,
				"--username", "alice",
				"--password-stdin",
				"--store", tc.store,
			)

			if tc.wantErrHas != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrHas)
				assert.NotContains(t, err.Error()+out+errOut, "dbus-launch")

				// Nothing at all is written when the password has nowhere to go.
				assert.NoFileExists(t, cfgPath)
				assert.NoFileExists(t, secretsPath)

				return
			}

			require.NoError(t, err)
			assert.Contains(t, out, tc.wantOutHas)
			assert.NotContains(t, out+errOut, "dbus-launch")

			cfg, loadErr := config.Load(cfgPath)
			require.NoError(t, loadErr)
			assert.Equal(t, serverURL, cfg.Servers["host:9091"].URL)

			if tc.wantSecretsFile {
				require.FileExists(t, secretsPath)

				store, storeErr := secrets.NewFileStore("")
				require.NoError(t, storeErr)
				stored, getErr := store.Get(serverURL)
				require.NoError(t, getErr)
				assert.Equal(t, tc.password, stored)

				return
			}

			// Only an explicit opt-in ever writes the plaintext file store.
			assert.NoFileExists(t, secretsPath)
		})
	}
}

func TestServerAddWritesNothingWhenPasswordHasNowhereToGo(t *testing.T) {
	t.Setenv(envSecretStore, "")
	secretsPath := stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	// A registry that already holds a server: the failed add must leave the file
	// byte-identical, not merely free of the new alias.
	keyring.MockInit()
	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"seedbox": {URL: "http://seedbox:9091/rpc", Username: "bob"}},
		DefaultServer: "seedbox",
	}, cfgPath))

	before, readErr := os.ReadFile(cfgPath)
	require.NoError(t, readErr)

	mockStoreUnavailable(t)
	resetStoreUnavailableWarning(t)

	out, errOut, err := runServer(t, "hunter2\n",
		"add",
		"--config", cfgPath,
		"--url", "http://host:9091/rpc",
		"--username", "alice",
		"--password-stdin",
	)
	require.Error(t, err)

	// The error names all three ways forward and no provider internal.
	msg := err.Error()
	assert.Contains(t, msg, "OS secret service")
	assert.Contains(t, msg, "--store=file")
	assert.Contains(t, msg, "plaintext")
	assert.Contains(t, msg, "0600")
	assert.Contains(t, msg, "--store=none")
	assert.NotContains(t, msg+out+errOut, "dbus-launch")

	after, readErr := os.ReadFile(cfgPath)
	require.NoError(t, readErr)
	assert.Equal(t, string(before), string(after))

	// The refusal must not have created the plaintext store as a side effect.
	assert.NoFileExists(t, secretsPath)
}

func TestServerAddFileStoreReportsPlaintextLocation(t *testing.T) {
	t.Setenv(envSecretStore, "")
	secretsPath := stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	mockStoreUnavailable(t)

	out, _, err := runServer(t, "hunter2\n",
		"add",
		"--config", cfgPath,
		"--url", "http://host:9091/rpc",
		"--password-stdin",
		"--store", "file",
	)
	require.NoError(t, err)

	// An operator who opted into plaintext is told exactly what is on disk.
	assert.Contains(t, out, "plaintext")
	assert.Contains(t, out, secretsPath)
	assert.Contains(t, out, "0600")
}

func TestKeyringModeNeverTouchesFileStore(t *testing.T) {
	t.Setenv(envSecretStore, "")
	secretsPath := stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	// The default mode is the keyring and only the keyring: an unreachable
	// keyring must not create, consult, or write the plaintext file store.
	mockStoreUnavailable(t)
	resetStoreUnavailableWarning(t)

	_, _, err := runServer(t, "",
		"add",
		"--config", cfgPath,
		"--url", "http://host:9091/rpc",
		"--username", "alice",
		"--password-stdin",
	)
	require.NoError(t, err)
	assert.NoFileExists(t, secretsPath)

	_, _, err = runServer(t, "", "list", "--config", cfgPath)
	require.NoError(t, err)
	assert.NoFileExists(t, secretsPath)
}

func TestServerListReadsResolvedStore(t *testing.T) {
	t.Setenv(envSecretStore, "")
	stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const fileURL = "http://filed:9091/rpc"
	const keyringURL = "http://keyed:9091/rpc"

	store, err := secrets.NewFileStore("")
	require.NoError(t, err)
	require.NoError(t, store.Set(fileURL, "hunter2"))

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"filed": {URL: fileURL, Username: "alice", SecretStore: "file"},
			"keyed": {URL: keyringURL, Username: "bob"},
		},
		DefaultServer: "filed",
	}, cfgPath))

	// With no keyring at all, the file-backed server still reports its secret.
	mockStoreUnavailable(t)

	out, _, err := runServer(t, "", "list", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var results []serverListResult
	require.NoError(t, json.Unmarshal([]byte(out), &results))
	require.Len(t, results, 2)
	assert.True(t, results[0].SecretStored, "file-backed server")
	assert.False(t, results[1].SecretStored, "keyring-backed server with no reachable keyring")
}

func TestServerRemoveDeletesFromResolvedStore(t *testing.T) {
	t.Setenv(envSecretStore, "")
	stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const url = "http://host:9091/rpc"

	store, err := secrets.NewFileStore("")
	require.NoError(t, err)
	require.NoError(t, store.Set(url, "hunter2"))

	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"host": {URL: url, Username: "alice", SecretStore: "file"}},
		DefaultServer: "host",
	}, cfgPath))

	mockStoreUnavailable(t)

	out, _, err := runServer(t, "", "remove", "--config", cfgPath)
	require.NoError(t, err)
	assert.Contains(t, out, "Removed server")

	_, getErr := store.Get(url)
	require.ErrorIs(t, getErr, secrets.ErrNotFound)
}

func TestDialUsesConfiguredFileStore(t *testing.T) {
	t.Setenv(envSecretStore, "")
	stateDir(t)

	user, password := authProbe(t, "file", "", "")
	assert.Equal(t, "alice", user)
	assert.Equal(t, "hunter2", password)
}

func TestDialEnvOverridesConfiguredStore(t *testing.T) {
	stateDir(t)

	// The config says "none", the environment says "file": the environment wins,
	// so the password stored in the file store is the one that is sent.
	user, password := authProbe(t, "none", "file", "")
	assert.Equal(t, "alice", user)
	assert.Equal(t, "hunter2", password)
}

func TestDialSendsNoPasswordInNoneMode(t *testing.T) {
	stateDir(t)

	// "none" reads nothing, even though the file store holds a password for this
	// exact URL.
	user, password := authProbe(t, "file", "none", "")
	assert.Equal(t, "alice", user)
	assert.Empty(t, password)
}

func TestPasswordFromEnv(t *testing.T) {
	tests := []struct {
		name   string
		env    string
		want   string
		wantOK bool
	}{
		{name: "set", env: "hunter2", want: "hunter2", wantOK: true},
		{name: "empty supplies nothing", env: ""},
		{name: "used verbatim", env: "  hunter2  ", want: "  hunter2  ", wantOK: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envPassword, tc.env)

			password, ok := passwordFromEnv()
			assert.Equal(t, tc.want, password)
			assert.Equal(t, tc.wantOK, ok)
		})
	}
}

func TestPasswordFromEnvUnset(t *testing.T) {
	// t.Setenv first so the variable is restored on cleanup however the process
	// inherited it.
	t.Setenv(envPassword, "hunter2")
	require.NoError(t, os.Unsetenv(envPassword))

	password, ok := passwordFromEnv()
	assert.Empty(t, password)
	assert.False(t, ok)
}

func TestDialPrefersEnvPasswordOverStore(t *testing.T) {
	stateDir(t)

	// The file store holds "hunter2" for this exact URL and the config names it,
	// so the environment value on the wire proves no store was consulted.
	user, password := authProbe(t, "file", "", "from-env")
	assert.Equal(t, "alice", user)
	assert.Equal(t, "from-env", password)
}

func TestDialUsesEnvPasswordInNoneMode(t *testing.T) {
	stateDir(t)

	// "none" plus SNATCHER_PASSWORD is the unattended configuration: nothing is
	// written to disk, and the password still reaches the server.
	user, password := authProbe(t, "none", "none", "from-env")
	assert.Equal(t, "alice", user)
	assert.Equal(t, "from-env", password)
}

func TestDialIgnoresEmptyEnvPassword(t *testing.T) {
	stateDir(t)

	// An empty SNATCHER_PASSWORD supplies nothing and defers to the store.
	user, password := authProbe(t, "file", "", "")
	assert.Equal(t, "alice", user)
	assert.Equal(t, "hunter2", password)
}

func TestDialEnvPasswordSkipsUnavailableStoreWarning(t *testing.T) {
	t.Setenv(envSecretStore, "")
	t.Setenv(envPassword, "from-env")
	stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const url = "http://host:9091/rpc"

	keyring.MockInit()
	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"host": {URL: url, Username: "alice"}},
		DefaultServer: "host",
	}, cfgPath))

	// The keyring is unreachable, but it is never consulted, so there is nothing
	// to warn about.
	mockStoreUnavailable(t)
	resetStoreUnavailableWarning(t)

	cmd := NewServerCmd()
	var errOut bytes.Buffer
	cmd.SetErr(&errOut)

	backend, dialedURL, err := dial(cmd, cfgPath, "")
	require.NoError(t, err)
	require.NotNil(t, backend)
	assert.Equal(t, url, dialedURL)
	assert.Empty(t, errOut.String())
}

func TestEnvPasswordNeverAppearsInOutput(t *testing.T) {
	const secret = "s3cr3t-from-env"

	t.Setenv(envSecretStore, "")
	t.Setenv(envPassword, secret)
	stateDir(t)
	keyring.MockInit()
	resetStoreUnavailableWarning(t)

	ok := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "6.0.0",
		responses: map[string]string{
			"session_get": `{"jsonrpc": "2.0", "result": {"version": "4.1.0", "rpc_version_semver": "6.0.0"}, "id": 1}`,
		},
	}.handler())
	defer ok.Close()

	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer denied.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"ok":     {URL: ok.URL, Username: "alice"},
			"denied": {URL: denied.URL, Username: "alice"},
		},
		DefaultServer: "ok",
	}, cfgPath))

	// Success path: the password authenticates the request and stays off both
	// streams, structured output included.
	out, errOut, err := runServer(t, "", "ping", "--config", cfgPath, "--json")
	require.NoError(t, err)
	assert.Contains(t, out, `"connected": true`)
	assert.NotContains(t, out+errOut, secret)

	// Failure path: a rejected credential must not be echoed back while
	// explaining itself.
	out, errOut, err = runServer(t, "", "ping", "denied", "--config", cfgPath, "--json")
	require.Error(t, err)
	assert.NotContains(t, out+errOut+err.Error(), secret)
}

// authProbe runs "server ping" against a stub Transmission server for a config
// whose sole server names configStore, with SNATCHER_SECRET_STORE set to envStore
// and SNATCHER_PASSWORD to envPass (each behaving as unset when empty) and the
// password "hunter2" pre-stored in the file store. It returns the Basic Auth
// credentials the request actually carried, which is the only externally visible
// proof of which password source dial read.
func authProbe(t *testing.T, configStore, envStore, envPass string) (string, string) {
	t.Helper()

	t.Setenv(envSecretStore, envStore)
	t.Setenv(envPassword, envPass)

	var gotUser, gotPassword string
	stub := transmissionStub{
		rpcVersionHeader: "6.0.0",
		responses: map[string]string{
			"session_get": `{"jsonrpc": "2.0", "result": {"version": "4.1.0", "rpc_version_semver": "6.0.0"}, "id": 1}`,
		},
	}.handler()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, password, ok := r.BasicAuth(); ok {
			gotUser, gotPassword = user, password
		}
		stub(w, r)
	}))
	defer srv.Close()

	store, err := secrets.NewFileStore("")
	require.NoError(t, err)
	require.NoError(t, store.Set(srv.URL, "hunter2"))

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"test": {URL: srv.URL, Username: "alice", SecretStore: configStore}},
		DefaultServer: "test",
	}, cfgPath))

	// No OS keyring: anything read had to come from the resolved store.
	mockStoreUnavailable(t)
	resetStoreUnavailableWarning(t)

	_, _, runErr := runServer(t, "", "ping", "--config", cfgPath, "--json")
	require.NoError(t, runErr)

	return gotUser, gotPassword
}
