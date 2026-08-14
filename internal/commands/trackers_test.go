package commands

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// bespokeTrackersResponse is a torrent-get payload whose first torrent carries a
// camelCase trackerStats array (the bespoke wire spelling: announceState,
// seederCount, leecherCount, downloadCount, lastAnnounceResult,
// nextAnnounceTime; tier is spelled "tier" in both schemes) and whose second
// torrent has no trackers at all — the empty-collection path.
//
// The second tracker reports -1 for every scrape count, the sentinel
// Transmission uses for "not scraped yet", and a next announce time of 0, which
// output.Date already renders as "-".
const bespokeTrackersResponse = `{
	"arguments": {"torrents": [
		{
			"id": 1,
			"hashString": "` + infoHash1 + `",
			"name": "ubuntu-24.04.iso",
			"status": 4,
			"trackerStats": [
				{
					"announce": "https://tracker.example.com:443/announce",
					"host": "https://tracker.example.com:443",
					"sitename": "example",
					"tier": 0,
					"announceState": 1,
					"seederCount": 42,
					"leecherCount": 7,
					"downloadCount": 1234,
					"lastAnnounceResult": "Success",
					"nextAnnounceTime": 1700000000
				},
				{
					"announce": "udp://backup.example.org:6969/announce",
					"host": "udp://backup.example.org:6969",
					"sitename": "backup",
					"tier": 1,
					"announceState": 0,
					"seederCount": -1,
					"leecherCount": -1,
					"downloadCount": -1,
					"lastAnnounceResult": "",
					"nextAnnounceTime": 0
				}
			]
		},
		{
			"id": 2,
			"hashString": "` + infoHash2 + `",
			"name": "debian.iso",
			"status": 6,
			"trackerStats": []
		}
	]},
	"result": "success",
	"tag": 1
}`

// bespokeTrackersSanitizeResponse is a torrent-get payload whose announce URL,
// host, sitename, and last announce result all carry terminal control sequences.
// Announce URLs come from the torrent's own metadata, the host and sitename are
// derived from them by the server, and the announce result is text the tracker
// chose — none is authored by the operator.
//
// Every column trackerFields marks sanitize is represented here, so removing any
// one of those flags fails TestTrackersSanitizesHumanOutputNotJSON rather than
// passing unnoticed against a clean fixture value.
const bespokeTrackersSanitizeResponse = `{
	"arguments": {"torrents": [
		{
			"id": 1,
			"hashString": "` + infoHash1 + `",
			"name": "ubuntu-24.04.iso",
			"status": 4,
			"trackerStats": [
				{
					"announce": "https://\u001b[2Jevil\u0007.example.com/announce",
					"host": "https://\u001b[2Jevilhost\u0007.example.com",
					"sitename": "\u001b[31mevilsite\u0007",
					"tier": 0,
					"announceState": 3,
					"seederCount": 1,
					"leecherCount": 0,
					"downloadCount": 0,
					"lastAnnounceResult": "\u001b[31mtracker gone\u0007",
					"nextAnnounceTime": 1700000000
				}
			]
		}
	]},
	"result": "success",
	"tag": 1
}`

// runTrackers executes the trackers command with args, returning stdout, stderr,
// and the Execute error. The command's output is a *bytes.Buffer, so isTTY
// reports false and the piped (tab-separated) plaintext contract is exercised.
func runTrackers(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewTrackersCmd()
	AddGlobalFlags(cmd)

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

func TestTrackersPipedTabSeparated(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeTrackersResponse)
	defer closeSrv()

	out, _, err := runTrackers(t, "--config", cfgPath, "1")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2, "piped output is header-less, one row per tracker")

	first := strings.Split(lines[0], "\t")
	require.Len(t, first, len(trackerFieldKeys))
	assert.Equal(t, "https://tracker.example.com:443/announce", first[0])
	assert.Equal(t, "0", first[1], "tier is decoded (US-006) and rendered")
	assert.Equal(t, "waiting", first[2], "announceState 1 is named")
	assert.Equal(t, output.Date(1700000000), first[3], "rendered in the local zone, like every other date column")
	assert.Equal(t, "42", first[4])
	assert.Equal(t, "7", first[5])
	assert.Equal(t, "1234", first[6])

	second := strings.Split(lines[1], "\t")
	assert.Equal(t, "udp://backup.example.org:6969/announce", second[0])
	assert.Equal(t, "1", second[1])
	assert.Equal(t, "inactive", second[2], "announceState 0 is named")
	assert.Equal(t, "-", second[3], "a zero next announce time renders as -")
	assert.Equal(t, "-", second[4], "an unknown seeder count renders as -")
	assert.Equal(t, "-", second[5], "an unknown leecher count renders as -")
	assert.Equal(t, "-", second[6], "an unknown download count renders as -")
}

