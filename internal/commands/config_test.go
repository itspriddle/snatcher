package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runConfig executes the config command tree with args, returning stdout, the
// combined stderr, and the error from Execute. The command's output is a
// *bytes.Buffer, so isTTY reports false and the piped (tab-separated) plaintext
// contract is exercised.
func runConfig(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	root := NewConfigCmd()

	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)

	err := root.Execute()

	return out.String(), errOut.String(), err
}

// writeConfigFile writes body as a config file in a fresh temp directory and
// returns its path. Fixtures are raw JSON, not config.Save output, so a test can
// stage exactly the hand-edit it wants to describe — including one that is
// structurally valid but semantically nonsense.
func writeConfigFile(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	return path
}

// configRows splits piped "config list" output into its tab-separated cells.
func configRows(t *testing.T, out string) [][]string {
	t.Helper()

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}

	return rows
}

// TestClientKeyRegistryMatchesDocumentedKeys pins the registry's key set — and
// its order — to the six keys documented in the PRD's key table, so a key added
// later cannot skip the registry and land in the config with no validator,
// default, or accessor.
func TestClientKeyRegistryMatchesDocumentedKeys(t *testing.T) {
	documented := []string{
		"client.theme",
		"client.nerd_font",
		"client.poll_interval",
		"client.list_columns",
		"client.list_sort",
		"client.list_reverse",
	}

	keys := make([]string, len(clientKeyRegistry))
	for i, def := range clientKeyRegistry {
		keys[i] = def.key
	}

	assert.Equal(t, documented, keys)

	sorted := []string{
		"client.list_columns",
		"client.list_reverse",
		"client.list_sort",
		"client.nerd_font",
		"client.poll_interval",
		"client.theme",
	}
	assert.Equal(t, sorted, allClientKeys())
}

// TestClientKeyRegistryEntriesAreComplete requires every entry to name a
// default, an accessor, and a validator. A nil validator would be a key that
// accepts anything without anyone deciding it should.
func TestClientKeyRegistryEntriesAreComplete(t *testing.T) {
	for _, def := range clientKeyRegistry {
		assert.NotNil(t, def.def, "%s has no default", def.key)
		assert.NotNil(t, def.accessor, "%s has no accessor", def.key)
		assert.NotNil(t, def.validate, "%s has no validator", def.key)
	}
}

// TestClientKeyRegistryDefaultsAreValid checks every default against its own
// validator, so a typo in a default is a failing test rather than an error the
// operator sees on a config they never wrote.
func TestClientKeyRegistryDefaultsAreValid(t *testing.T) {
	for _, def := range clientKeyRegistry {
		assert.NoError(t, def.validate(def.def), "default for %s is invalid", def.key)
	}
}

// TestConfigListReportsDefaultsWithNoConfigFile pins the "fresh install" view:
// every key present, every value the documented default, every source "default".
func TestConfigListReportsDefaultsWithNoConfigFile(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	out, _, err := runConfig(t, "list", "--config", cfgPath)
	require.NoError(t, err)

	assert.Equal(t, [][]string{
		{"client.list_columns", "id,name,status,done,size,down,up,ratio,eta", "default"},
		{"client.list_reverse", "false", "default"},
		{"client.list_sort", "queue", "default"},
		{"client.nerd_font", "true", "default"},
		{"client.poll_interval", "5s", "default"},
		{"client.theme", "default", "default"},
	}, configRows(t, out))
}

// TestConfigListReportsStoredValues checks that a stored key reports its own
// value with source "config" while its unset neighbors still report defaults —
// the distinction the whole command exists to make visible.
func TestConfigListReportsStoredValues(t *testing.T) {
	cfgPath := writeConfigFile(t, `{
		"servers": {},
		"client": {
			"theme": "dracula",
			"nerd_font": false,
			"list_columns": ["id", "name", "ratio"],
			"list_reverse": true
		}
	}`)

	out, _, err := runConfig(t, "list", "--config", cfgPath)
	require.NoError(t, err)

	assert.Equal(t, [][]string{
		{"client.list_columns", "id,name,ratio", "config"},
		{"client.list_reverse", "true", "config"},
		{"client.list_sort", "queue", "default"},
		{"client.nerd_font", "false", "config"},
		{"client.poll_interval", "5s", "default"},
		{"client.theme", "dracula", "config"},
	}, configRows(t, out))
}

