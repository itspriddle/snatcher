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

// runPort executes the port command tree with args, returning stdout, stderr,
// and the Execute error. The command's output is a *bytes.Buffer, so isTTY
// reports false where it matters.
func runPort(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewPortCmd()
	AddGlobalFlags(cmd)

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// portConfigPath stands up a transmission stub serving the shared bespoke
// session-get payload and writes a config pointing at it. The payload's
// peer-port is 31129, with random-on-start false and forwarding true.
func portConfigPath(t *testing.T) (string, func()) {
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

func TestPortGetPlaintext(t *testing.T) {
	cfgPath, closeSrv := portConfigPath(t)
	defer closeSrv()

	out, _, err := runPort(t, "get", "--config", cfgPath)
	require.NoError(t, err)

	assert.Contains(t, out, "Port:")
	assert.Contains(t, out, "31129")
	assert.Contains(t, out, "Random on start:")
	assert.Contains(t, out, "Forwarding enabled:")

	// random-on-start is false and forwarding is true in the canned payload.
	lines := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		label, value, ok := strings.Cut(line, ":")
		require.True(t, ok, "plaintext is a Label: value block")
		lines[strings.TrimSpace(label)] = strings.TrimSpace(value)
	}
	assert.Equal(t, "31129", lines["Port"])
	assert.Equal(t, "no", lines["Random on start"])
	assert.Equal(t, "yes", lines["Forwarding enabled"])
}

func TestPortGetJSON(t *testing.T) {
	cfgPath, closeSrv := portConfigPath(t)
	defer closeSrv()

	out, _, err := runPort(t, "get", "--config", cfgPath, "--json")
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"port": 31129,
		"random_on_start": false,
		"forwarding_enabled": true
	}`, out)
}

func TestPortGetJQ(t *testing.T) {
	cfgPath, closeSrv := portConfigPath(t)
	defer closeSrv()

	out, _, err := runPort(t, "get", "--config", cfgPath, "--jq", ".port")
	require.NoError(t, err)
	assert.Equal(t, "31129", strings.TrimSpace(out))
}

func TestPortGetNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runPort(t, "get", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestPortBarePrintsHelp(t *testing.T) {
	out, _, err := runPort(t)
	require.NoError(t, err)
	assert.Contains(t, out, "Usage:")
	assert.Contains(t, out, "port")
}

// capturedPortServer stands up a transmission stub on the bespoke path that
// records every request body (so a test can assert what session-set sent) and
// answers session-get with the canned bespoke payload. The stub is stateless:
// the re-fetch after a write returns the same payload, which is sufficient to
// exercise the command's "report the server's values" flow.
func capturedPortServer(t *testing.T, captured *[]string) (string, func()) {
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

// portSetBody returns the captured session-set request body, failing the test
// if no write was sent.
func portSetBody(t *testing.T, captured []string) string {
	t.Helper()

	for _, body := range captured {
		if strings.Contains(body, "session-set") {
			return body
		}
	}

	require.FailNow(t, "no session-set request was sent")

	return ""
}

func TestPortSetPortDisablesRandomOnStart(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedPortServer(t, &captured)
	defer closeSrv()

	out, _, err := runPort(t, "set", "51413", "--config", cfgPath)
	require.NoError(t, err)

	// A positional port pins the port: it sends peer-port plus
	// peer-port-random-on-start=false (the transmission adapter kebab->snake).
	setBody := portSetBody(t, captured)
	assert.Contains(t, setBody, "51413", "the parsed port reaches the wire")
	assert.Contains(t, setBody, `"peer-port":51413`, "the kebab-case key reaches the wire")
	assert.Contains(t, setBody, `"peer-port-random-on-start":false`,
		"the port is pinned by disabling random-on-start")

	// After the write the resulting port state is rendered (re-fetched fixture).
	assert.Contains(t, out, "Port:")
	assert.Contains(t, out, "31129")
}

func TestPortSetExplicitRandomOverridesAutoDisable(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedPortServer(t, &captured)
	defer closeSrv()

	// An explicit --random keeps random-on-start true and wins over the
	// auto-disable that a positional port would otherwise trigger.
	_, _, err := runPort(t, "set", "51413", "--random", "--config", cfgPath)
	require.NoError(t, err)

	setBody := portSetBody(t, captured)
	assert.Contains(t, setBody, `"peer-port":51413`)
	assert.Contains(t, setBody, `"peer-port-random-on-start":true`,
		"the explicit --random overrides the auto-disable")
}

func TestPortSetExplicitRandomFalseWithPort(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedPortServer(t, &captured)
	defer closeSrv()

	// --random=false alongside a port is consistent with the auto-disable but is
	// an explicit user choice all the same.
	_, _, err := runPort(t, "set", "51413", "--random=false", "--config", cfgPath)
	require.NoError(t, err)

	setBody := portSetBody(t, captured)
	assert.Contains(t, setBody, `"peer-port-random-on-start":false`)
}

func TestPortSetForwardingOnlyWritesForwarding(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedPortServer(t, &captured)
	defer closeSrv()

	// --forwarding=false with no port writes only port-forwarding-enabled.
	_, _, err := runPort(t, "set", "--forwarding=false", "--config", cfgPath)
	require.NoError(t, err)

	setBody := portSetBody(t, captured)
	assert.Contains(t, setBody, `"port-forwarding-enabled":false`)
	assert.NotContains(t, setBody, "peer-port", "no port was given, so peer-port is not written")
}

func TestPortSetRejectsInvalidPort(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedPortServer(t, &captured)
	defer closeSrv()

	for _, bad := range []string{"0", "70000", "abc"} {
		captured = captured[:0]
		_, _, err := runPort(t, "set", bad, "--config", cfgPath)
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "invalid port", bad)
		assert.Contains(t, err.Error(), "must be 1-65535", bad)
		assert.Empty(t, captured, "an invalid port aborts before any write")
	}
}

func TestPortSetNothingToSet(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedPortServer(t, &captured)
	defer closeSrv()

	_, _, err := runPort(t, "set", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing to set")
	assert.Empty(t, captured, "no port and no flags makes no write")
}

func TestPortSetJSON(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedPortServer(t, &captured)
	defer closeSrv()

	out, _, err := runPort(t, "set", "51413", "--config", cfgPath, "--json")
	require.NoError(t, err)

	// The result is rendered in the same portState shape as "port get".
	assert.JSONEq(t, `{
		"port": 31129,
		"random_on_start": false,
		"forwarding_enabled": true
	}`, out)
}

func TestPortSetJQ(t *testing.T) {
	var captured []string
	cfgPath, closeSrv := capturedPortServer(t, &captured)
	defer closeSrv()

	out, _, err := runPort(t, "set", "51413", "--config", cfgPath, "--jq", ".port")
	require.NoError(t, err)
	assert.Equal(t, "31129", strings.TrimSpace(out))
}

func TestPortSetNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	// Validation passes, but no server is configured, so dial fails before any
	// network write.
	_, _, err := runPort(t, "set", "51413", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestPortSetTooManyArgs(t *testing.T) {
	cfgPath, closeSrv := portConfigPath(t)
	defer closeSrv()

	_, _, err := runPort(t, "set", "51413", "extra", "--config", cfgPath)
	require.Error(t, err, "more than one positional argument is an error")
}

// portTestConfigPath stands up a transmission stub answering the port-reachability
// probe and writes a config pointing at it. A non-empty rpcVersionHeader selects
// the JSON-RPC 2.0 path (method port_test); an empty one selects the bespoke path
// (method port-test). The response body is the canned answer for that method.
func portTestConfigPath(t *testing.T, rpcVersion, method, body string) (string, func()) {
	t.Helper()

	keyring.MockInit()

	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: rpcVersion,
		responses: map[string]string{
			method: body,
		},
	}.handler())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	return cfgPath, srv.Close
}

func TestPortTestOpenPlaintext(t *testing.T) {
	cfgPath, closeSrv := portTestConfigPath(t, "", "port-test",
		`{"arguments": {"port-is-open": true}, "result": "success", "tag": 1}`)
	defer closeSrv()

	out, _, err := runPort(t, "test", "--config", cfgPath)
	require.NoError(t, err, "an open port exits zero")
	assert.Contains(t, out, "Port is open")
}

func TestPortTestClosedPlaintextExitsNonZero(t *testing.T) {
	cfgPath, closeSrv := portTestConfigPath(t, "", "port-test",
		`{"arguments": {"port-is-open": false}, "result": "success", "tag": 1}`)
	defer closeSrv()

	out, _, err := runPort(t, "test", "--config", cfgPath)
	// A reachable-but-closed port is a probe failure: it renders the result but
	// returns the sentinel so Execute exits non-zero.
	require.Error(t, err)
	require.ErrorIs(t, err, errPortClosed)
	assert.Contains(t, out, "Port is closed")
}

func TestPortTestClosedJSONStillRendered(t *testing.T) {
	cfgPath, closeSrv := portTestConfigPath(t, "", "port-test",
		`{"arguments": {"port-is-open": false}, "result": "success", "tag": 1}`)
	defer closeSrv()

	out, _, err := runPort(t, "test", "--config", cfgPath, "--json")
	// Closed exits non-zero, but the structured {open:false} payload is still
	// written to stdout for --json consumers.
	require.Error(t, err)
	require.ErrorIs(t, err, errPortClosed)
	assert.JSONEq(t, `{"open": false}`, out)
}

func TestPortTestClosedJQStillRendered(t *testing.T) {
	cfgPath, closeSrv := portTestConfigPath(t, "", "port-test",
		`{"arguments": {"port-is-open": false}, "result": "success", "tag": 1}`)
	defer closeSrv()

	out, _, err := runPort(t, "test", "--config", cfgPath, "--jq", ".open")
	require.Error(t, err)
	require.ErrorIs(t, err, errPortClosed)
	assert.Equal(t, "false", strings.TrimSpace(out))
}

func TestPortTestJSONRPCSurfacesIPProtocol(t *testing.T) {
	cfgPath, closeSrv := portTestConfigPath(t, "6.0.0", "port_test",
		`{"jsonrpc": "2.0", "result": {"port_is_open": true, "ip_protocol": "ipv6"}, "id": 1}`)
	defer closeSrv()

	out, _, err := runPort(t, "test", "--config", cfgPath, "--json")
	require.NoError(t, err)
	// ip_protocol is present only on the JSON-RPC 2.0 scheme.
	assert.JSONEq(t, `{"open": true, "ip_protocol": "ipv6"}`, out)
}

func TestPortTestBespokeOmitsIPProtocol(t *testing.T) {
	cfgPath, closeSrv := portTestConfigPath(t, "", "port-test",
		`{"arguments": {"port-is-open": true}, "result": "success", "tag": 1}`)
	defer closeSrv()

	out, _, err := runPort(t, "test", "--config", cfgPath, "--json")
	require.NoError(t, err)
	// The bespoke scheme reports no ip_protocol, so it is omitted (omitempty).
	assert.JSONEq(t, `{"open": true}`, out)
}

func TestPortTestPlaintextShowsIPProtocol(t *testing.T) {
	cfgPath, closeSrv := portTestConfigPath(t, "6.0.0", "port_test",
		`{"jsonrpc": "2.0", "result": {"port_is_open": true, "ip_protocol": "ipv6"}, "id": 1}`)
	defer closeSrv()

	out, _, err := runPort(t, "test", "--config", cfgPath)
	require.NoError(t, err)
	assert.Contains(t, out, "Port is open")
	assert.Contains(t, out, "ipv6")
}

func TestPortTestUnreachableRendersNoResult(t *testing.T) {
	keyring.MockInit()

	// A server that fails every request maps to a connection error, distinct from
	// a reachable-but-closed port: it returns an error and renders no result.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	out, _, err := runPort(t, "test", "--config", cfgPath)
	require.Error(t, err)
	require.NotErrorIs(t, err, errPortClosed, "a connection failure is distinct from a closed port")
	assert.Contains(t, err.Error(), srv.URL)
	assert.Empty(t, out, "no result is rendered on a connection failure")
}

func TestPortTestNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runPort(t, "test", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}
