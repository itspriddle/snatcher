package config

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

func TestSaveWritesNewSchemaAndNoSecretKeys(t *testing.T) {
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

	assert.Equal(t, "local", on["default_server"])

	servers, ok := on["servers"].(map[string]any)
	require.True(t, ok, "servers must be an object")
	local, ok := servers["local"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "http://example", local["url"])
	assert.Equal(t, "bob", local["username"])
}

func TestSaveLoadRoundTripsSecretStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	cfg := Config{
		Servers:       map[string]Server{"local": {URL: "http://example", Username: "bob", SecretStore: "file"}},
		DefaultServer: "local",
	}
	require.NoError(t, Save(cfg, path))

	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "file", got.Servers["local"].SecretStore)
}

func TestSaveOmitsEmptySecretStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	cfg := Config{
		Servers:       map[string]Server{"local": {URL: "http://example", Username: "bob"}},
		DefaultServer: "local",
	}
	require.NoError(t, Save(cfg, path))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	// The default store is expressed by the field's absence, so a config file
	// written without one is byte-identical to what earlier versions wrote.
	assert.NotContains(t, string(raw), "secret_store")
}

func TestLoadWithoutSecretStoreLeavesItEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	// A config file written before the field existed must load unchanged; an
	// empty value means the default store (resolved at the command layer).
	legacy := `{"servers":{"local":{"url":"http://example","username":"bob"}},"default_server":"local"}`
	require.NoError(t, os.WriteFile(path, []byte(legacy), filePerm))

	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, Server{URL: "http://example", Username: "bob"}, got.Servers["local"])
	assert.Empty(t, got.Servers["local"].SecretStore)
}

func TestLoadDoesNotMigrateLegacyKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	// An old flat single-server file (no servers map) is not migrated: none of
	// its keys populate the modeled fields. They are preserved verbatim rather
	// than dropped (see TestSavePreservesUnknownTopLevelKeys).
	legacy := `{"server_url":"http://Localhost:9091/transmission/rpc","username":"dave"}`
	require.NoError(t, os.WriteFile(path, []byte(legacy), filePerm))

	got, err := Load(path)
	require.NoError(t, err)
	assert.Empty(t, got.Servers)
	assert.Empty(t, got.DefaultServer)
	assert.True(t, got.Client.IsZero())
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

// ptr returns a pointer to v. Every client key is a pointer so that an unset key
// stays distinguishable from one set to its zero value.
func ptr[T any](v T) *T {
	return &v
}

// clientObject decodes the client object out of the file at path. It fails the
// test when the file is unreadable or the client key is absent.
func clientObject(t *testing.T, path string) map[string]any {
	t.Helper()

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var on map[string]any
	require.NoError(t, json.Unmarshal(raw, &on))

	client, ok := on["client"].(map[string]any)
	require.True(t, ok, "client must be an object, got %v", on["client"])

	return client
}

func TestSaveLoadRoundTripsEveryClientKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	want := Client{
		Theme:        ptr("dracula"),
		NerdFont:     ptr(false),
		PollInterval: ptr("1m30s"),
		ListColumns:  ptr([]string{"id", "name", "ratio"}),
		ListSort:     ptr("added"),
		ListReverse:  ptr(true),
	}

	require.NoError(t, Save(Config{Client: want}, path))

	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, want, got.Client)
}

func TestSaveWritesClientKeysWithTheirNaturalJSONTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	cfg := Config{Client: Client{
		Theme:        ptr("nord"),
		NerdFont:     ptr(false),
		PollInterval: ptr("5s"),
		ListColumns:  ptr([]string{"id", "name"}),
		ListSort:     ptr("queue"),
		ListReverse:  ptr(true),
	}}
	require.NoError(t, Save(cfg, path))

	client := clientObject(t, path)
	assert.Equal(t, "nord", client["theme"])
	assert.Equal(t, false, client["nerd_font"])
	assert.Equal(t, "5s", client["poll_interval"])
	assert.Equal(t, []any{"id", "name"}, client["list_columns"])
	assert.Equal(t, "queue", client["list_sort"])
	assert.Equal(t, true, client["list_reverse"])

	// The client section holds settings only — never a credential of any kind.
	assert.ElementsMatch(t, clientKeys, slices.Collect(maps.Keys(client)))
}

