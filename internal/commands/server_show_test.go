package commands

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
	"github.com/itspriddle/snatcher/internal/secrets"
)

// showConnectionFields splits the connection block of piped "server show" output
// into a key->value map. The connection lines are the two-cell rows; the
// three-cell rows that follow are settings.
func showConnectionFields(t *testing.T, out string) map[string]string {
	t.Helper()

	fields := make(map[string]string)
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		if cells := strings.Split(line, "\t"); len(cells) == 2 {
			fields[cells[0]] = cells[1]
		}
	}

	return fields
}

// pipedSettingRows splits the settings section of piped output into
// CATEGORY/KEY/VALUE cells, keyed by setting key for order-independent
// assertions. Two-cell connection lines are skipped.
func pipedSettingRows(t *testing.T, out string) map[string][3]string {
	t.Helper()

	rows := make(map[string][3]string)
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		cells := strings.Split(line, "\t")
		if len(cells) == 2 {
			continue
		}
		require.Len(t, cells, 3, "piped settings rows are CATEGORY\\tKEY\\tVALUE")
		rows[cells[1]] = [3]string{cells[0], cells[1], cells[2]}
	}

	return rows
}

func TestServerShowPipedTabSeparated(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runServer(t, "", "show", "--config", cfgPath)
	require.NoError(t, err)

	// The connection block comes first, as two-cell kebab-keyed lines.
	conn := showConnectionFields(t, out)
	assert.Equal(t, "test", conn["name"])
	assert.Equal(t, "yes", conn["is-default"])
	assert.Equal(t, cfgPath, conn["config-path"])
	assert.Equal(t, "4.0.6 (38c164933e)", conn["version"], "identity comes from the same session-get")
	assert.Equal(t, "5.3.0", conn["rpc-version-semver"])
	assert.Equal(t, "bespoke", conn["protocol"])

	rows := pipedSettingRows(t, out)
	require.Len(t, rows, 40, "every registry setting is listed")

	// Settings follow registry order (grouped by category): download first,
	// queue last.
	settingLines := make([]string, 0, len(rows))
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if len(strings.Split(line, "\t")) == 3 {
			settingLines = append(settingLines, line)
		}
	}
	assert.Equal(t, "download", strings.Split(settingLines[0], "\t")[0])
	assert.Equal(t, "download-dir", strings.Split(settingLines[0], "\t")[1])
	assert.Equal(t, "queue", strings.Split(settingLines[len(settingLines)-1], "\t")[0])

	// Typed values render in their plaintext form, across types.
	assert.Equal(t, [3]string{"network", "peer-port", "31129"}, rows["peer-port"])
	assert.Equal(t, [3]string{"download", "start-added-torrents", "true"}, rows["start-added-torrents"])
	assert.Equal(t, [3]string{"download", "seed-ratio-limit", "2.5"}, rows["seed-ratio-limit"])
	assert.Equal(t, [3]string{"bandwidth", "speed-limit-down", "100"}, rows["speed-limit-down"])

	// The bespoke wire's "tolerated" encryption normalizes to canonical "allowed".
	assert.Equal(t, [3]string{"network", "encryption", "allowed"}, rows["encryption"])

	// The read-only blocklist-size is still shown (update/reset reject it).
	assert.Equal(t, [3]string{"network", "blocklist-size", "1234"}, rows["blocklist-size"])
}

func TestServerShowSingleRoundTrip(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	_, _, err := runServer(t, "", "show", "--config", cfgPath)
	require.NoError(t, err)

	// Identity and settings are two projections of one payload, so the whole view
	// costs a single session-get on top of the protocol-detection probe (which is
	// itself a session-get). Fetching them separately would cost three.
	getCount := 0
	for _, body := range captured {
		if strings.Contains(body, "session-get") {
			getCount++
		}
	}
	assert.Equal(t, 2, getCount, "the detection probe plus one shared session-get")
}

func TestServerShowPipedSanitizesServerDerivedStrings(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runServer(t, "", "show", "--config", cfgPath)
	require.NoError(t, err)

	assert.NotContains(t, out, "\x1b", "raw ESC from download-dir must not reach plaintext output")

	rows := pipedSettingRows(t, out)
	assert.Equal(t, "/data[2Jdownloads", rows["download-dir"][2], "visible text survives sanitization")
}

