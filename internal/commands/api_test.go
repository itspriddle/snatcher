package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
	"github.com/itspriddle/snatcher/internal/torrent"
)

// sessionGetResponse is a bespoke session-get payload used as the canned answer
// for the api command's real call (the CSRF probe is answered separately by the
// stub's 409). The map key order in --raw output is the server's, so raw
// assertions use assert.JSONEq.
const sessionGetResponse = `{
	"arguments": {"version": "4.0.5", "rpc-version-semver": "5.4.0"},
	"result": "success",
	"tag": 1
}`

// runAPI executes the api command with args against a bytes.Buffer stdout,
// returning stdout, stderr, and the Execute error.
func runAPI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewAPICmd()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// apiConfigPath stands up a transmission stub serving body for the given method
// and writes a config pointing at it, returning the config path and a closer.
func apiConfigPath(t *testing.T, method, body string) (string, func()) {
	t.Helper()

	keyring.MockInit()

	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // bespoke path
		responses: map[string]string{
			method: body,
		},
	}.handler())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{ServerURL: srv.URL}, cfgPath))

	return cfgPath, srv.Close
}

func TestAPIHappyPath(t *testing.T) {
	cfgPath, closeSrv := apiConfigPath(t, "session-get", sessionGetResponse)
	defer closeSrv()

	out, _, err := runAPI(t, "--config", cfgPath, "session-get")
	require.NoError(t, err)

	// The normalized envelope is rendered, not the raw server bytes.
	var got apiEnvelope
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "success", got.Result)
	assert.Equal(t, 1, got.Tag)
	assert.Zero(t, got.Code)
	assert.JSONEq(t, `{"version": "4.0.5", "rpc-version-semver": "5.4.0"}`, string(got.Arguments))
}

func TestAPIRPCErrorPrintsEnvelopeAndExits(t *testing.T) {
	const body = `{"arguments": {}, "result": "method name not recognized", "tag": 1}`

	cfgPath, closeSrv := apiConfigPath(t, "bogus-method", body)
	defer closeSrv()

	out, _, err := runAPI(t, "--config", cfgPath, "bogus-method")
	require.Error(t, err, "an RPC-level failure exits non-zero")
	assert.Contains(t, err.Error(), "method name not recognized")

	// The envelope is still printed to stdout despite the failure.
	var got apiEnvelope
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "method name not recognized", got.Result)
}

func TestAPIJQ(t *testing.T) {
	cfgPath, closeSrv := apiConfigPath(t, "session-get", sessionGetResponse)
	defer closeSrv()

	out, _, err := runAPI(t, "--config", cfgPath, "session-get", "--jq", ".arguments.version")
	require.NoError(t, err)

	assert.Equal(t, `"4.0.5"`, strings.TrimSpace(out))
}

func TestAPIRaw(t *testing.T) {
	cfgPath, closeSrv := apiConfigPath(t, "session-get", sessionGetResponse)
	defer closeSrv()

	out, _, err := runAPI(t, "--config", cfgPath, "session-get", "--raw")
	require.NoError(t, err)

	// --raw echoes the server's exact bytes, including its top-level shape and key
	// order (asserted structurally, since map order is not guaranteed).
	assert.JSONEq(t, sessionGetResponse, out)
}

func TestAPIRawAndJQMutuallyExclusive(t *testing.T) {
	cfgPath, closeSrv := apiConfigPath(t, "session-get", sessionGetResponse)
	defer closeSrv()

	_, _, err := runAPI(t, "--config", cfgPath, "session-get", "--raw", "--jq", ".result")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestAPIRequiresMethodArg(t *testing.T) {
	cfgPath, closeSrv := apiConfigPath(t, "session-get", sessionGetResponse)
	defer closeSrv()

	_, _, err := runAPI(t, "--config", cfgPath)
	require.Error(t, err)
}

func TestAPINotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runAPI(t, "--config", cfgPath, "session-get")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// noAPIBackend implements torrent.Backend but not transmission.APICaller, so it
// exercises the missing-capability branch of apiCaller.
type noAPIBackend struct{}

func (noAPIBackend) ServerInfo(context.Context) (torrent.ServerInfo, error) {
	return torrent.ServerInfo{}, nil
}
func (noAPIBackend) Stats(context.Context) (torrent.Stats, error) { return torrent.Stats{}, nil }
func (noAPIBackend) ListTorrents(context.Context, []int64) ([]torrent.Torrent, error) {
	return nil, nil
}
func (noAPIBackend) StartTorrents(context.Context, torrent.Selector, bool) error { return nil }
func (noAPIBackend) StopTorrents(context.Context, torrent.Selector) error        { return nil }

func TestAPIMissingCapabilityErrors(t *testing.T) {
	_, err := apiCaller(noAPIBackend{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backend does not support raw API calls")
}