func TestTrackersResolvesByHash(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeTrackersResponse)
	defer closeSrv()

	// An uppercase hash resolves case-insensitively, via the same resolveTorrent
	// path show, files, and peers use.
	out, _, err := runTrackers(t, "--config", cfgPath, strings.ToUpper(infoHash1))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "https://tracker.example.com:443/announce", strings.Split(lines[0], "\t")[0])
}

func TestTrackersJSON(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeTrackersResponse)
	defer closeSrv()

	out, _, err := runTrackers(t, "--config", cfgPath, "1", "--json")
	require.NoError(t, err)

	var got []struct {
		Announce           string `json:"announce"`
		Host               string `json:"host"`
		Sitename           string `json:"sitename"`
		Tier               int    `json:"tier"`
		AnnounceState      int    `json:"announce_state"`
		SeederCount        int    `json:"seeder_count"`
		LeecherCount       int    `json:"leecher_count"`
		DownloadCount      int    `json:"download_count"`
		LastAnnounceResult string `json:"last_announce_result"`
		NextAnnounceTime   int64  `json:"next_announce_time"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 2)

	assert.Equal(t, "https://tracker.example.com:443/announce", got[0].Announce)
	assert.Equal(t, "https://tracker.example.com:443", got[0].Host)
	assert.Equal(t, "example", got[0].Sitename)
	assert.Equal(t, 0, got[0].Tier)
	assert.Equal(t, 1, got[0].AnnounceState, "JSON keeps the numeric state, not the name")
	assert.Equal(t, 42, got[0].SeederCount)
	assert.Equal(t, 7, got[0].LeecherCount)
	assert.Equal(t, 1234, got[0].DownloadCount)
	assert.Equal(t, "Success", got[0].LastAnnounceResult)
	assert.Equal(t, int64(1700000000), got[0].NextAnnounceTime)

	assert.Equal(t, 1, got[1].Tier, "tier is present in --json because US-006 decoded it")
	assert.Equal(t, -1, got[1].SeederCount, "an unknown count stays -1 in JSON")
	assert.Equal(t, -1, got[1].LeecherCount)
	assert.Equal(t, -1, got[1].DownloadCount)
}

func TestTrackersJSONEmptyIsArrayNotNull(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeTrackersResponse)
	defer closeSrv()

	out, _, err := runTrackers(t, "--config", cfgPath, "2", "--json")
	require.NoError(t, err)
	assert.Equal(t, "[]", strings.TrimSpace(out))
}

func TestTrackersJQ(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeTrackersResponse)
	defer closeSrv()

	out, _, err := runTrackers(t, "--config", cfgPath, "1", "--jq", ".[].tier")
	require.NoError(t, err)

	assert.Equal(t, "0\n1", strings.TrimSpace(out))
}

func TestTrackersPipedEmptyWritesNothing(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeTrackersResponse)
	defer closeSrv()

	out, _, err := runTrackers(t, "--config", cfgPath, "2")
	require.NoError(t, err)
	assert.Empty(t, out, "piped output for a torrent with no trackers is empty")
}

// TestTrackersTableEmpty covers the TTY branch of the empty case, which isTTY
// makes unreachable through the command with a *bytes.Buffer: the table writer
// prints the trackers command's own "No trackers." line.
func TestTrackersTableEmpty(t *testing.T) {
	cols, err := parseFields(trackerFields, trackerFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	writeListTable(&buf, []transmission.Tracker(nil), cols, 80, "No trackers.")

	assert.Equal(t, "No trackers.", strings.TrimSpace(buf.String()))
}

// TestTrackersTableAligned exercises the TTY table branch directly, since isTTY
// needs a real terminal: the header row is present and the rows follow it.
func TestTrackersTableAligned(t *testing.T) {
	trackers := []transmission.Tracker{
		{Announce: "https://tracker.example.com/announce", Tier: 0, AnnounceState: 3, SeederCount: 42, LeecherCount: 7, DownloadCount: 1234},
		{Announce: "udp://backup.example.org:6969/announce", Tier: 1, AnnounceState: 0, SeederCount: -1, LeecherCount: -1, DownloadCount: -1},
	}

	cols, err := parseFields(trackerFields, trackerFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	// width 0 disables truncation so the full announce URLs show.
	writeListTable(&buf, trackers, cols, 0, "No trackers.")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3, "header row plus one row per tracker")

	assert.True(t, strings.HasPrefix(lines[0], "Announce"))
	assert.Contains(t, lines[0], "Tier")
	assert.Contains(t, lines[0], "Next Announce")
	assert.Contains(t, lines[0], "Downloads")
	assert.Contains(t, lines[1], "https://tracker.example.com/announce")
	assert.Contains(t, lines[1], "active")
	assert.Contains(t, lines[2], "udp://backup.example.org:6969/announce")
	assert.Contains(t, lines[2], "inactive")
}

// TestTrackersSanitizesHumanOutputNotJSON asserts the sanitize boundary on both
// plaintext paths (piped and TTY table) while leaving the structured --json
// contract byte-for-byte intact. An announce URL comes from the torrent's
// metadata, the host and sitename are derived from it, and the last announce
// result is text the tracker chose; none is authored by the operator.
//
// Every sanitized column is selected on both paths, so a dropped sanitize flag
// fails here instead of hiding behind a column the run never renders.
func TestTrackersSanitizesHumanOutputNotJSON(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeTrackersSanitizeResponse)
	defer closeSrv()

	// Piped plaintext, end to end through the command, with every sanitized column
	// selected: announce is a default, host, sitename, and last_result are opt-in.
	piped, _, err := runTrackers(t, "--config", cfgPath, "1", "--fields", "announce,host,sitename,last_result")
	require.NoError(t, err)
	assert.NotContains(t, piped, "\x1b", "raw ESC must not reach the piped output")
	assert.NotContains(t, piped, "\x07", "raw BEL must not reach the piped output")
	assert.Contains(t, piped, "https://[2Jevil.example.com/announce", "visible announce text survives sanitization")
	assert.Contains(t, piped, "https://[2Jevilhost.example.com", "visible host text survives sanitization")
	assert.Contains(t, piped, "[31mevilsite", "visible sitename text survives sanitization")
	assert.Contains(t, piped, "[31mtracker gone", "visible last-result text survives sanitization")

	// TTY table path, exercised directly.
	cols, err := parseFields(trackerFields, allTrackerFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	writeListTable(&buf, []transmission.Tracker{{
		Announce:           "https://\x1b[2Jevil\x07.example.com/announce",
		Host:               "https://\x1b[2Jevilhost\x07.example.com",
		Sitename:           "\x1b[31mevilsite\x07",
		LastAnnounceResult: "\x1b[31mtracker gone\x07",
	}}, cols, 0, "No trackers.")

	table := buf.String()
	assert.NotContains(t, table, "\x1b", "raw ESC must not reach the table output")
	assert.NotContains(t, table, "\x07", "raw BEL must not reach the table output")
	assert.Contains(t, table, "https://[2Jevil.example.com/announce")
	assert.Contains(t, table, "https://[2Jevilhost.example.com")
	assert.Contains(t, table, "[31mevilsite")
	assert.Contains(t, table, "[31mtracker gone")

	// JSON is a structured contract and is never sanitized.
	raw, _, err := runTrackers(t, "--config", cfgPath, "1", "--json")
	require.NoError(t, err)

	var got []struct {
		Announce           string `json:"announce"`
		Host               string `json:"host"`
		Sitename           string `json:"sitename"`
		LastAnnounceResult string `json:"last_announce_result"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &got))
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Announce, "\x1b", "JSON announce keeps the raw ESC (unsanitized)")
	assert.Contains(t, got[0].Announce, "\x07", "JSON announce keeps the raw BEL")
	assert.Contains(t, got[0].Host, "\x1b", "JSON host keeps the raw ESC")
	assert.Contains(t, got[0].Host, "\x07", "JSON host keeps the raw BEL")
	assert.Contains(t, got[0].Sitename, "\x1b", "JSON sitename keeps the raw ESC")
	assert.Contains(t, got[0].Sitename, "\x07", "JSON sitename keeps the raw BEL")
	assert.Contains(t, got[0].LastAnnounceResult, "\x1b", "JSON last_announce_result keeps the raw ESC")
	assert.Contains(t, got[0].LastAnnounceResult, "\x07", "JSON last_announce_result keeps the raw BEL")
}

