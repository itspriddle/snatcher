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

	want := Config{ServerURL: "http://localhost:9091/transmission/rpc", Username: "alice"}

	require.NoError(t, Save(want, path))

	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestSaveCreatesParentDirsAndEnforcesPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits differ on windows")
	}

	dir := filepath.Join(t.TempDir(), "snatcher")
	path := filepath.Join(dir, "config.json")

	require.NoError(t, Save(Config{ServerURL: "http://example"}, path))

	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(dirPerm), dirInfo.Mode().Perm())

	fileInfo, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(filePerm), fileInfo.Mode().Perm())
}

func TestSaveWritesNoSecretField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, Save(Config{ServerURL: "http://example", Username: "bob"}, path))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var on map[string]any
	require.NoError(t, json.Unmarshal(raw, &on))

	for _, secretKey := range []string{"password", "secret", "token"} {
		_, exists := on[secretKey]
		assert.Falsef(t, exists, "config file must not contain %q", secretKey)
	}

	assert.Equal(t, "http://example", on["server_url"])
	assert.Equal(t, "bob", on["username"])
}

func TestLoadMissingFileReturnsZeroConfigAndNotConfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")

	got, err := Load(path)
	require.ErrorIs(t, err, ErrNotConfigured)
	assert.Equal(t, Config{}, got)
}

func TestLoadEnvOverridesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, Save(Config{ServerURL: "http://from-file", Username: "file-user"}, path))

	t.Setenv("SNATCHER_SERVER_URL", "http://from-env")

	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "http://from-env", got.ServerURL, "env var should override file value")
	assert.Equal(t, "file-user", got.Username, "unset env var should fall back to file value")
}

func TestLoadDefaultPathHonorsXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	want := Config{ServerURL: "http://default-path", Username: "carol"}
	require.NoError(t, Save(want, ""))

	// Confirm it landed at the XDG-derived default path.
	defaultFile := filepath.Join(xdg, "snatcher", "config.json")
	_, statErr := os.Stat(defaultFile)
	require.NoError(t, statErr)

	got, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}
