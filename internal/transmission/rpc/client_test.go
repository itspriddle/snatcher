package rpc

import (
	"context"
	"io"
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

func TestClientSessionStatsDispatchesThroughNegotiatedProtocol(t *testing.T) {
	// After the CSRF probe (which carries no rpc-version header), the client
	// negotiates bespoke and SessionStats decodes the camelCase/kebab payload.
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "",
		sessionResponse: `{
			"arguments": {
				"activeTorrentCount": 4,
				"pausedTorrentCount": 1,
				"torrentCount": 5,
				"downloadSpeed": 2048,
				"uploadSpeed": 256,
				"cumulative-stats": {"uploadedBytes": 9, "downloadedBytes": 8, "filesAdded": 7, "sessionCount": 6, "secondsActive": 5},
				"current-stats": {"uploadedBytes": 4, "downloadedBytes": 3, "filesAdded": 2, "sessionCount": 1, "secondsActive": 0}
			},
			"result": "success",
			"tag": 1
		}`,
	}.handler())
	defer srv.Close()

	c := NewClient(srv.URL)

	stats, err := c.SessionStats(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(4), stats.ActiveTorrentCount)
	assert.Equal(t, int64(5), stats.TorrentCount)
	assert.Equal(t, int64(2048), stats.DownloadSpeed)
	assert.Equal(t, int64(9), stats.CumulativeStats.UploadedBytes)
	assert.Equal(t, int64(4), stats.CurrentStats.UploadedBytes)
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

func TestClientTorrentGetDispatchesThroughNegotiatedProtocol(t *testing.T) {
	// The CSRF probe carries a 6.0.0 header, so the client negotiates JSON-RPC
	// 2.0 and TorrentGet decodes the snake_case payload.
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "6.0.0",
		sessionResponse: `{
			"jsonrpc": "2.0",
			"result": {"torrents": [{"id": 42, "hash_string": "deadbeef", "name": "debian.iso", "status": 6}]},
			"id": 1
		}`,
	}.handler())
	defer srv.Close()

	c := NewClient(srv.URL)

	torrents, err := c.TorrentGet(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, torrents, 1)
	assert.Equal(t, int64(42), torrents[0].ID)
	assert.Equal(t, "deadbeef", torrents[0].Hash)
	assert.Equal(t, 6, torrents[0].Status)
}

func TestClientTorrentAddDispatchesThroughNegotiatedProtocol(t *testing.T) {
	var captured string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			w.Header().Set(rpcVersionHeader, "6.0.0")
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		body, _ := io.ReadAll(r.Body)
		captured = string(body)
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"result": {"torrent_added": {"id": 7, "name": "debian.iso", "hash_string": "deadbeef"}},
			"id": 1
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)

	res, err := c.TorrentAdd(context.Background(), AddRequest{Metainfo: "YmFzZTY0", Paused: true})
	require.NoError(t, err)
	assert.Equal(t, int64(7), res.ID)
	assert.Equal(t, "debian.iso", res.Name)
	assert.Equal(t, "deadbeef", res.Hash)
	assert.False(t, res.Duplicate)
	assert.Contains(t, captured, `"method":"torrent_add"`)
	assert.Contains(t, captured, `"metainfo":"YmFzZTY0"`)
}

func TestClientTorrentStartDispatchesThroughNegotiatedProtocol(t *testing.T) {
	var captured string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			w.Header().Set(rpcVersionHeader, "6.0.0")
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		body, _ := io.ReadAll(r.Body)
		captured = string(body)
		_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)

	err := c.TorrentStart(context.Background(), []any{int64(1)}, true)
	require.NoError(t, err)
	assert.Contains(t, captured, `"method":"torrent_start_now"`)
	assert.Contains(t, captured, `"ids":[1]`)
}

func TestClientTorrentStopDispatchesThroughNegotiatedProtocol(t *testing.T) {
	var captured string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		body, _ := io.ReadAll(r.Body)
		captured = string(body)
		_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)

	err := c.TorrentStop(context.Background(), []any{int64(2), "deadbeef"})
	require.NoError(t, err)
	assert.Contains(t, captured, `"method":"torrent-stop"`)
	assert.Contains(t, captured, `"ids":[2,"deadbeef"]`)
}

func TestClientTorrentVerifyDispatchesThroughNegotiatedProtocol(t *testing.T) {
	var captured string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		body, _ := io.ReadAll(r.Body)
		captured = string(body)
		_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)

	err := c.TorrentVerify(context.Background(), []any{int64(2), "deadbeef"})
	require.NoError(t, err)
	assert.Contains(t, captured, `"method":"torrent-verify"`)
	assert.Contains(t, captured, `"ids":[2,"deadbeef"]`)
}

