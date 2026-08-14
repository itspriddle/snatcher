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

func TestJSONRPCProtocolTorrentGet(t *testing.T) {
	var rawBody []byte

	// snake_case torrent fields matching the JSON-RPC 2.0 scheme.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"result": {"torrents": [{
				"id": 7,
				"hash_string": "abc123",
				"name": "ubuntu.iso",
				"status": 4,
				"percent_done": 0.5,
				"total_size": 2048,
				"size_when_done": 2048,
				"left_until_done": 1024,
				"rate_download": 1000,
				"rate_upload": 200,
				"upload_ratio": 1.5,
				"eta": 3600,
				"peers_connected": 10,
				"peers_sending_to_us": 4,
				"peers_getting_from_us": 2,
				"added_date": 1700000000,
				"done_date": 0,
				"activity_date": 1700000100,
				"download_dir": "/downloads",
				"error": 0,
				"error_string": "",
				"labels": ["linux", "iso"],
				"queue_position": 3,
				"is_finished": false,
				"is_stalled": false
			}]},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	torrents, err := jsonrpcProtocol{}.TorrentGet(context.Background(), tr, nil, nil)
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

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// JSON-RPC 2.0 envelope: snake_case method, fields requested in params, no ids
	// when none are requested. An empty fields argument requests the full
	// canonical set translated to the JSON-RPC wire names (hash -> hash_string,
	// error -> error+error_string).
	assert.Equal(t, "torrent_get", gotBody["method"])
	params, ok := gotBody["params"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, params, "ids")
	assert.ElementsMatch(t, resolveTorrentGetFields(nil, torrentGetFieldsJSONRPCMap), fieldStrings(t, params["fields"]))
	assert.Contains(t, fieldStrings(t, params["fields"]), "hash_string")
	assert.Contains(t, fieldStrings(t, params["fields"]), "error_string")
}

func TestBespokeProtocolTorrentGet(t *testing.T) {
	var rawBody []byte

	// camelCase torrent fields matching the bespoke scheme.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{
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
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	torrents, err := bespokeProtocol{}.TorrentGet(context.Background(), tr, nil, nil)
	require.NoError(t, err)
	require.Len(t, torrents, 1)

	got := torrents[0]
	assert.Equal(t, int64(7), got.ID)
	assert.Equal(t, "abc123", got.Hash)
	assert.Equal(t, "ubuntu.iso", got.Name)
	assert.Equal(t, StatusDownloading, got.Status)
	assert.InDelta(t, 0.5, got.PercentDone, 0.0001)
	assert.Equal(t, int64(1000), got.RateDownload)
	assert.InDelta(t, 1.5, got.UploadRatio, 0.0001)
	assert.Equal(t, int64(3600), got.ETA)
	assert.Equal(t, []string{"linux", "iso"}, got.Labels)
	assert.Equal(t, int64(3), got.QueuePosition)

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// Bespoke envelope: kebab-case method, fields in arguments. An empty fields
	// argument requests the full canonical set translated to the bespoke wire
	// names (hash -> hashString, error -> error+errorString).
	assert.Equal(t, "torrent-get", gotBody["method"])
	args, ok := gotBody["arguments"].(map[string]any)
	require.True(t, ok)
	assert.ElementsMatch(t, resolveTorrentGetFields(nil, torrentGetFieldsBespokeMap), fieldStrings(t, args["fields"]))
	assert.Contains(t, fieldStrings(t, args["fields"]), "hashString")
	assert.Contains(t, fieldStrings(t, args["fields"]), "errorString")
}

func TestTorrentGetSendsIDs(t *testing.T) {
	t.Run("jsonrpc includes ids", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": {"torrents": []}, "id": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		_, err := jsonrpcProtocol{}.TorrentGet(context.Background(), tr, []any{int64(1), int64(2)}, nil)
		require.NoError(t, err)
		assert.Contains(t, string(rawBody), `"ids":[1,2]`)
	})

	t.Run("bespoke includes ids", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"arguments": {"torrents": []}, "result": "success", "tag": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		_, err := bespokeProtocol{}.TorrentGet(context.Background(), tr, []any{int64(3)}, nil)
		require.NoError(t, err)
		assert.Contains(t, string(rawBody), `"ids":[3]`)
	})

	t.Run("jsonrpc mixes ids and hashes", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": {"torrents": []}, "id": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		_, err := jsonrpcProtocol{}.TorrentGet(context.Background(), tr, []any{int64(1), "abc123"}, nil)
		require.NoError(t, err)
		assert.Contains(t, string(rawBody), `"ids":[1,"abc123"]`)
	})
}

// TestTorrentGetProjectsRequestedFields verifies that a requested canonical
// subset maps to the correct wire field names in both schemes, and that an empty
// fields argument requests the full canonical set.
func TestTorrentGetProjectsRequestedFields(t *testing.T) {
	captureFields := func(t *testing.T, proto Protocol, response string, fields []string) []string {
		t.Helper()

		var rawBody []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(response))
		}))
		defer srv.Close()

		_, err := proto.TorrentGet(context.Background(), NewTransport(srv.URL), nil, fields)
		require.NoError(t, err)

		var body map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &body))

		switch p := body["params"].(type) {
		case map[string]any:
			return fieldStrings(t, p["fields"])
		default:
			args, ok := body["arguments"].(map[string]any)
			require.True(t, ok)
			return fieldStrings(t, args["fields"])
		}
	}

	const jsonrpcResp = `{"jsonrpc": "2.0", "result": {"torrents": []}, "id": 1}`
	const bespokeResp = `{"arguments": {"torrents": []}, "result": "success", "tag": 1}`

	t.Run("jsonrpc subset maps canonical to wire", func(t *testing.T) {
		got := captureFields(t, jsonrpcProtocol{}, jsonrpcResp, []string{"id", "hash", "error"})
		// hash -> hash_string; error -> error + error_string.
		assert.Equal(t, []string{"id", "hash_string", "error", "error_string"}, got)
	})

	t.Run("bespoke subset maps canonical to wire", func(t *testing.T) {
		got := captureFields(t, bespokeProtocol{}, bespokeResp, []string{"id", "hash", "error"})
		// hash -> hashString; error -> error + errorString.
		assert.Equal(t, []string{"id", "hashString", "error", "errorString"}, got)
	})

	t.Run("jsonrpc empty fields requests full set", func(t *testing.T) {
		got := captureFields(t, jsonrpcProtocol{}, jsonrpcResp, nil)
		assert.ElementsMatch(t, resolveTorrentGetFields(nil, torrentGetFieldsJSONRPCMap), got)
	})

	t.Run("bespoke empty fields requests full set", func(t *testing.T) {
		got := captureFields(t, bespokeProtocol{}, bespokeResp, nil)
		assert.ElementsMatch(t, resolveTorrentGetFields(nil, torrentGetFieldsBespokeMap), got)
	})

	t.Run("unknown canonical fields are skipped", func(t *testing.T) {
		got := captureFields(t, jsonrpcProtocol{}, jsonrpcResp, []string{"id", "not_a_field"})
		assert.Equal(t, []string{"id"}, got)
	})
}

// fieldStrings converts a decoded JSON "fields" value (an []any of strings) to a
// []string for assertions.
func fieldStrings(t *testing.T, v any) []string {
	t.Helper()

	raw, ok := v.([]any)
	require.True(t, ok, "fields is not a JSON array")

	out := make([]string, len(raw))
	for i, item := range raw {
		s, ok := item.(string)
		require.True(t, ok, "field is not a string")
		out[i] = s
	}

	return out
}

