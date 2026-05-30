package transmission

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/itspriddle/snatcher/internal/torrent"
	"github.com/itspriddle/snatcher/internal/transmission/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sessionIDHeader and rpcVersionHeader mirror the CSRF/version headers the rpc
// transport handles. They are duplicated here (the rpc constants are
// unexported) so the stub can emulate a real Transmission server.
const (
	sessionIDHeader  = "X-Transmission-Session-Id"
	rpcVersionHeader = "X-Transmission-Rpc-Version"
)

// transmissionStub is an httptest handler that emulates a Transmission server:
// it answers the CSRF probe with a 409 carrying the session id (and optionally
// the rpc-version header), then answers later requests by dispatching on the
// requested method to the configured response body.
type transmissionStub struct {
	rpcVersionHeader string            // empty means the header is absent (old server)
	responses        map[string]string // method name -> JSON response body
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

		// The rpc.Client detection probe always sends a bespoke "session-get"
		// purely to run the CSRF handshake (its body is discarded). Answer any
		// method we have no canned response for with a benign empty success so
		// the handshake completes; the real call then routes to its response.
		if resp, ok := s.responses[decodeMethod(r)]; ok {
			_, _ = w.Write([]byte(resp))
			return
		}

		_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "id": 1}`))
	}
}

// decodeMethod reads the request body's "method" field, which both wire
// schemes share, so the stub can route to the right canned response.
func decodeMethod(r *http.Request) string {
	var envelope struct {
		Method string `json:"method"`
	}
	_ = json.NewDecoder(r.Body).Decode(&envelope)
	return envelope.Method
}

func TestBackendServerInfoJSONRPC(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "6.0.0",
		responses: map[string]string{
			"session_get": `{
				"jsonrpc": "2.0",
				"result": {"version": "4.1.0 (ae226418eb)", "rpc_version_semver": "6.0.0"},
				"id": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	info, err := b.ServerInfo(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "4.1.0 (ae226418eb)", info.Version)
	assert.Equal(t, "6.0.0", info.RPCVersionSemver)
	assert.Equal(t, rpc.ProtocolJSONRPC, info.Protocol) //nolint:testifylint // encoded-compare false positive: ProtocolJSONRPC is an identifier name, not a JSON literal
}