func TestServerShowSanitizesEncryption(t *testing.T) {
	cfgPath, closeSrv := maliciousEncryptionConfigPath(t)
	defer closeSrv()

	// Plaintext: the server-returned encryption value is sanitized like every
	// other server-derived string, so the raw ESC never reaches the terminal.
	out, _, err := runServer(t, "", "show", "--config", cfgPath, "--fields", "encryption")
	require.NoError(t, err)
	assert.NotContains(t, out, "\x1b", "raw ESC from encryption must not reach plaintext output")
	assert.Contains(t, out, "requ[2Jired", "visible text survives sanitization")
}

func TestServerShowJSON(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runServer(t, "", "show", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var result struct {
		serverShowResult
		Settings []struct {
			Key      string          `json:"key"`
			Category string          `json:"category"`
			Value    json.RawMessage `json:"value"`
		} `json:"settings"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &result))

	// The connection block keeps its established snake_case JSON contract.
	assert.Equal(t, "test", result.Name)
	assert.Equal(t, cfgPath, result.ConfigPath)
	assert.True(t, result.IsDefault)
	assert.True(t, result.Configured)

	// Identity is reported alongside it.
	assert.Equal(t, "4.0.6 (38c164933e)", result.Version)
	assert.Equal(t, "5.3.0", result.RPCVersionSemver)
	assert.Equal(t, "bespoke", result.Protocol)

	require.Len(t, result.Settings, 40)

	byKey := make(map[string]json.RawMessage)
	category := make(map[string]string)
	for _, r := range result.Settings {
		byKey[r.Key] = r.Value
		category[r.Key] = r.Category
	}

	// Values are native typed (number/bool/string), not stringified.
	assert.Equal(t, "network", category["peer-port"])
	assert.JSONEq(t, "31129", string(byKey["peer-port"]), "int value is a native number")
	assert.JSONEq(t, "true", string(byKey["start-added-torrents"]), "bool value is a native bool")
	assert.JSONEq(t, "2.5", string(byKey["seed-ratio-limit"]), "float value is a native number")
	assert.JSONEq(t, `"allowed"`, string(byKey["encryption"]), "enum value is a native string")

	// JSON is a structured contract: the server-derived download-dir is NOT
	// sanitized, so the raw ESC survives in the JSON value.
	assert.Contains(t, out, "\\u001b", "JSON value carries the raw control byte unsanitized")
}

func TestServerShowJQ(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	// --jq is how a script reads one bare setting value now that "settings get" is
	// gone.
	out, _, err := runServer(t, "", "show", "--config", cfgPath,
		"--jq", `.settings[] | select(.key=="peer-port") | .value`)
	require.NoError(t, err)

	assert.Equal(t, "31129", strings.TrimSpace(out))
}

func TestServerShowCategoryFilters(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runServer(t, "", "show", "--config", cfgPath, "--category", "network")
	require.NoError(t, err)

	rows := pipedSettingRows(t, out)
	require.Len(t, rows, 13, "the network category has 13 settings")
	for _, cells := range rows {
		assert.Equal(t, "network", cells[0], "only network rows survive the filter")
	}

	// Filtering settings does not remove the connection block.
	assert.Equal(t, "test", showConnectionFields(t, out)["name"])
}

func TestServerShowCategoryIsCaseInsensitive(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runServer(t, "", "show", "--config", cfgPath, "--category", "QUEUE")
	require.NoError(t, err)

	rows := pipedSettingRows(t, out)
	require.Len(t, rows, 6, "the queue category has 6 settings")
}

func TestServerShowInvalidCategoryErrors(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	_, _, err := runServer(t, "", "show", "--config", cfgPath, "--category", "bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid category "bogus"`)
	assert.Contains(t, err.Error(), "valid categories are", "the error lists the valid categories")
	assert.Contains(t, err.Error(), "download", "the valid-categories list includes the categories")
}

func TestServerShowSearchFiltersByKey(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	// --search narrows to keys containing the (case-insensitive) substring.
	out, _, err := runServer(t, "", "show", "--config", cfgPath, "--search", "SPEED")
	require.NoError(t, err)

	rows := pipedSettingRows(t, out)
	require.NotEmpty(t, rows)
	for key := range rows {
		assert.Contains(t, key, "speed", "every listed key contains the search substring")
	}
	assert.Contains(t, rows, "speed-limit-down")
	assert.Contains(t, rows, "alt-speed-enabled")
	assert.NotContains(t, rows, "peer-port", "non-matching keys are excluded")
}

func TestServerShowCategoryAppliesToJSON(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	// The category filter must apply to structured (--json) output, not only the
	// plaintext branch.
	out, _, err := runServer(t, "", "show", "--config", cfgPath, "--category", "queue", "--json")
	require.NoError(t, err)

	var result struct {
		Settings []struct {
			Category string `json:"category"`
		} `json:"settings"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	require.Len(t, result.Settings, 6, "only the queue category survives the filter in JSON output")
	for _, r := range result.Settings {
		assert.Equal(t, "queue", r.Category)
	}
}

func TestServerShowFieldsSelectsAcrossBothSections(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runServer(t, "", "show", "--config", cfgPath, "--fields", "peer-port,name,download-dir")
	require.NoError(t, err)

	conn := showConnectionFields(t, out)
	require.Len(t, conn, 1, "only the named connection field is shown")
	assert.Equal(t, "test", conn["name"])

	rows := pipedSettingRows(t, out)
	require.Len(t, rows, 2, "only the named settings are shown")
	assert.Contains(t, rows, "peer-port")
	assert.Contains(t, rows, "download-dir")
}

func TestServerShowFieldsSelectsWithoutReordering(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	// Named out of canonical order, each section still renders in its own order:
	// the connection block before the settings, and download-dir (download)
	// before peer-port (network).
	out, _, err := runServer(t, "", "show", "--config", cfgPath, "--fields", "peer-port,download-dir,name")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3)
	assert.True(t, strings.HasPrefix(lines[0], "name\t"))
	assert.True(t, strings.HasPrefix(lines[1], "download\tdownload-dir\t"))
	assert.True(t, strings.HasPrefix(lines[2], "network\tpeer-port\t"))
}

func TestServerShowFieldsOnlySettings(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runServer(t, "", "show", "--config", cfgPath, "--fields", "peer-port")
	require.NoError(t, err)

	assert.Equal(t, "network\tpeer-port\t31129\n", out, "no connection block when none is selected")
}

func TestServerShowFieldsInvalidKeyErrors(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	_, _, err := runServer(t, "", "show", "--config", cfgPath, "--fields", "peer-port,bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid field "bogus"`)
	assert.Contains(t, err.Error(), "valid fields are")
	assert.Contains(t, err.Error(), "server-url", "the valid-fields list includes the connection keys")
	assert.Contains(t, err.Error(), "peer-port", "the valid-fields list includes the setting keys")
}

func TestServerShowFieldsEmptyAfterParseErrors(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	_, _, err := runServer(t, "", "show", "--config", cfgPath, "--fields", ",,")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no fields selected")
}

func TestServerShowFieldsIgnoredByJSON(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	// --fields is a plaintext concern; --json always emits the full object.
	out, _, err := runServer(t, "", "show", "--config", cfgPath, "--fields", "name", "--json")
	require.NoError(t, err)

	var result serverShowResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, "test", result.Name)
	assert.Equal(t, "bespoke", result.Protocol, "unselected fields still appear in JSON")
}