func TestSaveDistinguishesExplicitZeroFromUnsetClientKey(t *testing.T) {
	dir := t.TempDir()

	explicit := filepath.Join(dir, "explicit.json")
	require.NoError(t, Save(Config{Client: Client{NerdFont: ptr(false), ListReverse: ptr(false)}}, explicit))

	got, err := Load(explicit)
	require.NoError(t, err)
	require.NotNil(t, got.Client.NerdFont, "an explicit false must survive the round trip")
	assert.False(t, *got.Client.NerdFont)
	require.NotNil(t, got.Client.ListReverse)
	assert.False(t, *got.Client.ListReverse)

	// Re-saving what was loaded must not turn the explicit false into an absent
	// key, which would silently promote it back to the default.
	require.NoError(t, Save(got, explicit))
	client := clientObject(t, explicit)
	assert.Equal(t, false, client["nerd_font"])

	unset := filepath.Join(dir, "unset.json")
	require.NoError(t, Save(Config{Client: Client{Theme: ptr("nord")}}, unset))

	fromUnset, err := Load(unset)
	require.NoError(t, err)
	assert.Nil(t, fromUnset.Client.NerdFont, "an absent key must load as unset, not as false")
	assert.NotContains(t, clientObject(t, unset), "nerd_font")
}

func TestSaveOmitsAnEmptyClientSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	cfg := Config{
		Servers:       map[string]Server{"local": {URL: "http://example", Username: "bob"}},
		DefaultServer: "local",
	}
	require.NoError(t, Save(cfg, path))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	// An operator who sets nothing keeps the file a version that predates the
	// client section would have written.
	assert.NotContains(t, string(raw), "client")
}

func TestSaveUnsettingAClientKeyRemovesItFromTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, Save(Config{Client: Client{Theme: ptr("nord"), ListSort: ptr("added")}}, path))

	cfg, err := Load(path)
	require.NoError(t, err)
	cfg.Client.Theme = nil
	require.NoError(t, Save(cfg, path))

	client := clientObject(t, path)
	assert.NotContains(t, client, "theme")
	assert.Equal(t, "added", client["list_sort"])
}

func TestSavePreservesUnknownTopLevelKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	hand := `{
  "servers": {"local": {"url": "http://example", "username": "bob"}},
  "default_server": "local",
  "future_feature": {"enabled": true, "note": "written by a newer snatcher"},
  "operator_note": "do not delete"
}`
	require.NoError(t, os.WriteFile(path, []byte(hand), filePerm))

	cfg, err := Load(path)
	require.NoError(t, err)
	cfg.Client.Theme = ptr("dracula")
	require.NoError(t, Save(cfg, path))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var on map[string]any
	require.NoError(t, json.Unmarshal(raw, &on))

	assert.Equal(t, map[string]any{"enabled": true, "note": "written by a newer snatcher"}, on["future_feature"])
	assert.Equal(t, "do not delete", on["operator_note"])
	assert.Equal(t, "local", on["default_server"])
}

func TestSavePreservesUnknownClientKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	hand := `{
  "servers": {},
  "default_server": "",
  "client": {"theme": "nord", "date_format": "iso8601", "future_toggle": false}
}`
	require.NoError(t, os.WriteFile(path, []byte(hand), filePerm))

	cfg, err := Load(path)
	require.NoError(t, err)
	cfg.Client.ListSort = ptr("added")
	require.NoError(t, Save(cfg, path))

	client := clientObject(t, path)
	assert.Equal(t, "iso8601", client["date_format"])
	assert.Equal(t, false, client["future_toggle"])
	assert.Equal(t, "nord", client["theme"])
	assert.Equal(t, "added", client["list_sort"])
}