func TestTrackersFieldsSelectsAndOrdersColumns(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeTrackersResponse)
	defer closeSrv()

	out, _, err := runTrackers(t, "--config", cfgPath, "1", "--fields", "Sitename, host ,last_result")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2)

	first := strings.Split(lines[0], "\t")
	require.Len(t, first, 3, "only the three selected columns are rendered")
	assert.Equal(t, "example", first[0], "sitename is first")
	assert.Equal(t, "https://tracker.example.com:443", first[1], "host is second")
	assert.Equal(t, "Success", first[2], "last_result is third")
}

// TestTrackersOptInFieldsAreNotDefault asserts host, sitename, and last_result
// are selectable but absent from the default table.
func TestTrackersOptInFieldsAreNotDefault(t *testing.T) {
	for _, key := range []string{"host", "sitename", "last_result"} {
		assert.NotContains(t, trackerFieldKeys, key, "%s is opt-in, not a default column", key)
		assert.Contains(t, allTrackerFieldKeys, key, "%s is named in the --fields help", key)
		_, err := parseFields(trackerFields, trackerFieldKeys, key)
		require.NoErrorf(t, err, "trackers accepts its opt-in key %q", key)
	}

	assert.Len(t, allTrackerFieldKeys, len(trackerFields), "the help names every registry key")
}

