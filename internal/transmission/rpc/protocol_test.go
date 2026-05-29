package rpc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONRPCProtocolSessionGet(t *testing.T) {
	var rawBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"result": {"version": "4.1.0 (ae226418eb)", "rpc_version_semver": "6.0.0"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	session, err := jsonrpcProtocol{}.SessionGet(context.Background(), tr)
	require.NoError(t, err)

	assert.Equal(t, "4.1.0 (ae226418eb)", session.Version)
	assert.Equal(t, "6.0.0", session.RPCVersionSemver)
	assert.Equal(t, ProtocolJSONRPC, session.Protocol) //nolint:testifylint // encoded-compare false positive: ProtocolJSONRPC is an identifier name, not a JSON literal

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// JSON-RPC 2.0 envelope: jsonrpc/method/params/id, snake_case method.
	assert.Equal(t, "2.0", gotBody["jsonrpc"])
	assert.Equal(t, "session_get", gotBody["method"])
	assert.Contains(t, gotBody, "params")
	assert.Contains(t, gotBody, "id")
	assert.NotContains(t, gotBody, "arguments")
	assert.NotContains(t, gotBody, "tag")
}

func TestBespokeProtocolSessionGet(t *testing.T) {
	var rawBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{
			"arguments": {"version": "4.0.6 (38c164933e)", "rpc-version-semver": "5.4.0"},
			"result": "success",
			"tag": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	session, err := bespokeProtocol{}.SessionGet(context.Background(), tr)
	require.NoError(t, err)

	assert.Equal(t, "4.0.6 (38c164933e)", session.Version)
	assert.Equal(t, "5.4.0", session.RPCVersionSemver)
	assert.Equal(t, ProtocolBespoke, session.Protocol)

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// Bespoke envelope: method/arguments/tag, kebab-case method.
	assert.Equal(t, "session-get", gotBody["method"])
	assert.Contains(t, gotBody, "arguments")
	assert.Contains(t, gotBody, "tag")
	assert.NotContains(t, gotBody, "jsonrpc")
	assert.NotContains(t, gotBody, "params")
}

func TestJSONRPCProtocolSessionGetMapsErrorObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"error": {"code": -32601, "message": "method not found"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	_, err := jsonrpcProtocol{}.SessionGet(context.Background(), tr)
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "method not found")
}

func TestBespokeProtocolSessionGetMapsNonSuccessResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result": "no such session", "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	_, err := bespokeProtocol{}.SessionGet(context.Background(), tr)
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "no such session")
}

func TestJSONRPCProtocolSessionStats(t *testing.T) {
	var rawBody []byte

	// Snake_case field names matching docs/transmission/rpc-spec-411.md.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"result": {
				"active_torrent_count": 3,
				"paused_torrent_count": 2,
				"torrent_count": 5,
				"download_speed": 1024,
				"upload_speed": 512,
				"cumulative_stats": {
					"uploaded_bytes": 1000000,
					"downloaded_bytes": 2000000,
					"files_added": 42,
					"session_count": 7,
					"seconds_active": 360000
				},
				"current_stats": {
					"uploaded_bytes": 100,
					"downloaded_bytes": 200,
					"files_added": 1,
					"session_count": 1,
					"seconds_active": 60
				}
			},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	stats, err := jsonrpcProtocol{}.SessionStats(context.Background(), tr)
	require.NoError(t, err)

	assert.Equal(t, int64(3), stats.ActiveTorrentCount)
	assert.Equal(t, int64(2), stats.PausedTorrentCount)
	assert.Equal(t, int64(5), stats.TorrentCount)
	assert.Equal(t, int64(1024), stats.DownloadSpeed)
	assert.Equal(t, int64(512), stats.UploadSpeed)

	assert.Equal(t, int64(1000000), stats.CumulativeStats.UploadedBytes)
	assert.Equal(t, int64(2000000), stats.CumulativeStats.DownloadedBytes)
	assert.Equal(t, int64(42), stats.CumulativeStats.FilesAdded)
	assert.Equal(t, int64(7), stats.CumulativeStats.SessionCount)
	assert.Equal(t, int64(360000), stats.CumulativeStats.SecondsActive)

	assert.Equal(t, int64(100), stats.CurrentStats.UploadedBytes)
	assert.Equal(t, int64(200), stats.CurrentStats.DownloadedBytes)
	assert.Equal(t, int64(1), stats.CurrentStats.FilesAdded)
	assert.Equal(t, int64(1), stats.CurrentStats.SessionCount)
	assert.Equal(t, int64(60), stats.CurrentStats.SecondsActive)

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// JSON-RPC 2.0 envelope: snake_case method, jsonrpc/id present.
	assert.Equal(t, "2.0", gotBody["jsonrpc"])
	assert.Equal(t, "session_stats", gotBody["method"])
	assert.Contains(t, gotBody, "id")
	assert.NotContains(t, gotBody, "tag")
}

