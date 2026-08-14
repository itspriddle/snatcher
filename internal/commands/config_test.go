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
	AddGlobalFlags(root)

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
// default, an accessor, a writer, and a validator. A nil validator would be a
// key that accepts anything without anyone deciding it should; a nil apply would
// be a key that reads but cannot be written.
func TestClientKeyRegistryEntriesAreComplete(t *testing.T) {
	for _, def := range clientKeyRegistry {
		assert.NotNil(t, def.def, "%s has no default", def.key)
		assert.NotNil(t, def.accessor, "%s has no accessor", def.key)
		assert.NotNil(t, def.apply, "%s has no writer", def.key)
		assert.NotNil(t, def.validate, "%s has no validator", def.key)
	}
}

// TestClientKeyRegistryOverridesMatchTheGlobalFlags pins which keys can be
// overridden from outside the config file to exactly the two that have a global
// flag and an environment companion. A key that grew an override without a flag
// — or a flag without an override — would be a rung an operator cannot see.
func TestClientKeyRegistryOverridesMatchTheGlobalFlags(t *testing.T) {
	overridable := []string{}
	for _, def := range clientKeyRegistry {
		if def.override != nil {
			overridable = append(overridable, def.key)
		}
	}

	assert.Equal(t, []string{"client.theme", "client.nerd_font"}, overridable)
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

// readConfigObject decodes the config file at path as a generic JSON object, so
// a test can assert on exactly what landed on disk rather than on what a
// round trip through config.Config reports.
func readConfigObject(t *testing.T, path string) map[string]any {
	t.Helper()

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var obj map[string]any
	require.NoError(t, json.Unmarshal(raw, &obj))

	return obj
}

// readClientObject decodes the client section of the config file at path. A
// missing section decodes as nil, which is what an entirely unset client section
// looks like on disk.
func readClientObject(t *testing.T, path string) map[string]any {
	t.Helper()

	client, ok := readConfigObject(t, path)["client"]
	if !ok {
		return nil
	}

	obj, ok := client.(map[string]any)
	require.True(t, ok, "client section is not an object")

	return obj
}

// TestConfigSetPersistsValue checks the happy path end to end: the value lands
// in the client section on disk, and "config get" reads it back.
func TestConfigSetPersistsValue(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)

	out, _, err := runConfig(t, "set", "client.theme", "dracula", "--config", cfgPath)
	require.NoError(t, err)

	assert.Equal(t, "Set client.theme to dracula\n", out)
	assert.Equal(t, "dracula", readClientObject(t, cfgPath)["theme"])

	out, _, err = runConfig(t, "get", "client.theme", "--config", cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "dracula\n", out)
}

// TestConfigSetStoresExplicitFalse checks the distinction the pointer fields
// exist for: setting a bool to its zero value is stored, not treated as unset.
func TestConfigSetStoresExplicitFalse(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)

	_, _, err := runConfig(t, "set", "client.nerd_font", "false", "--config", cfgPath)
	require.NoError(t, err)

	assert.Equal(t, false, readClientObject(t, cfgPath)["nerd_font"])

	out, _, err := runConfig(t, "get", "client.nerd_font", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var entry configEntry
	require.NoError(t, json.Unmarshal([]byte(out), &entry))
	assert.Equal(t, false, entry.Value)
	assert.Equal(t, "config", entry.Source)
}

// TestConfigSetSplitsListColumns checks the comma-separated spelling, including
// the whitespace an operator is likely to type.
func TestConfigSetSplitsListColumns(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)

	out, _, err := runConfig(t, "set", "client.list_columns", "id, name ,ratio", "--config", cfgPath)
	require.NoError(t, err)

	assert.Equal(t, "Set client.list_columns to id,name,ratio\n", out)
	assert.Equal(t, []any{"id", "name", "ratio"}, readClientObject(t, cfgPath)["list_columns"])
}

// TestConfigSetJSONReportsWrittenEntry pins the structured contract for a write:
// the same {key, value, source} object the read commands emit, with the value in
// its natural JSON type and the source now "config".
func TestConfigSetJSONReportsWrittenEntry(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)

	out, _, err := runConfig(t, "set", "client.list_columns", "id,name", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var entry configEntry
	require.NoError(t, json.Unmarshal([]byte(out), &entry))

	assert.Equal(t, "client.list_columns", entry.Key)
	assert.Equal(t, []any{"id", "name"}, entry.Value)
	assert.Equal(t, "config", entry.Source)
}