func TestProtocolTorrentStart(t *testing.T) {
	tests := []struct {
		name       string
		now        bool
		wantMethod string
	}{
		{name: "start respects queue", now: false, wantMethod: "torrent_start"},
		{name: "start-now bypasses queue", now: true, wantMethod: "torrent_start_now"},
	}

	for _, tt := range tests {
		t.Run("jsonrpc "+tt.name, func(t *testing.T) {
			var rawBody []byte

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rawBody, _ = io.ReadAll(r.Body)
				_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
			}))
			defer srv.Close()

			tr := NewTransport(srv.URL)

			err := jsonrpcProtocol{}.TorrentStart(context.Background(), tr, []any{int64(1), "deadbeef"}, tt.now)
			require.NoError(t, err)

			var gotBody map[string]any
			require.NoError(t, json.Unmarshal(rawBody, &gotBody))

			assert.Equal(t, tt.wantMethod, gotBody["method"])
			params, ok := gotBody["params"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, []any{float64(1), "deadbeef"}, params["ids"])
		})
	}

	for _, tt := range tests {
		t.Run("bespoke "+tt.name, func(t *testing.T) {
			var rawBody []byte

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rawBody, _ = io.ReadAll(r.Body)
				_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
			}))
			defer srv.Close()

			tr := NewTransport(srv.URL)

			wantMethod := "torrent-start"
			if tt.now {
				wantMethod = "torrent-start-now"
			}

			err := bespokeProtocol{}.TorrentStart(context.Background(), tr, []any{int64(1), "deadbeef"}, tt.now)
			require.NoError(t, err)

			var gotBody map[string]any
			require.NoError(t, json.Unmarshal(rawBody, &gotBody))

			assert.Equal(t, wantMethod, gotBody["method"])
			args, ok := gotBody["arguments"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, []any{float64(1), "deadbeef"}, args["ids"])
		})
	}
}

func TestProtocolTorrentStop(t *testing.T) {
	t.Run("jsonrpc sends method and mixed ids", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		err := jsonrpcProtocol{}.TorrentStop(context.Background(), tr, []any{int64(2), "cafebabe"})
		require.NoError(t, err)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		assert.Equal(t, "torrent_stop", gotBody["method"])
		params, ok := gotBody["params"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, []any{float64(2), "cafebabe"}, params["ids"])
	})

	t.Run("bespoke sends method and mixed ids", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		err := bespokeProtocol{}.TorrentStop(context.Background(), tr, []any{int64(2), "cafebabe"})
		require.NoError(t, err)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		assert.Equal(t, "torrent-stop", gotBody["method"])
		args, ok := gotBody["arguments"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, []any{float64(2), "cafebabe"}, args["ids"])
	})
}

func TestProtocolTorrentVerify(t *testing.T) {
	t.Run("jsonrpc sends method and mixed ids", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		err := jsonrpcProtocol{}.TorrentVerify(context.Background(), tr, []any{int64(2), "cafebabe"})
		require.NoError(t, err)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		assert.Equal(t, "torrent_verify", gotBody["method"])
		params, ok := gotBody["params"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, []any{float64(2), "cafebabe"}, params["ids"])
	})

	t.Run("bespoke sends method and mixed ids", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		err := bespokeProtocol{}.TorrentVerify(context.Background(), tr, []any{int64(2), "cafebabe"})
		require.NoError(t, err)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		assert.Equal(t, "torrent-verify", gotBody["method"])
		args, ok := gotBody["arguments"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, []any{float64(2), "cafebabe"}, args["ids"])
	})
}

func TestJSONRPCProtocolTorrentVerifyMapsErrorObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"error": {"code": -32601, "message": "method not found"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := jsonrpcProtocol{}.TorrentVerify(context.Background(), tr, []any{int64(1)})
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "method not found")
}

func TestBespokeProtocolTorrentVerifyMapsNonSuccessResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result": "invalid argument", "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := bespokeProtocol{}.TorrentVerify(context.Background(), tr, []any{int64(1)})
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "invalid argument")
}

func TestProtocolTorrentReannounce(t *testing.T) {
	t.Run("jsonrpc sends method and mixed ids", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		err := jsonrpcProtocol{}.TorrentReannounce(context.Background(), tr, []any{int64(2), "cafebabe"})
		require.NoError(t, err)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		assert.Equal(t, "torrent_reannounce", gotBody["method"])
		params, ok := gotBody["params"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, []any{float64(2), "cafebabe"}, params["ids"])
	})

	t.Run("jsonrpc omits ids when none given", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		err := jsonrpcProtocol{}.TorrentReannounce(context.Background(), tr, nil)
		require.NoError(t, err)

		assert.NotContains(t, string(rawBody), `"ids"`)
	})

	t.Run("bespoke sends method and mixed ids", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		err := bespokeProtocol{}.TorrentReannounce(context.Background(), tr, []any{int64(2), "cafebabe"})
		require.NoError(t, err)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		assert.Equal(t, "torrent-reannounce", gotBody["method"])
		args, ok := gotBody["arguments"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, []any{float64(2), "cafebabe"}, args["ids"])
	})

	t.Run("bespoke omits ids when none given", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		err := bespokeProtocol{}.TorrentReannounce(context.Background(), tr, nil)
		require.NoError(t, err)

		assert.NotContains(t, string(rawBody), `"ids"`)
	})
}

func TestJSONRPCProtocolTorrentReannounceMapsErrorObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"error": {"code": -32601, "message": "method not found"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := jsonrpcProtocol{}.TorrentReannounce(context.Background(), tr, []any{int64(1)})
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "method not found")
}

func TestBespokeProtocolTorrentReannounceMapsNonSuccessResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result": "invalid argument", "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := bespokeProtocol{}.TorrentReannounce(context.Background(), tr, []any{int64(1)})
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "invalid argument")
}

func TestProtocolQueueMove(t *testing.T) {
	tests := []struct {
		name          string
		call          func(Protocol, context.Context, transport, []any) error
		jsonrpcMethod string
		bespokeMethod string
	}{
		{
			name: "top",
			call: func(p Protocol, ctx context.Context, tr transport, ids []any) error {
				return p.QueueMoveTop(ctx, tr, ids)
			},
			jsonrpcMethod: "queue_move_top",
			bespokeMethod: "queue-move-top",
		},
		{
			name: "up",
			call: func(p Protocol, ctx context.Context, tr transport, ids []any) error {
				return p.QueueMoveUp(ctx, tr, ids)
			},
			jsonrpcMethod: "queue_move_up",
			bespokeMethod: "queue-move-up",
		},
		{
			name: "down",
			call: func(p Protocol, ctx context.Context, tr transport, ids []any) error {
				return p.QueueMoveDown(ctx, tr, ids)
			},
			jsonrpcMethod: "queue_move_down",
			bespokeMethod: "queue-move-down",
		},
		{
			name: "bottom",
			call: func(p Protocol, ctx context.Context, tr transport, ids []any) error {
				return p.QueueMoveBottom(ctx, tr, ids)
			},
			jsonrpcMethod: "queue_move_bottom",
			bespokeMethod: "queue-move-bottom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" jsonrpc sends method and mixed ids", func(t *testing.T) {
			var rawBody []byte

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rawBody, _ = io.ReadAll(r.Body)
				_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
			}))
			defer srv.Close()

			tr := NewTransport(srv.URL)

			err := tt.call(jsonrpcProtocol{}, context.Background(), tr, []any{int64(2), "cafebabe"})
			require.NoError(t, err)

			var gotBody map[string]any
			require.NoError(t, json.Unmarshal(rawBody, &gotBody))

			assert.Equal(t, tt.jsonrpcMethod, gotBody["method"])
			params, ok := gotBody["params"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, []any{float64(2), "cafebabe"}, params["ids"])
		})

		t.Run(tt.name+" jsonrpc omits ids when none given", func(t *testing.T) {
			var rawBody []byte

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rawBody, _ = io.ReadAll(r.Body)
				_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
			}))
			defer srv.Close()

			tr := NewTransport(srv.URL)

			err := tt.call(jsonrpcProtocol{}, context.Background(), tr, nil)
			require.NoError(t, err)

			assert.NotContains(t, string(rawBody), `"ids"`)
		})

		t.Run(tt.name+" bespoke sends method and mixed ids", func(t *testing.T) {
			var rawBody []byte

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rawBody, _ = io.ReadAll(r.Body)
				_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
			}))
			defer srv.Close()

			tr := NewTransport(srv.URL)

			err := tt.call(bespokeProtocol{}, context.Background(), tr, []any{int64(2), "cafebabe"})
			require.NoError(t, err)

			var gotBody map[string]any
			require.NoError(t, json.Unmarshal(rawBody, &gotBody))

			assert.Equal(t, tt.bespokeMethod, gotBody["method"])
			args, ok := gotBody["arguments"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, []any{float64(2), "cafebabe"}, args["ids"])
		})

		t.Run(tt.name+" bespoke omits ids when none given", func(t *testing.T) {
			var rawBody []byte

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rawBody, _ = io.ReadAll(r.Body)
				_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
			}))
			defer srv.Close()

			tr := NewTransport(srv.URL)

			err := tt.call(bespokeProtocol{}, context.Background(), tr, nil)
			require.NoError(t, err)

			assert.NotContains(t, string(rawBody), `"ids"`)
		})
	}
}

