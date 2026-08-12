package commands

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
)

// bespokeSessionResponse is a session-get payload (kebab-case settings keys,
// with the seedRatio camelCase exceptions and the cache-size-mb wire spelling)
// covering every category, used to exercise the server settings commands
// end-to-end on the bespoke path. The download-dir carries an embedded ESC so
// the sanitization contract can be checked. encryption is "tolerated" on the
// wire, which the rpc layer normalizes to the canonical "allowed". The version
// fields let "server show" report the server identity from the same payload.
const bespokeSessionResponse = `{
	"arguments": {
		"version": "4.0.6 (38c164933e)",
		"rpc-version": 17,
		"rpc-version-semver": "5.3.0",
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
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	return cfgPath, srv.Close
}

// maliciousEncryptionConfigPath stands up a stub whose session-get returns an
// encryption value carrying a terminal escape, modeling a compromised or MITM'd
// server. The bespoke rpc layer only remaps "tolerated"->"allowed", so any other
// encryption string passes through verbatim to the neutral SessionSettings.
func maliciousEncryptionConfigPath(t *testing.T) (string, func()) {
	t.Helper()

	keyring.MockInit()

	const resp = `{
		"arguments": {"encryption": "requ\u001b[2Jired"},
		"result": "success",
		"tag": 1
	}`

	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // bespoke path
		responses: map[string]string{
			"session-get": resp,
		},
	}.handler())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	return cfgPath, srv.Close
}

// capturedSettingsServer stands up a transmission stub on the bespoke path that
// records every request body (so a test can assert what session-set sent) and
// answers session-get with the canned bespoke payload. The stub is stateless:
// the re-fetch after a write returns the same payload, which is sufficient to
// exercise the command's "report the server's values" flow.
func capturedSettingsServer(t *testing.T, captured *[]string) (string, func()) {
	t.Helper()

	keyring.MockInit()

	srv := httptest.NewServer(func() http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(sessionIDHeader) == "" {
				w.Header().Set(sessionIDHeader, "session-token")
				w.WriteHeader(http.StatusConflict)
				return
			}

			body, _ := io.ReadAll(r.Body)
			*captured = append(*captured, string(body))

			var envelope struct {
				Method string `json:"method"`
			}
			_ = json.Unmarshal(body, &envelope)

			if envelope.Method == "session-get" {
				_, _ = w.Write([]byte(bespokeSessionResponse))
				return
			}

			_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
		}
	}())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	return cfgPath, srv.Close
}

// settingSetBody returns the captured session-set request body, or fails the
// test if none was sent.
func settingSetBody(t *testing.T, captured []string) string {
	t.Helper()

	for _, body := range captured {
		if strings.Contains(body, "session-set") {
			return body
		}
	}

	require.FailNow(t, "no session-set request was captured")

	return ""
}
