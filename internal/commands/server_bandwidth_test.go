package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
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
// on, schedule on) so a test can tell the two schemes — and the two alt
// booleans — apart. The schedule flag is true here and false in the bespoke
// fixture so neither value is the Go zero value in every test: a decoder that
// dropped the field would fail this scheme's assertions.
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
		"alt_speed_time_enabled": true
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

	return capturedBandwidthSessionServer(t, captured, rpcVersion, bespokeSessionResponse, jsonrpcBandwidthSessionResponse)
}

// capturedBandwidthSessionServer is capturedBandwidthServer with each scheme's
// session-get payload supplied by the caller, so a test that turns on the
// server's starting state — the alt switch a toggle inverts — can choose it.
func capturedBandwidthSessionServer(t *testing.T, captured *[]string, rpcVersion, bespokeSession, jsonrpcSession string) (string, func()) {
	t.Helper()

	keyring.MockInit()

	responses := map[string]string{
		"session-get":   bespokeSession,
		"session-stats": bespokeStatsResponse,
		"session_get":   jsonrpcSession,
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
		"Alt-speed:      yes (schedule: yes)",
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
		"alt_schedule_enabled": true
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
		"alt_schedule_enabled": true
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
		"alt_schedule_enabled": true
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
		"alt_schedule_enabled": true
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
		"Alt-speed:      yes (schedule: yes)",
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

// bandwidthSetBody returns the one captured session-set request body — under
// either wire scheme's method spelling — failing the test unless exactly one
// write was sent, which is itself the "a limit and its switch travel in a single
// SetSession" assertion.
func bandwidthSetBody(t *testing.T, captured []string) string {
	t.Helper()

	var bodies []string

	for _, body := range captured {
		var envelope struct {
			Method string `json:"method"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &envelope))

		if envelope.Method == "session-set" || envelope.Method == "session_set" {
			bodies = append(bodies, body)
		}
	}

	require.Len(t, bodies, 1, "exactly one session-set write is expected")

	return bodies[0]
}

func TestBandwidthUpdateWritesValueAndEnablesLimit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rpcVersion string
		direction  string
		wantValue  string
		wantSwitch string
	}{
		{"jsonrpc download", "6.0.0", "download", `"speed_limit_down":500`, `"speed_limit_down_enabled":true`},
		{"jsonrpc upload", "6.0.0", "upload", `"speed_limit_up":500`, `"speed_limit_up_enabled":true`},
		{"bespoke download", "", "download", `"speed-limit-down":500`, `"speed-limit-down-enabled":true`},
		{"bespoke upload", "", "upload", `"speed-limit-up":500`, `"speed-limit-up-enabled":true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var captured []string
			cfgPath, closeSrv := capturedBandwidthServer(t, &captured, tc.rpcVersion)
			defer closeSrv()

			_, _, err := runBandwidth(t, "update", tc.direction, "500", "--config", cfgPath)
			require.NoError(t, err)

			// The value and its enabled companion travel together: setting a limit
			// also turns it on, so the caller never writes the pair by hand.
			setBody := bandwidthSetBody(t, captured)
			assert.Contains(t, setBody, tc.wantValue, "the KB/s value reaches the wire as a number")
			assert.Contains(t, setBody, tc.wantSwitch, "the limit is enabled alongside its value")
		})
	}
}

func TestBandwidthUpdateOffWritesOnlyTheSwitch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rpcVersion string
		direction  string
		wantSwitch string
		wantNoKey  string
	}{
		{"jsonrpc download", "6.0.0", "download", `"speed_limit_down_enabled":false`, `"speed_limit_down":`},
		{"jsonrpc upload", "6.0.0", "upload", `"speed_limit_up_enabled":false`, `"speed_limit_up":`},
		{"bespoke download", "", "download", `"speed-limit-down-enabled":false`, `"speed-limit-down":`},
		{"bespoke upload", "", "upload", `"speed-limit-up-enabled":false`, `"speed-limit-up":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var captured []string
			cfgPath, closeSrv := capturedBandwidthServer(t, &captured, tc.rpcVersion)
			defer closeSrv()

			// "off" is case-insensitive, matching every other enum argument.
			_, _, err := runBandwidth(t, "update", tc.direction, "OFF", "--config", cfgPath)
			require.NoError(t, err)

			// Only the switch flips: the stored limit survives so re-enabling
			// restores the number the user last set.
			setBody := bandwidthSetBody(t, captured)
			assert.Contains(t, setBody, tc.wantSwitch)
			assert.NotContains(t, setBody, tc.wantNoKey, "the stored limit value is left untouched")
		})
	}
}

func TestBandwidthUpdateAltWritesOnlyTheStoredLimit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rpcVersion string
		direction  string
		wantValue  string
		wantNoKey  string
	}{
		{"jsonrpc download", "6.0.0", "download", `"alt_speed_down":50`, "alt_speed_enabled"},
		{"jsonrpc upload", "6.0.0", "upload", `"alt_speed_up":50`, "alt_speed_enabled"},
		{"bespoke download", "", "download", `"alt-speed-down":50`, "alt-speed-enabled"},
		{"bespoke upload", "", "upload", `"alt-speed-up":50`, "alt-speed-enabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var captured []string
			cfgPath, closeSrv := capturedBandwidthServer(t, &captured, tc.rpcVersion)
			defer closeSrv()

			_, _, err := runBandwidth(t, "update", tc.direction, "50", "--alt", "--config", cfgPath)
			require.NoError(t, err)

			// The alt pair is a stored profile, not a live limit: one shared switch
			// governs both directions, so writing it here would throttle the other
			// direction too.
			setBody := bandwidthSetBody(t, captured)
			assert.Contains(t, setBody, tc.wantValue)
			assert.NotContains(t, setBody, tc.wantNoKey,
				"an --alt write never touches the shared alt-speed switch")
		})
	}
}

func TestBandwidthUpdateAltOffIsRejected(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedBandwidthServer(t, &captured, "6.0.0")
	defer closeSrv()

	_, _, err := runBandwidth(t, "update", "download", "off", "--alt", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "snatcher server bandwidth alt off",
		"the error names the command that owns the shared switch")
	assert.Empty(t, captured, "the rejection aborts before any network call")
}

func TestBandwidthUpdateRejectsInvalidDirection(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedBandwidthServer(t, &captured, "6.0.0")
	defer closeSrv()

	_, _, err := runBandwidth(t, "update", "sideways", "500", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid direction "sideways"`)
	assert.Contains(t, err.Error(), "download")
	assert.Contains(t, err.Error(), "upload")
	assert.Empty(t, captured, "an invalid direction aborts before any network call")
}

