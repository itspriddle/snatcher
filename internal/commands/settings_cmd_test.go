package commands

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
)

// bespokeSessionResponse is a session-get payload (kebab-case settings keys,
// with the seedRatio camelCase exceptions and the cache-size-mb wire spelling)
// covering every category, used to exercise the settings command end-to-end on
// the bespoke path. The download-dir carries an embedded ESC so the
// sanitization contract can be checked. encryption is "tolerated" on the wire,
// which the rpc layer normalizes to the canonical "allowed".
const bespokeSessionResponse = `{
	"arguments": {
		"download-dir": "/data\u001b[2Jdownloads",
		"start-added-torrents": true,
		"rename-partial-files": true,
		"incomplete-dir-enabled": false,
		"incomplete-dir": "",
		"seedRatioLimited": true,
		"seedRatioLimit": 2.5,
		"idle-seeding-limit-enabled": false,
		"idle-seeding-limit": 30,
		"cache-size-mb": 8,
		"peer-port": 31129,
		"peer-port-random-on-start": false,
		"port-forwarding-enabled": true,
		"encryption": "tolerated",
		"peer-limit-global": 240,
		"peer-limit-per-torrent": 60,
		"pex-enabled": true,
		"dht-enabled": true,
		"lpd-enabled": false,
		"utp-enabled": true,
		"blocklist-enabled": false,
		"blocklist-url": "http://list.example/blocklist",
		"blocklist-size": 1234,
		"speed-limit-down-enabled": false,
		"speed-limit-down": 100,
		"speed-limit-up-enabled": false,
		"speed-limit-up": 100,
		"alt-speed-enabled": false,
		"alt-speed-down": 50,
		"alt-speed-up": 50,
		"alt-speed-time-enabled": false,
		"alt-speed-time-begin": 540,
		"alt-speed-time-end": 1020,
		"alt-speed-time-day": 127,
		"download-queue-enabled": true,
		"download-queue-size": 5,
		"seed-queue-enabled": false,
		"seed-queue-size": 10,
		"queue-stalled-enabled": true,
		"queue-stalled-minutes": 30
	},
	"result": "success",
	"tag": 1
}`

// runSettings executes the settings command with args, returning stdout,
// stderr, and the Execute error. The command's output is a *bytes.Buffer, so
// isTTY reports false and the piped (tab-separated) plaintext contract is
// exercised.
func runSettings(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewSettingsCmd()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// settingsConfigPath stands up a transmission stub serving the bespoke
// session-get payload and writes a config pointing at it.
func settingsConfigPath(t *testing.T) (string, func()) {
	t.Helper()

	keyring.MockInit()

	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // bespoke path
		responses: map[string]string{
			"session-get": bespokeSessionResponse,
		},
	}.handler())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{ServerURL: srv.URL}, cfgPath))

	return cfgPath, srv.Close
}

// pipedSettingRows splits piped settings output into CATEGORY/KEY/VALUE cells,
// keyed by setting key for order-independent assertions.
func pipedSettingRows(t *testing.T, out string) map[string][3]string {
	t.Helper()

	rows := make(map[string][3]string)
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		cells := strings.Split(line, "\t")
		require.Len(t, cells, 3, "piped settings rows are CATEGORY\\tKEY\\tVALUE")
		rows[cells[1]] = [3]string{cells[0], cells[1], cells[2]}
	}

	return rows
}