func TestJSONRPCProtocolQueueMoveMapsErrorObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"error": {"code": -32601, "message": "method not found"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := jsonrpcProtocol{}.QueueMoveTop(context.Background(), tr, []any{int64(1)})
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "method not found")
}

func TestBespokeProtocolQueueMoveMapsNonSuccessResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result": "invalid argument", "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := bespokeProtocol{}.QueueMoveBottom(context.Background(), tr, []any{int64(1)})
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "invalid argument")
}

func TestProtocolTorrentRemove(t *testing.T) {
	tests := []struct {
		name       string
		deleteData bool
	}{
		{name: "keep data", deleteData: false},
		{name: "delete data", deleteData: true},
	}

	for _, tt := range tests {
		t.Run("jsonrpc "+tt.name, func(t *testing.T) {
			var rawBody []byte

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rawBody, _ = io.ReadAll(r.Body)
				_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
			}))
			defer srv.Close()

			tr := NewTransport(srv.URL)

			err := jsonrpcProtocol{}.TorrentRemove(context.Background(), tr, []any{int64(2), "cafebabe"}, tt.deleteData)
			require.NoError(t, err)

			var gotBody map[string]any
			require.NoError(t, json.Unmarshal(rawBody, &gotBody))

			assert.Equal(t, "torrent_remove", gotBody["method"])
			params, ok := gotBody["params"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, []any{float64(2), "cafebabe"}, params["ids"])
			assert.Equal(t, tt.deleteData, params["delete_local_data"])
		})

		t.Run("bespoke "+tt.name, func(t *testing.T) {
			var rawBody []byte

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rawBody, _ = io.ReadAll(r.Body)
				_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
			}))
			defer srv.Close()

			tr := NewTransport(srv.URL)

			err := bespokeProtocol{}.TorrentRemove(context.Background(), tr, []any{int64(2), "cafebabe"}, tt.deleteData)
			require.NoError(t, err)

			var gotBody map[string]any
			require.NoError(t, json.Unmarshal(rawBody, &gotBody))

			assert.Equal(t, "torrent-remove", gotBody["method"])
			args, ok := gotBody["arguments"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, []any{float64(2), "cafebabe"}, args["ids"])
			assert.Equal(t, tt.deleteData, args["delete-local-data"])
		})
	}
}

func TestProtocolTorrentRemoveOmitsIDsWhenAll(t *testing.T) {
	t.Run("jsonrpc omits ids but keeps delete flag", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		// A nil ids slice (RPCIDs() for an All selector) omits ids but always
		// sends the delete flag.
		err := jsonrpcProtocol{}.TorrentRemove(context.Background(), tr, nil, true)
		require.NoError(t, err)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		assert.Equal(t, "torrent_remove", gotBody["method"])
		params, ok := gotBody["params"].(map[string]any)
		require.True(t, ok)
		assert.NotContains(t, params, "ids")
		assert.Equal(t, true, params["delete_local_data"])
	})

	t.Run("bespoke omits ids but keeps delete flag", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		err := bespokeProtocol{}.TorrentRemove(context.Background(), tr, nil, false)
		require.NoError(t, err)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		assert.Equal(t, "torrent-remove", gotBody["method"])
		args, ok := gotBody["arguments"].(map[string]any)
		require.True(t, ok)
		assert.NotContains(t, args, "ids")
		assert.Equal(t, false, args["delete-local-data"])
	})
}

func TestJSONRPCProtocolTorrentRemoveMapsErrorObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"error": {"code": -32601, "message": "method not found"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := jsonrpcProtocol{}.TorrentRemove(context.Background(), tr, []any{int64(1)}, false)
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "method not found")
}

func TestBespokeProtocolTorrentRemoveMapsNonSuccessResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result": "invalid argument", "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := bespokeProtocol{}.TorrentRemove(context.Background(), tr, []any{int64(1)}, true)
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "invalid argument")
}

func TestProtocolTorrentSetLocation(t *testing.T) {
	tests := []struct {
		name string
		move bool
	}{
		{name: "relocate files", move: true},
		{name: "in place", move: false},
	}

	for _, tt := range tests {
		t.Run("jsonrpc "+tt.name, func(t *testing.T) {
			var rawBody []byte

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rawBody, _ = io.ReadAll(r.Body)
				_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
			}))
			defer srv.Close()

			tr := NewTransport(srv.URL)

			err := jsonrpcProtocol{}.TorrentSetLocation(context.Background(), tr, []any{int64(2), "cafebabe"}, "/data/media", tt.move)
			require.NoError(t, err)

			var gotBody map[string]any
			require.NoError(t, json.Unmarshal(rawBody, &gotBody))

			assert.Equal(t, "torrent_set_location", gotBody["method"])
			params, ok := gotBody["params"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, []any{float64(2), "cafebabe"}, params["ids"])
			assert.Equal(t, "/data/media", params["location"])
			assert.Equal(t, tt.move, params["move"])
		})

		t.Run("bespoke "+tt.name, func(t *testing.T) {
			var rawBody []byte

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rawBody, _ = io.ReadAll(r.Body)
				_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
			}))
			defer srv.Close()

			tr := NewTransport(srv.URL)

			err := bespokeProtocol{}.TorrentSetLocation(context.Background(), tr, []any{int64(2), "cafebabe"}, "/data/media", tt.move)
			require.NoError(t, err)

			var gotBody map[string]any
			require.NoError(t, json.Unmarshal(rawBody, &gotBody))

			assert.Equal(t, "torrent-set-location", gotBody["method"])
			args, ok := gotBody["arguments"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, []any{float64(2), "cafebabe"}, args["ids"])
			assert.Equal(t, "/data/media", args["location"])
			assert.Equal(t, tt.move, args["move"])
		})
	}
}

func TestProtocolTorrentSetLocationOmitsIDsWhenAll(t *testing.T) {
	t.Run("jsonrpc omits ids but keeps location and move", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		// A nil ids slice (RPCIDs() for an All selector) omits ids but always
		// sends location and move.
		err := jsonrpcProtocol{}.TorrentSetLocation(context.Background(), tr, nil, "/data/media", true)
		require.NoError(t, err)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		assert.Equal(t, "torrent_set_location", gotBody["method"])
		params, ok := gotBody["params"].(map[string]any)
		require.True(t, ok)
		assert.NotContains(t, params, "ids")
		assert.Equal(t, "/data/media", params["location"])
		assert.Equal(t, true, params["move"])
	})

	t.Run("bespoke omits ids but keeps location and move", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		err := bespokeProtocol{}.TorrentSetLocation(context.Background(), tr, nil, "/data/media", false)
		require.NoError(t, err)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		assert.Equal(t, "torrent-set-location", gotBody["method"])
		args, ok := gotBody["arguments"].(map[string]any)
		require.True(t, ok)
		assert.NotContains(t, args, "ids")
		assert.Equal(t, "/data/media", args["location"])
		assert.Equal(t, false, args["move"])
	})
}

func TestJSONRPCProtocolTorrentSetLocationMapsErrorObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"error": {"code": -32601, "message": "method not found"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := jsonrpcProtocol{}.TorrentSetLocation(context.Background(), tr, []any{int64(1)}, "/data/media", true)
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "method not found")
}

func TestBespokeProtocolTorrentSetLocationMapsNonSuccessResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result": "invalid argument", "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := bespokeProtocol{}.TorrentSetLocation(context.Background(), tr, []any{int64(1)}, "/data/media", false)
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "invalid argument")
}

