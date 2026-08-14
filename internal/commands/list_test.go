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
	"github.com/itspriddle/snatcher/internal/transmission"
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
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

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
	torrents := []transmission.Torrent{
		{ID: 1, Name: "ubuntu", Status: transmission.StatusDownloading, PercentDone: 0.5, TotalSize: 2048, RateDownload: 1024, RateUpload: 512, UploadRatio: 0.5, ETA: 60},
		{ID: 200, Name: "debian", Status: transmission.StatusSeeding, PercentDone: 1.0, TotalSize: 4096, RateUpload: 256, UploadRatio: 2.0, ETA: -1},
	}

	cols, err := parseFields(listFields, defaultFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	// width 0 disables truncation so the full names show.
	writeListTable(&buf, torrents, cols, 0, "No torrents.")

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
	cols, err := parseFields(listFields, defaultFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	writeListTable(&buf, nil, cols, 80, "No torrents.")

	assert.Equal(t, "No torrents.", strings.TrimSpace(buf.String()))
}

func TestWriteListTableSanitizesControlChars(t *testing.T) {
	torrents := []transmission.Torrent{
		{ID: 1, Name: "\x1b[2Jmalicious", Status: transmission.StatusDownloading},
	}

	cols, err := parseFields(listFields, defaultFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	// width 0 disables truncation so the full (sanitized) name shows.
	writeListTable(&buf, torrents, cols, 0, "No torrents.")

	out := buf.String()
	assert.NotContains(t, out, "\x1b", "raw ESC must not reach the table output")
	assert.Contains(t, out, "[2Jmalicious", "visible text survives sanitization")
}

func TestWriteListPipedSanitizesControlChars(t *testing.T) {
	torrents := []transmission.Torrent{
		{
			ID:          1,
			Name:        "\x1b[2Jmalicious",
			DownloadDir: "/d\x1bir",
			Error:       "boom\x07",
			Labels:      []string{"li\x1bnux"},
			Status:      transmission.StatusDownloading,
		},
	}

	cols, err := parseFields(listFields, defaultFieldKeys, "name,download_dir,error,labels")
	require.NoError(t, err)

	var buf bytes.Buffer
	writeListPiped(&buf, torrents, cols)

	out := buf.String()
	assert.NotContains(t, out, "\x1b", "raw ESC must not reach the piped output")
	assert.NotContains(t, out, "\x07", "raw BEL must not reach the piped output")
	assert.Contains(t, out, "[2Jmalicious")
	assert.Contains(t, out, "/dir")
	assert.Contains(t, out, "boom")
	assert.Contains(t, out, "linux")
}

func TestWriteListPipedLeavesOrdinaryTextIntact(t *testing.T) {
	torrents := []transmission.Torrent{
		{
			ID:     1,
			Name:   "Debian ISO",
			Labels: []string{"linux", "iso"},
			Status: transmission.StatusSeeding,
		},
	}

	cols, err := parseFields(listFields, defaultFieldKeys, "name,labels")
	require.NoError(t, err)

	var buf bytes.Buffer
	writeListPiped(&buf, torrents, cols)

	assert.Equal(t, "Debian ISO\tlinux,iso\n", buf.String())
}

func TestListFieldsSelectsAndOrdersColumns(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	// Order is honored (hash before id) and only the requested columns appear.
	out, _, err := runList(t, "--config", cfgPath, "--fields", "hash,id,name")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2, "piped output is header-less, one row per torrent")

	first := strings.Split(lines[0], "\t")
	require.Len(t, first, 3, "only the three selected columns are rendered")
	assert.Equal(t, "aaa111", first[0], "hash is first")
	assert.Equal(t, "1", first[1], "id is second")
	assert.Equal(t, "ubuntu-24.04.iso", first[2], "name is third")
}

func TestListFieldsComposesWithFilterAndSort(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	// --fields composes with --filter and --sort: only the seeding torrent,
	// rendered with the selected columns.
	out, _, err := runList(t, "--config", cfgPath, "--filter", "seeding", "--sort", "name", "--fields", "id,labels")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 1, "only the seeding torrent matches")

	cells := strings.Split(lines[0], "\t")
	require.Len(t, cells, 2)
	assert.Equal(t, "2", cells[0])
}

