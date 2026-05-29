package rpc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// transmissionStub is an httptest handler that emulates a Transmission server:
// it answers the CSRF probe with a 409 carrying the session id (and optionally
// the rpc-version header), then answers session-get in the requested scheme.
type transmissionStub struct {
	rpcVersionHeader string // empty means the header is absent (old server)
	sessionResponse  string // JSON body returned for the real session-get call
}

func (s transmissionStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			if s.rpcVersionHeader != "" {
				w.Header().Set(rpcVersionHeader, s.rpcVersionHeader)
			}
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		_, _ = w.Write([]byte(s.sessionResponse))
	}
}

func TestClientDetectsJSONRPCFromHeader(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "6.0.0",
		sessionResponse: `{
			"jsonrpc": "2.0",
			"result": {"version": "4.1.0 (ae226418eb)", "rpc_version_semver": "6.0.0"},
			"id": 1
		}`,
	}.handler())
	defer srv.Close()

	c := NewClient(srv.URL)

	proto, err := c.Protocol(context.Background())
	require.NoError(t, err)
	assert.Equal(t, ProtocolJSONRPC, proto) //nolint:testifylint // encoded-compare false positive: ProtocolJSONRPC is an identifier name, not a JSON literal

	session, err := c.SessionGet(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "4.1.0 (ae226418eb)", session.Version)
	assert.Equal(t, "6.0.0", session.RPCVersionSemver)
	assert.Equal(t, ProtocolJSONRPC, session.Protocol) //nolint:testifylint // encoded-compare false positive: ProtocolJSONRPC is an identifier name, not a JSON literal
}

func TestClientDetectsBespokeFromOlderHeader(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "5.4.0",
		sessionResponse: `{
			"arguments": {"version": "4.0.6 (38c164933e)", "rpc-version-semver": "5.4.0"},
			"result": "success",
			"tag": 1
		}`,
	}.handler())
	defer srv.Close()

	c := NewClient(srv.URL)

	session, err := c.SessionGet(context.Background())
	require.NoError(t, err)
	assert.Equal(t, ProtocolBespoke, session.Protocol)
	assert.Equal(t, "4.0.6 (38c164933e)", session.Version)
	assert.Equal(t, "5.4.0", session.RPCVersionSemver)
}

func TestClientFallsBackToBespokeWhenHeaderAbsent(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // older server: no version header
		sessionResponse: `{
			"arguments": {"version": "2.93 (3c5870d4f5)", "rpc-version-semver": "3.7.0"},
			"result": "success",
			"tag": 1
		}`,
	}.handler())
	defer srv.Close()

	c := NewClient(srv.URL)

	proto, err := c.Protocol(context.Background())
	require.NoError(t, err)
	assert.Equal(t, ProtocolBespoke, proto)

	session, err := c.SessionGet(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "2.93 (3c5870d4f5)", session.Version)
}

func TestClientDetectsOncePerClient(t *testing.T) {
	var probes atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			probes.Add(1)
			w.Header().Set(rpcVersionHeader, "6.0.0")
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"result": {"version": "4.1.0", "rpc_version_semver": "6.0.0"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)

	for range 3 {
		_, err := c.SessionGet(context.Background())
		require.NoError(t, err)
	}

	// The CSRF/version probe (the only request that arrives without a cached
	// session id) happens exactly once thanks to sync.Once; later calls reuse
	// both the detected protocol and the cached session id.
	assert.Equal(t, int32(1), probes.Load())
}

func TestClientDetectSurfacesTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)

	_, err := c.SessionGet(context.Background())
	require.ErrorIs(t, err, ErrAuth)
}

func TestSemverMajor(t *testing.T) {
	tests := []struct {
		name      string
		semver    string
		wantMajor int
		wantOK    bool
	}{
		{name: "jsonrpc threshold", semver: "6.0.0", wantMajor: 6, wantOK: true},
		{name: "older bespoke", semver: "5.4.0", wantMajor: 5, wantOK: true},
		{name: "major only", semver: "7", wantMajor: 7, wantOK: true},
		{name: "empty", semver: "", wantMajor: 0, wantOK: false},
		{name: "non-numeric major", semver: "v6.0.0", wantMajor: 0, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			major, ok := semverMajor(tt.semver)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantMajor, major)
		})
	}
}
