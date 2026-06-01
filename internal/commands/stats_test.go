package commands

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
)

// jsonrpcStatsResponse is a session_stats payload matching rpc-spec-411.md
// (snake_case fields), used for the JSON-RPC 2.0 protocol path.
const jsonrpcStatsResponse = `{
	"jsonrpc": "2.0",
	"result": {
		"active_torrent_count": 3,
		"paused_torrent_count": 2,
		"torrent_count": 5,
		"download_speed": 1572864,
		"upload_speed": 524288,
		"cumulative_stats": {
			"uploaded_bytes": 10737418240,
			"downloaded_bytes": 21474836480,
			"files_added": 42,
			"session_count": 7,
			"seconds_active": 360000
		},
		"current_stats": {
			"uploaded_bytes": 1073741824,
			"downloaded_bytes": 2147483648,
			"files_added": 3,
			"session_count": 1,
			"seconds_active": 3600
		}
	},
	"id": 1
}`

// bespokeStatsResponse is a session-stats payload matching rpc-spec-406.md
// (camelCase top-level, kebab-case nested objects), used for the bespoke path.
const bespokeStatsResponse = `{
	"arguments": {
		"activeTorrentCount": 1,
		"pausedTorrentCount": 4,
		"torrentCount": 5,
		"downloadSpeed": 2097152,
		"uploadSpeed": 1048576,
		"cumulative-stats": {
			"uploadedBytes": 5368709120,
			"downloadedBytes": 10737418240,
			"filesAdded": 21,
			"sessionCount": 3,
			"secondsActive": 180000
		},
		"current-stats": {
			"uploadedBytes": 536870912,
			"downloadedBytes": 1073741824,
			"filesAdded": 2,
			"sessionCount": 1,
			"secondsActive": 1800
		}
	},
	"result": "success",
	"tag": 1
}`

// runStats executes the stats command with args against stdin, returning stdout,
// stderr, and the Execute error.
func runStats(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewStatsCmd()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// statsConfigPath stands up a transmission stub serving the given session-stats
// payload under both protocols and writes a config pointing at it.
func statsConfigPath(t *testing.T, rpcVersion, jsonrpcBody, bespokeBody string) (string, func()) {
	t.Helper()

	keyring.MockInit()

	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: rpcVersion,
		responses: map[string]string{
			"session_stats": jsonrpcBody,
			"session-stats": bespokeBody,
		},
	}.handler())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	return cfgPath, srv.Close
}

func TestStatsPlaintextJSONRPC(t *testing.T) {
	cfgPath, closeSrv := statsConfigPath(t, "6.0.0", jsonrpcStatsResponse, "")
	defer closeSrv()

	out, _, err := runStats(t, "--config", cfgPath)
	require.NoError(t, err)

	assert.Contains(t, out, "5 total, 3 active, 2 paused")
	assert.Contains(t, out, "Download speed:  1.5 MiB/s")
	assert.Contains(t, out, "Upload speed:    512.0 KiB/s")
	assert.Contains(t, out, "Cumulative:")
	assert.Contains(t, out, "10.0 GiB")
	assert.Contains(t, out, "Current session:")
}

func TestStatsPlaintextBespoke(t *testing.T) {
	cfgPath, closeSrv := statsConfigPath(t, "", "", bespokeStatsResponse)
	defer closeSrv()

	out, _, err := runStats(t, "--config", cfgPath)
	require.NoError(t, err)

	assert.Contains(t, out, "5 total, 1 active, 4 paused")
	assert.Contains(t, out, "Download speed:  2.0 MiB/s")
	assert.Contains(t, out, "Upload speed:    1.0 MiB/s")
}

func TestStatsJSON(t *testing.T) {
	cfgPath, closeSrv := statsConfigPath(t, "6.0.0", jsonrpcStatsResponse, "")
	defer closeSrv()

	out, _, err := runStats(t, "--config", cfgPath, "--json")
	require.NoError(t, err)

	var stats struct {
		TorrentCount  int64 `json:"torrent_count"`
		DownloadSpeed int64 `json:"download_speed"`
		UploadSpeed   int64 `json:"upload_speed"`
		Cumulative    struct {
			UploadedBytes int64 `json:"uploaded_bytes"`
		} `json:"cumulative_stats"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &stats))

	assert.Equal(t, int64(5), stats.TorrentCount)
	assert.Equal(t, int64(1572864), stats.DownloadSpeed)
	assert.Equal(t, int64(524288), stats.UploadSpeed)
	assert.Equal(t, int64(10737418240), stats.Cumulative.UploadedBytes)
}

func TestStatsJQ(t *testing.T) {
	cfgPath, closeSrv := statsConfigPath(t, "6.0.0", jsonrpcStatsResponse, "")
	defer closeSrv()

	out, _, err := runStats(t, "--config", cfgPath, "--jq", ".upload_speed")
	require.NoError(t, err)

	assert.Equal(t, "524288", strings.TrimSpace(out))
}

func TestStatsNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runStats(t, "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}