func TestListFieldsInvalidKeyErrors(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	_, _, err := runList(t, "--config", cfgPath, "--fields", "id,bogus,name")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid field "bogus"`)
	assert.Contains(t, err.Error(), "valid fields are", "the error lists the valid keys")
	assert.Contains(t, err.Error(), "download_dir", "the valid-keys list includes registry keys")
}

func TestParseFieldsDefault(t *testing.T) {
	cols, err := parseFields(listFields, defaultFieldKeys, "")
	require.NoError(t, err)
	require.Len(t, cols, len(defaultFieldKeys))

	assert.Equal(t, "ID", cols[0].header)
	assert.Equal(t, "Name", cols[1].header)
	assert.Equal(t, "ETA", cols[len(cols)-1].header)
}

func TestParseFieldsTrimsAndLowercases(t *testing.T) {
	cols, err := parseFields(listFields, defaultFieldKeys, " ID , Name ")
	require.NoError(t, err)
	require.Len(t, cols, 2)
	assert.Equal(t, "ID", cols[0].header)
	assert.Equal(t, "Name", cols[1].header)
}

func TestWriteListTableTruncatesName(t *testing.T) {
	torrents := []transmission.Torrent{
		{ID: 1, Name: "this-is-a-very-long-torrent-name-that-will-not-fit", Status: transmission.StatusSeeding},
	}

	cols, err := parseFields(listFields, defaultFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	// A narrow width forces the Name column to truncate with an ellipsis.
	writeListTable(&buf, torrents, cols, 40, "No torrents.")

	out := buf.String()
	assert.Contains(t, out, "…", "long name is truncated with an ellipsis")
	assert.NotContains(t, out, "this-is-a-very-long-torrent-name-that-will-not-fit")
}

// US-007: command-level coverage for --search, --reverse, and the
// "transforms apply to every output mode (including --json/--jq)" contract from
// US-004, plus invalid-flag errors and a guard that --filter's status vocabulary
// stays aligned with Status.Slug(). These exercise the flag wiring end-to-end
// through NewListCmd (via runList), which the field/render/transform unit tests
// above do not.

// nameField returns the index of the "name" cell in the default piped column
// order (the column index of "name" in defaultFieldKeys).
func defaultNameColumn(t *testing.T) int {
	t.Helper()

	for i, key := range defaultFieldKeys {
		if key == "name" {
			return i
		}
	}
	require.Fail(t, "name is not in defaultFieldKeys")

	return -1
}

func TestListSearchFiltersByName(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	// --search narrows to the single torrent whose name contains the text
	// (case-insensitively), exercised end-to-end through the command.
	out, _, err := runList(t, "--config", cfgPath, "--search", "ubuntu")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 1, "only the matching torrent is listed")

	nameCol := defaultNameColumn(t)
	cells := strings.Split(lines[0], "\t")
	assert.Equal(t, "ubuntu-24.04.iso", cells[nameCol])
	assert.NotContains(t, out, "debian.iso", "non-matching torrent is excluded")
}

func TestListReverseFlipsSortOrder(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	nameCol := defaultNameColumn(t)

	// Ascending by name: debian.iso before ubuntu-24.04.iso.
	asc, _, err := runList(t, "--config", cfgPath, "--sort", "name")
	require.NoError(t, err)
	ascLines := strings.Split(strings.TrimRight(asc, "\n"), "\n")
	require.Len(t, ascLines, 2)
	assert.Equal(t, "debian.iso", strings.Split(ascLines[0], "\t")[nameCol])
	assert.Equal(t, "ubuntu-24.04.iso", strings.Split(ascLines[1], "\t")[nameCol])

	// --reverse flips the order: ubuntu-24.04.iso before debian.iso.
	rev, _, err := runList(t, "--config", cfgPath, "--sort", "name", "--reverse")
	require.NoError(t, err)
	revLines := strings.Split(strings.TrimRight(rev, "\n"), "\n")
	require.Len(t, revLines, 2)
	assert.Equal(t, "ubuntu-24.04.iso", strings.Split(revLines[0], "\t")[nameCol])
	assert.Equal(t, "debian.iso", strings.Split(revLines[1], "\t")[nameCol])
}

func TestListFilterAppliesToJSON(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	// The status filter must apply to structured (--json) output, not only the
	// plaintext branch: --filter seeding yields exactly the seeding transmission.
	out, _, err := runList(t, "--config", cfgPath, "--filter", "seeding", "--json")
	require.NoError(t, err)

	var got []struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 1, "only the seeding torrent survives the filter in JSON output")
	assert.Equal(t, int64(2), got[0].ID)
	assert.Equal(t, "debian.iso", got[0].Name)
	assert.Equal(t, "seeding", got[0].Status, "the status slug is the seeding torrent's")
}

func TestListSearchAppliesToJQ(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	// --search must apply to --jq output too: only the matching name is emitted.
	out, _, err := runList(t, "--config", cfgPath, "--search", "debian", "--jq", ".[].name")
	require.NoError(t, err)

	assert.Equal(t, `"debian.iso"`, strings.TrimSpace(out))
}

func TestListInvalidFilterErrors(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	_, _, err := runList(t, "--config", cfgPath, "--filter", "bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid filter "bogus"`)
	assert.Contains(t, err.Error(), "valid filters are", "the error lists the valid filters")
	assert.Contains(t, err.Error(), "downloading", "the valid-filters list includes the status filters")
}

func TestListInvalidSortErrors(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	_, _, err := runList(t, "--config", cfgPath, "--sort", "bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid sort field "bogus"`)
	assert.Contains(t, err.Error(), "valid sort fields are", "the error lists the valid sort fields")
	assert.Contains(t, err.Error(), "name", "the valid-sort-fields list includes the sort fields")
}

// TestListFilterVocabularyMatchesStatusSlug guards the PRD's single-source-of-
// truth design: every Status.Slug() must be an accepted --filter value, so the
// --filter vocabulary cannot silently drift from the status slugs used by the
// JSON contract (e.g. `--jq 'select(.status=="seeding")'` aligning with
// `--filter seeding`).
func TestListFilterVocabularyMatchesStatusSlug(t *testing.T) {
	statuses := []transmission.Status{
		transmission.StatusStopped,
		transmission.StatusQueuedToVerify,
		transmission.StatusVerifying,
		transmission.StatusQueuedToDownload,
		transmission.StatusDownloading,
		transmission.StatusQueuedToSeed,
		transmission.StatusSeeding,
		transmission.StatusError,
	}

	for _, s := range statuses {
		slug := s.Slug()
		_, err := transmission.ParseFilter(slug)
		assert.NoErrorf(t, err, "status slug %q must be an accepted --filter value", slug)
	}
}
