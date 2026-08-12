package commands

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func TestServerResetWritesDefaultAndReportsServerValue(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	// reset writes the registry default for the key, then re-fetches and reports
	// the server's confirmed value (the stateless stub echoes the fixture's 31129).
	out, _, err := runServer(t, "", "reset", "peer-port", "--config", cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "peer-port: 31129\n", out)

	// A session-set carrying the Transmission default (51413) is sent before any
	// read.
	setBody := settingSetBody(t, captured)
	assert.Contains(t, setBody, "peer-port", "the kebab-case key reaches the wire")
	assert.Contains(t, setBody, "51413", "the registry default is sent, not the current value")

	// The write precedes the re-fetch (the bespoke detection probe issues an
	// initial session-get, so the re-fetch is the *last* session-get).
	setIdx, refetchIdx := -1, -1
	for i, body := range captured {
		if setIdx == -1 && strings.Contains(body, "session-set") {
			setIdx = i
		}
		if strings.Contains(body, "session-get") {
			refetchIdx = i
		}
	}
	require.GreaterOrEqual(t, setIdx, 0)
	require.GreaterOrEqual(t, refetchIdx, 0)
	assert.Less(t, setIdx, refetchIdx, "session-set is sent before the re-fetch session-get")
}

func TestServerResetMultipleKeysInOneWrite(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	// Several keys reset together travel in a single session-set, and are reported
	// in registry order regardless of the order they were named.
	out, _, err := runServer(t, "", "reset", "peer-port", "download-dir", "encryption", "--config", cfgPath)
	require.NoError(t, err)

	// download-dir (download) precedes peer-port and encryption (network).
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3)
	assert.True(t, strings.HasPrefix(lines[0], "download-dir: "))
	assert.True(t, strings.HasPrefix(lines[1], "peer-port: "))
	assert.True(t, strings.HasPrefix(lines[2], "encryption: "))

	setCount := 0
	for _, body := range captured {
		if strings.Contains(body, "session-set") {
			setCount++
		}
	}
	assert.Equal(t, 1, setCount, "every default travels in one session-set")

	setBody := settingSetBody(t, captured)
	assert.Contains(t, setBody, "51413", "the peer-port default")
	assert.Contains(t, setBody, "~/Downloads", "the download-dir default")
	assert.Contains(t, setBody, "preferred", "the encryption default")
}

func TestServerResetDuplicateKeysCollapse(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	out, _, err := runServer(t, "", "reset", "peer-port", "peer-port", "--config", cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "peer-port: 31129\n", out, "a repeated key is reported once")
}

func TestServerResetEnumDefault(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	// An enum setting resets to its canonical default value.
	_, _, err := runServer(t, "", "reset", "encryption", "--config", cfgPath)
	require.NoError(t, err)

	setBody := settingSetBody(t, captured)
	assert.Contains(t, setBody, "preferred", "the registry default enum value reaches the wire")
}

func TestServerResetJSON(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	out, _, err := runServer(t, "", "reset", "peer-port", "--config", cfgPath, "--json")
	require.NoError(t, err)

	var got []struct {
		Key      string          `json:"key"`
		Category string          `json:"category"`
		Value    json.RawMessage `json:"value"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 1, "only the reset setting is reported")

	assert.Equal(t, "peer-port", got[0].Key)
	assert.Equal(t, "network", got[0].Category)
	assert.JSONEq(t, "31129", string(got[0].Value), "value is the native re-fetched number")
}

func TestServerResetJQ(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	out, _, err := runServer(t, "", "reset", "peer-port", "--config", cfgPath, "--jq", ".[0].value")
	require.NoError(t, err)
	assert.Equal(t, "31129", strings.TrimSpace(out))
}

func TestServerResetSanitizesServerDerivedString(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	// The re-fetched download-dir (server-derived) is sanitized on the plaintext
	// path.
	out, _, err := runServer(t, "", "reset", "download-dir", "--config", cfgPath)
	require.NoError(t, err)
	assert.NotContains(t, out, "\x1b", "raw ESC from the re-fetched download-dir must not reach plaintext")
	assert.Equal(t, "download-dir: /data[2Jdownloads\n", out)
}

func TestServerResetUnknownKeyErrors(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	_, _, err := runServer(t, "", "reset", "bogus-key", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown setting "bogus-key"`)
	assert.Contains(t, err.Error(), "valid settings are", "the error lists the valid keys")
	assert.Contains(t, err.Error(), "peer-port", "the valid-keys list includes the registry keys")
	assert.Empty(t, captured, "an unknown key aborts before any write")
}

func TestServerResetRejectsBadKeyAmongGoodOnes(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	// Every key is validated up front, so one bad key aborts the whole batch with
	// no partial write.
	_, _, err := runServer(t, "", "reset", "peer-port", "bogus-key", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown setting "bogus-key"`)
	assert.Empty(t, captured, "no partial write when one key of a batch is invalid")
}

func TestServerResetReadOnlyKeyRejected(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	_, _, err := runServer(t, "", "reset", "blocklist-size", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `setting "blocklist-size" is read-only`)
	assert.Empty(t, captured, "a read-only key aborts before any write")
}

func TestServerResetRequiresAtLeastOneKey(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedSettingsServer(t, &captured)
	defer closeSrv()

	_, _, err := runServer(t, "", "reset", "--config", cfgPath)
	require.Error(t, err, "zero keys is an error")
	assert.Contains(t, err.Error(), "at least one setting key")
	assert.Empty(t, captured, "no request is made with no key")
}

func TestServerResetNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	// Validation passes, but there is no server configured, so dial fails before
	// any network write.
	_, _, err := runServer(t, "", "reset", "peer-port", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}