func TestBandwidthUpdateRejectsInvalidSpeed(t *testing.T) {
	// No human-unit parser exists here: a suffixed or fractional speed is an
	// error, not a value to interpret.
	for _, speed := range []string{"-1", "5M", "1.5", "fast", ""} {
		t.Run("speed="+speed, func(t *testing.T) {
			var captured []string
			cfgPath, closeSrv := capturedBandwidthServer(t, &captured, "6.0.0")
			defer closeSrv()

			// The speed follows "--" so a negative value is read as an argument
			// rather than as an unknown shorthand flag.
			_, _, err := runBandwidth(t, "update", "--config", cfgPath, "download", "--", speed)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "must be a non-negative integer (KB/s) or 'off'")
			assert.Empty(t, captured, "an invalid speed performs no write")
		})
	}
}

func TestBandwidthUpdateRejectsWrongArgCount(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "6.0.0")
	defer closeSrv()

	_, _, err := runBandwidth(t, "update", "download", "--config", cfgPath)
	require.Error(t, err, "both a direction and a speed are required")
}

func TestBandwidthUpdateRendersNarrowedReadBack(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "6.0.0")
	defer closeSrv()

	// The read-back is narrowed to the direction and profile that was written,
	// exactly as "show download" would render it (the stub is stateless, so the
	// re-read returns the fixture's values).
	out, _, err := runBandwidth(t, "update", "download", "500", "--config", cfgPath, "--json")
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"download": {"current_speed": 1572864, "limit": 500, "limit_enabled": true, "alt_limit": 50},
		"alt_enabled": true,
		"alt_schedule_enabled": true
	}`, out)
}

func TestBandwidthUpdateAltRendersAltReadBack(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "6.0.0")
	defer closeSrv()

	out, _, err := runBandwidth(t, "update", "download", "50", "--alt", "--config", cfgPath, "--json")
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"download": {"current_speed": 1572864, "alt_limit": 50},
		"alt_enabled": true,
		"alt_schedule_enabled": true
	}`, out)
}