func TestServerShowLocalSkipsTheServer(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	out, _, err := runServer(t, "", "show", "--config", cfgPath, "--local")
	require.NoError(t, err)

	assert.Empty(t, captured, "--local never contacts the server")

	conn := showConnectionFields(t, out)
	assert.Equal(t, "test", conn["name"])
	assert.NotContains(t, conn, "version", "live-only fields are dropped, not rendered blank")
	assert.NotContains(t, conn, "protocol")
	assert.Empty(t, pipedSettingRows(t, out), "no settings are reported offline")
}

func TestServerShowLocalJSONOmitsLiveFields(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runServer(t, "", "show", "--config", cfgPath, "--local", "--json")
	require.NoError(t, err)

	assert.NotContains(t, out, `"settings"`, "the settings key is absent offline")
	assert.NotContains(t, out, `"version"`, "the identity keys are absent offline")

	var result serverShowResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, "test", result.Name)
	assert.True(t, result.Configured)
}

func TestServerShowLocalRejectsSettingFilters(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	for _, flag := range []struct{ name, value string }{
		{"--category", "network"},
		{"--search", "speed"},
		{"--fields", "peer-port"},
	} {
		_, _, err := runServer(t, "", "show", "--config", cfgPath, "--local", flag.name, flag.value)
		require.Error(t, err, flag.name)
		assert.Contains(t, err.Error(), "--local and "+flag.name+" cannot be used together")
	}
}