func TestBackendServerInfoBespoke(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // old server: no version header -> bespoke
		responses: map[string]string{
			"session-get": `{
				"arguments": {"version": "4.0.6 (38c164933e)", "rpc-version-semver": "5.4.0"},
				"result": "success",
				"tag": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	info, err := b.ServerInfo(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "4.0.6 (38c164933e)", info.Version)
	assert.Equal(t, "5.4.0", info.RPCVersionSemver)
	assert.Equal(t, rpc.ProtocolBespoke, info.Protocol)
}

func TestBackendStatsJSONRPC(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "6.0.0",
		responses: map[string]string{
			"session_stats": `{
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
			}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	stats, err := b.Stats(context.Background())
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
	assert.Equal(t, int64(60), stats.CurrentStats.SecondsActive)
}

func TestBackendStatsBespoke(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "",
		responses: map[string]string{
			"session-stats": `{
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
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	stats, err := b.Stats(context.Background())
	require.NoError(t, err)

	assert.Equal(t, int64(4), stats.ActiveTorrentCount)
	assert.Equal(t, int64(1), stats.PausedTorrentCount)
	assert.Equal(t, int64(5), stats.TorrentCount)
	assert.Equal(t, int64(2048), stats.DownloadSpeed)
	assert.Equal(t, int64(256), stats.UploadSpeed)

	assert.Equal(t, int64(9), stats.CumulativeStats.UploadedBytes)
	assert.Equal(t, int64(8), stats.CumulativeStats.DownloadedBytes)
	assert.Equal(t, int64(4), stats.CurrentStats.UploadedBytes)
	assert.Equal(t, int64(0), stats.CurrentStats.SecondsActive)
}

func TestBackendMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.ServerInfo(context.Background())
	require.ErrorIs(t, err, torrent.ErrAuth)
	// The underlying rpc sentinel is preserved in the wrap chain.
	require.ErrorIs(t, err, rpc.ErrAuth)
	assert.NotErrorIs(t, err, torrent.ErrUnreachable)
}

func TestBackendMapsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.ServerInfo(context.Background())
	require.ErrorIs(t, err, torrent.ErrUnreachable)
	require.ErrorIs(t, err, rpc.ErrTransport)
	assert.NotErrorIs(t, err, torrent.ErrAuth)
}

func TestBackendMapsRPCError(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "",
		responses: map[string]string{
			"session-stats": `{"result": "no such session", "tag": 1}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.Stats(context.Background())
	// A server-side RPC error (bespoke result != "success") maps to
	// ErrUnreachable while preserving the rpc.ErrRPC sentinel and its message.
	require.ErrorIs(t, err, torrent.ErrUnreachable)
	require.ErrorIs(t, err, rpc.ErrRPC)
	assert.ErrorContains(t, err, "no such session")
}

func TestBackendListTorrents(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // bespoke path: camelCase torrent fields
		responses: map[string]string{
			"torrent-get": `{
				"arguments": {"torrents": [{
					"id": 7,
					"hashString": "abc123",
					"name": "ubuntu.iso",
					"status": 4,
					"percentDone": 0.5,
					"totalSize": 2048,
					"sizeWhenDone": 2048,
					"leftUntilDone": 1024,
					"rateDownload": 1000,
					"rateUpload": 200,
					"uploadRatio": 1.5,
					"eta": 3600,
					"peersConnected": 10,
					"peersSendingToUs": 4,
					"peersGettingFromUs": 2,
					"addedDate": 1700000000,
					"doneDate": 0,
					"activityDate": 1700000100,
					"downloadDir": "/downloads",
					"error": 0,
					"errorString": "",
					"labels": ["linux", "iso"],
					"queuePosition": 3,
					"isFinished": false,
					"isStalled": false
				}]},
				"result": "success",
				"tag": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	torrents, err := b.ListTorrents(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, torrents, 1)

	got := torrents[0]
	assert.Equal(t, int64(7), got.ID)
	assert.Equal(t, "abc123", got.Hash)
	assert.Equal(t, "ubuntu.iso", got.Name)
	assert.Equal(t, torrent.StatusDownloading, got.Status)
	assert.InDelta(t, 0.5, got.PercentDone, 0.0001)
	assert.Equal(t, int64(2048), got.TotalSize)
	assert.Equal(t, int64(1000), got.RateDownload)
	assert.Equal(t, int64(200), got.RateUpload)
	assert.InDelta(t, 1.5, got.UploadRatio, 0.0001)
	assert.Equal(t, int64(3600), got.ETA)
	assert.Equal(t, int64(10), got.PeersConnected)
	assert.Equal(t, "/downloads", got.DownloadDir)
	assert.Equal(t, []string{"linux", "iso"}, got.Labels)
	assert.Equal(t, int64(3), got.QueuePosition)
}

func TestBackendListTorrentsPromotesErrorStatus(t *testing.T) {
	// A non-zero error code (or non-empty errorString) promotes status to error,
	// even though the raw transfer status is "downloading" (4).
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "",
		responses: map[string]string{
			"torrent-get": `{
				"arguments": {"torrents": [{
					"id": 1,
					"hashString": "h",
					"name": "broken",
					"status": 4,
					"error": 3,
					"errorString": "tracker gave error"
				}]},
				"result": "success",
				"tag": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	torrents, err := b.ListTorrents(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, torrents, 1)

	assert.Equal(t, torrent.StatusError, torrents[0].Status)
	assert.Equal(t, "tracker gave error", torrents[0].Error)
}

func TestBackendListTorrentsEmpty(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "",
		responses: map[string]string{
			"torrent-get": `{"arguments": {"torrents": []}, "result": "success", "tag": 1}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	torrents, err := b.ListTorrents(context.Background(), nil)
	require.NoError(t, err)
	assert.NotNil(t, torrents)
	assert.Empty(t, torrents)
}

func TestBackendListTorrentsFiltersByID(t *testing.T) {
	var captured string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		body, _ := io.ReadAll(r.Body)
		captured = string(body)

		_, _ = w.Write([]byte(`{"arguments": {"torrents": [{"id": 2, "hashString": "h2", "name": "two", "status": 6}]}, "result": "success", "tag": 1}`))
	}))
	defer srv.Close()

	b := New(srv.URL)

	torrents, err := b.ListTorrents(context.Background(), []int64{2})
	require.NoError(t, err)
	require.Len(t, torrents, 1)
	assert.Equal(t, int64(2), torrents[0].ID)
	assert.Equal(t, torrent.StatusSeeding, torrents[0].Status)
	assert.Contains(t, captured, `"ids":[2]`)
}

func TestBackendListTorrentsMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.ListTorrents(context.Background(), nil)
	require.ErrorIs(t, err, torrent.ErrAuth)
	assert.NotErrorIs(t, err, torrent.ErrUnreachable)
}

func TestBackendListTorrentsMapsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.ListTorrents(context.Background(), nil)
	require.ErrorIs(t, err, torrent.ErrUnreachable)
	assert.NotErrorIs(t, err, torrent.ErrAuth)
}

// captureActionServer is a Transmission stub that records the body of the first
// non-handshake request (the action call) so tests can assert the method and
// ids. The rpcVersion header drives protocol negotiation.
func captureActionServer(t *testing.T, rpcVersion string, captured *string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			if rpcVersion != "" {
				w.Header().Set(rpcVersionHeader, rpcVersion)
			}
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		body, _ := io.ReadAll(r.Body)
		*captured = string(body)
		// A body valid for both schemes: bespoke checks result == "success";
		// JSON-RPC checks only that error is absent (result value is ignored).
		_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": "success", "arguments": {}, "id": 1, "tag": 1}`))
	}))
}