func TestBespokeProtocolSessionStats(t *testing.T) {
	var rawBody []byte

	// camelCase top-level + kebab-case nested keys matching
	// docs/transmission/rpc-spec-406.md.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{
			"arguments": {
				"activeTorrentCount": 3,
				"pausedTorrentCount": 2,
				"torrentCount": 5,
				"downloadSpeed": 1024,
				"uploadSpeed": 512,
				"cumulative-stats": {
					"uploadedBytes": 1000000,
					"downloadedBytes": 2000000,
					"filesAdded": 42,
					"sessionCount": 7,
					"secondsActive": 360000
				},
				"current-stats": {
					"uploadedBytes": 100,
					"downloadedBytes": 200,
					"filesAdded": 1,
					"sessionCount": 1,
					"secondsActive": 60
				}
			},
			"result": "success",
			"tag": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	stats, err := bespokeProtocol{}.SessionStats(context.Background(), tr)
	require.NoError(t, err)

	assert.Equal(t, int64(3), stats.ActiveTorrentCount)
	assert.Equal(t, int64(2), stats.PausedTorrentCount)
	assert.Equal(t, int64(5), stats.TorrentCount)
	assert.Equal(t, int64(1024), stats.DownloadSpeed)
	assert.Equal(t, int64(512), stats.UploadSpeed)

	assert.Equal(t, int64(1000000), stats.CumulativeStats.UploadedBytes)
	assert.Equal(t, int64(2000000), stats.CumulativeStats.DownloadedBytes)
	assert.Equal(t, int64(42), stats.CumulativeStats.FilesAdded)
	assert.Equal(t, int64(7), stats.CumulativeStats.SessionCount)
	assert.Equal(t, int64(360000), stats.CumulativeStats.SecondsActive)

	assert.Equal(t, int64(100), stats.CurrentStats.UploadedBytes)
	assert.Equal(t, int64(200), stats.CurrentStats.DownloadedBytes)
	assert.Equal(t, int64(1), stats.CurrentStats.FilesAdded)
	assert.Equal(t, int64(1), stats.CurrentStats.SessionCount)
	assert.Equal(t, int64(60), stats.CurrentStats.SecondsActive)

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// Bespoke envelope: kebab-case method, tag present, no jsonrpc.
	assert.Equal(t, "session-stats", gotBody["method"])
	assert.Contains(t, gotBody, "tag")
	assert.NotContains(t, gotBody, "jsonrpc")
}

func TestJSONRPCProtocolSessionStatsMapsErrorObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"error": {"code": -32601, "message": "method not found"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	_, err := jsonrpcProtocol{}.SessionStats(context.Background(), tr)
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "method not found")
}

func TestBespokeProtocolSessionStatsMapsNonSuccessResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result": "no such session", "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	_, err := bespokeProtocol{}.SessionStats(context.Background(), tr)
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "no such session")
}

func TestProtocolNames(t *testing.T) {
	assert.Equal(t, ProtocolJSONRPC, jsonrpcProtocol{}.Name()) //nolint:testifylint // encoded-compare false positive: ProtocolJSONRPC is an identifier name, not a JSON literal
	assert.Equal(t, ProtocolBespoke, bespokeProtocol{}.Name())
}