func TestSaveKeepsAnUnknownClientKeyAsTheOnlyClientContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	hand := `{"servers":{},"default_server":"","client":{"date_format":"iso8601"}}`
	require.NoError(t, os.WriteFile(path, []byte(hand), filePerm))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.False(t, cfg.Client.IsZero(), "a preserved unknown key makes the section non-empty")
	require.NoError(t, Save(cfg, path))

	assert.Equal(t, map[string]any{"date_format": "iso8601"}, clientObject(t, path))
}

func TestSaveCanonicalizesACaseVariantClientKeyInsteadOfDuplicatingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	// encoding/json matches struct fields case-insensitively, so "Theme" lands in
	// Client.Theme. It must not also be kept as an unknown key, or the file ends
	// up with both spellings and the shadow copy outlives every unset.
	hand := `{"servers":{},"default_server":"","client":{"Theme":"dracula"}}`
	require.NoError(t, os.WriteFile(path, []byte(hand), filePerm))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Client.Theme)
	require.Equal(t, "dracula", *cfg.Client.Theme)

	require.NoError(t, Save(cfg, path))
	assert.Equal(t, map[string]any{"theme": "dracula"}, clientObject(t, path))

	// Clearing the field clears the setting: no case-variant copy is left to
	// reload it.
	cfg, err = Load(path)
	require.NoError(t, err)
	cfg.Client.Theme = nil
	require.NoError(t, Save(cfg, path))

	reloaded, err := Load(path)
	require.NoError(t, err)
	assert.Nil(t, reloaded.Client.Theme)
}

func TestSaveCanonicalizesACaseVariantTopLevelKeyInsteadOfDuplicatingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	hand := `{"SERVERS":{"local":{"url":"http://example","username":"bob"}},"Client":{"theme":"nord"}}`
	require.NoError(t, os.WriteFile(path, []byte(hand), filePerm))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Client.Theme)
	require.Equal(t, "nord", *cfg.Client.Theme)

	require.NoError(t, Save(cfg, path))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var on map[string]any
	require.NoError(t, json.Unmarshal(raw, &on))
	assert.ElementsMatch(t, configKeys, slices.Collect(maps.Keys(on)))
	assert.Equal(t, "nord", clientObject(t, path)["theme"])
}

func TestLoadAcceptsSemanticallyInvalidClientValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	// Nonsense values must load so the CLI stays a route back to a valid file;
	// meaning is checked at the command layer, not here.
	hand := `{"servers":{},"default_server":"","client":{"theme":"bogus","poll_interval":"banana","list_sort":"nope","list_columns":["not_a_column"]}}`
	require.NoError(t, os.WriteFile(path, []byte(hand), filePerm))

	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "bogus", *got.Client.Theme)
	assert.Equal(t, "banana", *got.Client.PollInterval)
	assert.Equal(t, "nope", *got.Client.ListSort)
	assert.Equal(t, []string{"not_a_column"}, *got.Client.ListColumns)
}

func TestLoadRejectsAClientValueOfTheWrongJSONType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	hand := `{"servers":{},"default_server":"","client":{"nerd_font":"yes"}}`
	require.NoError(t, os.WriteFile(path, []byte(hand), filePerm))

	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing")
}

func TestSaveTightensLoosePermissionsOnAnExistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits differ on windows")
	}

	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"servers":{},"default_server":""}`), 0o644))

	require.NoError(t, Save(Config{Client: Client{Theme: ptr("nord")}}, path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(filePerm), info.Mode().Perm())
}

func TestSaveLeavesNoTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	require.NoError(t, Save(Config{Client: Client{Theme: ptr("nord")}}, path))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "config.json", entries[0].Name())
}

func TestSaveLeavesTheExistingFileIntactWhenTheWriteFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits differ on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the directory permission that makes the write fail")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	require.NoError(t, Save(Config{Client: Client{Theme: ptr("nord")}}, path))

	before, err := os.ReadFile(path)
	require.NoError(t, err)

	// A read-only directory blocks the temp file, which is the whole point of
	// writing to one: the previous config.json is never opened for truncation.
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, dirPerm) })

	require.Error(t, Save(Config{Client: Client{Theme: ptr("dracula")}}, path))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}