func TestBackendStartTorrents(t *testing.T) {
	tests := []struct {
		name       string
		now        bool
		rpcVersion string
		wantMethod string
	}{
		{name: "jsonrpc start", now: false, rpcVersion: "6.0.0", wantMethod: `"method":"torrent_start"`},
		{name: "jsonrpc start-now", now: true, rpcVersion: "6.0.0", wantMethod: `"method":"torrent_start_now"`},
		{name: "bespoke start", now: false, rpcVersion: "", wantMethod: `"method":"torrent-start"`},
		{name: "bespoke start-now", now: true, rpcVersion: "", wantMethod: `"method":"torrent-start-now"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured string

			srv := captureActionServer(t, tt.rpcVersion, &captured)
			defer srv.Close()

			b := New(srv.URL)

			sel := torrent.Selector{IDs: []int64{1}, Hashes: []string{"deadbeef"}}
			err := b.StartTorrents(context.Background(), sel, tt.now)
			require.NoError(t, err)

			assert.Contains(t, captured, tt.wantMethod)
			assert.Contains(t, captured, `"ids":[1,"deadbeef"]`)
		})
	}
}

func TestBackendStopTorrents(t *testing.T) {
	var captured string

	srv := captureActionServer(t, "6.0.0", &captured)
	defer srv.Close()

	b := New(srv.URL)

	sel := torrent.Selector{IDs: []int64{2}, Hashes: []string{"cafebabe"}}
	err := b.StopTorrents(context.Background(), sel)
	require.NoError(t, err)

	assert.Contains(t, captured, `"method":"torrent_stop"`)
	assert.Contains(t, captured, `"ids":[2,"cafebabe"]`)
}

func TestBackendStartStopAllOmitsIDs(t *testing.T) {
	t.Run("start --all omits ids", func(t *testing.T) {
		var captured string

		srv := captureActionServer(t, "6.0.0", &captured)
		defer srv.Close()

		b := New(srv.URL)

		err := b.StartTorrents(context.Background(), torrent.Selector{All: true}, false)
		require.NoError(t, err)

		assert.Contains(t, captured, `"method":"torrent_start"`)
		assert.NotContains(t, captured, `"ids"`)
	})

	t.Run("stop --all omits ids", func(t *testing.T) {
		var captured string

		srv := captureActionServer(t, "6.0.0", &captured)
		defer srv.Close()

		b := New(srv.URL)

		err := b.StopTorrents(context.Background(), torrent.Selector{All: true})
		require.NoError(t, err)

		assert.Contains(t, captured, `"method":"torrent_stop"`)
		assert.NotContains(t, captured, `"ids"`)
	})
}

func TestBackendStopTorrentsMapsRPCError(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "",
		responses: map[string]string{
			"torrent-stop": `{"result": "invalid argument", "tag": 1}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	err := b.StopTorrents(context.Background(), torrent.Selector{IDs: []int64{1}})
	require.ErrorIs(t, err, torrent.ErrUnreachable)
	require.ErrorIs(t, err, rpc.ErrRPC)
	assert.ErrorContains(t, err, "invalid argument")
}

func TestBackendStartTorrentsMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	err := b.StartTorrents(context.Background(), torrent.Selector{IDs: []int64{1}}, false)
	require.ErrorIs(t, err, torrent.ErrAuth)
	assert.NotErrorIs(t, err, torrent.ErrUnreachable)
}

func TestBackendRemoveTorrents(t *testing.T) {
	tests := []struct {
		name       string
		deleteData bool
		rpcVersion string
		wantMethod string
		wantFlag   string
	}{
		{name: "jsonrpc keep data", deleteData: false, rpcVersion: "6.0.0", wantMethod: `"method":"torrent_remove"`, wantFlag: `"delete_local_data":false`},
		{name: "jsonrpc delete data", deleteData: true, rpcVersion: "6.0.0", wantMethod: `"method":"torrent_remove"`, wantFlag: `"delete_local_data":true`},
		{name: "bespoke keep data", deleteData: false, rpcVersion: "", wantMethod: `"method":"torrent-remove"`, wantFlag: `"delete-local-data":false`},
		{name: "bespoke delete data", deleteData: true, rpcVersion: "", wantMethod: `"method":"torrent-remove"`, wantFlag: `"delete-local-data":true`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured string

			srv := captureActionServer(t, tt.rpcVersion, &captured)
			defer srv.Close()

			b := New(srv.URL)

			sel := torrent.Selector{IDs: []int64{2}, Hashes: []string{"cafebabe"}}
			err := b.RemoveTorrents(context.Background(), sel, tt.deleteData)
			require.NoError(t, err)

			assert.Contains(t, captured, tt.wantMethod)
			assert.Contains(t, captured, `"ids":[2,"cafebabe"]`)
			assert.Contains(t, captured, tt.wantFlag)
		})
	}
}

