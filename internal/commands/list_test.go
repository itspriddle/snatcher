package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

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
			"labels": ["linux"],
			"trackers": [
				{"announce": "https://Tracker.Private.example:8443/SECRETPASSKEY/announce", "id": 0, "scrape": "", "sitename": "private", "tier": 0}
			]
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
	AddGlobalFlags(cmd)

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// listConfigPath stands up a transmission stub serving the given torrent-get
// payload on the bespoke path and writes a config pointing at it, with no client
// section — the shape of an operator's file before the section existed.
func listConfigPath(t *testing.T, body string) (string, func()) {
	t.Helper()

	return listConfigWithClient(t, body, "")
}

// listConfigWithClient is listConfigPath with a client section: clientJSON is
// the raw JSON body of the config's "client" object, or empty for no section at
// all. The fixture is raw JSON rather than config.Save output so a test can
// stage exactly the hand-edit it wants to describe, including one that is
// structurally valid but semantically nonsense.
func listConfigWithClient(t *testing.T, body, clientJSON string) (string, func()) {
	t.Helper()

	keyring.MockInit()

	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // bespoke path
		responses: map[string]string{
			"torrent-get": body,
		},
	}.handler())

	client := ""
	if clientJSON != "" {
		client = fmt.Sprintf(`, "client": %s`, clientJSON)
	}

	cfgPath := writeConfigFile(t, fmt.Sprintf(
		`{"servers": {"test": {"url": %q}}, "default_server": "test"%s}`, srv.URL, client))

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
	assert.Equal(t, "2.0 GB", first[4])
	assert.Equal(t, "1.5 MB/s", first[5])
	assert.Equal(t, "512.0 KB/s", first[6])
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

	// Pinned byte-for-byte: the header row and the data rows must land in one
	// grid (a regression guard for the stray leading tab that used to shift every
	// data row one column right of the header), numeric columns right-aligned
	// under their own right-aligned headers, text columns left-aligned, and no
	// line carrying a tail of trailing spaces.
	want := "" +
		" ID  Name    Status       Done    Size      Down       Up  Ratio  ETA\n" +
		"  1  ubuntu  Downloading   50%  2.0 KB  1.0 KB/s  512 B/s   0.50   1m\n" +
		"200  debian  Seeding      100%  4.0 KB     0 B/s  256 B/s   2.00    -\n"

	assert.Equal(t, want, buf.String())
}

// TestWriteListTableAlignsInteriorRightColumn covers a right-aligned column that
// is neither first nor last. That is the class of column whose alignment the
// previous tabwriter-tab trick silently dropped, so the leading-column golden
// above would not have caught it alone.
func TestWriteListTableAlignsInteriorRightColumn(t *testing.T) {
	torrents := []transmission.Torrent{
		{Name: "ubuntu", Status: transmission.StatusDownloading, PercentDone: 0.5},
		{Name: "debian", Status: transmission.StatusSeeding, PercentDone: 1.0},
	}

	cols, err := parseFields(listFields, defaultFieldKeys, "name,done,status")
	require.NoError(t, err)

	var buf bytes.Buffer
	writeListTable(&buf, torrents, cols, 0, "No torrents.")

	want := "" +
		"Name    Done  Status\n" +
		"ubuntu   50%  Downloading\n" +
		"debian  100%  Seeding\n"

	assert.Equal(t, want, buf.String())
}

// TestWriteListTableNoTrailingWhitespace guards the render loop's TrimRight: a
// short final cell must not leave the line padded out to the column width.
func TestWriteListTableNoTrailingWhitespace(t *testing.T) {
	torrents := []transmission.Torrent{
		{ID: 1, Name: "ubuntu", DownloadDir: "/downloads/complete"},
		{ID: 2, Name: "debian", DownloadDir: "/d"},
	}

	cols, err := parseFields(listFields, defaultFieldKeys, "id,name,download_dir")
	require.NoError(t, err)

	var buf bytes.Buffer
	writeListTable(&buf, torrents, cols, 0, "No torrents.")

	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		assert.Equal(t, strings.TrimRight(line, " "), line, "no line carries trailing spaces")
	}
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

// TestListFieldKeysCoverRegistry is list's copy of the registry drift guard
// US-008 added to show, files, peers, and trackers. The --fields help string and
// the default column order are built from the hand-written key slices while
// parseFields validates against the registry map, so a column added to
// listFields but forgotten in allFieldKeys is silently unselectable and every
// slice-iterating test still passes. list was the last registry without the
// guard, and its absence surfaced in the wrong place: detailFields promotes
// listFields, so the omission failed show_test.go's guard instead.
func TestListFieldKeysCoverRegistry(t *testing.T) {
	assert.Len(t, allFieldKeys, len(listFields), "the help names every registry key")
	assert.Subset(t, allFieldKeys, defaultFieldKeys,
		"the default column set is drawn from the help set")
}

