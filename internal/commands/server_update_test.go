package commands

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// serverSubcommand returns the named child of the server command group, failing
// the test when it is missing.
func serverSubcommand(t *testing.T, name string) *cobra.Command {
	t.Helper()

	for _, c := range NewServerCmd().Commands() {
		if c.Name() == name {
			return c
		}
	}

	require.FailNowf(t, "missing subcommand", "server %s is not registered", name)

	return nil
}

func TestServerUpdateWritesAndEchoesValues(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	// peer-port and dht-enabled are written in a single call via typed flags; the
	// command echoes back exactly what it wrote (no re-fetch), in registry order.
	out, _, err := runServer(t, "", "update", "--peer-port", "12345", "--dht-enabled=false", "--config", cfgPath)
	require.NoError(t, err)

	// peer-port (network) precedes dht-enabled (network) in registry order.
	assert.Equal(t, "peer-port: 12345\ndht-enabled: false\n", out)

	// A session-set carrying the typed values was sent.
	setBody := settingSetBody(t, captured)
	assert.Contains(t, setBody, `"peer-port":12345`, "the int value reaches the wire as a number")
	assert.Contains(t, setBody, `"dht-enabled":false`, "the bool value reaches the wire as a bool")

	// The command does not re-fetch: the only session-get is the protocol
	// detection probe, sent before the write.
	setIdx, getCount := -1, 0
	for i, body := range captured {
		if setIdx == -1 && strings.Contains(body, "session-set") {
			setIdx = i
		}
		if strings.Contains(body, "session-get") {
			getCount++
		}
	}
	require.GreaterOrEqual(t, setIdx, 0)
	assert.LessOrEqual(t, getCount, 1, "no re-fetch: at most the one detection-probe session-get")
}

func TestServerUpdateWireTypesPerSettingType(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	// One flag of each setting type, asserting the neutral wire mapping.
	_, _, err := runServer(t, "", "update",
		"--dht-enabled=true",   // bool
		"--peer-port", "12345", // int
		"--speed-limit-down", "500", // int64
		"--seed-ratio-limit", "2.5", // float
		"--download-dir", "/tmp/dl", // string
		"--encryption", "REQUIRED", // enum
		"--config", cfgPath,
	)
	require.NoError(t, err)

	setBody := settingSetBody(t, captured)
	assert.Contains(t, setBody, `"dht-enabled":true`, "bool")
	assert.Contains(t, setBody, `"peer-port":12345`, "int")
	assert.Contains(t, setBody, `"speed-limit-down":500`, "int64")
	// seed-ratio-limit maps to the bespoke wire's camelCase seedRatioLimit.
	assert.Contains(t, setBody, `"seedRatioLimit":2.5`, "float")
	assert.Contains(t, setBody, `/tmp/dl`, "string")
	assert.Contains(t, setBody, `required`, "enum normalized to canonical lowercase")
}

func TestServerUpdateUnsetFlagsAreOmittedFromWire(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	// Only --peer-port is passed; no other setting key may appear on the wire.
	_, _, err := runServer(t, "", "update", "--peer-port", "12345", "--config", cfgPath)
	require.NoError(t, err)

	setBody := settingSetBody(t, captured)
	assert.Contains(t, setBody, `"peer-port":12345`)
	assert.NotContains(t, setBody, "dht-enabled", "an unset flag's wire key is absent")
	assert.NotContains(t, setBody, "download-dir", "an unset flag's wire key is absent")
	assert.NotContains(t, setBody, "encryption", "an unset flag's wire key is absent")
}

func TestServerUpdateEnumValue(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	// An enum value is normalized to its canonical lowercase form on the way out.
	_, _, err := runServer(t, "", "update", "--encryption", "REQUIRED", "--config", cfgPath)
	require.NoError(t, err)

	setBody := settingSetBody(t, captured)
	assert.Contains(t, setBody, "required", "the canonical enum value reaches the wire")
}