// TestConfigSetJQOutranksJSON pins the fixed precedence on the write path too.
func TestConfigSetJQOutranksJSON(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)

	out, _, err := runConfig(t, "set", "client.list_sort", "added", "--config", cfgPath, "--json", "--jq", ".value")
	require.NoError(t, err)

	assert.Equal(t, "\"added\"\n", out)
}

// TestConfigSetRejectsUnknownKey checks an unknown key is an error naming every
// valid key, and never a silent write of a key the registry does not know.
func TestConfigSetRejectsUnknownKey(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)
	before := configBytes(t, cfgPath)

	out, _, err := runConfig(t, "set", "client.bogus", "1", "--config", cfgPath)
	require.Error(t, err)

	assert.Empty(t, out)
	assert.Contains(t, err.Error(), `unknown config key "client.bogus"`)
	for _, key := range allClientKeys() {
		assert.Contains(t, err.Error(), key)
	}
	assert.Equal(t, before, configBytes(t, cfgPath))
}

// TestConfigUnsetRejectsUnknownKey checks unset refuses the same way, so neither
// write path can invent a key.
func TestConfigUnsetRejectsUnknownKey(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)
	before := configBytes(t, cfgPath)

	_, _, err := runConfig(t, "unset", "client.bogus", "--config", cfgPath)
	require.Error(t, err)

	assert.Contains(t, err.Error(), `unknown config key "client.bogus"`)
	assert.Equal(t, before, configBytes(t, cfgPath))
}

// configBytes reads the config file verbatim, for the "left untouched" checks: a
// rejected write must not rewrite the file at all, not even to an equivalent
// encoding.
func configBytes(t *testing.T, path string) []byte {
	t.Helper()

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	return raw
}

// TestConfigSetRejectsInvalidValues covers every rejection the registry can
// produce through "config set", and pins that the file's bytes are unchanged
// after each one — a typo must never be half-applied.
//
// Every case is passed after a "--" terminator: a value that starts with a dash
// (a negative duration) is otherwise read as a flag by pflag, which is the shell
// convention rather than anything specific to this command.
func TestConfigSetRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantErr string
	}{
		{name: "theme", key: "client.theme", value: "solarized", wantErr: "not a known theme"},
		{name: "bool", key: "client.nerd_font", value: "yes-please", wantErr: "not a boolean"},
		{name: "duration unparseable", key: "client.poll_interval", value: "soon", wantErr: "not a duration"},
		{name: "duration zero", key: "client.poll_interval", value: "0s", wantErr: "out of range"},
		{name: "duration negative", key: "client.poll_interval", value: "-5s", wantErr: "out of range"},
		{name: "duration too short", key: "client.poll_interval", value: "999ms", wantErr: "out of range"},
		{name: "duration too long", key: "client.poll_interval", value: "1h1s", wantErr: "out of range"},
		{name: "columns unknown", key: "client.list_columns", value: "id,tint", wantErr: `"tint" is not a column`},
		{name: "columns empty", key: "client.list_columns", value: "", wantErr: "no columns selected"},
		{name: "columns duplicated", key: "client.list_columns", value: "id,name,id", wantErr: "listed more than once"},
		{name: "sort", key: "client.list_sort", value: "sideways", wantErr: "invalid sort field"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfgPath := writeConfigFile(t, `{"servers": {}, "default_server": ""}`)
			before := configBytes(t, cfgPath)

			out, _, err := runConfig(t, "set", "--config", cfgPath, "--", tt.key, tt.value)
			require.Error(t, err)

			assert.Empty(t, out)
			assert.Contains(t, err.Error(), tt.key)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Equal(t, before, configBytes(t, cfgPath), "a rejected set rewrote the config file")
		})
	}
}

// TestConfigSetAcceptsPollIntervalBounds checks the accepted end of the duration
// rules, which the rejection table above only bounds from outside.
func TestConfigSetAcceptsPollIntervalBounds(t *testing.T) {
	for _, value := range []string{"1s", "1m30s", "1h"} {
		t.Run(value, func(t *testing.T) {
			cfgPath := writeConfigFile(t, `{"servers": {}}`)

			_, _, err := runConfig(t, "set", "client.poll_interval", value, "--config", cfgPath)
			require.NoError(t, err)

			assert.Equal(t, value, readClientObject(t, cfgPath)["poll_interval"])
		})
	}
}