func TestProtocolTorrentSet(t *testing.T) {
	t.Run("jsonrpc sends snake_case keys verbatim with ids", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		err := jsonrpcProtocol{}.TorrentSet(context.Background(), tr, []any{int64(2), "cafebabe"}, map[string]any{
			"bandwidth_priority": 1,
			"seed_ratio_mode":    1,
			"peer_limit":         50,
			"labels":             []string{"a", "b"},
		})
		require.NoError(t, err)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		// JSON-RPC 2.0 envelope: snake_case method, snake_case keys sent verbatim.
		assert.Equal(t, "torrent_set", gotBody["method"])
		params, ok := gotBody["params"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, []any{float64(2), "cafebabe"}, params["ids"])
		assert.Equal(t, float64(1), params["bandwidth_priority"])
		assert.Equal(t, float64(1), params["seed_ratio_mode"])
		assert.Equal(t, float64(50), params["peer_limit"])
		assert.Equal(t, []any{"a", "b"}, params["labels"])
	})

	t.Run("bespoke remaps keys to camelCase/kebab with ids", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		err := bespokeProtocol{}.TorrentSet(context.Background(), tr, []any{int64(2), "cafebabe"}, map[string]any{
			"bandwidth_priority": 1,
			"seed_ratio_mode":    1,
			"seed_idle_limit":    30,
			"tracker_list":       "http://tracker",
			"peer_limit":         50,
			"labels":             []string{"a", "b"},
			"group":              "media",
		})
		require.NoError(t, err)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		// Bespoke envelope: kebab-case method; the documented exceptions become
		// camelCase, while plain keys (peer-limit, labels, group) use the
		// mechanical "_"->"-" transform.
		assert.Equal(t, "torrent-set", gotBody["method"])
		args, ok := gotBody["arguments"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, []any{float64(2), "cafebabe"}, args["ids"])
		assert.Equal(t, float64(1), args["bandwidthPriority"])
		assert.Equal(t, float64(1), args["seedRatioMode"])
		assert.Equal(t, float64(30), args["seedIdleLimit"])
		assert.Equal(t, "http://tracker", args["trackerList"])
		assert.Equal(t, float64(50), args["peer-limit"])
		assert.Equal(t, []any{"a", "b"}, args["labels"])
		assert.Equal(t, "media", args["group"])
		assert.NotContains(t, args, "bandwidth_priority")
		assert.NotContains(t, args, "seed_ratio_mode")
	})
}

func TestProtocolTorrentSetOmitsIDsWhenAll(t *testing.T) {
	t.Run("jsonrpc omits ids but keeps settings", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		// A nil ids slice (RPCIDs() for an All selector) omits ids but still sends
		// the settings.
		err := jsonrpcProtocol{}.TorrentSet(context.Background(), tr, nil, map[string]any{"honors_session_limits": false})
		require.NoError(t, err)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		assert.Equal(t, "torrent_set", gotBody["method"])
		params, ok := gotBody["params"].(map[string]any)
		require.True(t, ok)
		assert.NotContains(t, params, "ids")
		assert.Equal(t, false, params["honors_session_limits"])
	})

	t.Run("bespoke omits ids but keeps settings", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		err := bespokeProtocol{}.TorrentSet(context.Background(), tr, nil, map[string]any{"honors_session_limits": false})
		require.NoError(t, err)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		assert.Equal(t, "torrent-set", gotBody["method"])
		args, ok := gotBody["arguments"].(map[string]any)
		require.True(t, ok)
		assert.NotContains(t, args, "ids")
		assert.Equal(t, false, args["honorsSessionLimits"])
	})
}

func TestJSONRPCProtocolTorrentSetMapsErrorObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"error": {"code": -32602, "message": "invalid argument"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := jsonrpcProtocol{}.TorrentSet(context.Background(), tr, []any{int64(1)}, map[string]any{"peer_limit": 1})
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "invalid argument")
}

func TestBespokeProtocolTorrentSetMapsNonSuccessResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result": "invalid argument", "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := bespokeProtocol{}.TorrentSet(context.Background(), tr, []any{int64(1)}, map[string]any{"peer_limit": 1})
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "invalid argument")
}

func TestProtocolActionOmitsIDsWhenAll(t *testing.T) {
	t.Run("jsonrpc omits ids", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		// A nil ids slice (RPCIDs() for an All selector) omits the params entirely.
		err := jsonrpcProtocol{}.TorrentStop(context.Background(), tr, nil)
		require.NoError(t, err)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		assert.Equal(t, "torrent_stop", gotBody["method"])
		assert.NotContains(t, gotBody, "params")
	})

	t.Run("bespoke omits ids", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		err := bespokeProtocol{}.TorrentStart(context.Background(), tr, nil, false)
		require.NoError(t, err)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		assert.Equal(t, "torrent-start", gotBody["method"])
		assert.NotContains(t, gotBody, "arguments")
	})
}

func TestJSONRPCProtocolActionMapsErrorObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"error": {"code": -32601, "message": "method not found"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := jsonrpcProtocol{}.TorrentStart(context.Background(), tr, []any{int64(1)}, false)
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "method not found")
}

func TestBespokeProtocolActionMapsNonSuccessResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result": "invalid argument", "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := bespokeProtocol{}.TorrentStop(context.Background(), tr, []any{int64(1)})
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "invalid argument")
}

func TestJSONRPCProtocolTorrentGetMapsErrorObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"error": {"code": -32601, "message": "method not found"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	_, err := jsonrpcProtocol{}.TorrentGet(context.Background(), tr, nil, nil)
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "method not found")
}

func TestBespokeProtocolTorrentGetMapsNonSuccessResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result": "forbidden", "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	_, err := bespokeProtocol{}.TorrentGet(context.Background(), tr, nil, nil)
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "forbidden")
}

func TestJSONRPCProtocolTorrentAdd(t *testing.T) {
	t.Run("added", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{
				"jsonrpc": "2.0",
				"result": {"torrent_added": {"id": 11, "name": "debian.iso", "hash_string": "deadbeef"}},
				"id": 1
			}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		res, err := jsonrpcProtocol{}.TorrentAdd(context.Background(), tr, AddRequest{
			Metainfo:    "YmFzZTY0",
			DownloadDir: "/downloads",
			Paused:      true,
			Labels:      []string{"linux"},
		})
		require.NoError(t, err)

		assert.Equal(t, int64(11), res.ID)
		assert.Equal(t, "debian.iso", res.Name)
		assert.Equal(t, "deadbeef", res.Hash)
		assert.False(t, res.Duplicate)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		// JSON-RPC 2.0 envelope: snake_case method, snake_case params; empty
		// filename is omitted.
		assert.Equal(t, "torrent_add", gotBody["method"])
		params, ok := gotBody["params"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "YmFzZTY0", params["metainfo"])
		assert.Equal(t, "/downloads", params["download_dir"])
		assert.Equal(t, true, params["paused"])
		assert.Equal(t, []any{"linux"}, params["labels"])
		assert.NotContains(t, params, "filename")
	})

	t.Run("duplicate", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{
				"jsonrpc": "2.0",
				"result": {"torrent_duplicate": {"id": 12, "name": "ubuntu.iso", "hash_string": "cafebabe"}},
				"id": 1
			}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		res, err := jsonrpcProtocol{}.TorrentAdd(context.Background(), tr, AddRequest{Filename: "magnet:?xt=urn:btih:cafebabe"})
		require.NoError(t, err)

		assert.Equal(t, int64(12), res.ID)
		assert.Equal(t, "ubuntu.iso", res.Name)
		assert.Equal(t, "cafebabe", res.Hash)
		assert.True(t, res.Duplicate)
	})

	t.Run("server error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{
				"jsonrpc": "2.0",
				"error": {"code": -32602, "message": "invalid metainfo"},
				"id": 1
			}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		_, err := jsonrpcProtocol{}.TorrentAdd(context.Background(), tr, AddRequest{Metainfo: "bogus"})
		require.ErrorIs(t, err, ErrRPC)
		assert.ErrorContains(t, err, "invalid metainfo")
	})
}

