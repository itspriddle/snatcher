package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// TestListViewFilterThenSort checks the view filters and then sorts, the order
// `snatcher list` applies them in.
func TestListViewFilterThenSort(t *testing.T) {
	v := defaultListView()
	v.filter = viewFilter{kind: filterByLabel, label: "linux"}
	v.sort = transmission.SortSize
	v.reverse = true

	assert.Equal(t, []int64{1, 3, 2}, rowIDs(v.apply(mockTorrents())))

	v.filter = viewFilter{kind: filterByStatus, status: transmission.FilterSeeding}
	v.sort, v.reverse = transmission.SortQueue, false

	assert.Equal(t, []int64{2, 7, 10}, rowIDs(v.apply(mockTorrents())), "seeding includes queued to seed")
}

// TestListPrefsFromConfig checks the stored sort, direction, and columns are
// what the window starts with, with column keys normalized as the CLI does and
// an unknown one skipped.
func TestListPrefsFromConfig(t *testing.T) {
	cfg := newFakeConfig().
		setEntry(clientKeyListSort, "size").
		setEntry(clientKeyListReverse, true).
		setEntry(clientKeyListColumns, []string{" Name", "bogus", "id"})

	v, err := listPrefs(cfg)
	require.NoError(t, err)

	assert.Equal(t, transmission.SortSize, v.sort)
	assert.True(t, v.reverse)
	assert.Equal(t, []string{"name", "id"}, v.columns)

	deps := testDeps()
	deps.Config = cfg

	m := New(deps).applyTheme(true).resize(120, 40)

	assert.Equal(t, []string{"name", "id"}, m.torrents.listView.columns)
	assert.Equal(t, int64(1), m.torrents.rows[0].ID, "the largest torrent leads")
	assert.Len(t, m.torrents.list.cols, len(prefixColumns)+2)
}

// TestListPrefsDefaults checks no store, and a store holding nothing usable,
// both mean the CLI's defaults.
func TestListPrefsDefaults(t *testing.T) {
	v, err := listPrefs(nil)
	require.NoError(t, err)
	assert.Equal(t, defaultListView(), v)

	v, err = listPrefs(newFakeConfig().setEntry(clientKeyListColumns, []string{"bogus"}).setEntry(clientKeyListSort, "nope"))
	require.NoError(t, err)
	assert.Equal(t, defaultListView(), v)
}

// TestListPrefsErrorToasts checks an unreadable store falls back to the defaults
// and says why.
func TestListPrefsErrorToasts(t *testing.T) {
	cfg := newFakeConfig()
	cfg.entriesErr = errors.New("invalid config value for client.list_sort")

	deps := testDeps()
	deps.Config = cfg

	m := New(deps)

	assert.Equal(t, "config: invalid config value for client.list_sort", m.toast)
	assert.Equal(t, defaultListView(), m.torrents.listView)
}

// TestUsedBytesIgnoresTheFilter checks a filter narrows the table and not the
// status bar's used space.
func TestUsedBytesIgnoresTheFilter(t *testing.T) {
	m := newTestModel(120, 40)
	before := m.usedBytes()

	v := m.torrents.listView
	v.filter = viewFilter{kind: filterByLabel, label: "linux"}
	m.torrents = m.torrents.setView(v, m.icons)

	require.Len(t, m.torrents.rows, 3)
	assert.Equal(t, before, m.usedBytes())
}

// TestCursorFollowsItsTorrentAcrossASort checks a re-sort moves the cursor with
// its torrent, and the table's cursor with the selection.
func TestCursorFollowsItsTorrentAcrossASort(t *testing.T) {
	m := press(newTestModel(120, 40), "j", "j")

	row, ok := m.torrents.sel.row(m.torrents.rows)
	require.True(t, ok)
	require.Equal(t, int64(3), row.ID)

	v := m.torrents.listView
	v.sort = transmission.SortName
	m.torrents = m.torrents.setView(v, m.icons)

	row, ok = m.torrents.sel.row(m.torrents.rows)
	require.True(t, ok)
	assert.Equal(t, int64(3), row.ID)
	assert.Equal(t, m.torrents.sel.cursor, m.torrents.list.cursor())
	assert.NotEqual(t, 2, m.torrents.sel.cursor, "the sort moved the torrent")
}

// TestColumnChangeRebuildsTheRows checks switching columns rebuilds every row
// with one cell per new column.
func TestColumnChangeRebuildsTheRows(t *testing.T) {
	m := newTestModel(120, 40)

	v := m.torrents.listView
	v.columns = []string{"name", "labels"}
	m.torrents = m.torrents.setView(v, m.icons)

	for _, row := range m.torrents.list.table.Rows() {
		assert.Len(t, row, len(prefixColumns)+2)
	}

	assert.Contains(t, m.render(), "Labels")
	assert.NotContains(t, m.render(), "Ratio")
}

// TestFilterPrunesHiddenMarksOnPoll checks a marked torrent that leaves the
// filter on a poll loses its mark, as a torrent that vanished does.
func TestFilterPrunesHiddenMarksOnPoll(t *testing.T) {
	stub, srv := newRPCStub(t)

	m := pollOnce(t, liveModel(t, srv, 120, 40))

	v := m.torrents.listView
	v.filter = viewFilter{kind: filterByLabel}
	m.torrents = m.torrents.setView(v, m.icons)

	require.Equal(t, []int64{2, 3}, rowIDs(m.torrents.rows))

	m = press(m, "space", "down", "space")
	require.Equal(t, 2, m.torrents.sel.markCount())

	stub.respond("torrent-get", strings.Replace(bespokeTorrentsResponse,
		`"queuePosition": 1, "labels": []`, `"queuePosition": 1, "labels": ["linux"]`, 1))

	m = pollOnce(t, m)

	assert.Equal(t, []int64{2}, rowIDs(m.torrents.rows))
	assert.Equal(t, 1, m.torrents.sel.markCount())
	assert.True(t, m.torrents.sel.isMarked(2))
}

// TestResetKeepsPreferences checks a server switch clears the filter and search
// but keeps the sort and columns, which are the user's rather than the server's.
func TestResetKeepsPreferences(t *testing.T) {
	m := newTestModel(120, 40)

	v := m.torrents.listView
	v.sort, v.reverse, v.columns = transmission.SortSize, true, []string{"name"}
	v.filter = viewFilter{kind: filterByLabel, label: "linux"}
	v.search = "deb"
	m.torrents = m.torrents.setView(v, m.icons)

	got := m.torrents.reset(m.icons).listView

	assert.Equal(t, transmission.SortSize, got.sort)
	assert.True(t, got.reverse)
	assert.Equal(t, []string{"name"}, got.columns)
	assert.Equal(t, viewFilter{}, got.filter)
	assert.Empty(t, got.search)
}
