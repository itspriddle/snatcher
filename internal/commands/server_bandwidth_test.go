package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
)

// jsonrpcBandwidthSessionResponse is a session_get payload matching
// rpc-spec-411.md (snake_case fields) carrying the seven bandwidth keys, used
// for the JSON-RPC 2.0 protocol path. Its values are deliberately distinct from
// the bespoke fixture's (asymmetric limits, an enabled download limit, alt-speed
// on, schedule off) so a test can tell the two schemes — and the two alt
// booleans — apart.
const jsonrpcBandwidthSessionResponse = `{
	"jsonrpc": "2.0",
	"result": {
		"version": "4.1.0 (ae226418eb)",
		"rpc_version_semver": "6.0.0",
		"speed_limit_down": 500,
		"speed_limit_down_enabled": true,
		"speed_limit_up": 100,
		"speed_limit_up_enabled": false,
		"alt_speed_enabled": true,
		"alt_speed_down": 50,
		"alt_speed_up": 25,
		"alt_speed_time_enabled": false
	},
	"id": 1
}`

// runBandwidth executes the bandwidth command tree with args, returning stdout,
// stderr, and the Execute error.
func runBandwidth(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewBandwidthCmd()
	AddGlobalFlags(cmd)

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// capturedBandwidthServer stands up a transmission stub serving session-get and
// session-stats under both wire schemes and recording every request body, then
// writes a config pointing at it. A non-empty rpcVersion selects the JSON-RPC
// 2.0 path (methods session_get / session_stats); an empty one selects the
// bespoke path (session-get / session-stats). The client's one-shot protocol
// probe always speaks the bespoke session-get envelope, so that method answers
// on both paths.
func capturedBandwidthServer(t *testing.T, captured *[]string, rpcVersion string) (string, func()) {
	t.Helper()

	keyring.MockInit()

	responses := map[string]string{
		"session-get":   bespokeSessionResponse,
		"session-stats": bespokeStatsResponse,
		"session_get":   jsonrpcBandwidthSessionResponse,
		"session_stats": jsonrpcStatsResponse,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			if rpcVersion != "" {
				w.Header().Set(rpcVersionHeader, rpcVersion)
			}
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

		if resp, ok := responses[envelope.Method]; ok {
			_, _ = w.Write([]byte(resp))
			return
		}

		_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
	}))

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	return cfgPath, srv.Close
}

// bandwidthConfigPath is capturedBandwidthServer for tests that do not inspect
// the requests.
func bandwidthConfigPath(t *testing.T, rpcVersion string) (string, func()) {
	t.Helper()

	var discarded []string

	return capturedBandwidthServer(t, &discarded, rpcVersion)
}

// capturedMethods returns the RPC method of every captured request body, in
// order.
func capturedMethods(t *testing.T, captured []string) []string {
	t.Helper()

	methods := make([]string, 0, len(captured))

	for _, body := range captured {
		var envelope struct {
			Method string `json:"method"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &envelope))
		methods = append(methods, envelope.Method)
	}

	return methods
}

func TestBandwidthBarePrintsHelp(t *testing.T) {
	out, _, err := runBandwidth(t)
	require.NoError(t, err)
	assert.Contains(t, out, "Usage:")
	assert.Contains(t, out, "bandwidth")
}

func TestBandwidthShowPlaintextJSONRPC(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "6.0.0")
	defer closeSrv()

	out, _, err := runBandwidth(t, "show", "--config", cfgPath)
	require.NoError(t, err)

	assert.Equal(t, strings.Join([]string{
		"Alt-speed:      yes (schedule: no)",
		"Download:",
		"  Current:      1.5 MiB/s",
		"  Limit:        500 KB/s",
		"  Alt limit:    50 KB/s",
		"Upload:",
		"  Current:      512.0 KiB/s",
		"  Limit:        100 KB/s (disabled)",
		"  Alt limit:    25 KB/s",
		"",
	}, "\n"), out)
}

func TestBandwidthShowPlaintextBespoke(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "")
	defer closeSrv()

	out, _, err := runBandwidth(t, "show", "--config", cfgPath)
	require.NoError(t, err)

	// Both limits are disabled in the bespoke fixture, so both keep their stored
	// value and carry the marker.
	assert.Equal(t, strings.Join([]string{
		"Alt-speed:      no (schedule: no)",
		"Download:",
		"  Current:      2.0 MiB/s",
		"  Limit:        100 KB/s (disabled)",
		"  Alt limit:    50 KB/s",
		"Upload:",
		"  Current:      1.0 MiB/s",
		"  Limit:        100 KB/s (disabled)",
		"  Alt limit:    50 KB/s",
		"",
	}, "\n"), out)
}

func TestBandwidthShowJSONJSONRPC(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "6.0.0")
	defer closeSrv()

	out, _, err := runBandwidth(t, "show", "--config", cfgPath, "--json")
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"download": {"current_speed": 1572864, "limit": 500, "limit_enabled": true, "alt_limit": 50},
		"upload": {"current_speed": 524288, "limit": 100, "limit_enabled": false, "alt_limit": 25},
		"alt_enabled": true,
		"alt_schedule_enabled": false
	}`, out)
}

func TestBandwidthShowJSONBespoke(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "")
	defer closeSrv()

	out, _, err := runBandwidth(t, "show", "--config", cfgPath, "--json")
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"download": {"current_speed": 2097152, "limit": 100, "limit_enabled": false, "alt_limit": 50},
		"upload": {"current_speed": 1048576, "limit": 100, "limit_enabled": false, "alt_limit": 50},
		"alt_enabled": false,
		"alt_schedule_enabled": false
	}`, out)
}

func TestBandwidthShowDownloadOnly(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "6.0.0")
	defer closeSrv()

	out, _, err := runBandwidth(t, "show", "download", "--config", cfgPath, "--json")
	require.NoError(t, err)

	// The other direction's key is absent entirely, not null and not zero-valued.
	assert.JSONEq(t, `{
		"download": {"current_speed": 1572864, "limit": 500, "limit_enabled": true, "alt_limit": 50},
		"alt_enabled": true,
		"alt_schedule_enabled": false
	}`, out)
	assert.NotContains(t, out, "upload")
}

func TestBandwidthShowUploadOnly(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "")
	defer closeSrv()

	out, _, err := runBandwidth(t, "show", "upload", "--config", cfgPath, "--json")
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"upload": {"current_speed": 1048576, "limit": 100, "limit_enabled": false, "alt_limit": 50},
		"alt_enabled": false,
		"alt_schedule_enabled": false
	}`, out)
	assert.NotContains(t, out, "download")
}