func TestSettingsPipedTabSeparated(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runSettings(t, "--config", cfgPath)
	require.NoError(t, err)

	rows := pipedSettingRows(t, out)
	require.Len(t, rows, 40, "every registry setting is listed")

	// Rows follow registry order (grouped by category): download first, queue last.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	assert.Equal(t, "download", strings.Split(lines[0], "\t")[0])
	assert.Equal(t, "download-dir", strings.Split(lines[0], "\t")[1])
	assert.Equal(t, "queue", strings.Split(lines[len(lines)-1], "\t")[0])

	// Typed values render in their plaintext form, across types.
	assert.Equal(t, [3]string{"network", "peer-port", "31129"}, rows["peer-port"])
	assert.Equal(t, [3]string{"download", "start-added-torrents", "true"}, rows["start-added-torrents"])
	assert.Equal(t, [3]string{"download", "seed-ratio-limit", "2.5"}, rows["seed-ratio-limit"])
	assert.Equal(t, [3]string{"bandwidth", "speed-limit-down", "100"}, rows["speed-limit-down"])

	// The bespoke wire's "tolerated" encryption normalizes to canonical "allowed".
	assert.Equal(t, [3]string{"network", "encryption", "allowed"}, rows["encryption"])

	// The read-only blocklist-size is still listed (set/reset reject it elsewhere).
	assert.Equal(t, [3]string{"network", "blocklist-size", "1234"}, rows["blocklist-size"])
}

func TestSettingsPipedSanitizesServerDerivedStrings(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runSettings(t, "--config", cfgPath)
	require.NoError(t, err)

	assert.NotContains(t, out, "\x1b", "raw ESC from download-dir must not reach plaintext output")

	rows := pipedSettingRows(t, out)
	assert.Equal(t, "/data[2Jdownloads", rows["download-dir"][2], "visible text survives sanitization")
}

func TestSettingsJSON(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runSettings(t, "--config", cfgPath, "--json")
	require.NoError(t, err)

	var got []struct {
		Key      string          `json:"key"`
		Category string          `json:"category"`
		Value    json.RawMessage `json:"value"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 40)

	// Values are native typed (number/bool/string), not stringified.
	byKey := make(map[string]struct {
		Category string
		Value    json.RawMessage
	})
	for _, r := range got {
		byKey[r.Key] = struct {
			Category string
			Value    json.RawMessage
		}{r.Category, r.Value}
	}

	assert.Equal(t, "network", byKey["peer-port"].Category)
	assert.JSONEq(t, "31129", string(byKey["peer-port"].Value), "int value is a native number")
	assert.JSONEq(t, "true", string(byKey["start-added-torrents"].Value), "bool value is a native bool")
	assert.JSONEq(t, "2.5", string(byKey["seed-ratio-limit"].Value), "float value is a native number")
	assert.JSONEq(t, `"allowed"`, string(byKey["encryption"].Value), "enum value is a native string")

	// JSON is a structured contract: the server-derived download-dir is NOT
	// sanitized, so the raw ESC survives in the JSON value.
	assert.Contains(t, out, "\\u001b", "JSON value carries the raw control byte unsanitized")
}

func TestSettingsJSONEmptyIsArrayNotNull(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	// A search that matches nothing yields an empty JSON array, not null.
	out, _, err := runSettings(t, "--config", cfgPath, "--search", "no-such-key", "--json")
	require.NoError(t, err)
	assert.Equal(t, "[]", strings.TrimSpace(out))
}

func TestSettingsJQ(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runSettings(t, "--config", cfgPath, "--jq", `.[] | select(.key=="peer-port") | .value`)
	require.NoError(t, err)

	assert.Equal(t, "31129", strings.TrimSpace(out))
}

func TestSettingsCategoryFilters(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runSettings(t, "--config", cfgPath, "--category", "network")
	require.NoError(t, err)

	rows := pipedSettingRows(t, out)
	require.Len(t, rows, 13, "the network category has 13 settings")
	for _, cells := range rows {
		assert.Equal(t, "network", cells[0], "only network rows survive the filter")
	}
}

func TestSettingsCategoryIsCaseInsensitive(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runSettings(t, "--config", cfgPath, "--category", "QUEUE")
	require.NoError(t, err)

	rows := pipedSettingRows(t, out)
	require.Len(t, rows, 6, "the queue category has 6 settings")
}

func TestSettingsInvalidCategoryErrors(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	_, _, err := runSettings(t, "--config", cfgPath, "--category", "bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid category "bogus"`)
	assert.Contains(t, err.Error(), "valid categories are", "the error lists the valid categories")
	assert.Contains(t, err.Error(), "download", "the valid-categories list includes the categories")
}