func TestBespokeProtocolTorrentAdd(t *testing.T) {
	t.Run("added", func(t *testing.T) {
		var rawBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{
				"arguments": {"torrent-added": {"id": 11, "name": "debian.iso", "hashString": "deadbeef"}},
				"result": "success",
				"tag": 1
			}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		res, err := bespokeProtocol{}.TorrentAdd(context.Background(), tr, AddRequest{
			Metainfo:    "YmFzZTY0",
			DownloadDir: "/downloads",
			Paused:      true,
			Labels:      []string{"linux"},
		})
		require.NoError(t, err)

		assert.Equal(t, int64(11), res.ID)
		assert.Equal(t, "debian.iso", res.Name)
		assert.Equal(t, "deadbeef", res.Hash)
		assert.False(t, res.Duplicate)

		var gotBody map[string]any
		require.NoError(t, json.Unmarshal(rawBody, &gotBody))

		// Bespoke envelope: kebab-case method, kebab-case download-dir; empty
		// filename is omitted.
		assert.Equal(t, "torrent-add", gotBody["method"])
		args, ok := gotBody["arguments"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "YmFzZTY0", args["metainfo"])
		assert.Equal(t, "/downloads", args["download-dir"])
		assert.Equal(t, true, args["paused"])
		assert.Equal(t, []any{"linux"}, args["labels"])
		assert.NotContains(t, args, "filename")
	})

	t.Run("duplicate", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{
				"arguments": {"torrent-duplicate": {"id": 12, "name": "ubuntu.iso", "hashString": "cafebabe"}},
				"result": "success",
				"tag": 1
			}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		res, err := bespokeProtocol{}.TorrentAdd(context.Background(), tr, AddRequest{Filename: "magnet:?xt=urn:btih:cafebabe"})
		require.NoError(t, err)

		assert.Equal(t, int64(12), res.ID)
		assert.Equal(t, "ubuntu.iso", res.Name)
		assert.Equal(t, "cafebabe", res.Hash)
		assert.True(t, res.Duplicate)
	})

	t.Run("server error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"result": "invalid or corrupt torrent file", "tag": 1}`))
		}))
		defer srv.Close()

		tr := NewTransport(srv.URL)

		_, err := bespokeProtocol{}.TorrentAdd(context.Background(), tr, AddRequest{Metainfo: "bogus"})
		require.ErrorIs(t, err, ErrRPC)
		assert.ErrorContains(t, err, "invalid or corrupt torrent file")
	})
}

func TestTranslateMethod(t *testing.T) {
	// JSON-RPC 2.0 spells methods snake_case; the bespoke scheme uses the
	// canonical kebab-case name verbatim.
	assert.Equal(t, "queue_move_top", translateMethod(ProtocolJSONRPC, "queue-move-top"))
	assert.Equal(t, "session_get", translateMethod(ProtocolJSONRPC, "session-get"))
	assert.Equal(t, "queue-move-top", translateMethod(ProtocolBespoke, "queue-move-top"))
	assert.Equal(t, "session-get", translateMethod(ProtocolBespoke, "session-get"))
}

func TestJSONRPCProtocolCallSuccess(t *testing.T) {
	var rawBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": {"version": "4.1.0"}, "id": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	resp, err := jsonrpcProtocol{}.Call(context.Background(), tr, "session-get", nil)
	require.NoError(t, err)

	// Normalized envelope: synthesized "success", result -> arguments, id -> tag.
	assert.True(t, resp.OK)
	assert.Equal(t, "success", resp.Result)
	assert.JSONEq(t, `{"version": "4.1.0"}`, string(resp.Arguments))
	assert.Equal(t, 1, resp.Tag)
	assert.Equal(t, 0, resp.Code)

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// JSON-RPC 2.0 envelope with the snake_case translated method name; a
	// zero-argument call omits params.
	assert.Equal(t, "session_get", gotBody["method"])
	assert.Equal(t, "2.0", gotBody["jsonrpc"])
	assert.NotContains(t, gotBody, "params")
}

func TestBespokeProtocolCallSuccess(t *testing.T) {
	var rawBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"arguments": {"version": "4.0.6"}, "result": "success", "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	resp, err := bespokeProtocol{}.Call(context.Background(), tr, "session-get", nil)
	require.NoError(t, err)

	// Bespoke passes result/arguments/tag straight through.
	assert.True(t, resp.OK)
	assert.Equal(t, "success", resp.Result)
	assert.JSONEq(t, `{"version": "4.0.6"}`, string(resp.Arguments))
	assert.Equal(t, 1, resp.Tag)

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// Bespoke envelope with the verbatim kebab-case method name; a zero-argument
	// call omits arguments.
	assert.Equal(t, "session-get", gotBody["method"])
	assert.NotContains(t, gotBody, "arguments")
}

func TestJSONRPCProtocolCallPassesArguments(t *testing.T) {
	var rawBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": {"torrents": []}, "id": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	// A large id must survive untouched (no float64 coercion) since args ride as
	// a raw fragment.
	args := json.RawMessage(`{"ids": [9007199254740993]}`)

	resp, err := jsonrpcProtocol{}.Call(context.Background(), tr, "torrent-get", args)
	require.NoError(t, err)
	assert.True(t, resp.OK)

	assert.Contains(t, string(rawBody), `"params":{"ids":[9007199254740993]}`)
}

func TestJSONRPCProtocolCallRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "error": {"code": -32601, "message": "method not found"}, "id": 7}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	// An RPC-level error does not return a Go error: it surfaces via OK == false.
	resp, err := jsonrpcProtocol{}.Call(context.Background(), tr, "bogus-method", nil)
	require.NoError(t, err)

	assert.False(t, resp.OK)
	assert.Equal(t, "method not found", resp.Result)
	assert.Equal(t, -32601, resp.Code)
	assert.Equal(t, 7, resp.Tag)
	assert.Nil(t, resp.Arguments)
}

func TestBespokeProtocolCallRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result": "invalid argument", "tag": 3}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	resp, err := bespokeProtocol{}.Call(context.Background(), tr, "bogus-method", nil)
	require.NoError(t, err)

	assert.False(t, resp.OK)
	assert.Equal(t, "invalid argument", resp.Result)
	assert.Equal(t, 3, resp.Tag)
}

func TestProtocolCallRawBytespassthrough(t *testing.T) {
	const jsonrpcBody = `{"jsonrpc":"2.0","result":{"torrents":[{"id":1,"name":"a"}]},"id":1}`
	const bespokeBody = `{"arguments":{"torrents":[{"id":1,"name":"a"}]},"result":"success","tag":1}`

	t.Run("jsonrpc", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(jsonrpcBody))
		}))
		defer srv.Close()

		resp, err := jsonrpcProtocol{}.Call(context.Background(), NewTransport(srv.URL), "torrent-get", nil)
		require.NoError(t, err)

		// Raw carries the exact server bytes, byte-for-byte.
		assert.JSONEq(t, jsonrpcBody, string(resp.Raw))
	})

	t.Run("bespoke", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(bespokeBody))
		}))
		defer srv.Close()

		resp, err := bespokeProtocol{}.Call(context.Background(), NewTransport(srv.URL), "torrent-get", nil)
		require.NoError(t, err)

		assert.JSONEq(t, bespokeBody, string(resp.Raw))
	})
}

func TestProtocolCallEmptyBodyIsSuccess(t *testing.T) {
	t.Run("jsonrpc", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		resp, err := jsonrpcProtocol{}.Call(context.Background(), NewTransport(srv.URL), "queue-move-top", nil)
		require.NoError(t, err)
		assert.True(t, resp.OK)
		assert.Equal(t, "success", resp.Result)
		assert.Nil(t, resp.Arguments)
	})

	t.Run("bespoke", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		resp, err := bespokeProtocol{}.Call(context.Background(), NewTransport(srv.URL), "queue-move-top", nil)
		require.NoError(t, err)
		assert.True(t, resp.OK)
		assert.Equal(t, "success", resp.Result)
		assert.Nil(t, resp.Arguments)
	})
}