func TestClientTorrentReannounceDispatchesThroughNegotiatedProtocol(t *testing.T) {
	var captured string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		body, _ := io.ReadAll(r.Body)
		captured = string(body)
		_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)

	err := c.TorrentReannounce(context.Background(), []any{int64(2), "deadbeef"})
	require.NoError(t, err)
	assert.Contains(t, captured, `"method":"torrent-reannounce"`)
	assert.Contains(t, captured, `"ids":[2,"deadbeef"]`)
}

func TestClientTorrentRemoveDispatchesThroughNegotiatedProtocol(t *testing.T) {
	var captured string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		body, _ := io.ReadAll(r.Body)
		captured = string(body)
		_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)

	err := c.TorrentRemove(context.Background(), []any{int64(2), "deadbeef"}, true)
	require.NoError(t, err)
	assert.Contains(t, captured, `"method":"torrent-remove"`)
	assert.Contains(t, captured, `"ids":[2,"deadbeef"]`)
	assert.Contains(t, captured, `"delete-local-data":true`)
}

func TestClientSessionSetDispatchesThroughNegotiatedProtocol(t *testing.T) {
	t.Run("jsonrpc", func(t *testing.T) {
		var captured string

		// The CSRF probe carries a 6.0.0 header, so the client negotiates JSON-RPC
		// 2.0 and SessionSet sends snake_case method and keys verbatim.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(sessionIDHeader) == "" {
				w.Header().Set(rpcVersionHeader, "6.0.0")
				w.Header().Set(sessionIDHeader, "session-token")
				w.WriteHeader(http.StatusConflict)
				return
			}

			body, _ := io.ReadAll(r.Body)
			captured = string(body)
			_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
		}))
		defer srv.Close()

		c := NewClient(srv.URL)

		err := c.SessionSet(context.Background(), map[string]any{"peer_port": 12345})
		require.NoError(t, err)
		assert.Contains(t, captured, `"method":"session_set"`)
		assert.Contains(t, captured, `"peer_port":12345`)
	})

	t.Run("bespoke", func(t *testing.T) {
		var captured string

		// The CSRF probe carries no rpc-version header, so the client negotiates
		// bespoke and SessionSet remaps the snake_case key to kebab-case.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(sessionIDHeader) == "" {
				w.Header().Set(sessionIDHeader, "session-token")
				w.WriteHeader(http.StatusConflict)
				return
			}

			body, _ := io.ReadAll(r.Body)
			captured = string(body)
			_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
		}))
		defer srv.Close()

		c := NewClient(srv.URL)

		err := c.SessionSet(context.Background(), map[string]any{"peer_port": 12345})
		require.NoError(t, err)
		assert.Contains(t, captured, `"method":"session-set"`)
		assert.Contains(t, captured, `"peer-port":12345`)
	})
}

func TestClientPortTestDispatchesThroughNegotiatedProtocol(t *testing.T) {
	t.Run("jsonrpc", func(t *testing.T) {
		// The CSRF probe carries a 6.0.0 header, so the client negotiates JSON-RPC
		// 2.0 and PortTest decodes the snake_case result, including ip_protocol.
		srv := httptest.NewServer(transmissionStub{
			rpcVersionHeader: "6.0.0",
			sessionResponse:  `{"jsonrpc": "2.0", "result": {"port_is_open": true, "ip_protocol": "ipv4"}, "id": 1}`,
		}.handler())
		defer srv.Close()

		c := NewClient(srv.URL)

		result, err := c.PortTest(context.Background())
		require.NoError(t, err)
		assert.True(t, result.Open)
		assert.Equal(t, "ipv4", result.IPProtocol)
	})

	t.Run("bespoke", func(t *testing.T) {
		// The CSRF probe carries no rpc-version header, so the client negotiates
		// bespoke and PortTest decodes only the kebab-case port-is-open flag.
		srv := httptest.NewServer(transmissionStub{
			rpcVersionHeader: "",
			sessionResponse:  `{"arguments": {"port-is-open": false}, "result": "success", "tag": 1}`,
		}.handler())
		defer srv.Close()

		c := NewClient(srv.URL)

		result, err := c.PortTest(context.Background())
		require.NoError(t, err)
		assert.False(t, result.Open)
		assert.Empty(t, result.IPProtocol)
	})
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