func TestBackendRemoveTorrentsAllOmitsIDs(t *testing.T) {
	var captured string

	srv := captureActionServer(t, "6.0.0", &captured)
	defer srv.Close()

	b := New(srv.URL)

	err := b.RemoveTorrents(context.Background(), torrent.Selector{All: true}, true)
	require.NoError(t, err)

	assert.Contains(t, captured, `"method":"torrent_remove"`)
	assert.Contains(t, captured, `"delete_local_data":true`)
	assert.NotContains(t, captured, `"ids"`)
}

func TestBackendRemoveTorrentsMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	err := b.RemoveTorrents(context.Background(), torrent.Selector{IDs: []int64{1}}, false)
	require.ErrorIs(t, err, torrent.ErrAuth)
	assert.NotErrorIs(t, err, torrent.ErrUnreachable)
}

func TestBackendRemoveTorrentsMapsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := New(srv.URL)

	err := b.RemoveTorrents(context.Background(), torrent.Selector{IDs: []int64{1}}, false)
	require.ErrorIs(t, err, torrent.ErrUnreachable)
	assert.NotErrorIs(t, err, torrent.ErrAuth)
}

// captureAddServer is a Transmission stub that records the body of the
// non-handshake request and answers with a valid torrent-add result (a new
// add). The reply shape differs per scheme — JSON-RPC's "result" must be the
// torrent_added object, bespoke's "result" is the "success" string with the
// torrent-added object under "arguments" — so the response is chosen from the
// negotiated protocol (empty rpcVersion -> bespoke).
func captureAddServer(t *testing.T, rpcVersion string, captured *string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			if rpcVersion != "" {
				w.Header().Set(rpcVersionHeader, rpcVersion)
			}
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		body, _ := io.ReadAll(r.Body)
		*captured = string(body)

		if rpcVersion == "" {
			_, _ = w.Write([]byte(`{"arguments": {"torrent-added": {"id": 1, "name": "n", "hashString": "h"}}, "result": "success", "tag": 1}`))
			return
		}

		_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": {"torrent_added": {"id": 1, "name": "n", "hash_string": "h"}}, "id": 1}`))
	}))
}

