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
	"github.com/itspriddle/snatcher/internal/torrent"
)

// bespokeListResponse is a torrent-get payload (camelCase fields) covering two
// torrents in different states, used to exercise the list command end-to-end.
const bespokeListResponse = `{
	"arguments": {"torrents": [
		{
			"id": 1,
			"hashString": "aaa111",
			"name": "ubuntu-24.04.iso",
			"status": 4,
			"percentDone": 0.5,
			"totalSize": 2147483648,
			"rateDownload": 1572864,
			"rateUpload": 524288,
			"uploadRatio": 0.5,
			"eta": 3600,
			"queuePosition": 0,
			"labels": ["linux"]
		},
		{
			"id": 2,
			"hashString": "bbb222",
			"name": "debian.iso",
			"status": 6,
			"percentDone": 1.0,
			"totalSize": 1073741824,
			"rateDownload": 0,
			"rateUpload": 131072,
			"uploadRatio": 2.5,
			"eta": -1,
			"queuePosition": 1,
			"labels": []
		}
	]},
	"result": "success",
	"tag": 1
}`

// bespokeEmptyListResponse is a torrent-get payload with no torrents.
const bespokeEmptyListResponse = `{
	"arguments": {"torrents": []},
	"result": "success",
	"tag": 1
}`

// runList executes the list command with args, returning stdout, stderr, and the
// Execute error. The command's output is a *bytes.Buffer, so isTTY reports false
// and the piped (tab-separated) plaintext contract is exercised.
func runList(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewListCmd()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// listConfigPath stands up a transmission stub serving the given torrent-get
// payload on the bespoke path and writes a config pointing at it.
func listConfigPath(t *testing.T, body string) (string, func()) {
	t.Helper()

	keyring.MockInit()

	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // bespoke path
		responses: map[string]string{
			"torrent-get": body,
		},
	}.handler())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{ServerURL: srv.URL}, cfgPath))

	return cfgPath, srv.Close
}

func TestListPipedTabSeparated(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	out, _, err := runList(t, "--config", cfgPath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2, "piped output is header-less, one row per torrent")

	// Full, untruncated, tab-separated cells in the default column order.
	first := strings.Split(lines[0], "\t")
	require.Len(t, first, 9)
	assert.Equal(t, "1", first[0])
	assert.Equal(t, "ubuntu-24.04.iso", first[1])
	assert.Equal(t, "Downloading", first[2])
	assert.Equal(t, "50%", first[3])
	assert.Equal(t, "2.0 GiB", first[4])
	assert.Equal(t, "1.5 MiB/s", first[5])
	assert.Equal(t, "512.0 KiB/s", first[6])
	assert.Equal(t, "0.50", first[7])
	assert.Equal(t, "1h", first[8])

	second := strings.Split(lines[1], "\t")
	assert.Equal(t, "2", second[0])
	assert.Equal(t, "debian.iso", second[1])
	assert.Equal(t, "Seeding", second[2])
	assert.Equal(t, "-", second[8], "unknown ETA renders as a dash")
}

func TestListPipedEmptyWritesNothing(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeEmptyListResponse)
	defer closeSrv()

	out, _, err := runList(t, "--config", cfgPath)
	require.NoError(t, err)
	assert.Empty(t, out, "piped output for an empty list is empty")
}

func TestListJSON(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	out, _, err := runList(t, "--config", cfgPath, "--json")
	require.NoError(t, err)

	var got []struct {
		ID     int64  `json:"id"`
		Hash   string `json:"hash"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 2)

	assert.Equal(t, int64(1), got[0].ID)
	assert.Equal(t, "aaa111", got[0].Hash, "hash is present in JSON output")
	assert.Equal(t, "downloading", got[0].Status, "status serializes as its slug")
	assert.Equal(t, "seeding", got[1].Status)
}

func TestListJSONEmptyIsArrayNotNull(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeEmptyListResponse)
	defer closeSrv()

	out, _, err := runList(t, "--config", cfgPath, "--json")
	require.NoError(t, err)
	assert.Equal(t, "[]", strings.TrimSpace(out))
}

func TestListJQ(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	out, _, err := runList(t, "--config", cfgPath, "--jq", ".[].name")
	require.NoError(t, err)

	assert.Equal(t, `"ubuntu-24.04.iso"`+"\n"+`"debian.iso"`, strings.TrimSpace(out))
}

func TestListNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runList(t, "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestWriteListTableAligned(t *testing.T) {
	torrents := []torrent.Torrent{
		{ID: 1, Name: "ubuntu", Status: torrent.StatusDownloading, PercentDone: 0.5, TotalSize: 2048, RateDownload: 1024, RateUpload: 512, UploadRatio: 0.5, ETA: 60},
		{ID: 200, Name: "debian", Status: torrent.StatusSeeding, PercentDone: 1.0, TotalSize: 4096, RateUpload: 256, UploadRatio: 2.0, ETA: -1},
	}

	var buf bytes.Buffer
	// width 0 disables truncation so the full names show.
	writeListTable(&buf, torrents, 0)

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3, "header row plus one row per torrent")

	// Header row is present and ordered.
	assert.True(t, strings.HasPrefix(lines[0], "ID"))
	assert.Contains(t, lines[0], "Name")
	assert.Contains(t, lines[0], "ETA")

	// The ID column is right-aligned: the single-digit "1" is padded so it lines
	// up under the trailing "0" of "200".
	idCol := strings.Index(lines[0], "ID")
	assert.Equal(t, byte(' '), lines[1][idCol], "narrow ID is right-aligned (space before digit)")

	assert.Contains(t, out, "ubuntu")
	assert.Contains(t, out, "Downloading")
	assert.Contains(t, out, "Seeding")
}

func TestWriteListTableEmpty(t *testing.T) {
	var buf bytes.Buffer
	writeListTable(&buf, nil, 80)

	assert.Equal(t, "No torrents.", strings.TrimSpace(buf.String()))
}

func TestWriteListTableTruncatesName(t *testing.T) {
	torrents := []torrent.Torrent{
		{ID: 1, Name: "this-is-a-very-long-torrent-name-that-will-not-fit", Status: torrent.StatusSeeding},
	}

	var buf bytes.Buffer
	// A narrow width forces the Name column to truncate with an ellipsis.
	writeListTable(&buf, torrents, 40)

	out := buf.String()
	assert.Contains(t, out, "…", "long name is truncated with an ellipsis")
	assert.NotContains(t, out, "this-is-a-very-long-torrent-name-that-will-not-fit")
}