// TestParseFieldsRejectsUnknownDefault pins the validation of the *caller's*
// defaults slice, not the user's --fields spec. Before it, a typo in a defaults
// slice produced a zero column[T] whose nil value accessor panicked in
// cellValue at render time; now it is an error naming the key.
func TestParseFieldsRejectsUnknownDefault(t *testing.T) {
	_, err := parseFields(listFields, []string{"id", "bogus"}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `default field "bogus" is not in the registry`)
	assert.Contains(t, err.Error(), "valid fields are", "the error lists the valid keys")
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

// TestWriteListTableFitsWidth checks what truncation is for: when the terminal
// has room for the fixed columns, every rendered line stays inside the width.
// (A terminal too narrow for the fixed columns alone deliberately overflows
// instead of hiding the name entirely — see nameColumnLimit's minName floor.)
func TestWriteListTableFitsWidth(t *testing.T) {
	torrents := []transmission.Torrent{
		{ID: 1, Name: "this-is-a-very-long-torrent-name-that-will-not-fit", Status: transmission.StatusSeeding},
		{ID: 2, Name: "short", Status: transmission.StatusDownloading},
	}

	cols, err := parseFields(listFields, defaultFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	writeListTable(&buf, torrents, cols, 80, "No torrents.")

	out := buf.String()
	assert.Contains(t, out, "…", "the long name is truncated")

	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		assert.LessOrEqual(t, cellDisplayWidth(line), 80, "rendered line fits the terminal width: %q", line)
	}
}

// TestNameColumnLimitBudgetsFixedColumns pins the width math the renderer shares
// with the table: the Name budget is what is left of the terminal after every
// other column's widest cell and the inter-column gutters.
func TestNameColumnLimitBudgetsFixedColumns(t *testing.T) {
	torrents := []transmission.Torrent{
		{ID: 1, Name: "this-is-a-very-long-torrent-name", Status: transmission.StatusSeeding},
	}

	cols, err := parseFields(listFields, defaultFieldKeys, "id,name,status")
	require.NoError(t, err)

	// "ID" (2) + "Seeding" (7) + two 2-space gutters = 13 fixed columns.
	assert.Equal(t, 40-13, nameColumnLimit(torrents, cols, 40))

	// A name that already fits is left alone, as is an unknown width.
	assert.Zero(t, nameColumnLimit(torrents, cols, 200))
	assert.Zero(t, nameColumnLimit(torrents, cols, 0))

	// A hopelessly narrow terminal still shows a sliver of the name.
	assert.Equal(t, 8, nameColumnLimit(torrents, cols, 10))
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

func TestListTrackerFiltersByHost(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	// --tracker matches the announce host case-insensitively, without its port.
	out, _, err := runList(t, "--config", cfgPath, "--tracker", "TRACKER.private.example")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 1, "only the matching torrent is listed")
	assert.Equal(t, "ubuntu-24.04.iso", strings.Split(lines[0], "\t")[defaultNameColumn(t)])

	none, _, err := runList(t, "--config", cfgPath, "--tracker", "nope.example")
	require.NoError(t, err)
	assert.NotContains(t, none, ".iso", "an unknown host matches nothing")
}

func TestListJSONCarriesTrackerHostsNotURLs(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	out, _, err := runList(t, "--config", cfgPath, "--json", "--tracker", "tracker.private.example")
	require.NoError(t, err)

	// The announce URL embeds a passkey; only its host may reach the output.
	assert.NotContains(t, out, "SECRETPASSKEY")
	assert.NotContains(t, out, "announce")

	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &rows))
	require.Len(t, rows, 1)
	assert.Equal(t, []any{map[string]any{"host": "tracker.private.example", "sitename": "private", "tier": float64(0)}}, rows[0]["trackers"])
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

// US-006: the stored client.list_* settings are what list renders when the
// matching flag is not passed, which is what makes the config load-bearing
// rather than write-only.

// listNames returns the name cell of every piped row, in output order.
func listNames(t *testing.T, out string) []string {
	t.Helper()

	nameCol := defaultNameColumn(t)

	rows := configRows(t, out)
	names := make([]string, len(rows))
	for i, cells := range rows {
		require.Greater(t, len(cells), nameCol)
		names[i] = cells[nameCol]
	}

	return names
}