// TestConfigUnsetRemovesKey checks the key leaves the file entirely — reverting
// to its default rather than persisting as a null — and that its neighbors stay.
func TestConfigUnsetRemovesKey(t *testing.T) {
	cfgPath := writeConfigFile(t, `{
		"servers": {},
		"client": {"theme": "nord", "list_sort": "added"}
	}`)

	out, _, err := runConfig(t, "unset", "client.theme", "--config", cfgPath)
	require.NoError(t, err)

	assert.Equal(t, "Unset client.theme, now default (its default)\n", out)

	client := readClientObject(t, cfgPath)
	assert.NotContains(t, client, "theme")
	assert.Equal(t, "added", client["list_sort"])
}

// TestConfigUnsetIsIdempotent checks unsetting a key that was never set succeeds
// and changes nothing, so the command is safe to re-run in a script.
func TestConfigUnsetIsIdempotent(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"theme": "nord"}}`)

	_, _, err := runConfig(t, "unset", "client.list_sort", "--config", cfgPath)
	require.NoError(t, err)

	first := configBytes(t, cfgPath)

	_, _, err = runConfig(t, "unset", "client.list_sort", "--config", cfgPath)
	require.NoError(t, err)

	assert.Equal(t, first, configBytes(t, cfgPath))
	assert.Equal(t, "nord", readClientObject(t, cfgPath)["theme"])
}

// TestConfigUnsetJSONReportsTheDefault checks the reported entry describes the
// state unset leaves behind: the default value, sourced from the default.
func TestConfigUnsetJSONReportsTheDefault(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"list_reverse": true}}`)

	out, _, err := runConfig(t, "unset", "client.list_reverse", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var entry configEntry
	require.NoError(t, json.Unmarshal([]byte(out), &entry))

	assert.Equal(t, "client.list_reverse", entry.Key)
	assert.Equal(t, false, entry.Value)
	assert.Equal(t, "default", entry.Source)
}

// TestConfigWritesRepairAnotherKeysBadHandEdit is the repairability guarantee: a
// nonsense value hand-edited into one key must not block writing or clearing
// another, or the CLI could not fix the file it wrote about.
func TestConfigWritesRepairAnotherKeysBadHandEdit(t *testing.T) {
	body := `{"servers": {}, "client": {"theme": "bogus", "list_sort": "sideways"}}`

	t.Run("set", func(t *testing.T) {
		cfgPath := writeConfigFile(t, body)

		_, _, err := runConfig(t, "set", "client.theme", "nord", "--config", cfgPath)
		require.NoError(t, err)

		client := readClientObject(t, cfgPath)
		assert.Equal(t, "nord", client["theme"])
		assert.Equal(t, "sideways", client["list_sort"], "an untouched key was rewritten")
	})

	t.Run("unset", func(t *testing.T) {
		cfgPath := writeConfigFile(t, body)

		_, _, err := runConfig(t, "unset", "client.list_sort", "--config", cfgPath)
		require.NoError(t, err)

		client := readClientObject(t, cfgPath)
		assert.NotContains(t, client, "list_sort")
		assert.Equal(t, "bogus", client["theme"], "an untouched key was rewritten")
	})
}

// TestConfigSetPreservesTheRestOfTheFile checks a write leaves the servers map,
// the default server, and the unknown keys US-001 preserves exactly as they were.
func TestConfigSetPreservesTheRestOfTheFile(t *testing.T) {
	cfgPath := writeConfigFile(t, `{
		"servers": {"home": {"url": "https://example.test/transmission/rpc", "username": "josh"}},
		"default_server": "home",
		"future_key": {"kept": true},
		"client": {"nerd_font": false, "future_client_key": 7}
	}`)

	_, _, err := runConfig(t, "set", "client.theme", "nord", "--config", cfgPath)
	require.NoError(t, err)

	obj := readConfigObject(t, cfgPath)
	assert.Equal(t, "home", obj["default_server"])
	assert.Equal(t, map[string]any{"kept": true}, obj["future_key"])
	assert.Equal(t, map[string]any{
		"home": map[string]any{
			"url":      "https://example.test/transmission/rpc",
			"username": "josh",
		},
	}, obj["servers"])

	client := readClientObject(t, cfgPath)
	assert.Equal(t, "nord", client["theme"])
	assert.Equal(t, false, client["nerd_font"])
	assert.Equal(t, float64(7), client["future_client_key"])
}

// TestConfigWritesCreateAMissingConfigFile checks a first run configures snatcher
// rather than being refused for not being configured yet, and that the file it
// creates is as private as one "server add" would have written.
func TestConfigWritesCreateAMissingConfigFile(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "set", args: []string{"set", "client.theme", "nord"}},
		{name: "unset", args: []string{"unset", "client.theme"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "snatcher")
			cfgPath := filepath.Join(dir, "config.json")

			_, _, err := runConfig(t, append(tt.args, "--config", cfgPath)...)
			require.NoError(t, err)

			info, err := os.Stat(cfgPath)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

			dirInfo, err := os.Stat(dir)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
		})
	}
}

