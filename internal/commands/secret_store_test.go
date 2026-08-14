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

func TestRecordedStoreMode(t *testing.T) {
	tests := []struct {
		name   string
		env    string
		field  string
		want   storeMode
		errHas string
	}{
		{name: "no field defaults to the keyring", want: storeKeyring},
		{name: "the field is the answer", field: "file", want: storeFile},
		{name: "the environment is ignored", env: "none", field: "file", want: storeFile},
		{name: "the environment cannot supply a mode", env: "file", want: storeKeyring},
		{name: "the environment cannot rescue an invalid field", env: "file", field: "vault", errHas: "config secret_store"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envSecretStore, tc.env)

			got, err := recordedStoreMode(config.Server{URL: "http://host:9091/rpc", SecretStore: tc.field})
			if tc.errHas != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errHas)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestAddStoreMode(t *testing.T) {
	tests := []struct {
		name string

		storeFlag   string
		flagChanged bool
		env         string
		prev        string

		want   storeMode
		errHas string
	}{
		{name: "nothing set defaults to the keyring", want: storeKeyring},
		{name: "flag alone", storeFlag: "file", flagChanged: true, want: storeFile},
		{name: "env alone", env: "file", want: storeFile},
		{name: "previous server's store alone", prev: "file", want: storeFile},
		{name: "env overrides the previous store", env: "none", prev: "file", want: storeNone},
		{name: "flag overrides the env", storeFlag: "file", flagChanged: true, env: "none", want: storeFile},
		{name: "flag overrides both", storeFlag: "keyring", flagChanged: true, env: "file", prev: "none", want: storeKeyring},
		{name: "invalid flag", storeFlag: "vault", flagChanged: true, errHas: "invalid secret store"},
		{name: "invalid env", env: "vault", errHas: envSecretStore},
		{name: "invalid previous store", prev: "vault", errHas: "config secret_store"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envSecretStore, tc.env)

			got, err := addStoreMode(tc.storeFlag, tc.flagChanged, config.Server{
				URL:         "http://host:9091/rpc",
				SecretStore: tc.prev,
			})
			if tc.errHas != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errHas)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
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

func TestServerAddPersistsResolvedStoreWhenFlagOmitted(t *testing.T) {
	const serverURL = "http://host:9091/transmission/rpc"

	t.Setenv(envSecretStore, "file")
	secretsPath := stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	// No OS keyring at all, so the password can only have landed in the file
	// store the environment named.
	mockStoreUnavailable(t)
	resetStoreUnavailableWarning(t)

	out, errOut, err := runServer(t, "hunter2\n",
		"add",
		"--config", cfgPath,
		"--url", serverURL,
		"--username", "alice",
		"--password-stdin",
	)
	require.NoError(t, err)
	assert.Contains(t, out, "plaintext")
	assert.NotContains(t, out+errOut, "dbus-launch")

	store, storeErr := secrets.NewFileStore("")
	require.NoError(t, storeErr)
	password, getErr := store.Get(serverURL)
	require.NoError(t, getErr)
	assert.Equal(t, "hunter2", password)
	require.FileExists(t, secretsPath)

	// The store that took the password is the store recorded with the server,
	// not the default the flag would have left in place.
	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, "file", cfg.Servers["host:9091"].SecretStore)

	// So the next run, with the variable gone, still finds the password instead
	// of reporting an unreferenced plaintext credential as absent.
	t.Setenv(envSecretStore, "")

	listOut, _, listErr := runServer(t, "", "list", "--config", cfgPath, "--json")
	require.NoError(t, listErr)

	var results []serverListResult
	require.NoError(t, json.Unmarshal([]byte(listOut), &results))
	require.Len(t, results, 1)
	assert.True(t, results[0].SecretStored)
}

func TestServerAddStoreFlagWinsOverEnv(t *testing.T) {
	const serverURL = "http://host:9091/transmission/rpc"

	// The environment says "none", the command line says "file": a store named
	// on the command line outranks one exported in a shell, so the password is
	// stored rather than refused as undiscardable.
	t.Setenv(envSecretStore, "none")
	secretsPath := stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	mockStoreUnavailable(t)
	resetStoreUnavailableWarning(t)

	out, _, err := runServer(t, "hunter2\n",
		"add",
		"--config", cfgPath,
		"--url", serverURL,
		"--username", "alice",
		"--password-stdin",
		"--store", "file",
	)
	require.NoError(t, err)
	assert.Contains(t, out, "plaintext")

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, "file", cfg.Servers["host:9091"].SecretStore)

	store, storeErr := secrets.NewFileStore("")
	require.NoError(t, storeErr)
	password, getErr := store.Get(serverURL)
	require.NoError(t, getErr)
	assert.Equal(t, "hunter2", password)
	require.FileExists(t, secretsPath)
}