// TestTrackersFieldsRegistryIsTrackersOnly asserts the per-command registry
// boundary: an unknown key errors listing only the trackers command's own keys,
// and a key that is valid for another command (list/show/files/peers) is not
// silently accepted here.
func TestTrackersFieldsRegistryIsTrackersOnly(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeTrackersResponse)
	defer closeSrv()

	_, _, err := runTrackers(t, "--config", cfgPath, "1", "--fields", "announce,client")
	require.Error(t, err)
	assert.Equal(t, `invalid field "client": valid fields are `+validFieldKeys(trackerFields), err.Error(),
		"the error lists only the trackers command's own keys")

	for _, key := range allTrackerFieldKeys {
		_, err := parseFields(trackerFields, trackerFieldKeys, key)
		require.NoErrorf(t, err, "trackers accepts its own key %q", key)
	}
}

func TestTrackersWrongArgCount(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeTrackersResponse)
	defer closeSrv()

	t.Run("zero args", func(t *testing.T) {
		_, _, err := runTrackers(t, "--config", cfgPath)
		require.Error(t, err, "a bare invocation with no selector is an error, not a no-op")
	})

	t.Run("two args", func(t *testing.T) {
		_, _, err := runTrackers(t, "--config", cfgPath, "1", "2")
		require.Error(t, err, "more than one positional arg is a usage error")
	})
}

func TestTrackersInvalidSelector(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeTrackersResponse)
	defer closeSrv()

	_, _, err := runTrackers(t, "--config", cfgPath, "not-an-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid torrent selector")
}

func TestTrackersNotFoundErrors(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeTrackersResponse)
	defer closeSrv()

	absent := "cccccccccccccccccccccccccccccccccccccccc"
	out, _, err := runTrackers(t, "--config", cfgPath, absent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no torrent matched: "+absent)
	assert.Empty(t, out, "no output is written when nothing matched")
}

func TestTrackersNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runTrackers(t, "--config", cfgPath, "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// TestTrackersRegisteredOnTorrentGroup asserts the command is reachable at its
// real path, "snatcher torrent trackers", not just through its constructor.
func TestTrackersRegisteredOnTorrentGroup(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeTrackersResponse)
	defer closeSrv()

	out, errOut, err := runTorrent(t, "trackers", "1", "--config", cfgPath)
	require.NoError(t, err)
	assert.Empty(t, errOut, "nothing is written to stderr")

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "https://tracker.example.com:443/announce", strings.Split(lines[0], "\t")[0])
}

// TestTrackerCountUnknown covers the -1 sentinel directly, including the
// defensive negative case, since the wire contract only promises -1.
func TestTrackerCountUnknown(t *testing.T) {
	assert.Equal(t, "0", trackerCount(0))
	assert.Equal(t, "42", trackerCount(42))
	assert.Equal(t, "-", trackerCount(-1), "-1 is Transmission's not-yet-scraped sentinel")
	assert.Equal(t, "-", trackerCount(-2), "any negative count is treated as unknown")
}