// TestConfigSetIsCaseInsensitiveInTheKey checks the key is matched the way every
// other key in the CLI is, while the value's spelling stays the validator's call.
func TestConfigSetIsCaseInsensitiveInTheKey(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)

	_, _, err := runConfig(t, "set", " CLIENT.List_Sort ", "added", "--config", cfgPath)
	require.NoError(t, err)

	assert.Equal(t, "added", readClientObject(t, cfgPath)["list_sort"])
}

// configEntryFor runs "config get <key> --json" with args appended and decodes
// the reported entry, so a test can assert on both the effective value and the
// rung that supplied it.
func configEntryFor(t *testing.T, key string, args ...string) configEntry {
	t.Helper()

	out, _, err := runConfig(t, append([]string{"get", key, "--json"}, args...)...)
	require.NoError(t, err)

	var entry configEntry
	require.NoError(t, json.Unmarshal([]byte(out), &entry))

	return entry
}

// TestClientSettingPrecedence walks the whole chain — flag > env > config >
// default — for both overridable keys, including every case where a lower rung
// is set and a higher one is not, which is where a precedence bug actually
// lives.
//
// The theme cases pin the Changed detection too: "--theme default" passes the
// registry default explicitly and must still report source "flag", which a
// zero-value comparison would get wrong. The nerd-font cases pin the negation:
// the flag and the variable are negative, the config key is positive.
func TestClientSettingPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		client     string
		envName    string
		envValue   string
		args       []string
		wantValue  any
		wantSource string
	}{
		{
			name:       "theme default with nothing set",
			key:        "client.theme",
			wantValue:  "default",
			wantSource: sourceDefault,
		},
		{
			name:       "theme from the config file",
			key:        "client.theme",
			client:     `"theme": "nord"`,
			wantValue:  "nord",
			wantSource: sourceConfig,
		},
		{
			name:       "theme env outranks the config file",
			key:        "client.theme",
			client:     `"theme": "nord"`,
			envName:    envTheme,
			envValue:   "dracula",
			wantValue:  "dracula",
			wantSource: sourceEnv,
		},
		{
			name:       "theme env with no stored value",
			key:        "client.theme",
			envName:    envTheme,
			envValue:   "dracula",
			wantValue:  "dracula",
			wantSource: sourceEnv,
		},
		{
			name:       "theme flag outranks env and the config file",
			key:        "client.theme",
			client:     `"theme": "nord"`,
			envName:    envTheme,
			envValue:   "dracula",
			args:       []string{"--theme", "default"},
			wantValue:  "default",
			wantSource: sourceFlag,
		},
		{
			name:       "theme empty env falls through to the config file",
			key:        "client.theme",
			client:     `"theme": "nord"`,
			envName:    envTheme,
			envValue:   "",
			wantValue:  "nord",
			wantSource: sourceConfig,
		},
		{
			name:       "nerd font default with nothing set",
			key:        "client.nerd_font",
			wantValue:  true,
			wantSource: sourceDefault,
		},
		{
			name:       "nerd font from the config file",
			key:        "client.nerd_font",
			client:     `"nerd_font": false`,
			wantValue:  false,
			wantSource: sourceConfig,
		},
		{
			name:       "truthy env turns off a config file that says on",
			key:        "client.nerd_font",
			client:     `"nerd_font": true`,
			envName:    envNoNerdFont,
			envValue:   "1",
			wantValue:  false,
			wantSource: sourceEnv,
		},
		{
			name:       "falsy env turns on a config file that says off",
			key:        "client.nerd_font",
			client:     `"nerd_font": false`,
			envName:    envNoNerdFont,
			envValue:   "false",
			wantValue:  true,
			wantSource: sourceEnv,
		},
		{
			name:       "nerd font flag outranks a falsy env",
			key:        "client.nerd_font",
			client:     `"nerd_font": true`,
			envName:    envNoNerdFont,
			envValue:   "false",
			args:       []string{"--no-nerd-font"},
			wantValue:  false,
			wantSource: sourceFlag,
		},
		{
			name:       "nerd font empty env falls through to the config file",
			key:        "client.nerd_font",
			client:     `"nerd_font": false`,
			envName:    envNoNerdFont,
			envValue:   "",
			wantValue:  false,
			wantSource: sourceConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envName != "" {
				t.Setenv(tt.envName, tt.envValue)
			}

			cfgPath := writeConfigFile(t, `{"servers": {}, "client": {`+tt.client+`}}`)

			entry := configEntryFor(t, tt.key, append([]string{"--config", cfgPath}, tt.args...)...)

			assert.Equal(t, tt.key, entry.Key)
			assert.Equal(t, tt.wantValue, entry.Value)
			assert.Equal(t, tt.wantSource, entry.Source)
		})
	}
}