func TestJSONRPCProtocolSessionGetDecodesSettings(t *testing.T) {
	var rawBody []byte

	// snake_case session settings matching the JSON-RPC 2.0 scheme; cache uses
	// the real wire key "cache_size_mb".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"result": {
				"version": "4.1.0 (ae226418eb)",
				"rpc_version_semver": "6.0.0",
				"download_dir": "/downloads",
				"start_added_torrents": true,
				"seed_ratio_limit": 2.5,
				"cache_size_mb": 8,
				"peer_port": 51413,
				"encryption": "preferred",
				"speed_limit_down": 100,
				"alt_speed_up": 50,
				"download_queue_size": 5,
				"queue_stalled_minutes": 30
			},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	session, err := jsonrpcProtocol{}.SessionGet(context.Background(), tr)
	require.NoError(t, err)

	assert.Equal(t, "4.1.0 (ae226418eb)", session.Version)
	require.NotNil(t, session.DownloadDir)
	assert.Equal(t, "/downloads", *session.DownloadDir)
	require.NotNil(t, session.StartAddedTorrents)
	assert.True(t, *session.StartAddedTorrents)
	require.NotNil(t, session.SeedRatioLimit)
	assert.InDelta(t, 2.5, *session.SeedRatioLimit, 0.0001)
	require.NotNil(t, session.CacheSizeMiB)
	assert.Equal(t, 8, *session.CacheSizeMiB)
	require.NotNil(t, session.PeerPort)
	assert.Equal(t, 51413, *session.PeerPort)
	require.NotNil(t, session.Encryption)
	assert.Equal(t, "preferred", *session.Encryption)
	require.NotNil(t, session.SpeedLimitDown)
	assert.Equal(t, int64(100), *session.SpeedLimitDown)
	require.NotNil(t, session.AltSpeedUp)
	assert.Equal(t, int64(50), *session.AltSpeedUp)
	require.NotNil(t, session.DownloadQueueSize)
	assert.Equal(t, 5, *session.DownloadQueueSize)
	require.NotNil(t, session.QueueStalledMinutes)
	assert.Equal(t, 30, *session.QueueStalledMinutes)

	// Omitted settings stay nil (distinguishable from a zero value).
	assert.Nil(t, session.IncompleteDir)
	assert.Nil(t, session.BlocklistSize)

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// The request asks for the version fields plus the settings keys.
	assert.Equal(t, "session_get", gotBody["method"])
	params, ok := gotBody["params"].(map[string]any)
	require.True(t, ok)
	fields, ok := params["fields"].([]any)
	require.True(t, ok)
	assert.Contains(t, fields, "download_dir")
	assert.Contains(t, fields, "cache_size_mb")
	assert.Contains(t, fields, "encryption")
}

func TestBespokeProtocolSessionGetDecodesSettings(t *testing.T) {
	var rawBody []byte

	// kebab-case settings (with the documented camelCase seed-ratio keys and the
	// "tolerated" encryption value) matching the bespoke scheme.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{
			"arguments": {
				"version": "4.0.6 (38c164933e)",
				"rpc-version-semver": "5.4.0",
				"download-dir": "/data",
				"seedRatioLimited": true,
				"seedRatioLimit": 1.5,
				"cache-size-mb": 16,
				"peer-port": 12345,
				"encryption": "tolerated",
				"speed-limit-up": 200,
				"download-queue-size": 7
			},
			"result": "success",
			"tag": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	session, err := bespokeProtocol{}.SessionGet(context.Background(), tr)
	require.NoError(t, err)

	require.NotNil(t, session.DownloadDir)
	assert.Equal(t, "/data", *session.DownloadDir)
	require.NotNil(t, session.SeedRatioLimited)
	assert.True(t, *session.SeedRatioLimited)
	require.NotNil(t, session.SeedRatioLimit)
	assert.InDelta(t, 1.5, *session.SeedRatioLimit, 0.0001)
	require.NotNil(t, session.CacheSizeMiB)
	assert.Equal(t, 16, *session.CacheSizeMiB)
	require.NotNil(t, session.PeerPort)
	assert.Equal(t, 12345, *session.PeerPort)
	require.NotNil(t, session.SpeedLimitUp)
	assert.Equal(t, int64(200), *session.SpeedLimitUp)
	require.NotNil(t, session.DownloadQueueSize)
	assert.Equal(t, 7, *session.DownloadQueueSize)

	// The wire's "tolerated" normalizes to the canonical "allowed".
	require.NotNil(t, session.Encryption)
	assert.Equal(t, "allowed", *session.Encryption)

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	assert.Equal(t, "session-get", gotBody["method"])
	args, ok := gotBody["arguments"].(map[string]any)
	require.True(t, ok)
	fields, ok := args["fields"].([]any)
	require.True(t, ok)
	assert.Contains(t, fields, "download-dir")
	assert.Contains(t, fields, "seedRatioLimited")
	assert.Contains(t, fields, "cache-size-mb")
}

func TestJSONRPCProtocolSessionSet(t *testing.T) {
	var rawBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": null, "id": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := jsonrpcProtocol{}.SessionSet(context.Background(), tr, map[string]any{
		"peer_port":      12345,
		"dht_enabled":    false,
		"cache_size_mib": 8,
		"encryption":     "allowed",
	})
	require.NoError(t, err)

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// JSON-RPC 2.0 envelope: snake_case method, snake_case params sent verbatim
	// except the cache key, which becomes the real wire key "cache_size_mb".
	// Encryption is not remapped on the JSON-RPC scheme.
	assert.Equal(t, "session_set", gotBody["method"])
	params, ok := gotBody["params"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(12345), params["peer_port"])
	assert.Equal(t, false, params["dht_enabled"])
	assert.Equal(t, float64(8), params["cache_size_mb"])
	assert.NotContains(t, params, "cache_size_mib")
	assert.Equal(t, "allowed", params["encryption"])
}

func TestBespokeProtocolSessionSet(t *testing.T) {
	var rawBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := bespokeProtocol{}.SessionSet(context.Background(), tr, map[string]any{
		"peer_port":          12345,
		"dht_enabled":        false,
		"seed_ratio_limit":   2.0,
		"seed_ratio_limited": true,
		"cache_size_mib":     8,
		"encryption":         "allowed",
	})
	require.NoError(t, err)

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// Bespoke envelope: kebab-case method; snake keys remap to kebab, the
	// seed-ratio keys to camelCase, the cache key to "cache-size-mb", and the
	// canonical "allowed" encryption to the wire's "tolerated".
	assert.Equal(t, "session-set", gotBody["method"])
	args, ok := gotBody["arguments"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(12345), args["peer-port"])
	assert.Equal(t, false, args["dht-enabled"])
	assert.Equal(t, float64(2.0), args["seedRatioLimit"])
	assert.Equal(t, true, args["seedRatioLimited"])
	assert.Equal(t, float64(8), args["cache-size-mb"])
	assert.Equal(t, "tolerated", args["encryption"])
	assert.NotContains(t, args, "peer_port")
	assert.NotContains(t, args, "cache_size_mib")
}

func TestJSONRPCProtocolSessionSetMapsErrorObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"error": {"code": -32602, "message": "invalid argument"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := jsonrpcProtocol{}.SessionSet(context.Background(), tr, map[string]any{"peer_port": 1})
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "invalid argument")
}

func TestBespokeProtocolSessionSetMapsNonSuccessResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result": "invalid argument", "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	err := bespokeProtocol{}.SessionSet(context.Background(), tr, map[string]any{"peer_port": 1})
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "invalid argument")
}

func TestJSONRPCProtocolPortTest(t *testing.T) {
	var rawBody []byte

	// Snake_case result keys matching docs/transmission/rpc-spec-411.md.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"result": {"port_is_open": true, "ip_protocol": "ipv6"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	result, err := jsonrpcProtocol{}.PortTest(context.Background(), tr)
	require.NoError(t, err)

	assert.True(t, result.Open)
	assert.Equal(t, "ipv6", result.IPProtocol)

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// JSON-RPC 2.0 envelope: snake_case method, jsonrpc/id present, no tag.
	assert.Equal(t, "2.0", gotBody["jsonrpc"])
	assert.Equal(t, "port_test", gotBody["method"])
	assert.Contains(t, gotBody, "id")
	assert.NotContains(t, gotBody, "tag")
}

func TestJSONRPCProtocolPortTestClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"result": {"port_is_open": false, "ip_protocol": "ipv4"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	result, err := jsonrpcProtocol{}.PortTest(context.Background(), tr)
	require.NoError(t, err)

	assert.False(t, result.Open)
	assert.Equal(t, "ipv4", result.IPProtocol)
}

func TestBespokeProtocolPortTest(t *testing.T) {
	var rawBody []byte

	// Kebab-case result keys matching docs/transmission/rpc-spec-406.md; the
	// bespoke scheme has no ip-protocol field.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{
			"arguments": {"port-is-open": true},
			"result": "success",
			"tag": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	result, err := bespokeProtocol{}.PortTest(context.Background(), tr)
	require.NoError(t, err)

	assert.True(t, result.Open)
	assert.Empty(t, result.IPProtocol, "the bespoke scheme reports no ip protocol")

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// Bespoke envelope: kebab-case method, tag present, no jsonrpc.
	assert.Equal(t, "port-test", gotBody["method"])
	assert.Contains(t, gotBody, "tag")
	assert.NotContains(t, gotBody, "jsonrpc")
}

func TestBespokeProtocolPortTestClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"arguments": {"port-is-open": false}, "result": "success", "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	result, err := bespokeProtocol{}.PortTest(context.Background(), tr)
	require.NoError(t, err)

	assert.False(t, result.Open)
	assert.Empty(t, result.IPProtocol)
}