// TestConfigListJSONCarriesNaturalTypes pins the structured contract: an array
// sorted by key, each object {key, value, source}, with the value in its natural
// JSON type rather than stringified.
func TestConfigListJSONCarriesNaturalTypes(t *testing.T) {
	cfgPath := writeConfigFile(t, `{
		"servers": {},
		"client": {"nerd_font": false, "list_columns": ["id", "name"]}
	}`)

	out, _, err := runConfig(t, "list", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var entries []struct {
		Key    string `json:"key"`
		Value  any    `json:"value"`
		Source string `json:"source"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &entries))
	require.Len(t, entries, len(clientKeyRegistry))

	keys := make([]string, len(entries))
	byKey := make(map[string]any, len(entries))
	sources := make(map[string]string, len(entries))
	for i, entry := range entries {
		keys[i] = entry.Key
		byKey[entry.Key] = entry.Value
		sources[entry.Key] = entry.Source
	}

	assert.Equal(t, allClientKeys(), keys)
	assert.Equal(t, false, byKey["client.nerd_font"])
	assert.Equal(t, false, byKey["client.list_reverse"])
	assert.Equal(t, []any{"id", "name"}, byKey["client.list_columns"])
	assert.Equal(t, "5s", byKey["client.poll_interval"])
	assert.Equal(t, "default", byKey["client.theme"])
	assert.Equal(t, "config", sources["client.nerd_font"])
	assert.Equal(t, "default", sources["client.theme"])
}

// TestConfigListJQOutranksJSON pins the fixed --jq -> --json -> plaintext
// precedence for the group.
func TestConfigListJQOutranksJSON(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"theme": "nord"}}`)

	out, _, err := runConfig(t, "list", "--config", cfgPath, "--json",
		"--jq", `.[] | select(.key == "client.theme") | .value`)
	require.NoError(t, err)

	assert.Equal(t, "\"nord\"\n", out)
}

// TestConfigGetPrintsBareValue pins the shell-capture contract: the value and
// nothing else, no key name, padding, or decoration.
func TestConfigGetPrintsBareValue(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"theme": "dracula"}}`)

	out, _, err := runConfig(t, "get", "client.theme", "--config", cfgPath)
	require.NoError(t, err)

	assert.Equal(t, "dracula\n", out)
}

// TestConfigGetPrintsDefaultForUnsetKey checks an unset key still answers, with
// its default.
func TestConfigGetPrintsDefaultForUnsetKey(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)

	out, _, err := runConfig(t, "get", "client.poll_interval", "--config", cfgPath)
	require.NoError(t, err)

	assert.Equal(t, "5s\n", out)
}

// TestConfigGetJoinsListColumns checks the list-shaped key renders as the same
// comma-separated spelling that --fields and (later) "config set" accept.
func TestConfigGetJoinsListColumns(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"list_columns": ["id", "name", "ratio"]}}`)

	out, _, err := runConfig(t, "get", "client.list_columns", "--config", cfgPath)
	require.NoError(t, err)

	assert.Equal(t, "id,name,ratio\n", out)
}

// TestConfigGetJSONEmitsSingleObject pins the single-object shape, not an array
// of one.
func TestConfigGetJSONEmitsSingleObject(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"nerd_font": false}}`)

	out, _, err := runConfig(t, "get", "client.nerd_font", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var entry struct {
		Key    string `json:"key"`
		Value  any    `json:"value"`
		Source string `json:"source"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &entry))

	assert.Equal(t, "client.nerd_font", entry.Key)
	assert.Equal(t, false, entry.Value)
	assert.Equal(t, "config", entry.Source)
}

// TestConfigGetUnknownKey checks an unknown key is a non-zero exit whose message
// names the key and every valid one.
func TestConfigGetUnknownKey(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)

	out, _, err := runConfig(t, "get", "client.bogus", "--config", cfgPath)
	require.Error(t, err)

	assert.Empty(t, out)
	assert.Contains(t, err.Error(), `unknown config key "client.bogus"`)
	for _, key := range allClientKeys() {
		assert.Contains(t, err.Error(), key)
	}
}