func TestListUsesConfiguredColumns(t *testing.T) {
	// "ID" also pins the case-insensitive match a stored list gets: the same
	// spelling rule --fields follows, so a hand-edit is not held to a stricter
	// standard than the flag.
	cfgPath, closeSrv := listConfigWithClient(t, bespokeListResponse, `{"list_columns": ["ID", "name", "ratio"]}`)
	defer closeSrv()

	out, _, err := runList(t, "--config", cfgPath)
	require.NoError(t, err)

	rows := configRows(t, out)
	require.Len(t, rows, 2)
	require.Len(t, rows[0], 3, "the configured columns replace the built-in default set")
	assert.Equal(t, "1", rows[0][0])
	assert.Equal(t, "ubuntu-24.04.iso", rows[0][1])
	assert.Equal(t, "0.50", rows[0][2])
}

func TestListUsesConfiguredSortAndReverse(t *testing.T) {
	cfgPath, closeSrv := listConfigWithClient(t, bespokeListResponse, `{"list_sort": "name", "list_reverse": true}`)
	defer closeSrv()

	out, _, err := runList(t, "--config", cfgPath)
	require.NoError(t, err)

	// Descending by name, which is neither the queue order the server returns
	// nor the ascending order an unreversed sort would give.
	assert.Equal(t, []string{"ubuntu-24.04.iso", "debian.iso"}, listNames(t, out))
}

// TestListFlagsOutrankConfiguredDefaults pins the precedence, including the case
// the Changed check exists for: --sort queue and --reverse=false are the flags'
// own defaults, and passing them explicitly still overrides a stored setting.
func TestListFlagsOutrankConfiguredDefaults(t *testing.T) {
	cfgPath, closeSrv := listConfigWithClient(t, bespokeListResponse,
		`{"list_columns": ["id"], "list_sort": "name", "list_reverse": true}`)
	defer closeSrv()

	out, _, err := runList(t, "--config", cfgPath, "--fields", "name", "--sort", "queue", "--reverse=false")
	require.NoError(t, err)

	rows := configRows(t, out)
	require.Len(t, rows, 2)
	for _, cells := range rows {
		require.Len(t, cells, 1, "--fields outranks the configured columns")
	}

	assert.Equal(t, "ubuntu-24.04.iso", rows[0][0], "--sort queue outranks the configured sort field")
	assert.Equal(t, "debian.iso", rows[1][0], "--reverse=false outranks the configured direction")
}