func TestJSONRPCProtocolPortTestMapsErrorObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"error": {"code": 7, "message": "Couldn't test port"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	_, err := jsonrpcProtocol{}.PortTest(context.Background(), tr)
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "Couldn't test port")
}

func TestBespokeProtocolPortTestMapsNonSuccessResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result": "no such session", "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	_, err := bespokeProtocol{}.PortTest(context.Background(), tr)
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "no such session")
}

func TestJSONRPCProtocolFreeSpace(t *testing.T) {
	var rawBody []byte

	// Snake_case result keys matching docs/transmission/rpc-spec-411.md; a 4.1+
	// server reports total_size.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"result": {"path": "/downloads", "size_bytes": 1024, "total_size": 4096},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	result, err := jsonrpcProtocol{}.FreeSpace(context.Background(), tr, "/downloads")
	require.NoError(t, err)

	assert.Equal(t, "/downloads", result.Path)
	assert.Equal(t, int64(1024), result.SizeBytes)
	assert.Equal(t, int64(4096), result.TotalBytes)

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// JSON-RPC 2.0 envelope: snake_case method, jsonrpc/id present, no tag; the
	// queried path is sent in params.
	assert.Equal(t, "2.0", gotBody["jsonrpc"])
	assert.Equal(t, "free_space", gotBody["method"])
	assert.Contains(t, gotBody, "id")
	assert.NotContains(t, gotBody, "tag")

	params, ok := gotBody["params"].(map[string]any)
	require.True(t, ok, "params is an object")
	assert.Equal(t, "/downloads", params["path"])
}

func TestBespokeProtocolFreeSpace(t *testing.T) {
	var rawBody []byte

	// Kebab-case free size (size-bytes) matching docs/transmission/rpc-spec-406.md;
	// the total capacity is spelled total_size on the wire even here.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{
			"arguments": {"path": "/downloads", "size-bytes": 1024, "total_size": 4096},
			"result": "success",
			"tag": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	result, err := bespokeProtocol{}.FreeSpace(context.Background(), tr, "/downloads")
	require.NoError(t, err)

	assert.Equal(t, "/downloads", result.Path)
	assert.Equal(t, int64(1024), result.SizeBytes)
	assert.Equal(t, int64(4096), result.TotalBytes)

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// Bespoke envelope: kebab-case method, tag present, no jsonrpc; the queried
	// path is sent in arguments.
	assert.Equal(t, "free-space", gotBody["method"])
	assert.Contains(t, gotBody, "tag")
	assert.NotContains(t, gotBody, "jsonrpc")

	args, ok := gotBody["arguments"].(map[string]any)
	require.True(t, ok, "arguments is an object")
	assert.Equal(t, "/downloads", args["path"])
}

func TestBespokeProtocolFreeSpaceOmitsTotalOnOldServer(t *testing.T) {
	// A bespoke server too old to return total_size omits it; TotalBytes stays 0.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"arguments": {"path": "/downloads", "size-bytes": 2048},
			"result": "success",
			"tag": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	result, err := bespokeProtocol{}.FreeSpace(context.Background(), tr, "/downloads")
	require.NoError(t, err)

	assert.Equal(t, int64(2048), result.SizeBytes)
	assert.Zero(t, result.TotalBytes, "an old bespoke server reports no total capacity")
}

func TestJSONRPCProtocolFreeSpaceMapsErrorObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"error": {"code": 7, "message": "No such directory"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	_, err := jsonrpcProtocol{}.FreeSpace(context.Background(), tr, "/nope")
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "No such directory")
}

func TestBespokeProtocolFreeSpaceMapsNonSuccessResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result": "no such session", "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	_, err := bespokeProtocol{}.FreeSpace(context.Background(), tr, "/downloads")
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "no such session")
}

// piecesB64 is a base64 bitfield for a 10-piece torrent: bytes 0xB0 0x40, so
// pieces 0, 2, 3, and 9 are complete (MSB-first within each byte).
const piecesB64 = "sEA="

// assertDetail asserts the shared detail expectations both protocols must decode
// identically, so the two golden tests can't drift apart. It covers the embedded
// torrent identity, the merged file selection state, the peer/tracker fields, the
// base64 pieces bitfield, the magnet link, and the overview scalars.
func assertDetail(t *testing.T, got TorrentDetail) {
	t.Helper()

	// Embedded Torrent (the list fields) is present.
	assert.Equal(t, int64(7), got.ID)
	assert.Equal(t, "abc123", got.Hash)
	assert.Equal(t, "ubuntu.iso", got.Name)

	// Files merge the parallel files/wanted/priorities arrays by index. The first
	// file is wanted at high priority; the second is unwanted at low priority.
	require.Len(t, got.Files, 2)
	assert.Equal(t, "ubuntu.iso", got.Files[0].Name)
	assert.Equal(t, int64(2048), got.Files[0].Length)
	assert.Equal(t, int64(1024), got.Files[0].BytesCompleted)
	assert.True(t, got.Files[0].Wanted)
	assert.Equal(t, 1, got.Files[0].Priority)
	assert.Equal(t, "readme.txt", got.Files[1].Name)
	assert.False(t, got.Files[1].Wanted)
	assert.Equal(t, -1, got.Files[1].Priority)

	// Peers.
	require.Len(t, got.Peers, 1)
	assert.Equal(t, "10.0.0.1", got.Peers[0].Address)
	assert.Equal(t, 51413, got.Peers[0].Port)
	assert.Equal(t, "Transmission 4.0", got.Peers[0].Client)
	assert.InDelta(t, 0.5, got.Peers[0].Progress, 0.0001)
	assert.Equal(t, int64(1000), got.Peers[0].RateToClient)
	assert.Equal(t, int64(200), got.Peers[0].RateToPeer)
	assert.Equal(t, "TDdEi", got.Peers[0].FlagStr)
	assert.True(t, got.Peers[0].IsEncrypted)

	// Trackers (from trackerStats).
	require.Len(t, got.Trackers, 1)
	assert.Equal(t, "http://tracker.example/announce", got.Trackers[0].Announce)
	assert.Equal(t, "tracker.example", got.Trackers[0].Host)
	assert.Equal(t, "example", got.Trackers[0].Sitename)
	assert.Equal(t, 2, got.Trackers[0].Tier)
	assert.Equal(t, 1, got.Trackers[0].AnnounceState)
	assert.Equal(t, 42, got.Trackers[0].SeederCount)
	assert.Equal(t, 7, got.Trackers[0].LeecherCount)
	assert.Equal(t, 100, got.Trackers[0].DownloadCount)
	assert.Equal(t, "Success", got.Trackers[0].LastAnnounceResult)
	assert.Equal(t, int64(1700000200), got.Trackers[0].NextAnnounceTime)

	// Pieces bitfield: pieces 0, 2, 3, and 9 are complete.
	assert.Equal(t,
		[]bool{true, false, true, true, false, false, false, false, false, true},
		got.Pieces,
	)

	// Overview scalars.
	assert.Equal(t, "magnet:?xt=urn:btih:abc123", got.MagnetLink)
	assert.Equal(t, int64(4096), got.DownloadedEver)
	assert.Equal(t, int64(8192), got.UploadedEver)
	assert.Equal(t, int64(0), got.CorruptEver)
	assert.Equal(t, int64(1024), got.HaveValid)
	assert.Equal(t, int64(10), got.PieceCount)
	assert.Equal(t, int64(262144), got.PieceSize)
	assert.Equal(t, "a comment", got.Comment)
	assert.Equal(t, "mkbt", got.Creator)
	assert.Equal(t, int64(1690000000), got.DateCreated)
	assert.True(t, got.IsPrivate)
	assert.Equal(t, int64(3600), got.SecondsDownloading)
	assert.Equal(t, int64(7200), got.SecondsSeeding)
	assert.InDelta(t, 1.0, got.RecheckProgress, 0.0001)
	assert.InDelta(t, 1.0, got.MetadataPercentComplete, 0.0001)
}