func TestBandwidthShowDirectionIsCaseInsensitive(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "6.0.0")
	defer closeSrv()

	out, _, err := runBandwidth(t, "show", "DOWNLOAD", "--config", cfgPath, "--json")
	require.NoError(t, err)
	assert.NotContains(t, out, "upload")
	assert.Contains(t, out, "download")
}

func TestBandwidthShowAltNarrowsBothDirections(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "6.0.0")
	defer closeSrv()

	out, _, err := runBandwidth(t, "show", "--config", cfgPath, "--alt", "--json")
	require.NoError(t, err)

	// --alt keeps alt_limit and drops the normal-profile pair, while the two alt
	// header fields survive every narrowing.
	assert.JSONEq(t, `{
		"download": {"current_speed": 1572864, "alt_limit": 50},
		"upload": {"current_speed": 524288, "alt_limit": 25},
		"alt_enabled": true,
		"alt_schedule_enabled": false
	}`, out)
	assert.NotContains(t, out, "limit_enabled")
}

func TestBandwidthShowDirectionAndAltCombined(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "6.0.0")
	defer closeSrv()

	out, _, err := runBandwidth(t, "show", "download", "--alt", "--config", cfgPath, "--json")
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"download": {"current_speed": 1572864, "alt_limit": 50},
		"alt_enabled": true,
		"alt_schedule_enabled": false
	}`, out)
}

func TestBandwidthShowNarrowedPlaintextMatchesJSON(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "6.0.0")
	defer closeSrv()

	out, _, err := runBandwidth(t, "show", "download", "--alt", "--config", cfgPath)
	require.NoError(t, err)

	// The plaintext field set matches the narrowed JSON exactly: no Upload block
	// and no normal-profile Limit line.
	assert.Equal(t, strings.Join([]string{
		"Alt-speed:      yes (schedule: no)",
		"Download:",
		"  Current:      1.5 MiB/s",
		"  Alt limit:    50 KB/s",
		"",
	}, "\n"), out)
}

func TestBandwidthShowJQ(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "6.0.0")
	defer closeSrv()

	out, _, err := runBandwidth(t, "show", "--config", cfgPath, "--jq", ".alt_enabled")
	require.NoError(t, err)
	assert.Equal(t, "true", strings.TrimSpace(out))

	out, _, err = runBandwidth(t, "show", "--config", cfgPath, "--jq", ".download.limit")
	require.NoError(t, err)
	assert.Equal(t, "500", strings.TrimSpace(out))
}

func TestBandwidthShowRejectsInvalidDirection(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedBandwidthServer(t, &captured, "6.0.0")
	defer closeSrv()

	_, _, err := runBandwidth(t, "show", "sideways", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid direction "sideways"`)
	assert.Contains(t, err.Error(), "download")
	assert.Contains(t, err.Error(), "upload")
	assert.Empty(t, captured, "an invalid direction aborts before any network call")
}

func TestBandwidthShowRejectsTooManyArgs(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "6.0.0")
	defer closeSrv()

	_, _, err := runBandwidth(t, "show", "download", "upload", "--config", cfgPath)
	require.Error(t, err, "more than one positional argument is an error")
}

func TestBandwidthShowIssuesOneSessionAndOneStats(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rpcVersion string
		want       []string
	}{
		// The first request is always the client's one-shot protocol probe, which
		// speaks the bespoke session-get envelope whatever scheme is in force.
		{"jsonrpc", "6.0.0", []string{"session-get", "session_get", "session_stats"}},
		{"bespoke", "", []string{"session-get", "session-get", "session-stats"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var captured []string
			cfgPath, closeSrv := capturedBandwidthServer(t, &captured, tc.rpcVersion)
			defer closeSrv()

			_, _, err := runBandwidth(t, "show", "--config", cfgPath)
			require.NoError(t, err)

			assert.Equal(t, tc.want, capturedMethods(t, captured))
		})
	}
}

func TestBandwidthShowSessionFailureWrapsServerURL(t *testing.T) {
	keyring.MockInit()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	out, _, err := runBandwidth(t, "show", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to "+srv.URL)
	assert.Empty(t, out, "no state is rendered on a connection failure")
}

func TestBandwidthShowStatsFailureWrapsServerURL(t *testing.T) {
	keyring.MockInit()

	// The session read succeeds and the stats read fails, proving the second
	// round-trip's failure is wrapped the same way as the first.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		body, _ := io.ReadAll(r.Body)

		var envelope struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &envelope)

		if envelope.Method == "session-stats" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		_, _ = w.Write([]byte(bespokeSessionResponse))
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	out, _, err := runBandwidth(t, "show", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to "+srv.URL)
	assert.Empty(t, out, "no state is rendered when the stats read fails")
}

func TestBandwidthShowNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runBandwidth(t, "show", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}
