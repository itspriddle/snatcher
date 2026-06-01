package transmission

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

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
	require.ErrorIs(t, err, ErrAuth)
	// The underlying rpc sentinel is preserved in the wrap chain.
	require.ErrorIs(t, err, rpc.ErrAuth)
	assert.NotErrorIs(t, err, ErrUnreachable)
}

func TestBackendMapsUnsupportedVersion(t *testing.T) {
	// No version header -> bespoke path runs the 4.0+ floor against the probe's
	// session-get body. A server reporting rpc-version 16 (Transmission 3.00) is
	// refused and mapped to the neutral ErrUnsupported.
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "",
		responses: map[string]string{
			"session-get": `{
				"arguments": {"version": "3.00 (bb6b5a062e)", "rpc-version": 16},
				"result": "success",
				"tag": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.ServerInfo(context.Background())
	require.ErrorIs(t, err, ErrUnsupported)
	require.ErrorIs(t, err, rpc.ErrUnsupported)
	assert.NotErrorIs(t, err, ErrUnreachable)
}

func TestBackendMapsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.ServerInfo(context.Background())
	require.ErrorIs(t, err, ErrUnreachable)
	require.ErrorIs(t, err, rpc.ErrTransport)
	assert.NotErrorIs(t, err, ErrAuth)
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
	// A server-side RPC error (bespoke result != "success") maps to ErrServer
	// while preserving the rpc.ErrRPC sentinel and its message. It is distinct
	// from a transport failure (ErrUnreachable).
	require.ErrorIs(t, err, ErrServer)
	require.ErrorIs(t, err, rpc.ErrRPC)
	require.ErrorContains(t, err, "no such session")
	assert.NotErrorIs(t, err, ErrUnreachable)
}

// TestMapErrorDistinguishesServerFromTransport verifies that mapError separates a
// server-side RPC application error (ErrServer) from a transport failure
// (ErrUnreachable), and that the underlying rpc sentinels survive the wrap so
// errors.Is against them still matches.
func TestMapErrorDistinguishesServerFromTransport(t *testing.T) {
	require.NoError(t, mapError(nil))

	serverErr := mapError(fmt.Errorf("%w: bad argument", rpc.ErrRPC))
	require.ErrorIs(t, serverErr, ErrServer)
	require.ErrorIs(t, serverErr, rpc.ErrRPC)
	require.NotErrorIs(t, serverErr, ErrUnreachable)

	transportErr := mapError(fmt.Errorf("%w: connection refused", rpc.ErrTransport))
	require.ErrorIs(t, transportErr, ErrUnreachable)
	require.ErrorIs(t, transportErr, rpc.ErrTransport)
	assert.NotErrorIs(t, transportErr, ErrServer)
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

	torrents, err := b.ListTorrents(context.Background(), Selector{All: true})
	require.NoError(t, err)
	require.Len(t, torrents, 1)

	got := torrents[0]
	assert.Equal(t, int64(7), got.ID)
	assert.Equal(t, "abc123", got.Hash)
	assert.Equal(t, "ubuntu.iso", got.Name)
	assert.Equal(t, StatusDownloading, got.Status)
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

	torrents, err := b.ListTorrents(context.Background(), Selector{All: true})
	require.NoError(t, err)
	require.Len(t, torrents, 1)

	assert.Equal(t, StatusError, torrents[0].Status)
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

	torrents, err := b.ListTorrents(context.Background(), Selector{All: true})
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

	torrents, err := b.ListTorrents(context.Background(), Selector{IDs: []int64{2}})
	require.NoError(t, err)
	require.Len(t, torrents, 1)
	assert.Equal(t, int64(2), torrents[0].ID)
	assert.Equal(t, StatusSeeding, torrents[0].Status)
	assert.Contains(t, captured, `"ids":[2]`)
}

// TestBackendListTorrentsProjectsFields asserts the fields projection reaches the
// wire: a PollFields request asks the server for a strict subset of the wire
// fields the full (empty) projection requests.
func TestBackendListTorrentsProjectsFields(t *testing.T) {
	var captured string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		body, _ := io.ReadAll(r.Body)
		captured = string(body)

		_, _ = w.Write([]byte(`{"arguments": {"torrents": []}, "result": "success", "tag": 1}`))
	}))
	defer srv.Close()

	b := New(srv.URL)

	// The full set (empty projection) requests every wire field.
	_, err := b.ListTorrents(context.Background(), Selector{All: true})
	require.NoError(t, err)
	fullFields := wireFields(t, captured)

	// PollFields requests a strict subset of the wire fields.
	_, err = b.ListTorrents(context.Background(), Selector{All: true}, PollFields...)
	require.NoError(t, err)
	pollFields := wireFields(t, captured)

	require.NotEmpty(t, pollFields)
	assert.Less(t, len(pollFields), len(fullFields), "PollFields requests fewer wire fields than the full set")
	for _, f := range pollFields {
		assert.Contains(t, fullFields, f, "poll wire field %q is also in the full set", f)
	}
}

// wireFields decodes the "fields" array from a captured torrent-get request body
// (it is "fields" under "arguments" on both wire schemes).
func wireFields(t *testing.T, body string) []string {
	t.Helper()

	var envelope struct {
		Arguments struct {
			Fields []string `json:"fields"`
		} `json:"arguments"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &envelope))

	return envelope.Arguments.Fields
}

func TestBackendListTorrentsMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.ListTorrents(context.Background(), Selector{All: true})
	require.ErrorIs(t, err, ErrAuth)
	assert.NotErrorIs(t, err, ErrUnreachable)
}

func TestBackendListTorrentsMapsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.ListTorrents(context.Background(), Selector{All: true})
	require.ErrorIs(t, err, ErrUnreachable)
	assert.NotErrorIs(t, err, ErrAuth)
}

func TestBackendTorrentDetail(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // bespoke path: camelCase detail fields, 0/1 wanted
		responses: map[string]string{
			"torrent-get": `{
				"arguments": {"torrents": [{
					"id": 7,
					"hashString": "abc123",
					"name": "ubuntu.iso",
					"status": 4,
					"files": [
						{"name": "ubuntu.iso", "length": 2048, "bytesCompleted": 1024}
					],
					"wanted": [1],
					"priorities": [1],
					"peers": [{"address": "10.0.0.1", "port": 51413, "clientName": "T", "isEncrypted": true}],
					"trackerStats": [{"announce": "http://t/announce", "seederCount": 42}],
					"pieces": "gA==",
					"pieceCount": 1,
					"magnetLink": "magnet:?xt=urn:btih:abc123",
					"downloadedEver": 4096,
					"isPrivate": true
				}]},
				"result": "success",
				"tag": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	detail, err := b.TorrentDetail(context.Background(), 7)
	require.NoError(t, err)

	// The embedded Torrent carries the derived Status (status promotion applies).
	assert.Equal(t, int64(7), detail.ID)
	assert.Equal(t, "abc123", detail.Hash)
	assert.Equal(t, StatusDownloading, detail.Status)

	// The detail collections and scalars map across.
	require.Len(t, detail.Files, 1)
	assert.Equal(t, "ubuntu.iso", detail.Files[0].Name)
	assert.True(t, detail.Files[0].Wanted)
	assert.Equal(t, 1, detail.Files[0].Priority)
	require.Len(t, detail.Peers, 1)
	assert.Equal(t, "10.0.0.1", detail.Peers[0].Address)
	assert.True(t, detail.Peers[0].IsEncrypted)
	require.Len(t, detail.Trackers, 1)
	assert.Equal(t, 42, detail.Trackers[0].SeederCount)
	assert.Equal(t, []bool{true}, detail.Pieces)
	assert.Equal(t, "magnet:?xt=urn:btih:abc123", detail.MagnetLink)
	assert.Equal(t, int64(4096), detail.DownloadedEver)
	assert.True(t, detail.IsPrivate)
}

func TestBackendTorrentDetailNoMatchErrors(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "",
		responses: map[string]string{
			"torrent-get": `{"arguments": {"torrents": []}, "result": "success", "tag": 1}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.TorrentDetail(context.Background(), 99)
	require.Error(t, err)
	assert.ErrorContains(t, err, "no torrent matched id 99")
}

func TestBackendTorrentDetailMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.TorrentDetail(context.Background(), 1)
	require.ErrorIs(t, err, ErrAuth)
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

			sel := Selector{IDs: []int64{1}, Hashes: []string{"deadbeef"}}
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

	sel := Selector{IDs: []int64{2}, Hashes: []string{"cafebabe"}}
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

		err := b.StartTorrents(context.Background(), Selector{All: true}, false)
		require.NoError(t, err)

		assert.Contains(t, captured, `"method":"torrent_start"`)
		assert.NotContains(t, captured, `"ids"`)
	})

	t.Run("stop --all omits ids", func(t *testing.T) {
		var captured string

		srv := captureActionServer(t, "6.0.0", &captured)
		defer srv.Close()

		b := New(srv.URL)

		err := b.StopTorrents(context.Background(), Selector{All: true})
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

	err := b.StopTorrents(context.Background(), Selector{IDs: []int64{1}})
	require.ErrorIs(t, err, ErrServer)
	require.ErrorIs(t, err, rpc.ErrRPC)
	assert.ErrorContains(t, err, "invalid argument")
}

func TestBackendVerifyTorrents(t *testing.T) {
	tests := []struct {
		name       string
		rpcVersion string
		wantMethod string
	}{
		{name: "jsonrpc", rpcVersion: "6.0.0", wantMethod: `"method":"torrent_verify"`},
		{name: "bespoke", rpcVersion: "", wantMethod: `"method":"torrent-verify"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured string

			srv := captureActionServer(t, tt.rpcVersion, &captured)
			defer srv.Close()

			b := New(srv.URL)

			sel := Selector{IDs: []int64{1}, Hashes: []string{"deadbeef"}}
			err := b.VerifyTorrents(context.Background(), sel)
			require.NoError(t, err)

			assert.Contains(t, captured, tt.wantMethod)
			assert.Contains(t, captured, `"ids":[1,"deadbeef"]`)
		})
	}
}

func TestBackendVerifyTorrentsAllOmitsIDs(t *testing.T) {
	var captured string

	srv := captureActionServer(t, "6.0.0", &captured)
	defer srv.Close()

	b := New(srv.URL)

	err := b.VerifyTorrents(context.Background(), Selector{All: true})
	require.NoError(t, err)

	assert.Contains(t, captured, `"method":"torrent_verify"`)
	assert.NotContains(t, captured, `"ids"`)
}

func TestBackendVerifyTorrentsMapsRPCError(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "",
		responses: map[string]string{
			"torrent-verify": `{"result": "invalid argument", "tag": 1}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	err := b.VerifyTorrents(context.Background(), Selector{IDs: []int64{1}})
	require.ErrorIs(t, err, ErrServer)
	require.ErrorIs(t, err, rpc.ErrRPC)
	assert.ErrorContains(t, err, "invalid argument")
}

func TestBackendReannounceTorrents(t *testing.T) {
	tests := []struct {
		name       string
		rpcVersion string
		wantMethod string
	}{
		{name: "jsonrpc", rpcVersion: "6.0.0", wantMethod: `"method":"torrent_reannounce"`},
		{name: "bespoke", rpcVersion: "", wantMethod: `"method":"torrent-reannounce"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured string

			srv := captureActionServer(t, tt.rpcVersion, &captured)
			defer srv.Close()

			b := New(srv.URL)

			sel := Selector{IDs: []int64{1}, Hashes: []string{"deadbeef"}}
			err := b.ReannounceTorrents(context.Background(), sel)
			require.NoError(t, err)

			assert.Contains(t, captured, tt.wantMethod)
			assert.Contains(t, captured, `"ids":[1,"deadbeef"]`)
		})
	}
}

func TestBackendReannounceTorrentsAllOmitsIDs(t *testing.T) {
	var captured string

	srv := captureActionServer(t, "6.0.0", &captured)
	defer srv.Close()

	b := New(srv.URL)

	err := b.ReannounceTorrents(context.Background(), Selector{All: true})
	require.NoError(t, err)

	assert.Contains(t, captured, `"method":"torrent_reannounce"`)
	assert.NotContains(t, captured, `"ids"`)
}

func TestBackendReannounceTorrentsMapsRPCError(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "",
		responses: map[string]string{
			"torrent-reannounce": `{"result": "invalid argument", "tag": 1}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	err := b.ReannounceTorrents(context.Background(), Selector{IDs: []int64{1}})
	require.ErrorIs(t, err, ErrServer)
	require.ErrorIs(t, err, rpc.ErrRPC)
	assert.ErrorContains(t, err, "invalid argument")
}

func TestBackendReannounceTorrentsMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	err := b.ReannounceTorrents(context.Background(), Selector{IDs: []int64{1}})
	require.ErrorIs(t, err, ErrAuth)
	assert.NotErrorIs(t, err, ErrUnreachable)
}

func TestBackendQueueMove(t *testing.T) {
	tests := []struct {
		name          string
		call          func(*Backend, context.Context, Selector) error
		jsonrpcMethod string
		bespokeMethod string
	}{
		{
			name:          "top",
			call:          func(b *Backend, ctx context.Context, sel Selector) error { return b.QueueMoveTop(ctx, sel) },
			jsonrpcMethod: `"method":"queue_move_top"`,
			bespokeMethod: `"method":"queue-move-top"`,
		},
		{
			name:          "up",
			call:          func(b *Backend, ctx context.Context, sel Selector) error { return b.QueueMoveUp(ctx, sel) },
			jsonrpcMethod: `"method":"queue_move_up"`,
			bespokeMethod: `"method":"queue-move-up"`,
		},
		{
			name:          "down",
			call:          func(b *Backend, ctx context.Context, sel Selector) error { return b.QueueMoveDown(ctx, sel) },
			jsonrpcMethod: `"method":"queue_move_down"`,
			bespokeMethod: `"method":"queue-move-down"`,
		},
		{
			name:          "bottom",
			call:          func(b *Backend, ctx context.Context, sel Selector) error { return b.QueueMoveBottom(ctx, sel) },
			jsonrpcMethod: `"method":"queue_move_bottom"`,
			bespokeMethod: `"method":"queue-move-bottom"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" jsonrpc", func(t *testing.T) {
			var captured string

			srv := captureActionServer(t, "6.0.0", &captured)
			defer srv.Close()

			b := New(srv.URL)

			sel := Selector{IDs: []int64{1}, Hashes: []string{"deadbeef"}}
			err := tt.call(b, context.Background(), sel)
			require.NoError(t, err)

			assert.Contains(t, captured, tt.jsonrpcMethod)
			assert.Contains(t, captured, `"ids":[1,"deadbeef"]`)
		})

		t.Run(tt.name+" bespoke", func(t *testing.T) {
			var captured string

			srv := captureActionServer(t, "", &captured)
			defer srv.Close()

			b := New(srv.URL)

			sel := Selector{IDs: []int64{1}, Hashes: []string{"deadbeef"}}
			err := tt.call(b, context.Background(), sel)
			require.NoError(t, err)

			assert.Contains(t, captured, tt.bespokeMethod)
			assert.Contains(t, captured, `"ids":[1,"deadbeef"]`)
		})
	}
}

func TestBackendQueueMoveAllOmitsIDs(t *testing.T) {
	var captured string

	srv := captureActionServer(t, "6.0.0", &captured)
	defer srv.Close()

	b := New(srv.URL)

	err := b.QueueMoveTop(context.Background(), Selector{All: true})
	require.NoError(t, err)

	assert.Contains(t, captured, `"method":"queue_move_top"`)
	assert.NotContains(t, captured, `"ids"`)
}

func TestBackendQueueMoveMapsRPCError(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "",
		responses: map[string]string{
			"queue-move-up": `{"result": "invalid argument", "tag": 1}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	err := b.QueueMoveUp(context.Background(), Selector{IDs: []int64{1}})
	require.ErrorIs(t, err, ErrServer)
	require.ErrorIs(t, err, rpc.ErrRPC)
	assert.ErrorContains(t, err, "invalid argument")
}

func TestBackendQueueMoveMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	err := b.QueueMoveBottom(context.Background(), Selector{IDs: []int64{1}})
	require.ErrorIs(t, err, ErrAuth)
	assert.NotErrorIs(t, err, ErrUnreachable)
}

func TestBackendStartTorrentsMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	err := b.StartTorrents(context.Background(), Selector{IDs: []int64{1}}, false)
	require.ErrorIs(t, err, ErrAuth)
	assert.NotErrorIs(t, err, ErrUnreachable)
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

			sel := Selector{IDs: []int64{2}, Hashes: []string{"cafebabe"}}
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

	err := b.RemoveTorrents(context.Background(), Selector{All: true}, true)
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

	err := b.RemoveTorrents(context.Background(), Selector{IDs: []int64{1}}, false)
	require.ErrorIs(t, err, ErrAuth)
	assert.NotErrorIs(t, err, ErrUnreachable)
}

func TestBackendRemoveTorrentsMapsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := New(srv.URL)

	err := b.RemoveTorrents(context.Background(), Selector{IDs: []int64{1}}, false)
	require.ErrorIs(t, err, ErrUnreachable)
	assert.NotErrorIs(t, err, ErrAuth)
}

func TestBackendMoveTorrents(t *testing.T) {
	tests := []struct {
		name       string
		move       bool
		rpcVersion string
		wantMethod string
		wantFlag   string
	}{
		{name: "jsonrpc relocate", move: true, rpcVersion: "6.0.0", wantMethod: `"method":"torrent_set_location"`, wantFlag: `"move":true`},
		{name: "jsonrpc in-place", move: false, rpcVersion: "6.0.0", wantMethod: `"method":"torrent_set_location"`, wantFlag: `"move":false`},
		{name: "bespoke relocate", move: true, rpcVersion: "", wantMethod: `"method":"torrent-set-location"`, wantFlag: `"move":true`},
		{name: "bespoke in-place", move: false, rpcVersion: "", wantMethod: `"method":"torrent-set-location"`, wantFlag: `"move":false`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured string

			srv := captureActionServer(t, tt.rpcVersion, &captured)
			defer srv.Close()

			b := New(srv.URL)

			sel := Selector{IDs: []int64{2}, Hashes: []string{"cafebabe"}}
			err := b.MoveTorrents(context.Background(), sel, "/data/media", tt.move)
			require.NoError(t, err)

			assert.Contains(t, captured, tt.wantMethod)
			assert.Contains(t, captured, `"ids":[2,"cafebabe"]`)
			assert.Contains(t, captured, `"location":"/data/media"`)
			assert.Contains(t, captured, tt.wantFlag)
		})
	}
}

func TestBackendMoveTorrentsAllOmitsIDs(t *testing.T) {
	var captured string

	srv := captureActionServer(t, "6.0.0", &captured)
	defer srv.Close()

	b := New(srv.URL)

	err := b.MoveTorrents(context.Background(), Selector{All: true}, "/data/media", true)
	require.NoError(t, err)

	assert.Contains(t, captured, `"method":"torrent_set_location"`)
	assert.Contains(t, captured, `"location":"/data/media"`)
	assert.NotContains(t, captured, `"ids"`)
}

func TestBackendMoveTorrentsMapsRPCError(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "",
		responses: map[string]string{
			"torrent-set-location": `{"result": "invalid argument", "tag": 1}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	err := b.MoveTorrents(context.Background(), Selector{IDs: []int64{1}}, "/data/media", true)
	require.ErrorIs(t, err, ErrServer)
	require.ErrorIs(t, err, rpc.ErrRPC)
	assert.ErrorContains(t, err, "invalid argument")
}

func TestBackendMoveTorrentsMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	err := b.MoveTorrents(context.Background(), Selector{IDs: []int64{1}}, "/data/media", true)
	require.ErrorIs(t, err, ErrAuth)
	assert.NotErrorIs(t, err, ErrUnreachable)
}

func TestBackendMoveTorrentsMapsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := New(srv.URL)

	err := b.MoveTorrents(context.Background(), Selector{IDs: []int64{1}}, "/data/media", true)
	require.ErrorIs(t, err, ErrUnreachable)
	assert.NotErrorIs(t, err, ErrAuth)
}

func TestBackendSetTorrentsTranslatesKeys(t *testing.T) {
	tests := []struct {
		name       string
		rpcVersion string
		// wantWire are wire-key fragments expected in the captured request body:
		// snake_case for JSON-RPC, the bespoke camelCase/kebab spelling otherwise.
		wantWire []string
	}{
		{
			name:       "jsonrpc",
			rpcVersion: "6.0.0",
			wantWire: []string{
				`"method":"torrent_set"`,
				`"ids":[3,"deadbeef"]`,
				`"bandwidth_priority":1`,
				`"seed_ratio_mode":1`,
				`"peer_limit":50`,
			},
		},
		{
			name:       "bespoke",
			rpcVersion: "",
			wantWire: []string{
				`"method":"torrent-set"`,
				`"ids":[3,"deadbeef"]`,
				`"bandwidthPriority":1`,
				`"seedRatioMode":1`,
				`"peer-limit":50`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured string

			srv := captureActionServer(t, tt.rpcVersion, &captured)
			defer srv.Close()

			b := New(srv.URL)

			sel := Selector{IDs: []int64{3}, Hashes: []string{"deadbeef"}}
			err := b.SetTorrents(context.Background(), sel, map[string]any{
				"bandwidth-priority": 1,
				"seed-ratio-mode":    1,
				"peer-limit":         50,
			})
			require.NoError(t, err)

			for _, want := range tt.wantWire {
				assert.Contains(t, captured, want)
			}
		})
	}
}

func TestBackendSetFilesTranslatesKeys(t *testing.T) {
	tests := []struct {
		name       string
		rpcVersion string
		// wantWire are wire-key fragments expected in the captured request body:
		// snake_case for JSON-RPC (sent verbatim), the mechanical kebab spelling for
		// bespoke. No file-selection key is a bespoke camelCase exception.
		wantWire []string
	}{
		{
			name:       "jsonrpc",
			rpcVersion: "6.0.0",
			wantWire: []string{
				`"method":"torrent_set"`,
				`"ids":[3,"deadbeef"]`,
				`"files_wanted":[0,1]`,
				`"files_unwanted":[2]`,
				`"priority_high":[0]`,
				`"priority_normal":[1]`,
				`"priority_low":[2]`,
			},
		},
		{
			name:       "bespoke",
			rpcVersion: "",
			wantWire: []string{
				`"method":"torrent-set"`,
				`"ids":[3,"deadbeef"]`,
				`"files-wanted":[0,1]`,
				`"files-unwanted":[2]`,
				`"priority-high":[0]`,
				`"priority-normal":[1]`,
				`"priority-low":[2]`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured string

			srv := captureActionServer(t, tt.rpcVersion, &captured)
			defer srv.Close()

			b := New(srv.URL)

			sel := Selector{IDs: []int64{3}, Hashes: []string{"deadbeef"}}
			err := b.SetFiles(context.Background(), sel, FileChanges{
				Wanted:         []int{0, 1},
				Unwanted:       []int{2},
				PriorityHigh:   []int{0},
				PriorityNormal: []int{1},
				PriorityLow:    []int{2},
			})
			require.NoError(t, err)

			for _, want := range tt.wantWire {
				assert.Contains(t, captured, want)
			}
		})
	}
}

func TestBackendSetFilesOmitsEmptyFields(t *testing.T) {
	var captured string

	srv := captureActionServer(t, "6.0.0", &captured)
	defer srv.Close()

	b := New(srv.URL)

	// Only Wanted is populated; the other four FileChanges fields must not appear
	// as torrent-set arguments.
	err := b.SetFiles(context.Background(), Selector{IDs: []int64{1}}, FileChanges{
		Wanted: []int{0},
	})
	require.NoError(t, err)

	assert.Contains(t, captured, `"files_wanted":[0]`)
	assert.NotContains(t, captured, "files_unwanted")
	assert.NotContains(t, captured, "priority_high")
	assert.NotContains(t, captured, "priority_normal")
	assert.NotContains(t, captured, "priority_low")
}

func TestBackendSetFilesAllOmitsIDs(t *testing.T) {
	var captured string

	srv := captureActionServer(t, "6.0.0", &captured)
	defer srv.Close()

	b := New(srv.URL)

	err := b.SetFiles(context.Background(), Selector{All: true}, FileChanges{
		Wanted: []int{0},
	})
	require.NoError(t, err)

	assert.Contains(t, captured, `"method":"torrent_set"`)
	assert.Contains(t, captured, `"files_wanted":[0]`)
	assert.NotContains(t, captured, `"ids"`)
}

func TestBackendSetFilesMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	err := b.SetFiles(context.Background(), Selector{IDs: []int64{1}}, FileChanges{Wanted: []int{0}})
	require.ErrorIs(t, err, ErrAuth)
	require.ErrorIs(t, err, rpc.ErrAuth)
	assert.NotErrorIs(t, err, ErrUnreachable)
}

func TestBackendSetTorrentsAllOmitsIDs(t *testing.T) {
	var captured string

	srv := captureActionServer(t, "6.0.0", &captured)
	defer srv.Close()

	b := New(srv.URL)

	err := b.SetTorrents(context.Background(), Selector{All: true}, map[string]any{
		"honors-session-limits": false,
	})
	require.NoError(t, err)

	assert.Contains(t, captured, `"method":"torrent_set"`)
	assert.Contains(t, captured, `"honors_session_limits":false`)
	assert.NotContains(t, captured, `"ids"`)
}

func TestBackendSetTorrentsMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	err := b.SetTorrents(context.Background(), Selector{IDs: []int64{1}}, map[string]any{"peer-limit": 50})
	require.ErrorIs(t, err, ErrAuth)
	require.ErrorIs(t, err, rpc.ErrAuth)
	assert.NotErrorIs(t, err, ErrUnreachable)
}

func TestBackendSetTorrentsMapsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := New(srv.URL)

	err := b.SetTorrents(context.Background(), Selector{IDs: []int64{1}}, map[string]any{"peer-limit": 50})
	require.ErrorIs(t, err, ErrUnreachable)
	assert.NotErrorIs(t, err, ErrAuth)
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
	res, err := b.AddTorrent(context.Background(), AddRequest{
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

	_, err := b.AddTorrent(context.Background(), AddRequest{
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

	_, err := b.AddTorrent(context.Background(), AddRequest{
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

	res, err := b.AddTorrent(context.Background(), AddRequest{URL: "https://example.com/a.torrent"})
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

	res, err := b.AddTorrent(context.Background(), AddRequest{Magnet: "magnet:?xt=urn:btih:cafebabe"})
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

	_, err := b.AddTorrent(context.Background(), AddRequest{URL: "https://example.com/a.torrent"})
	require.ErrorIs(t, err, ErrAuth)
	assert.NotErrorIs(t, err, ErrUnreachable)
}

func TestBackendCallAPISuccess(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // bespoke path
		responses: map[string]string{
			"free-space": `{"arguments": {"size-bytes": 1234}, "result": "success", "tag": 1}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	resp, err := b.CallAPI(context.Background(), "free-space", nil)
	require.NoError(t, err)

	assert.True(t, resp.OK)
	assert.Equal(t, "success", resp.Result)
	assert.JSONEq(t, `{"size-bytes": 1234}`, string(resp.Arguments))
	assert.JSONEq(t, `{"arguments": {"size-bytes": 1234}, "result": "success", "tag": 1}`, string(resp.Raw))
}

func TestBackendCallAPILeavesRPCErrorAsNonError(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "",
		responses: map[string]string{
			"bogus-method": `{"result": "no such method", "tag": 1}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	// An RPC-level failure is *not* a Go error: it surfaces via OK == false so
	// the api command can print the envelope and set its own exit code.
	resp, err := b.CallAPI(context.Background(), "bogus-method", nil)
	require.NoError(t, err)

	assert.False(t, resp.OK)
	assert.Equal(t, "no such method", resp.Result)
}

func TestBackendCallAPIMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.CallAPI(context.Background(), "session-get", nil)
	require.ErrorIs(t, err, ErrAuth)
	require.ErrorIs(t, err, rpc.ErrAuth)
	assert.NotErrorIs(t, err, ErrUnreachable)
}

func TestBackendCallAPIMapsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.CallAPI(context.Background(), "session-get", nil)
	require.ErrorIs(t, err, ErrUnreachable)
	require.ErrorIs(t, err, rpc.ErrTransport)
	assert.NotErrorIs(t, err, ErrAuth)
}

func TestBackendGetSessionJSONRPC(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "6.0.0",
		responses: map[string]string{
			"session_get": `{
				"jsonrpc": "2.0",
				"result": {
					"download_dir": "/downloads",
					"start_added_torrents": true,
					"incomplete_dir": "/incomplete",
					"seed_ratio_limit": 2.5,
					"cache_size_mb": 8,
					"peer_port": 51413,
					"encryption": "required",
					"peer_limit_global": 240,
					"dht_enabled": true,
					"speed_limit_down": 1500,
					"alt_speed_up": 75,
					"alt_speed_time_begin": 540,
					"download_queue_size": 5,
					"queue_stalled_minutes": 30
				},
				"id": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	settings, err := b.GetSession(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "/downloads", settings.DownloadDir)
	assert.True(t, settings.StartAddedTorrents)
	assert.Equal(t, "/incomplete", settings.IncompleteDir)
	assert.InEpsilon(t, 2.5, settings.SeedRatioLimit, 1e-9)
	assert.Equal(t, 8, settings.CacheSizeMiB)
	assert.Equal(t, 51413, settings.PeerPort)
	assert.Equal(t, "required", settings.Encryption)
	assert.Equal(t, 240, settings.PeerLimitGlobal)
	assert.True(t, settings.DHTEnabled)
	assert.Equal(t, int64(1500), settings.SpeedLimitDown)
	assert.Equal(t, int64(75), settings.AltSpeedUp)
	assert.Equal(t, 540, settings.AltSpeedTimeBegin)
	assert.Equal(t, 5, settings.DownloadQueueSize)
	assert.Equal(t, 30, settings.QueueStalledMinutes)

	// Fields the server omitted flatten to their zero value.
	assert.False(t, settings.PexEnabled)
	assert.Empty(t, settings.BlocklistURL)
}

func TestBackendGetSessionBespoke(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // bespoke
		responses: map[string]string{
			"session-get": `{
				"arguments": {
					"download-dir": "/data",
					"peer-port": 12345,
					"encryption": "tolerated",
					"seedRatioLimit": 1.5,
					"cache-size-mb": 16,
					"speed-limit-up": 200
				},
				"result": "success",
				"tag": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	settings, err := b.GetSession(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "/data", settings.DownloadDir)
	assert.Equal(t, 12345, settings.PeerPort)
	// The bespoke wire's "tolerated" is normalized to the canonical "allowed"
	// by the rpc layer before the adapter sees it.
	assert.Equal(t, "allowed", settings.Encryption)
	assert.InEpsilon(t, 1.5, settings.SeedRatioLimit, 1e-9)
	assert.Equal(t, 16, settings.CacheSizeMiB)
	assert.Equal(t, int64(200), settings.SpeedLimitUp)
}

func TestBackendGetSessionMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.GetSession(context.Background())
	require.ErrorIs(t, err, ErrAuth)
	require.ErrorIs(t, err, rpc.ErrAuth)
	assert.NotErrorIs(t, err, ErrUnreachable)
}

func TestBackendSetSessionTranslatesKeys(t *testing.T) {
	tests := []struct {
		name       string
		rpcVersion string
		// wantWire are wire-key fragments expected in the captured request body
		// (snake_case for JSON-RPC, kebab-case for bespoke).
		wantWire []string
	}{
		{
			name:       "jsonrpc",
			rpcVersion: "6.0.0",
			wantWire: []string{
				`"method":"session_set"`,
				`"peer_port":12345`,
				`"dht_enabled":false`,
			},
		},
		{
			name:       "bespoke",
			rpcVersion: "",
			wantWire: []string{
				`"method":"session-set"`,
				`"peer-port":12345`,
				`"dht-enabled":false`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured string

			srv := captureActionServer(t, tt.rpcVersion, &captured)
			defer srv.Close()

			b := New(srv.URL)

			err := b.SetSession(context.Background(), map[string]any{
				"peer-port":   12345,
				"dht-enabled": false,
			})
			require.NoError(t, err)

			for _, want := range tt.wantWire {
				assert.Contains(t, captured, want)
			}
		})
	}
}

func TestBackendSetSessionTranslatesCacheSizeKey(t *testing.T) {
	// The neutral "cache-size-mib" key translates to the rpc canonical
	// "cache_size_mib", which the rpc layer then maps to the real wire key
	// ("cache_size_mb" / "cache-size-mb").
	tests := []struct {
		name       string
		rpcVersion string
		wantWire   string
	}{
		{name: "jsonrpc", rpcVersion: "6.0.0", wantWire: `"cache_size_mb":8`},
		{name: "bespoke", rpcVersion: "", wantWire: `"cache-size-mb":8`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured string

			srv := captureActionServer(t, tt.rpcVersion, &captured)
			defer srv.Close()

			b := New(srv.URL)

			err := b.SetSession(context.Background(), map[string]any{"cache-size-mib": 8})
			require.NoError(t, err)
			assert.Contains(t, captured, tt.wantWire)
		})
	}
}

func TestBackendSetSessionMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	err := b.SetSession(context.Background(), map[string]any{"peer-port": 12345})
	require.ErrorIs(t, err, ErrAuth)
	require.ErrorIs(t, err, rpc.ErrAuth)
	assert.NotErrorIs(t, err, ErrUnreachable)
}

func TestBackendTestPortJSONRPC(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "6.0.0",
		responses: map[string]string{
			"port_test": `{
				"jsonrpc": "2.0",
				"result": {"port_is_open": true, "ip_protocol": "ipv6"},
				"id": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	result, err := b.TestPort(context.Background())
	require.NoError(t, err)
	assert.True(t, result.Open)
	assert.Equal(t, "ipv6", result.IPProtocol)
}

func TestBackendTestPortBespoke(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // old server: no version header -> bespoke
		responses: map[string]string{
			"port-test": `{
				"arguments": {"port-is-open": false},
				"result": "success",
				"tag": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	result, err := b.TestPort(context.Background())
	require.NoError(t, err)
	assert.False(t, result.Open)
	// The bespoke scheme carries no ip protocol, so the neutral field stays empty.
	assert.Empty(t, result.IPProtocol)
}

func TestBackendTestPortMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.TestPort(context.Background())
	require.ErrorIs(t, err, ErrAuth)
	require.ErrorIs(t, err, rpc.ErrAuth)
	assert.NotErrorIs(t, err, ErrUnreachable)
}

func TestBackendTestPortMapsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.TestPort(context.Background())
	require.ErrorIs(t, err, ErrUnreachable)
	require.ErrorIs(t, err, rpc.ErrTransport)
	assert.NotErrorIs(t, err, ErrAuth)
}

func TestBackendFreeSpaceJSONRPC(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "6.0.0",
		responses: map[string]string{
			"free_space": `{
				"jsonrpc": "2.0",
				"result": {"path": "/downloads", "size_bytes": 1024, "total_size": 4096},
				"id": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	result, err := b.FreeSpace(context.Background(), "/downloads")
	require.NoError(t, err)
	assert.Equal(t, "/downloads", result.Path)
	assert.Equal(t, int64(1024), result.SizeBytes)
	// A 4.1+ (JSON-RPC) server reports the total capacity.
	assert.Equal(t, int64(4096), result.TotalBytes)
}

func TestBackendFreeSpaceBespoke(t *testing.T) {
	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // old server: no version header -> bespoke
		responses: map[string]string{
			"free-space": `{
				"arguments": {"path": "/downloads", "size-bytes": 2048},
				"result": "success",
				"tag": 1
			}`,
		},
	}.handler())
	defer srv.Close()

	b := New(srv.URL)

	result, err := b.FreeSpace(context.Background(), "/downloads")
	require.NoError(t, err)
	assert.Equal(t, "/downloads", result.Path)
	assert.Equal(t, int64(2048), result.SizeBytes)
	// A bespoke server too old to report total_size omits it; the neutral field
	// stays 0 (and its json tag is omitempty).
	assert.Zero(t, result.TotalBytes)
}

func TestBackendFreeSpaceMapsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.FreeSpace(context.Background(), "/downloads")
	require.ErrorIs(t, err, ErrAuth)
	require.ErrorIs(t, err, rpc.ErrAuth)
	assert.NotErrorIs(t, err, ErrUnreachable)
}

func TestBackendFreeSpaceMapsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := New(srv.URL)

	_, err := b.FreeSpace(context.Background(), "/downloads")
	require.ErrorIs(t, err, ErrUnreachable)
	require.ErrorIs(t, err, rpc.ErrTransport)
	assert.NotErrorIs(t, err, ErrAuth)
}