// TestJSONRPCProtocolTorrentGetDetail decodes a full snake_case detail response,
// exercising the JSON-RPC parity case: wanted is a JSON boolean array, and pieces
// is a base64 bitfield.
func TestJSONRPCProtocolTorrentGetDetail(t *testing.T) {
	var rawBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"result": {"torrents": [{
				"id": 7,
				"hash_string": "abc123",
				"name": "ubuntu.iso",
				"status": 4,
				"files": [
					{"name": "ubuntu.iso", "length": 2048, "bytes_completed": 1024},
					{"name": "readme.txt", "length": 256, "bytes_completed": 0}
				],
				"wanted": [true, false],
				"priorities": [1, -1],
				"peers": [{
					"address": "10.0.0.1",
					"port": 51413,
					"client_name": "Transmission 4.0",
					"progress": 0.5,
					"rate_to_client": 1000,
					"rate_to_peer": 200,
					"flag_str": "TDdEi",
					"is_encrypted": true
				}],
				"tracker_stats": [{
					"announce": "http://tracker.example/announce",
					"host": "tracker.example",
					"sitename": "example",
					"tier": 2,
					"announce_state": 1,
					"seeder_count": 42,
					"leecher_count": 7,
					"download_count": 100,
					"last_announce_result": "Success",
					"next_announce_time": 1700000200
				}],
				"pieces": "` + piecesB64 + `",
				"magnet_link": "magnet:?xt=urn:btih:abc123",
				"downloaded_ever": 4096,
				"uploaded_ever": 8192,
				"corrupt_ever": 0,
				"have_valid": 1024,
				"piece_count": 10,
				"piece_size": 262144,
				"comment": "a comment",
				"creator": "mkbt",
				"date_created": 1690000000,
				"is_private": true,
				"seconds_downloading": 3600,
				"seconds_seeding": 7200,
				"recheck_progress": 1.0,
				"metadata_percent_complete": 1.0
			}]},
			"id": 1
		}`))
	}))
	defer srv.Close()

	details, err := jsonrpcProtocol{}.TorrentGetDetail(context.Background(), NewTransport(srv.URL), []any{int64(7)})
	require.NoError(t, err)
	require.Len(t, details, 1)

	assertDetail(t, details[0])

	// The request asks for the detail field set (including the detail-only fields)
	// and the selected id.
	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))
	assert.Equal(t, "torrent_get", gotBody["method"])
	params, ok := gotBody["params"].(map[string]any)
	require.True(t, ok)
	fields := fieldStrings(t, params["fields"])
	assert.Contains(t, fields, "files")
	assert.Contains(t, fields, "tracker_stats")
	assert.Contains(t, fields, "pieces")
	assert.Contains(t, fields, "magnet_link")
	assert.Equal(t, []any{float64(7)}, params["ids"])
}

// TestBespokeProtocolTorrentGetDetail decodes a full camelCase detail response,
// exercising the bespoke parity case: wanted is a 0/1 integer array (not
// booleans), and pieces is a base64 bitfield. The decoded shape must match the
// JSON-RPC golden exactly.
func TestBespokeProtocolTorrentGetDetail(t *testing.T) {
	var rawBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{
			"arguments": {"torrents": [{
				"id": 7,
				"hashString": "abc123",
				"name": "ubuntu.iso",
				"status": 4,
				"files": [
					{"name": "ubuntu.iso", "length": 2048, "bytesCompleted": 1024},
					{"name": "readme.txt", "length": 256, "bytesCompleted": 0}
				],
				"wanted": [1, 0],
				"priorities": [1, -1],
				"peers": [{
					"address": "10.0.0.1",
					"port": 51413,
					"clientName": "Transmission 4.0",
					"progress": 0.5,
					"rateToClient": 1000,
					"rateToPeer": 200,
					"flagStr": "TDdEi",
					"isEncrypted": true
				}],
				"trackerStats": [{
					"announce": "http://tracker.example/announce",
					"host": "tracker.example",
					"sitename": "example",
					"tier": 2,
					"announceState": 1,
					"seederCount": 42,
					"leecherCount": 7,
					"downloadCount": 100,
					"lastAnnounceResult": "Success",
					"nextAnnounceTime": 1700000200
				}],
				"pieces": "` + piecesB64 + `",
				"magnetLink": "magnet:?xt=urn:btih:abc123",
				"downloadedEver": 4096,
				"uploadedEver": 8192,
				"corruptEver": 0,
				"haveValid": 1024,
				"pieceCount": 10,
				"pieceSize": 262144,
				"comment": "a comment",
				"creator": "mkbt",
				"dateCreated": 1690000000,
				"isPrivate": true,
				"secondsDownloading": 3600,
				"secondsSeeding": 7200,
				"recheckProgress": 1.0,
				"metadataPercentComplete": 1.0
			}]},
			"result": "success",
			"tag": 1
		}`))
	}))
	defer srv.Close()

	details, err := bespokeProtocol{}.TorrentGetDetail(context.Background(), NewTransport(srv.URL), []any{int64(7)})
	require.NoError(t, err)
	require.Len(t, details, 1)

	assertDetail(t, details[0])

	// The request asks for the camelCase detail field set and the selected id.
	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))
	assert.Equal(t, "torrent-get", gotBody["method"])
	args, ok := gotBody["arguments"].(map[string]any)
	require.True(t, ok)
	fields := fieldStrings(t, args["fields"])
	assert.Contains(t, fields, "files")
	assert.Contains(t, fields, "trackerStats")
	assert.Contains(t, fields, "pieces")
	assert.Contains(t, fields, "magnetLink")
}

// TestTorrentGetDetailTrackerTierOmitted confirms a tracker entry with no "tier"
// key decodes to 0 without error in both schemes. Tier is spelled "tier" in the
// snake_case and the camelCase response alike, so a server that omits it (rather
// than one that spells it differently) is the only failure mode worth covering.
func TestTorrentGetDetailTrackerTierOmitted(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		protocol Protocol
	}{
		{
			name: "jsonrpc",
			body: `{
				"jsonrpc": "2.0",
				"result": {"torrents": [{
					"id": 7,
					"tracker_stats": [{"announce": "http://tracker.example/announce"}]
				}]},
				"id": 1
			}`,
			protocol: jsonrpcProtocol{},
		},
		{
			name: "bespoke",
			body: `{
				"arguments": {"torrents": [{
					"id": 7,
					"trackerStats": [{"announce": "http://tracker.example/announce"}]
				}]},
				"result": "success",
				"tag": 1
			}`,
			protocol: bespokeProtocol{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			details, err := tc.protocol.TorrentGetDetail(context.Background(), NewTransport(srv.URL), []any{int64(7)})
			require.NoError(t, err)
			require.Len(t, details, 1)
			require.Len(t, details[0].Trackers, 1)

			assert.Equal(t, "http://tracker.example/announce", details[0].Trackers[0].Announce)
			assert.Equal(t, 0, details[0].Trackers[0].Tier)
		})
	}
}

// TestDecodePiecesBitfield covers the base64 bitfield decoder's bit order, the
// pieceCount bound (padding bits past the last piece are dropped), and the
// graceful handling of empty or malformed input.
func TestDecodePiecesBitfield(t *testing.T) {
	t.Run("decodes MSB-first within each byte, bounded by pieceCount", func(t *testing.T) {
		got := decodePiecesBitfield(piecesB64, 10)
		assert.Equal(t,
			[]bool{true, false, true, true, false, false, false, false, false, true},
			got,
		)
	})

	t.Run("drops padding bits past pieceCount", func(t *testing.T) {
		// Same two bytes, but only 4 pieces: the trailing bits are ignored.
		got := decodePiecesBitfield(piecesB64, 4)
		assert.Equal(t, []bool{true, false, true, true}, got)
	})

	t.Run("empty string yields nil", func(t *testing.T) {
		assert.Nil(t, decodePiecesBitfield("", 10))
	})

	t.Run("zero pieceCount yields nil", func(t *testing.T) {
		assert.Nil(t, decodePiecesBitfield(piecesB64, 0))
	})

	t.Run("invalid base64 yields nil, not an error", func(t *testing.T) {
		assert.Nil(t, decodePiecesBitfield("not base64!!", 8))
	})

	t.Run("bounds allocation by payload, not by an oversized pieceCount", func(t *testing.T) {
		// piecesB64 decodes to 2 bytes, so the bitfield can hold at most 16
		// bits. A malicious pieceCount must not drive the result (or its
		// preallocation) past len(raw)*8.
		got := decodePiecesBitfield(piecesB64, 1_000_000)
		assert.Len(t, got, 16)
	})
}