func TestSettingsSearchFiltersByKey(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	// --search narrows to keys containing the (case-insensitive) substring.
	out, _, err := runSettings(t, "--config", cfgPath, "--search", "SPEED")
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

func TestSettingsCategoryAppliesToJSON(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	// The category filter must apply to structured (--json) output, not only the
	// plaintext branch.
	out, _, err := runSettings(t, "--config", cfgPath, "--category", "queue", "--json")
	require.NoError(t, err)

	var got []struct {
		Category string `json:"category"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 6, "only the queue category survives the filter in JSON output")
	for _, r := range got {
		assert.Equal(t, "queue", r.Category)
	}
}

func TestSettingsNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runSettings(t, "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestSettingsGetBareValue(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	// Plaintext "get" prints only the bare value (no table) so it composes in
	// scripts.
	out, _, err := runSettings(t, "get", "peer-port", "--config", cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "31129", strings.TrimSpace(out))
}

func TestSettingsGetTypedValues(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	cases := map[string]string{
		"start-added-torrents": "true",                          // bool
		"seed-ratio-limit":     "2.5",                           // float
		"speed-limit-down":     "100",                           // int64
		"encryption":           "allowed",                       // enum (tolerated->allowed)
		"blocklist-size":       "1234",                          // read-only is still gettable
		"blocklist-url":        "http://list.example/blocklist", // string
	}
	for key, want := range cases {
		out, _, err := runSettings(t, "get", key, "--config", cfgPath)
		require.NoError(t, err, key)
		assert.Equal(t, want, strings.TrimSpace(out), key)
	}
}

func TestSettingsGetSanitizesServerDerivedString(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runSettings(t, "get", "download-dir", "--config", cfgPath)
	require.NoError(t, err)

	assert.NotContains(t, out, "\x1b", "raw ESC from download-dir must not reach plaintext output")
	assert.Equal(t, "/data[2Jdownloads", strings.TrimSpace(out), "visible text survives sanitization")
}

func TestSettingsGetUnknownKeyErrors(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	_, _, err := runSettings(t, "get", "bogus-key", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown setting "bogus-key"`)
	assert.Contains(t, err.Error(), "valid settings are", "the error lists the valid keys")
	assert.Contains(t, err.Error(), "peer-port", "the valid-keys list includes the registry keys")
}

func TestSettingsGetRequiresExactlyOneKey(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	_, _, errZero := runSettings(t, "get", "--config", cfgPath)
	require.Error(t, errZero, "zero keys is an error")

	_, _, errTwo := runSettings(t, "get", "peer-port", "dht-enabled", "--config", cfgPath)
	require.Error(t, errTwo, "two keys is an error")
}

func TestSettingsGetJSONSingleObject(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runSettings(t, "get", "peer-port", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var got struct {
		Key      string          `json:"key"`
		Category string          `json:"category"`
		Value    json.RawMessage `json:"value"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	assert.Equal(t, "peer-port", got.Key)
	assert.Equal(t, "network", got.Category)
	assert.JSONEq(t, "31129", string(got.Value), "int value is a native number")
}

func TestSettingsGetJSONNotSanitized(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	// JSON is a structured contract: the server-derived download-dir is NOT
	// sanitized, so the raw control byte survives in the JSON value.
	out, _, err := runSettings(t, "get", "download-dir", "--config", cfgPath, "--json")
	require.NoError(t, err)
	assert.Contains(t, out, "\\u001b", "JSON value carries the raw control byte unsanitized")
}

func TestSettingsGetJQFiltersTheObject(t *testing.T) {
	cfgPath, closeSrv := settingsConfigPath(t)
	defer closeSrv()

	out, _, err := runSettings(t, "get", "peer-port", "--config", cfgPath, "--jq", ".value")
	require.NoError(t, err)
	assert.Equal(t, "31129", strings.TrimSpace(out))
}

func TestSettingsGetNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runSettings(t, "get", "peer-port", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}