func TestServerUpdateInvalidEnumAbortsWithoutWrite(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	// An invalid enum value aborts before any network call, listing allowed values.
	_, _, err := runServer(t, "", "update", "--encryption", "bogus", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encryption")
	assert.Contains(t, err.Error(), "must be one of: allowed, preferred, required")
	assert.Empty(t, captured, "an invalid enum aborts before any write")
}

func TestServerUpdateJSON(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	out, _, err := runServer(t, "", "update", "--peer-port", "12345", "--dht-enabled=false", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var got []struct {
		Key      string          `json:"key"`
		Category string          `json:"category"`
		Value    json.RawMessage `json:"value"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 2, "only the two affected settings are reported")

	// Echoed values are exactly what was written, in registry order.
	assert.Equal(t, "peer-port", got[0].Key)
	assert.Equal(t, "network", got[0].Category)
	assert.JSONEq(t, "12345", string(got[0].Value), "value is the native written number")
	assert.Equal(t, "dht-enabled", got[1].Key)
	assert.JSONEq(t, "false", string(got[1].Value))
}

func TestServerUpdateJQ(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	out, _, err := runServer(t, "", "update", "--peer-port", "12345",
		"--config", cfgPath, "--jq", ".[0].value")
	require.NoError(t, err)
	assert.Equal(t, "12345", strings.TrimSpace(out))
}

func TestServerUpdateSanitizesServerDerivedString(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	// A server-derived string setting (download-dir) carrying an escape sequence is
	// sanitized on the plaintext echo, even though the value originated from the
	// command line.
	out, _, err := runServer(t, "", "update", "--download-dir", "/tmp\x1b[2Jdl", "--config", cfgPath)
	require.NoError(t, err)
	assert.NotContains(t, out, "\x1b", "raw ESC must not reach the plaintext echo")
	assert.Equal(t, "download-dir: /tmp[2Jdl\n", out)
}

func TestServerUpdateJSONNotSanitized(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	// JSON is a structured contract: the server-derived download-dir is NOT
	// sanitized, so the raw control byte survives in the JSON value.
	out, _, err := runServer(t, "", "update", "--download-dir", "/tmp\x1b[2Jdl", "--config", cfgPath, "--json")
	require.NoError(t, err)
	assert.Contains(t, out, "\\u001b", "JSON value carries the raw control byte unsanitized")
}

func TestServerUpdateRejectsPositionalArgs(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	// The positional key/value form is gone; positional args are rejected.
	_, _, err := runServer(t, "", "update", "peer-port", "12345", "--config", cfgPath)
	require.Error(t, err)
	assert.Empty(t, captured, "no request is made when positional args are given")
}

func TestServerUpdateRequiresAtLeastOneFlag(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	_, _, err := runServer(t, "", "update", "--config", cfgPath)
	require.Error(t, err, "no setting flag is an error")
	assert.Contains(t, err.Error(), "at least one setting flag")
	assert.Empty(t, captured, "no request is made with no setting flag")
}

func TestServerUpdateNoBlocklistSizeFlag(t *testing.T) {
	updateCmd := serverSubcommand(t, "update")

	assert.Nil(t, updateCmd.Flags().Lookup("blocklist-size"), "the read-only blocklist-size has no flag")
	assert.NotNil(t, updateCmd.Flags().Lookup("peer-port"), "settable settings have a flag")
}

func TestServerUpdateNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	// Validation passes, but there is no server configured, so dial fails before
	// any network write.
	_, _, err := runServer(t, "", "update", "--peer-port", "12345", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestServerUpdateOutputFollowsRegistryOrder(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	// Flags given out of registry order are still echoed in registry order:
	// download-dir (download) precedes peer-port (network).
	out, _, err := runServer(t, "", "update", "--peer-port", "12345", "--download-dir", "/tmp/dl", "--config", cfgPath)
	require.NoError(t, err)

	assert.Equal(t, "download-dir: /tmp/dl\npeer-port: 12345\n", out)
}