func TestBandwidthUpdatePlaintextAndJQ(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "")
	defer closeSrv()

	out, _, err := runBandwidth(t, "update", "upload", "100", "--config", cfgPath)
	require.NoError(t, err)

	assert.Equal(t, strings.Join([]string{
		"Alt-speed:      no (schedule: no)",
		"Upload:",
		"  Current:      1.0 MiB/s",
		"  Limit:        100 KB/s (disabled)",
		"  Alt limit:    50 KB/s",
		"",
	}, "\n"), out)

	out, _, err = runBandwidth(t, "update", "upload", "100", "--config", cfgPath, "--jq", ".upload.limit")
	require.NoError(t, err)
	assert.Equal(t, "100", strings.TrimSpace(out))
}

func TestBandwidthUpdateWriteFailureWrapsServerURL(t *testing.T) {
	keyring.MockInit()

	// The protocol probe succeeds and the write fails, so the failure surfaces
	// with the resolved server URL like every sibling in the group.
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

		if envelope.Method == "session-set" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		_, _ = w.Write([]byte(bespokeSessionResponse))
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	out, _, err := runBandwidth(t, "update", "download", "500", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to "+srv.URL)
	assert.Empty(t, out, "no state is rendered when the write fails")
}

// withAltSpeedEnabled returns a session-get fixture with its alt-speed switch
// forced to enabled, under the given scheme's key spelling. The two shared
// fixtures start on opposite states, so this is what lets a toggle test pick
// either starting state in either wire scheme; requiring the literal to be
// present keeps a later fixture edit from silently neutering the test.
func withAltSpeedEnabled(t *testing.T, body, key string, enabled bool) string {
	t.Helper()

	want := fmt.Sprintf("%q: %t", key, enabled)
	if strings.Contains(body, want) {
		return body
	}

	from := fmt.Sprintf("%q: %t", key, !enabled)
	require.Contains(t, body, from, "the fixture carries no alt-speed switch to force")

	return strings.Replace(body, from, want, 1)
}

// bandwidthAltServer stands up the capturing stub with the named scheme's
// alt-speed switch forced to enabled, returning the config path and a cleanup.
func bandwidthAltServer(t *testing.T, captured *[]string, rpcVersion string, enabled bool) (string, func()) {
	t.Helper()

	if rpcVersion == "" {
		bespoke := withAltSpeedEnabled(t, bespokeSessionResponse, "alt-speed-enabled", enabled)

		return capturedBandwidthSessionServer(t, captured, rpcVersion, bespoke, jsonrpcBandwidthSessionResponse)
	}

	jsonrpc := withAltSpeedEnabled(t, jsonrpcBandwidthSessionResponse, "alt_speed_enabled", enabled)

	return capturedBandwidthSessionServer(t, captured, rpcVersion, bespokeSessionResponse, jsonrpc)
}

func TestBandwidthAltOnAndOffWriteDirectly(t *testing.T) {
	for _, tc := range []struct {
		name        string
		rpcVersion  string
		state       string
		want        string
		wantMethods []string
	}{
		// The first request is the client's one-shot protocol probe, which always
		// speaks the bespoke session-get envelope. Nothing else precedes the write:
		// "on" and "off" know the value they want without reading it.
		{"jsonrpc on", "6.0.0", "on", `"alt_speed_enabled":true`, []string{"session-get", "session_set", "session_get", "session_stats"}},
		{"jsonrpc off", "6.0.0", "off", `"alt_speed_enabled":false`, []string{"session-get", "session_set", "session_get", "session_stats"}},
		{"bespoke on", "", "on", `"alt-speed-enabled":true`, []string{"session-get", "session-set", "session-get", "session-stats"}},
		{"bespoke off", "", "off", `"alt-speed-enabled":false`, []string{"session-get", "session-set", "session-get", "session-stats"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var captured []string
			cfgPath, closeSrv := capturedBandwidthServer(t, &captured, tc.rpcVersion)
			defer closeSrv()

			_, _, err := runBandwidth(t, "alt", tc.state, "--config", cfgPath)
			require.NoError(t, err)

			assert.Contains(t, bandwidthSetBody(t, captured), tc.want)
			assert.Equal(t, tc.wantMethods, capturedMethods(t, captured),
				"on and off write the switch without reading it first")
		})
	}
}

func TestBandwidthAltToggleWritesTheInverse(t *testing.T) {
	for _, tc := range []struct {
		name        string
		rpcVersion  string
		starting    bool
		want        string
		wantMethods []string
	}{
		// toggle reads the switch and writes its inverse, so its request sequence
		// carries an extra session-get ahead of the write.
		{"jsonrpc from off", "6.0.0", false, `"alt_speed_enabled":true`, []string{"session-get", "session_get", "session_set", "session_get", "session_stats"}},
		{"jsonrpc from on", "6.0.0", true, `"alt_speed_enabled":false`, []string{"session-get", "session_get", "session_set", "session_get", "session_stats"}},
		{"bespoke from off", "", false, `"alt-speed-enabled":true`, []string{"session-get", "session-get", "session-set", "session-get", "session-stats"}},
		{"bespoke from on", "", true, `"alt-speed-enabled":false`, []string{"session-get", "session-get", "session-set", "session-get", "session-stats"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var captured []string
			cfgPath, closeSrv := bandwidthAltServer(t, &captured, tc.rpcVersion, tc.starting)
			defer closeSrv()

			_, _, err := runBandwidth(t, "alt", "toggle", "--config", cfgPath)
			require.NoError(t, err)

			assert.Contains(t, bandwidthSetBody(t, captured), tc.want)
			assert.Equal(t, tc.wantMethods, capturedMethods(t, captured))
		})
	}
}

func TestBandwidthAltStateIsCaseInsensitive(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedBandwidthServer(t, &captured, "6.0.0")
	defer closeSrv()

	_, _, err := runBandwidth(t, "alt", "ON", "--config", cfgPath)
	require.NoError(t, err)
	assert.Contains(t, bandwidthSetBody(t, captured), `"alt_speed_enabled":true`)
}

func TestBandwidthAltRejectsInvalidState(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedBandwidthServer(t, &captured, "6.0.0")
	defer closeSrv()

	_, _, err := runBandwidth(t, "alt", "maybe", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid state "maybe"`)
	assert.Contains(t, err.Error(), "on")
	assert.Contains(t, err.Error(), "off")
	assert.Contains(t, err.Error(), "toggle")
	assert.Empty(t, captured, "an invalid state aborts before any network call")
}

func TestBandwidthAltRejectsWrongArgCount(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "6.0.0")
	defer closeSrv()

	_, _, err := runBandwidth(t, "alt", "--config", cfgPath)
	require.Error(t, err, "exactly one state argument is required")

	_, _, err = runBandwidth(t, "alt", "on", "off", "--config", cfgPath)
	require.Error(t, err, "exactly one state argument is required")
}

func TestBandwidthAltRendersUnnarrowedReadBack(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "6.0.0")
	defer closeSrv()

	// Flipping the switch changes which profile is in force for both directions,
	// so the read-back is the full picture, not a narrowed slice of it. The stub
	// is stateless, so the re-read returns the fixture's values.
	out, _, err := runBandwidth(t, "alt", "on", "--config", cfgPath, "--json")
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"download": {"current_speed": 1572864, "limit": 500, "limit_enabled": true, "alt_limit": 50},
		"upload": {"current_speed": 524288, "limit": 100, "limit_enabled": false, "alt_limit": 25},
		"alt_enabled": true,
		"alt_schedule_enabled": true
	}`, out)
}

func TestBandwidthAltPlaintextAndJQ(t *testing.T) {
	cfgPath, closeSrv := bandwidthConfigPath(t, "")
	defer closeSrv()

	out, _, err := runBandwidth(t, "alt", "off", "--config", cfgPath)
	require.NoError(t, err)

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

	out, _, err = runBandwidth(t, "alt", "toggle", "--config", cfgPath, "--jq", ".alt_enabled")
	require.NoError(t, err)
	assert.Equal(t, "false", strings.TrimSpace(out))
}

func TestBandwidthAltWriteFailureWrapsServerURL(t *testing.T) {
	keyring.MockInit()

	// The protocol probe succeeds and the write fails, so the failure surfaces
	// with the resolved server URL like every sibling in the group.
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

		if envelope.Method == "session-set" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		_, _ = w.Write([]byte(bespokeSessionResponse))
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	out, _, err := runBandwidth(t, "alt", "toggle", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to "+srv.URL)
	assert.Empty(t, out, "no state is rendered when the write fails")
}

func TestBandwidthShowNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runBandwidth(t, "show", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}