// TestConfigListReportsOverrideSources checks the listing reports the rung each
// value came from, so "config list" shows what this process would actually use
// rather than only what the file holds. Keys with no override rung are
// unaffected by the flags and variables in force.
func TestConfigListReportsOverrideSources(t *testing.T) {
	t.Setenv(envNoNerdFont, "true")

	cfgPath := writeConfigFile(t, `{
		"servers": {},
		"client": {"theme": "nord", "nerd_font": true, "list_sort": "added"}
	}`)

	out, _, err := runConfig(t, "list", "--config", cfgPath, "--theme", "dracula")
	require.NoError(t, err)

	assert.Equal(t, [][]string{
		{"client.list_columns", "id,name,status,done,size,down,up,ratio,eta", "default"},
		{"client.list_reverse", "false", "default"},
		{"client.list_sort", "added", "config"},
		{"client.nerd_font", "false", "env"},
		{"client.poll_interval", "5s", "default"},
		{"client.theme", "dracula", "flag"},
	}, configRows(t, out))
}

// TestConfigGetPrintsTheOverriddenValue checks the bare plaintext value is the
// effective one, so a shell capture agrees with what the process would use.
func TestConfigGetPrintsTheOverriddenValue(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"theme": "nord"}}`)

	out, _, err := runConfig(t, "get", "client.theme", "--config", cfgPath, "--theme", "dracula")
	require.NoError(t, err)

	assert.Equal(t, "dracula\n", out)
}

// TestConfigReadsRejectInvalidOverrides checks a bad override is an error naming
// its rung, rather than a silent fall back to the config file — the whole point
// of reporting sources is that a rung is never invisible.
func TestConfigReadsRejectInvalidOverrides(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"theme": "nord"}}`)

	t.Run("flag", func(t *testing.T) {
		out, _, err := runConfig(t, "get", "client.theme", "--config", cfgPath, "--theme", "solarized")
		require.Error(t, err)

		assert.Empty(t, out)
		assert.Contains(t, err.Error(), "--theme")
		assert.Contains(t, err.Error(), "not a known theme")
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv(envNoNerdFont, "yes-please")

		out, _, err := runConfig(t, "list", "--config", cfgPath)
		require.Error(t, err)

		assert.Empty(t, out)
		assert.Contains(t, err.Error(), envNoNerdFont)
		assert.Contains(t, err.Error(), "not a boolean")
	})
}

// TestConfigWritesIgnoreOverrides is the separation the two commands depend on:
// a --theme or a SNATCHER_NO_NERD_FONT in force for this invocation is an
// override for this process and must never leak into the file, nor into what the
// write reports having done.
func TestConfigWritesIgnoreOverrides(t *testing.T) {
	t.Setenv(envNoNerdFont, "true")

	t.Run("set", func(t *testing.T) {
		cfgPath := writeConfigFile(t, `{"servers": {}}`)

		out, _, err := runConfig(t, "set", "client.theme", "nord", "--config", cfgPath,
			"--theme", "dracula", "--json")
		require.NoError(t, err)

		var entry configEntry
		require.NoError(t, json.Unmarshal([]byte(out), &entry))
		assert.Equal(t, "nord", entry.Value, "the write reported the override, not the file")
		assert.Equal(t, sourceConfig, entry.Source)

		client := readClientObject(t, cfgPath)
		assert.Equal(t, "nord", client["theme"])
		assert.NotContains(t, client, "nerd_font", "an override was persisted")
	})

	t.Run("unset", func(t *testing.T) {
		cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"nerd_font": false}}`)

		out, _, err := runConfig(t, "unset", "client.nerd_font", "--config", cfgPath,
			"--theme", "dracula", "--json")
		require.NoError(t, err)

		var entry configEntry
		require.NoError(t, json.Unmarshal([]byte(out), &entry))
		assert.Equal(t, true, entry.Value, "unset reported the override, not the default it left behind")
		assert.Equal(t, sourceDefault, entry.Source)

		assert.NotContains(t, readClientObject(t, cfgPath), "nerd_font")
	})
}