func TestBackendAddTorrentByFile(t *testing.T) {
	var captured string

	srv := captureAddServer(t, "6.0.0", &captured)
	defer srv.Close()

	b := New(srv.URL)

	metainfo := []byte("d8:announce0:e") // not real, just bytes to encode
	res, err := b.AddTorrent(context.Background(), torrent.AddRequest{
		Metainfo:    metainfo,
		DownloadDir: "/downloads",
		Paused:      true,
		Labels:      []string{"linux", "iso"},
	})
	require.NoError(t, err)
	assert.False(t, res.Duplicate)

	// Raw metainfo is base64-encoded into the wire "metainfo" field; URL/magnet
	// "filename" is absent for a file source.
	wantMetainfo := base64.StdEncoding.EncodeToString(metainfo)
	assert.Contains(t, captured, `"method":"torrent_add"`)
	assert.Contains(t, captured, `"metainfo":"`+wantMetainfo+`"`)
	assert.NotContains(t, captured, `"filename"`)
	assert.Contains(t, captured, `"download_dir":"/downloads"`)
	assert.Contains(t, captured, `"paused":true`)
	assert.Contains(t, captured, `"labels":["linux","iso"]`)
}

func TestBackendAddTorrentByURL(t *testing.T) {
	var captured string

	srv := captureAddServer(t, "6.0.0", &captured)
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.AddTorrent(context.Background(), torrent.AddRequest{
		URL: "https://example.com/a.torrent",
	})
	require.NoError(t, err)

	assert.Contains(t, captured, `"method":"torrent_add"`)
	assert.Contains(t, captured, `"filename":"https://example.com/a.torrent"`)
	assert.NotContains(t, captured, `"metainfo"`)
}

func TestBackendAddTorrentByMagnet(t *testing.T) {
	var captured string

	srv := captureAddServer(t, "", &captured) // bespoke path
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.AddTorrent(context.Background(), torrent.AddRequest{
		Magnet: "magnet:?xt=urn:btih:deadbeef",
	})
	require.NoError(t, err)

	assert.Contains(t, captured, `"method":"torrent-add"`)
	assert.Contains(t, captured, `"filename":"magnet:?xt=urn:btih:deadbeef"`)
	assert.NotContains(t, captured, `"metainfo"`)
}

func TestBackendAddTorrentAddedResult(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "6.0.0",
		responses: map[string]string{
			"torrent_add": `{
				"jsonrpc": "2.0",
				"result": {"torrent_added": {"id": 7, "name": "ubuntu.iso", "hash_string": "abc123"}},
				"id": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	res, err := b.AddTorrent(context.Background(), torrent.AddRequest{URL: "https://example.com/a.torrent"})
	require.NoError(t, err)
	assert.Equal(t, int64(7), res.ID)
	assert.Equal(t, "ubuntu.iso", res.Name)
	assert.Equal(t, "abc123", res.Hash)
	assert.False(t, res.Duplicate)
}

func TestBackendAddTorrentDuplicateResult(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // bespoke: hashString, torrent-duplicate
		responses: map[string]string{
			"torrent-add": `{
				"arguments": {"torrent-duplicate": {"id": 3, "name": "already-here", "hashString": "cafebabe"}},
				"result": "success",
				"tag": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	res, err := b.AddTorrent(context.Background(), torrent.AddRequest{Magnet: "magnet:?xt=urn:btih:cafebabe"})
	// A duplicate is reported via the result, not as an error.
	require.NoError(t, err)
	assert.True(t, res.Duplicate)
	assert.Equal(t, int64(3), res.ID)
	assert.Equal(t, "already-here", res.Name)
	assert.Equal(t, "cafebabe", res.Hash)
}

func TestBackendAddTorrentMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.AddTorrent(context.Background(), torrent.AddRequest{URL: "https://example.com/a.torrent"})
	require.ErrorIs(t, err, torrent.ErrAuth)
	assert.NotErrorIs(t, err, torrent.ErrUnreachable)
}