// TestListRejectsInvalidStoredSetting checks a nonsense hand-edit is an error
// naming the config key, so the operator knows the file is what needs fixing —
// never a silent fall back to the default.
func TestListRejectsInvalidStoredSetting(t *testing.T) {
	tests := []struct {
		name   string
		client string
		want   []string
	}{
		{
			name:   "sort field",
			client: `{"list_sort": "bogus"}`,
			want:   []string{"client.list_sort", `invalid sort field "bogus"`, "valid sort fields are"},
		},
		{
			name:   "column",
			client: `{"list_columns": ["id", "bogus"]}`,
			want:   []string{"client.list_columns", `"bogus" is not a column`, "valid columns are"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, closeSrv := listConfigWithClient(t, bespokeListResponse, tc.client)
			defer closeSrv()

			_, _, err := runList(t, "--config", cfgPath)
			require.Error(t, err)
			for _, want := range tc.want {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

// TestListWithoutClientSettingsIsUnchanged pins the upgrade guarantee: a config
// with no client section, and one with an empty client object, both render
// exactly what list rendered before the section existed.
func TestListWithoutClientSettingsIsUnchanged(t *testing.T) {
	plainPath, closePlain := listConfigPath(t, bespokeListResponse)
	defer closePlain()

	plain, _, err := runList(t, "--config", plainPath)
	require.NoError(t, err)

	emptyPath, closeEmpty := listConfigWithClient(t, bespokeListResponse, `{}`)
	defer closeEmpty()

	empty, _, err := runList(t, "--config", emptyPath)
	require.NoError(t, err)

	assert.Equal(t, plain, empty, "an empty client section changes nothing")

	rows := configRows(t, plain)
	require.Len(t, rows, 2)
	require.Len(t, rows[0], len(defaultFieldKeys), "the built-in default column set still applies")
	assert.Equal(t, []string{"ubuntu-24.04.iso", "debian.iso"}, listNames(t, plain), "the default sort is still queue order")
}

// TestListConfiguredSortAppliesToStructuredOutput checks the stored defaults
// reach --json and --jq exactly as the flags do, so a scripted consumer and a
// human see the same list.
func TestListConfiguredSortAppliesToStructuredOutput(t *testing.T) {
	cfgPath, closeSrv := listConfigWithClient(t, bespokeListResponse, `{"list_sort": "name"}`)
	defer closeSrv()

	out, _, err := runList(t, "--config", cfgPath, "--json")
	require.NoError(t, err)

	var got []struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 2)
	assert.Equal(t, "debian.iso", got[0].Name, "the configured sort field orders JSON output too")
	assert.Equal(t, "ubuntu-24.04.iso", got[1].Name)

	out, _, err = runList(t, "--config", cfgPath, "--jq", ".[].name")
	require.NoError(t, err)
	assert.Equal(t, `"debian.iso"`+"\n"+`"ubuntu-24.04.iso"`, strings.TrimSpace(out))
}

// US-003 (M7): --top truncates the final rendered order, in every output mode.

func TestListTopTruncatesRows(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	out, _, err := runList(t, "--config", cfgPath, "--top", "1")
	require.NoError(t, err)
	assert.Equal(t, []string{"ubuntu-24.04.iso"}, listNames(t, out), "only the first row of the rendered order survives")
}

// TestListTopAtOrAboveResultSize pins the two boundaries: asking for exactly as
// many rows as exist, and asking for more than exist, both render everything
// rather than erroring.
func TestListTopAtOrAboveResultSize(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	want := []string{"ubuntu-24.04.iso", "debian.iso"}

	exact, _, err := runList(t, "--config", cfgPath, "--top", "2")
	require.NoError(t, err)
	assert.Equal(t, want, listNames(t, exact))

	over, _, err := runList(t, "--config", cfgPath, "--top", "99")
	require.NoError(t, err)
	assert.Equal(t, want, listNames(t, over))
}

func TestListTopOnEmptyResult(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeEmptyListResponse)
	defer closeSrv()

	out, _, err := runList(t, "--config", cfgPath, "--top", "5")
	require.NoError(t, err, "a --top larger than an empty result is not an error")
	assert.Empty(t, out)
}

// TestListTopComposesWithSortAndReverse is the reason truncation is applied
// last: "the 1 biggest torrent" must be the largest, not whichever torrent the
// server happened to return first.
func TestListTopComposesWithSortAndReverse(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	biggest, _, err := runList(t, "--config", cfgPath, "--sort", "size", "--reverse", "--top", "1")
	require.NoError(t, err)
	assert.Equal(t, []string{"ubuntu-24.04.iso"}, listNames(t, biggest), "the 2 GB torrent")

	smallest, _, err := runList(t, "--config", cfgPath, "--sort", "size", "--top", "1")
	require.NoError(t, err)
	assert.Equal(t, []string{"debian.iso"}, listNames(t, smallest), "the 1 GB torrent")
}

func TestListTopAppliesToStructuredOutput(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	out, _, err := runList(t, "--config", cfgPath, "--sort", "size", "--reverse", "--top", "1", "--json")
	require.NoError(t, err)

	var got []struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 1, "the JSON array is truncated too, not only the table")
	assert.Equal(t, "ubuntu-24.04.iso", got[0].Name)

	out, _, err = runList(t, "--config", cfgPath, "--sort", "size", "--reverse", "--top", "1", "--jq", ".[].name")
	require.NoError(t, err)
	assert.Equal(t, `"ubuntu-24.04.iso"`, strings.TrimSpace(out))
}

func TestListWithoutTopIsUnlimited(t *testing.T) {
	cfgPath, closeSrv := listConfigPath(t, bespokeListResponse)
	defer closeSrv()

	out, _, err := runList(t, "--config", cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"ubuntu-24.04.iso", "debian.iso"}, listNames(t, out), "an absent --top truncates nothing")
}

// TestListTopBelowOneIsRejectedBeforeDial points --config at an empty config
// file, so reaching dial would fail with "not configured". Getting the --top
// error instead is what proves the validation runs before the server is dialed.
func TestListTopBelowOneIsRejectedBeforeDial(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runList(t, "--config", cfgPath, "--top", "0")
	require.EqualError(t, err, "invalid --top 0: must be at least 1")

	_, _, err = runList(t, "--config", cfgPath, "--top", "-3")
	require.EqualError(t, err, "invalid --top -3: must be at least 1")
}
