package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultPathWithXDGSet(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	got, err := DefaultPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(xdg, "snatcher", "config.json"), got)
}

func TestDefaultPathWithXDGUnset(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")

	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Skip("windows is not supported")
	}
	t.Setenv("HOME", home)

	got, err := DefaultPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".config", "snatcher", "config.json"), got)
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")

	want := Config{
		Servers: map[string]Server{
			"local":   {URL: "http://localhost:9091/transmission/rpc", Username: "alice"},
			"seedbox": {URL: "https://seedbox.example:443/rpc", Username: "bob"},
		},
		DefaultServer: "local",
	}

	require.NoError(t, Save(want, path))

	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, want.Servers, got.Servers)
	assert.Equal(t, want.DefaultServer, got.DefaultServer)
}

func TestSaveLoadPreservesMixedCaseAlias(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	want := Config{
		Servers:       map[string]Server{"MixedCase": {URL: "http://example:9091/rpc", Username: "alice"}},
		DefaultServer: "MixedCase",
	}

	require.NoError(t, Save(want, path))

	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, want.Servers, got.Servers, "alias case must be preserved on round-trip")
	assert.Equal(t, "MixedCase", got.DefaultServer)
}

func TestSaveCreatesParentDirsAndEnforcesPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits differ on windows")
	}

	dir := filepath.Join(t.TempDir(), "snatcher")
	path := filepath.Join(dir, "config.json")

	cfg := Config{
		Servers:       map[string]Server{"local": {URL: "http://example"}},
		DefaultServer: "local",
	}
	require.NoError(t, Save(cfg, path))

	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(dirPerm), dirInfo.Mode().Perm())

	fileInfo, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(filePerm), fileInfo.Mode().Perm())
}

func TestSaveWritesNewSchemaAndNoLegacyOrSecretKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	cfg := Config{
		Servers:       map[string]Server{"local": {URL: "http://example", Username: "bob"}},
		DefaultServer: "local",
	}
	require.NoError(t, Save(cfg, path))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var on map[string]any
	require.NoError(t, json.Unmarshal(raw, &on))

	for _, secretKey := range []string{"password", "secret", "token"} {
		_, exists := on[secretKey]
		assert.Falsef(t, exists, "config file must not contain %q", secretKey)
	}

	for _, legacyKey := range []string{"server_url", "username"} {
		_, exists := on[legacyKey]
		assert.Falsef(t, exists, "config file must not contain legacy key %q", legacyKey)
	}

	assert.Equal(t, "local", on["default_server"])

	servers, ok := on["servers"].(map[string]any)
	require.True(t, ok, "servers must be an object")
	local, ok := servers["local"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "http://example", local["url"])
	assert.Equal(t, "bob", local["username"])
}

func TestLoadMigratesLegacyConfigOnRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	// A legacy on-disk file written by an older snatcher.
	legacy := `{"server_url":"http://Localhost:9091/transmission/rpc","username":"dave"}`
	require.NoError(t, os.WriteFile(path, []byte(legacy), filePerm))

	got, err := Load(path)
	require.NoError(t, err)

	// Keyed by the lowercased URL host (with port), set as the default.
	assert.Equal(t, "localhost:9091", got.DefaultServer)
	assert.Equal(t, map[string]Server{
		"localhost:9091": {URL: "http://Localhost:9091/transmission/rpc", Username: "dave"},
	}, got.Servers)
}

func TestLoadPrefersServersOverLegacyFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	// A file that has both the new servers map and stray legacy keys: the
	// servers map wins and migration does not run.
	mixed := `{"servers":{"a":{"url":"http://a","username":"x"}},"default_server":"a","server_url":"http://legacy","username":"y"}`
	require.NoError(t, os.WriteFile(path, []byte(mixed), filePerm))

	got, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, []string{"a"}, got.Names())
	assert.Equal(t, "a", got.DefaultServer)
}

func TestLoadMissingFileReturnsZeroConfigAndNotConfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")

	got, err := Load(path)
	require.ErrorIs(t, err, ErrNotConfigured)
	assert.Equal(t, Config{}, got)
}

func TestLoadRejectsDefaultServerWithoutMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	bad := `{"servers":{"a":{"url":"http://a"}},"default_server":"missing"}`
	require.NoError(t, os.WriteFile(path, []byte(bad), filePerm))

	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

func TestLoadDefaultPathHonorsXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	want := Config{
		Servers:       map[string]Server{"local": {URL: "http://default-path", Username: "carol"}},
		DefaultServer: "local",
	}
	require.NoError(t, Save(want, ""))

	// Confirm it landed at the XDG-derived default path.
	defaultFile := filepath.Join(xdg, "snatcher", "config.json")
	_, statErr := os.Stat(defaultFile)
	require.NoError(t, statErr)

	got, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, want.Servers, got.Servers)
	assert.Equal(t, want.DefaultServer, got.DefaultServer)
}

func TestResolveDefault(t *testing.T) {
	cfg := Config{
		Servers: map[string]Server{
			"local":   {URL: "http://local"},
			"seedbox": {URL: "http://seedbox"},
		},
		DefaultServer: "seedbox",
	}

	name, srv, err := Resolve(cfg, "")
	require.NoError(t, err)
	assert.Equal(t, "seedbox", name)
	assert.Equal(t, "http://seedbox", srv.URL)
}

func TestResolveNamed(t *testing.T) {
	cfg := Config{
		Servers: map[string]Server{
			"local":   {URL: "http://local"},
			"seedbox": {URL: "http://seedbox"},
		},
		DefaultServer: "seedbox",
	}

	name, srv, err := Resolve(cfg, "local")
	require.NoError(t, err)
	assert.Equal(t, "local", name)
	assert.Equal(t, "http://local", srv.URL)
}

func TestResolveUnknown(t *testing.T) {
	cfg := Config{
		Servers:       map[string]Server{"local": {URL: "http://local"}},
		DefaultServer: "local",
	}

	_, _, err := Resolve(cfg, "nope")
	require.ErrorIs(t, err, ErrUnknownServer)
	assert.Contains(t, err.Error(), "nope")
}

func TestResolveNoDefault(t *testing.T) {
	cfg := Config{
		Servers: map[string]Server{"local": {URL: "http://local"}},
	}

	_, _, err := Resolve(cfg, "")
	require.ErrorIs(t, err, ErrNoDefaultServer)
}

func TestNamesSortedAscending(t *testing.T) {
	cfg := Config{
		Servers: map[string]Server{
			"zulu":  {URL: "http://z"},
			"alpha": {URL: "http://a"},
			"mike":  {URL: "http://m"},
		},
	}

	assert.Equal(t, []string{"alpha", "mike", "zulu"}, cfg.Names())
}

func TestNamesEmpty(t *testing.T) {
	assert.Empty(t, Config{}.Names())
}
