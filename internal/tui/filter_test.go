package tui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// filterItemNamed is the sidebar row with a label, failing the test when there
// is none.
func filterItemNamed(t *testing.T, m Model, label string) filterItem {
	t.Helper()

	for _, item := range m.torrents.filter.items {
		if item.selectable() && item.label == label {
			return item
		}
	}

	t.Fatalf("no sidebar row %q", label)

	return filterItem{}
}

// seedingFiltered applies the sidebar's Seeding row.
func seedingFiltered(t *testing.T, m Model) Model {
	t.Helper()

	return m.applyFilterItem(filterItemNamed(t, m, "Seeding"))
}

// TestSidebarEnterFiltersRowsAndClearsMarks checks enter in the sidebar hides
// the rows the filter excludes, clears marks, and marks the row applied.
func TestSidebarEnterFiltersRowsAndClearsMarks(t *testing.T) {
	m := press(newTestModel(120, 40), "space").setFocus(paneFilter)
	require.Equal(t, 1, m.torrents.sel.markCount())

	m = press(m, "j", "j", "enter")

	assert.Equal(t, []int64{2, 7, 10}, rowIDs(m.torrents.rows))
	assert.Zero(t, m.torrents.sel.markCount())
	assert.Equal(t, "filter: Seeding", m.toast)
	assert.Equal(t, m.torrents.filter.cursor, m.torrents.filter.applied)
	assert.Len(t, m.torrents.all, 12, "the polled set is kept whole")
}

// TestFilterModalMatchesTheSidebar checks f lists the sidebar's rows and applies
// the same filter, with the sidebar closed.
func TestFilterModalMatchesTheSidebar(t *testing.T) {
	m := press(newTestModel(120, 40), "{", "f")

	o, ok := topAs[filterPickOverlay](m)
	require.True(t, ok)
	assert.Equal(t, m.torrents.filter.items, o.pane.items)

	m = press(m, "j", "j", "enter")

	assert.False(t, m.hasOverlay(filterPickKind))
	assert.Equal(t, []int64{2, 7, 10}, rowIDs(m.torrents.rows))

	reopened, _ := topAs[filterPickOverlay](press(m, "f"))
	assert.Equal(t, m.torrents.filter.applied, reopened.pane.cursor, "the modal opens on the filter in force")
	assert.Contains(t, ansi.Strip(press(m, "f").render()), "Seeding  (current)")

	assert.False(t, press(m, "f", "f").hasOverlay(filterPickKind))
	assert.False(t, press(m, "f", "esc").hasOverlay(filterPickKind))
}

// TestFilterModalLeavesTheSidebarCursor checks applying from the modal does not
// move the sidebar's cursor.
func TestFilterModalLeavesTheSidebarCursor(t *testing.T) {
	m := newTestModel(120, 40)
	before := m.torrents.filter.cursor

	m = press(m, "f", "j", "j", "enter")

	assert.Equal(t, before, m.torrents.filter.cursor)
	assert.NotEqual(t, before, m.torrents.filter.applied)
}

// TestLabelFilterMatchesTheRawLabel checks a label row filters by the label the
// server sent, not its sanitized display, and that the display is sanitized.
func TestLabelFilterMatchesTheRawLabel(t *testing.T) {
	deps := testDeps()
	deps.Torrents = []transmission.Torrent{
		{ID: 1, Name: "one", Labels: []string{"tag\x1b[31m"}},
		{ID: 2, Name: "two", Labels: []string{"tag"}},
	}

	m := New(deps).applyTheme(true).resize(120, 40)

	var hostile filterItem

	for _, item := range m.torrents.filter.items {
		if item.labelRow && item.raw == "tag\x1b[31m" {
			hostile = item
		}
	}

	require.True(t, hostile.labelRow, "the hostile label has a row")
	assert.NotContains(t, hostile.label, "\x1b", "its display is sanitized")

	filtered := m.applyFilterItem(hostile)
	assert.Equal(t, []int64{1}, rowIDs(filtered.torrents.rows))
	assert.NotContains(t, filtered.render(), "\x1b[31m")

	assert.Equal(t, []int64{2}, rowIDs(m.applyFilterItem(filterItemNamed(t, m, "tag")).torrents.rows))
}

// TestNoLabelRow checks the catch-all row keeps exactly the unlabeled torrents.
func TestNoLabelRow(t *testing.T) {
	m := newTestModel(120, 40)
	item := filterItemNamed(t, m, noLabel)

	m = m.applyFilterItem(item)

	require.Len(t, m.torrents.rows, item.count)

	for _, row := range m.torrents.rows {
		assert.Emptyf(t, row.Labels, "torrent %d has labels", row.ID)
	}

	assert.Equal(t, "filter: no label", m.toast)
}

// TestFilterAllClears checks the All row removes the filter and the footer.
func TestFilterAllClears(t *testing.T) {
	m := seedingFiltered(t, newTestModel(120, 40))
	require.Len(t, m.torrents.rows, 3)

	m = m.applyFilterItem(filterItemNamed(t, m, "All"))

	assert.False(t, m.torrents.listView.filter.set())
	assert.Len(t, m.torrents.rows, 12)
	assert.Equal(t, "filter cleared", m.toast)
	assert.Empty(t, m.torrents.footer())
	assert.Equal(t, newTestModel(120, 40).torrents.list.table.Height(), m.torrents.list.table.Height(),
		"the table takes the footer's row back")
}

// TestFilterFooterShowsTheLabel checks the list pane's footer names the filter,
// and costs the table one row.
func TestFilterFooterShowsTheLabel(t *testing.T) {
	base := newTestModel(120, 40)
	m := base.applyFilterItem(filterItemNamed(t, base, "linux"))

	assert.Contains(t, ansi.Strip(m.render()), "filter: label linux")
	assert.Equal(t, base.torrents.list.table.Height()-1, m.torrents.list.table.Height())
}

// TestFilteredEmptyPlaceholder checks a filter that matches nothing says so,
// rather than claiming the server has no torrents.
func TestFilteredEmptyPlaceholder(t *testing.T) {
	deps := testDeps()
	deps.Torrents = []transmission.Torrent{{ID: 1, Name: "one", Status: transmission.StatusSeeding}}

	base := New(deps).applyTheme(true).resize(120, 40)
	m := base.applyFilterItem(filterItemNamed(t, base, "Downloading"))

	frame := ansi.Strip(m.render())

	assert.Contains(t, frame, "no torrents match")
	assert.Contains(t, frame, "filter: Downloading")
}

// TestTorrentsFilteredGolden pins a filtered list: the narrowed rows, the
// sidebar's applied marker, the counts still taken from every torrent, and the
// footer.
func TestTorrentsFilteredGolden(t *testing.T) {
	assertGolden(t, "torrents_filtered", seedingFiltered(t, newTestModel(120, 40)).render())
}

// TestFilterOverlayGolden pins the filter modal.
func TestFilterOverlayGolden(t *testing.T) {
	assertGolden(t, "filter_overlay", press(newTestModel(120, 40), "f", "j").render())
}