// TestConfigGetIsCaseInsensitive checks a key is matched the way every other key
// in the CLI is: case-insensitively, after trimming.
func TestConfigGetIsCaseInsensitive(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"theme": "nord"}}`)

	out, _, err := runConfig(t, "get", " CLIENT.Theme ", "--config", cfgPath)
	require.NoError(t, err)

	assert.Equal(t, "nord\n", out)
}

// TestConfigGetRejectsInvalidStoredValue checks a hand-edited nonsense value is
// reported, naming the key, the offending value, and the valid set — never
// silently replaced by the default.
func TestConfigGetRejectsInvalidStoredValue(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"theme": "bogus"}}`)

	out, _, err := runConfig(t, "get", "client.theme", "--config", cfgPath)
	require.Error(t, err)

	assert.Empty(t, out)
	assert.Contains(t, err.Error(), "client.theme")
	assert.Contains(t, err.Error(), `"bogus"`)
	for _, theme := range clientThemes {
		assert.Contains(t, err.Error(), theme)
	}
}

// TestConfigListRejectsInvalidStoredValue checks the listing fails the same way
// rather than reporting the offending key as a default.
func TestConfigListRejectsInvalidStoredValue(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"list_sort": "sideways"}}`)

	out, _, err := runConfig(t, "list", "--config", cfgPath)
	require.Error(t, err)

	assert.Empty(t, out)
	assert.Contains(t, err.Error(), "client.list_sort")
	assert.Contains(t, err.Error(), `"sideways"`)
	assert.Contains(t, err.Error(), "queue")
}

// TestClientValidators covers each validator's own rules, which the two
// commands above exercise only one path of.
func TestClientValidators(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   any
		wantErr string
	}{
		{name: "theme accepted", key: "client.theme", value: "nord"},
		{name: "theme rejected", key: "client.theme", value: "solarized", wantErr: "not a known theme"},
		{name: "nerd_font accepts either bool", key: "client.nerd_font", value: false},
		{name: "list_reverse accepts either bool", key: "client.list_reverse", value: true},
		{name: "poll interval accepted", key: "client.poll_interval", value: "1m30s"},
		{name: "poll interval unparseable", key: "client.poll_interval", value: "soon", wantErr: "not a duration"},
		{name: "poll interval zero", key: "client.poll_interval", value: "0s", wantErr: "out of range"},
		{name: "poll interval negative", key: "client.poll_interval", value: "-5s", wantErr: "out of range"},
		{name: "poll interval too short", key: "client.poll_interval", value: "500ms", wantErr: "out of range"},
		{name: "poll interval too long", key: "client.poll_interval", value: "2h", wantErr: "out of range"},
		{name: "poll interval at the floor", key: "client.poll_interval", value: "1s"},
		{name: "poll interval at the ceiling", key: "client.poll_interval", value: "1h"},
		{name: "columns accepted", key: "client.list_columns", value: []string{"id", "name"}},
		{name: "columns case-insensitive", key: "client.list_columns", value: []string{"ID", " Name "}},
		{name: "columns empty", key: "client.list_columns", value: []string{}, wantErr: "no columns selected"},
		{name: "columns unknown", key: "client.list_columns", value: []string{"id", "tint"}, wantErr: `"tint" is not a column`},
		{name: "columns duplicated", key: "client.list_columns", value: []string{"id", "id"}, wantErr: "listed more than once"},
		{name: "sort accepted", key: "client.list_sort", value: "added"},
		{name: "sort rejected", key: "client.list_sort", value: "sideways", wantErr: "invalid sort field"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, err := lookupClientKey(tt.key)
			require.NoError(t, err)

			err = def.validate(tt.value)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestClientValidatorsRejectMismatchedTypes checks the type guard reports rather
// than panicking. Only a registry mistake can reach it — config.Load already
// rejects a JSON value whose type does not match its field.
func TestClientValidatorsRejectMismatchedTypes(t *testing.T) {
	def, err := lookupClientKey("client.theme")
	require.NoError(t, err)

	require.ErrorContains(t, def.validate(42), "expected string")
}

// TestConfigGroupPrintsHelp checks the bare group is help, not an action.
func TestConfigGroupPrintsHelp(t *testing.T) {
	out, _, err := runConfig(t)
	require.NoError(t, err)

	assert.Contains(t, out, "Available Commands:")
	assert.Contains(t, out, "list")
	assert.Contains(t, out, "get")
}

// TestConfigListCmdHasLsAlias keeps the group consistent with "torrent list" and
// "server list".
func TestConfigListCmdHasLsAlias(t *testing.T) {
	if got := newConfigListCmd().Aliases; len(got) != 1 || got[0] != "ls" {
		t.Fatalf("expected config list command alias [ls], got %v", got)
	}
}