func TestServerAddStoreFlagWinsOverEnvWhenItRefuses(t *testing.T) {
	// The same precedence in the direction that fails: --store=keyring with no
	// reachable keyring must abort, even though the environment names a store
	// that would have taken the password.
	t.Setenv(envSecretStore, "file")
	secretsPath := stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	mockStoreUnavailable(t)
	resetStoreUnavailableWarning(t)

	out, errOut, err := runServer(t, "hunter2\n",
		"add",
		"--config", cfgPath,
		"--url", "http://host:9091/transmission/rpc",
		"--username", "alice",
		"--password-stdin",
		"--store", "keyring",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--store=file")
	assert.NotContains(t, err.Error()+out+errOut, "dbus-launch")

	// The refusal is still atomic, and the file store the environment named was
	// never written to as a consolation.
	assert.NoFileExists(t, cfgPath)
	assert.NoFileExists(t, secretsPath)
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
// written, which store it records, and whether the plaintext file store was
// created. "keyring
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

			// The store written to is the store recorded. A config naming a
			// different one leaves the password unreachable on the next run —
			// and, under "file", an orphaned plaintext credential on disk.
			assert.Equal(t, tc.store, cfg.Servers["host:9091"].SecretStore)

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

// TestServerAddRefusesAWorldReadableFileStore pins the third answer a store can
// give: not "no entry", not "unavailable", but a genuine error. A secrets.json
// left readable by anyone is the realistic case — FileStore.load refuses it
// rather than trusting or rewriting it — and "server add" must surface that
// error as it stands, naming the file and the mode, instead of folding it into
// the unavailable advice (which would tell an operator to install a secret
// service they already have) or swallowing it and writing anyway.
func TestServerAddRefusesAWorldReadableFileStore(t *testing.T) {
	t.Setenv(envSecretStore, "")
	secretsPath := stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	keyring.MockInit()

	// A secrets file already holding another server's password, widened to 0644
	// by a careless editor, a restore from backup, or a permissive umask.
	require.NoError(t, os.MkdirAll(filepath.Dir(secretsPath), 0o700))
	require.NoError(t, os.WriteFile(secretsPath, []byte("{\n  \"http://other:9091/rpc\": \"hunter1\"\n}\n"), 0o600))
	require.NoError(t, os.Chmod(secretsPath, 0o644))

	before, readErr := os.ReadFile(secretsPath)
	require.NoError(t, readErr)

	out, errOut, err := runServer(t, "hunter2\n",
		"add",
		"--config", cfgPath,
		"--url", "http://host:9091/rpc",
		"--username", "alice",
		"--password-stdin",
		"--store", "file",
	)
	require.Error(t, err)

	// The store's own diagnosis reaches the operator: which file, the mode it
	// has, and the mode it must have.
	msg := err.Error()
	assert.Contains(t, msg, secretsPath)
	assert.Contains(t, msg, "0644")
	assert.Contains(t, msg, "0600")
	assert.NotContains(t, msg, "install or start an OS secret service")
	assert.NotContains(t, msg+out+errOut, "dbus-launch")

	// Nothing is written: no server registered, and the widened file is left
	// exactly as it was rather than quietly rewritten at 0600.
	assert.NoFileExists(t, cfgPath)

	after, readErr := os.ReadFile(secretsPath)
	require.NoError(t, readErr)
	assert.Equal(t, string(before), string(after))

	info, statErr := os.Stat(secretsPath)
	require.NoError(t, statErr)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

// TestServerAddProbesTheStoreBeforeDroppingTheOldSecret is what the up-front
// probe buys on a genuine store error: the abort has to happen before the
// pre-clear, or moving a working server to a store that turns out to be
// unusable deletes the password it already had and puts nothing in its place.
// The write path repeats the same permission check, so only the ordering
// distinguishes a probed store from an unprobed one.
func TestServerAddProbesTheStoreBeforeDroppingTheOldSecret(t *testing.T) {
	t.Setenv(envSecretStore, "")
	secretsPath := stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	const serverURL = "http://host:9091/rpc"

	keyring.MockInit()

	// A working keyring-backed server.
	_, _, err := runServer(t, "hunter2\n",
		"add",
		"--config", cfgPath,
		"--url", serverURL,
		"--username", "alice",
		"--password-stdin",
	)
	require.NoError(t, err)

	before, readErr := os.ReadFile(cfgPath)
	require.NoError(t, readErr)

	// The file store it is being moved to cannot be read.
	require.NoError(t, os.MkdirAll(filepath.Dir(secretsPath), 0o700))
	require.NoError(t, os.WriteFile(secretsPath, []byte("{}\n"), 0o600))
	require.NoError(t, os.Chmod(secretsPath, 0o644))

	_, _, err = runServer(t, "hunter3\n",
		"add",
		"--config", cfgPath,
		"--url", serverURL,
		"--username", "alice",
		"--password-stdin",
		"--store", "file",
	)
	require.Error(t, err)

	// The server is exactly as it was: same config, same password, still usable.
	after, readErr := os.ReadFile(cfgPath)
	require.NoError(t, readErr)
	assert.Equal(t, string(before), string(after))

	stored, getErr := secrets.Get(serverURL)
	require.NoError(t, getErr)
	assert.Equal(t, "hunter2", stored)
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

// assertStoredPassword asserts what store holds for serverURL. An empty want
// means the store must hold no entry at all.
func assertStoredPassword(t *testing.T, store secretStore, serverURL, want string) {
	t.Helper()

	stored, err := store.Get(serverURL)
	if want == "" {
		require.ErrorIs(t, err, secrets.ErrNotFound)

		return
	}

	require.NoError(t, err)
	assert.Equal(t, want, stored)
}

// TestServerAddClearsSecretWhenStoreChanges pins the pre-clear on a store
// change, not only a URL change. Re-adding the same server with a different
// store used to report the new choice while leaving the old plaintext password
// readable in secrets.json — output contradicting the disk after an explicit
// opt-out.
//
// The keyring-to-file case is the likeliest real migration on a headless box,
// and the only direction where the store being cleared is the keyring.
func TestServerAddClearsSecretWhenStoreChanges(t *testing.T) {
	const serverURL = "http://host:9091/transmission/rpc"

	tests := []struct {
		name       string
		from       string
		to         string
		password   string
		wantOutHas string
	}{
		{name: "file to none", from: "file", to: "none", wantOutHas: "no password stored"},
		{name: "file to keyring", from: "file", to: "keyring", password: "hunter3", wantOutHas: "Saved credentials"},
		{name: "keyring to file", from: "keyring", to: "file", password: "hunter3", wantOutHas: "Saved credentials"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envSecretStore, "")
			stateDir(t)
			cfgPath := filepath.Join(t.TempDir(), "config.json")

			keyring.MockInit()
			resetStoreUnavailableWarning(t)

			// The server starts out in the store it is about to move off.
			_, _, err := runServer(t, "hunter2\n",
				"add",
				"--config", cfgPath,
				"--url", serverURL,
				"--username", "alice",
				"--password-stdin",
				"--store", tc.from,
			)
			require.NoError(t, err)

			fromStore, storeErr := openStore(storeMode(tc.from))
			require.NoError(t, storeErr)
			assertStoredPassword(t, fromStore, serverURL, "hunter2")

			stdin := tc.password
			if stdin != "" {
				stdin += "\n"
			}

			out, _, err := runServer(t, stdin,
				"add",
				"--config", cfgPath,
				"--url", serverURL,
				"--username", "alice",
				"--password-stdin",
				"--store", tc.to,
			)
			require.NoError(t, err)
			assert.Contains(t, out, tc.wantOutHas)

			// Nothing is left behind in the store the server just moved off.
			assertStoredPassword(t, fromStore, serverURL, "")

			cfg, loadErr := config.Load(cfgPath)
			require.NoError(t, loadErr)
			assert.Equal(t, tc.to, cfg.Servers["host:9091"].SecretStore)

			if tc.password != "" {
				toStore, openErr := openStore(storeMode(tc.to))
				require.NoError(t, openErr)
				assertStoredPassword(t, toStore, serverURL, tc.password)
			}
		})
	}
}

// TestServerAddClearsSecretUnderStoreOverride is the pre-clear's half of the
// same rule "server remove" follows: the store being left is the one the server
// itself names. With SNATCHER_SECRET_STORE exported, both sides of the
// comparison used to resolve to the overriding value, so nothing looked like a
// change and the plaintext password stayed in secrets.json.
func TestServerAddClearsSecretUnderStoreOverride(t *testing.T) {
	const serverURL = "http://host:9091/transmission/rpc"

	t.Setenv(envSecretStore, "")
	secretsPath := stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	keyring.MockInit()
	resetStoreUnavailableWarning(t)

	_, _, err := runServer(t, "hunter2\n",
		"add",
		"--config", cfgPath,
		"--url", serverURL,
		"--username", "alice",
		"--password-stdin",
		"--store", "file",
	)
	require.NoError(t, err)
	require.FileExists(t, secretsPath)

	fileStore, storeErr := secrets.NewFileStore("")
	require.NoError(t, storeErr)
	stored, getErr := fileStore.Get(serverURL)
	require.NoError(t, getErr)
	require.Equal(t, "hunter2", stored)

	// The environment moves this add to "none"; the secret it replaces still
	// lives in the file store the server was registered with.
	t.Setenv(envSecretStore, "none")

	out, _, err := runServer(t, "",
		"add",
		"--config", cfgPath,
		"--url", serverURL,
		"--username", "alice",
		"--password-stdin",
	)
	require.NoError(t, err)
	assert.Contains(t, out, "no password stored")

	_, getErr = fileStore.Get(serverURL)
	require.ErrorIs(t, getErr, secrets.ErrNotFound)

	cfg, loadErr := config.Load(cfgPath)
	require.NoError(t, loadErr)
	assert.Equal(t, string(storeNone), cfg.Servers["host:9091"].SecretStore)
}

func TestServerAddDropsTheSecretWhenSavingConfigFails(t *testing.T) {
	const serverURL = "http://host:9091/transmission/rpc"

	t.Setenv(envSecretStore, "")
	stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	keyring.MockInit()

	// A hand-edited config whose default_server names nothing: it loads (the
	// servers map is empty, so the check does not apply) but fails to save once
	// this add puts a server in the map. The secret has been written by then.
	broken := `{"servers":{},"default_server":"ghost"}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(broken), 0o600))

	_, _, err := runServer(t, "hunter2\n",
		"add",
		"--config", cfgPath,
		"--url", serverURL,
		"--username", "alice",
		"--password-stdin",
		"--store", "file",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "default_server")

	// The save failed, so nothing on disk names this server — its password must
	// not be left behind in plaintext for a server that does not exist.
	after, readErr := os.ReadFile(cfgPath)
	require.NoError(t, readErr)
	assert.JSONEq(t, broken, string(after))

	store, storeErr := secrets.NewFileStore("")
	require.NoError(t, storeErr)
	_, getErr := store.Get(serverURL)
	require.ErrorIs(t, getErr, secrets.ErrNotFound)
}

// TestServerAddSaveFailureRollbackMatchesTheRekeyRule pins both halves of the
// config.Save rollback guard, which must ask the same question the pre-clear
// asks.
//
// It has to fire whenever the add pointed the alias somewhere new — a changed
// store counts, not only a changed URL. Skipping the rollback there orphans the
// freshly written password for good: a later corrective add compares the same
// store to itself and never clears it.
//
// It must not fire otherwise. On a failed username edit the saved config still
// names this URL and this store, so the password just written is that server's
// live credential, and deleting it would leave a working server unable to
// authenticate.
//
// The rekeyed case also pins the other half of that consequence: the secret the
// alias had before must survive, because the config still names the store
// holding it. That is what forces the pre-clear to run only after a successful
// save — dropping it earlier leaves the server with no password in any store.
func TestServerAddSaveFailureRollbackMatchesTheRekeyRule(t *testing.T) {
	const (
		serverURL  = "http://host:9091/transmission/rpc"
		firstPass  = "hunter2"
		secondPass = "hunter3"
	)

	tests := []struct {
		name        string
		firstStore  string
		wantFile    string // "" means the file store must hold no entry
		wantKeyring string // "" means the keyring must hold no entry
	}{
		{
			name:        "store changed drops the new secret and keeps the old one",
			firstStore:  "keyring",
			wantFile:    "",
			wantKeyring: firstPass,
		},
		{
			name:        "same URL and store keeps the password the add wrote",
			firstStore:  "file",
			wantFile:    secondPass,
			wantKeyring: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envSecretStore, "")
			stateDir(t)
			cfgPath := filepath.Join(t.TempDir(), "config.json")

			keyring.MockInit()

			_, _, err := runServer(t, firstPass+"\n",
				"add",
				"--config", cfgPath,
				"--url", serverURL,
				"--username", "alice",
				"--password-stdin",
				"--store", tc.firstStore,
			)
			require.NoError(t, err)

			before, readErr := os.ReadFile(cfgPath)
			require.NoError(t, readErr)

			// A read-only config file still loads, so the second add gets all the
			// way to config.Save — with the secret already written — and fails
			// there.
			require.NoError(t, os.Chmod(cfgPath, 0o400))
			t.Cleanup(func() { _ = os.Chmod(cfgPath, 0o600) })

			// Same alias, same URL, edited username, and a password the first add
			// did not store — so neither expectation can be met by an add that
			// never reached writeServerSecret. Only the first add's store differs
			// between the cases, so this is a store change or it is not.
			_, _, err = runServer(t, secondPass+"\n",
				"add",
				"--config", cfgPath,
				"--url", serverURL,
				"--username", "bob",
				"--password-stdin",
				"--store", "file",
			)
			require.Error(t, err)

			after, readErr := os.ReadFile(cfgPath)
			require.NoError(t, readErr)
			assert.JSONEq(t, string(before), string(after), "the failed save leaves the registered server as it was")

			fileStore, storeErr := secrets.NewFileStore("")
			require.NoError(t, storeErr)

			assertStoredPassword(t, fileStore, serverURL, tc.wantFile)
			assertStoredPassword(t, keyringSecretStore{}, serverURL, tc.wantKeyring)
		})
	}
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

func TestKeyringModeNeverReadsExistingFileStore(t *testing.T) {
	secretsPath := stateDir(t)

	// The mixed box: another server opted into the file store, so secrets.json
	// already exists and holds a password for this very URL (authProbe stores
	// one). With the keyring unreachable, keyring mode must still send no
	// password — "never consults the file store" is stronger than "never
	// creates it", and only the credentials on the wire can tell them apart.
	user, password := authProbe(t, "keyring", "", "")
	assert.Equal(t, "alice", user)
	assert.Empty(t, password)

	// The file the probe wrote was there to be read the whole time.
	assert.FileExists(t, secretsPath)
}

func TestServerListReadsEachServersOwnStore(t *testing.T) {
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

// reportingConfig registers three servers covering both answers "server list"
// and "server show" can give and both stores they can come from: a file-mode
// server holding a password, a file-mode server holding none, and a
// keyring-mode server holding one. It returns the config path.
//
// Read through SNATCHER_SECRET_STORE=none every one of them reports "no
// secret"; read through the store each server itself names, only the middle one
// does.
func reportingConfig(t *testing.T) string {
	t.Helper()

	stateDir(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	fileStore, err := secrets.NewFileStore("")
	require.NoError(t, err)
	require.NoError(t, fileStore.Set("http://filed:9091/rpc", "hunter2"))

	keyring.MockInit()
	require.NoError(t, keyringSecretStore{}.Set("http://keyed:9091/rpc", "hunter3"))

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"filed": {URL: "http://filed:9091/rpc", Username: "alice", SecretStore: "file"},
			"empty": {URL: "http://empty:9091/rpc", Username: "bob", SecretStore: "file"},
			"keyed": {URL: "http://keyed:9091/rpc", Username: "carol"},
		},
		DefaultServer: "filed",
	}, cfgPath))

	return cfgPath
}

// TestServerListIgnoresStoreOverride pins that "server list" reports whether
// each server has a secret, not whether the store this invocation happens to
// resolve to has one. Under the documented unattended pattern
// (SNATCHER_SECRET_STORE=none plus SNATCHER_PASSWORD) it used to report
// secret_stored false for every server, with empty stderr, while plaintext
// passwords sat in secrets.json — the reporting half of the misreport
// "server remove" already closed.
func TestServerListIgnoresStoreOverride(t *testing.T) {
	t.Setenv(envSecretStore, "none")
	cfgPath := reportingConfig(t)

	out, errOut, err := runServer(t, "", "list", "--config", cfgPath, "--json")
	require.NoError(t, err)
	assert.Empty(t, errOut)

	var results []serverListResult
	require.NoError(t, json.Unmarshal([]byte(out), &results))
	require.Len(t, results, 3)

	// Sorted by alias: empty, filed, keyed.
	assert.False(t, results[0].SecretStored, "file-mode server with no stored password")
	assert.True(t, results[1].SecretStored, "file-mode server whose password is on disk")
	assert.True(t, results[2].SecretStored, "keyring-mode server whose password is in the keyring")
}

// TestServerShowIgnoresStoreOverride is TestServerListIgnoresStoreOverride for
// the other reporting path: both answer the same question and now share one
// helper, so both need pinning against the override.
func TestServerShowIgnoresStoreOverride(t *testing.T) {
	t.Setenv(envSecretStore, "none")
	cfgPath := reportingConfig(t)

	tests := []struct {
		alias string
		want  bool
	}{
		{alias: "empty", want: false},
		{alias: "filed", want: true},
		{alias: "keyed", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.alias, func(t *testing.T) {
			out, errOut, err := runServer(t, "", "show", tc.alias, "--config", cfgPath, "--local", "--json")
			require.NoError(t, err)
			assert.Empty(t, errOut)

			var result serverShowResult
			require.NoError(t, json.Unmarshal([]byte(out), &result))
			assert.Equal(t, tc.want, result.SecretStored)
		})
	}
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

// TestServerRemoveIgnoresStoreOverride pins that removal cleans up the store the
// server itself names. Under SNATCHER_SECRET_STORE=none the delete used to go to
// the "none" store, which reports "no entry" for everything, so removing a
// file-backed server printed success with an empty stderr and left the plaintext
// password readable in secrets.json.
func TestServerRemoveIgnoresStoreOverride(t *testing.T) {
	t.Setenv(envSecretStore, "none")
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

	out, errOut, err := runServer(t, "", "remove", "--config", cfgPath)
	require.NoError(t, err)
	assert.Contains(t, out, "Removed server")
	assert.Empty(t, errOut)

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

func TestServerAddIgnoresEnvPassword(t *testing.T) {
	const serverURL = "http://host:9091/transmission/rpc"

	tests := []struct {
		name  string
		store string
	}{
		{name: "keyring", store: "keyring"},
		{name: "file", store: "file"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envSecretStore, "")
			t.Setenv(envPassword, "from-env")
			secretsPath := stateDir(t)
			cfgPath := filepath.Join(t.TempDir(), "config.json")

			keyring.MockInit()

			// SNATCHER_PASSWORD is a read override, never a source for "server
			// add": an empty typed password means this server has no stored
			// secret, whatever the environment holds. A regression here would
			// silently persist an env-supplied password.
			out, errOut, err := runServer(t, "",
				"add",
				"--config", cfgPath,
				"--url", serverURL,
				"--username", "alice",
				"--store", tc.store,
				"--password-stdin",
			)
			require.NoError(t, err)
			assert.Contains(t, out, "no password stored")
			assert.NotContains(t, out+errOut, "from-env")

			// Neither store took the value: not the keyring...
			_, getErr := secrets.Get(serverURL)
			require.ErrorIs(t, getErr, secrets.ErrNotFound)

			// ...and not the file store, which was never even created.
			assert.NoFileExists(t, secretsPath)
		})
	}
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