func TestServerShowFieldsRejectsSettingFilters(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	for _, flag := range []struct{ name, value string }{
		{"--category", "network"},
		{"--search", "speed"},
	} {
		_, _, err := runServer(t, "", "show", "--config", cfgPath, "--fields", "peer-port", flag.name, flag.value)
		require.Error(t, err, flag.name)
		assert.Contains(t, err.Error(), "--fields and "+flag.name+" cannot be used together")
	}
}

func TestServerShowPlaintextConnectionBlock(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"host:9091": {URL: "http://host:9091/rpc", Username: "bob"}}, DefaultServer: "host:9091"}, cfgPath))
	require.NoError(t, secrets.Set("http://host:9091/rpc", "pw"))

	// --local keeps the command usable when the server is unreachable.
	out, _, err := runServer(t, "", "show", "--config", cfgPath, "--local")
	require.NoError(t, err)

	conn := showConnectionFields(t, out)
	assert.Equal(t, "http://host:9091/rpc", conn["server-url"])
	assert.Equal(t, "bob", conn["username"])
	assert.Equal(t, cfgPath, conn["config-path"])
	assert.Equal(t, "yes", conn["secret-stored"])
	// Never leaks the secret value.
	assert.NotContains(t, out, "pw\n")
}

func TestServerShowJSONConnectionOnly(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"host:9091": {URL: "http://host:9091/rpc", Username: "bob"}}, DefaultServer: "host:9091"}, cfgPath))

	out, _, err := runServer(t, "", "show", "--config", cfgPath, "--local", "--json")
	require.NoError(t, err)

	var result serverShowResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, "host:9091", result.Name)
	assert.Equal(t, "http://host:9091/rpc", result.ServerURL)
	assert.Equal(t, "bob", result.Username)
	assert.Equal(t, cfgPath, result.ConfigPath)
	assert.False(t, result.SecretStored)
	assert.True(t, result.IsDefault)
	assert.True(t, result.Configured)
}

func TestServerShowNamedServer(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"local":   {URL: "http://local:9091/rpc", Username: "alice"},
			"seedbox": {URL: "http://seedbox:9091/rpc", Username: "bob"},
		},
		DefaultServer: "local",
	}, cfgPath))

	out, _, err := runServer(t, "", "show", "seedbox", "--config", cfgPath, "--local", "--json")
	require.NoError(t, err)

	var result serverShowResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, "seedbox", result.Name)
	assert.Equal(t, "http://seedbox:9091/rpc", result.ServerURL)
	assert.Equal(t, "bob", result.Username)
	// Naming a non-default server reports is_default false.
	assert.False(t, result.IsDefault)
	assert.True(t, result.Configured)
}

func TestServerShowOmittedAliasHonorsSnatcherServer(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers: map[string]config.Server{
			"local":   {URL: "http://local:9091/rpc", Username: "alice"},
			"seedbox": {URL: "http://seedbox:9091/rpc", Username: "bob"},
		},
		DefaultServer: "local",
	}, cfgPath))

	// An omitted alias resolves the way dial does: SNATCHER_SERVER outranks the
	// default, so the connection block describes the server the settings come
	// from.
	t.Setenv(envServer, "seedbox")

	out, _, err := runServer(t, "", "show", "--config", cfgPath, "--local", "--json")
	require.NoError(t, err)

	var result serverShowResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, "seedbox", result.Name)
	assert.Equal(t, "http://seedbox:9091/rpc", result.ServerURL)
	assert.False(t, result.IsDefault)

	// A positional alias still outranks the variable.
	out, _, err = runServer(t, "", "show", "local", "--config", cfgPath, "--local", "--json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Equal(t, "local", result.Name)
}

func TestServerShowUnknownAlias(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	require.NoError(t, config.Save(config.Config{
		Servers:       map[string]config.Server{"local": {URL: "http://local:9091/rpc"}},
		DefaultServer: "local",
	}, cfgPath))

	_, _, err := runServer(t, "", "show", "nope", "--config", cfgPath, "--json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown server "nope"`)
	assert.Contains(t, err.Error(), "server list")
}

func TestServerShowNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	// A first run with no servers reports the empty connection block rather than
	// erroring, and never tries to dial.
	out, _, err := runServer(t, "", "show", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var result serverShowResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	assert.Empty(t, result.ServerURL)
	assert.False(t, result.Configured)
	assert.False(t, result.SecretStored)
	assert.Empty(t, result.Settings)
}
