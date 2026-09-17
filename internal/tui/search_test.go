package tui

import (
	"reflect"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// searchFor presses `/`, types a query, and leaves the field focused.
func searchFor(m Model, query string) Model {
	return typeText(press(m, "/"), query)
}

// TestSlashFocusesTheSearchInput checks `/` hands the keyboard to the field and
// draws it in the footer.
func TestSlashFocusesTheSearchInput(t *testing.T) {
	m := press(newTestModel(120, 40), "/")

	assert.True(t, m.inputFocused)
	assert.Equal(t, ownerSearch, m.owner)
	assert.True(t, m.torrents.searching)
	assert.Contains(t, ansi.Strip(m.render()), "/ ")

	typed := typeText(m, "q?")
	assert.Equal(t, "q?", typed.torrents.listView.search, "q and ? go to the field, not to quit and help")
}

// TestTypingFiltersTheRows checks each keystroke narrows the list to the
// matches rather than jumping a cursor through a full list.
func TestTypingFiltersTheRows(t *testing.T) {
	full := newTestModel(120, 40)
	require.Len(t, full.torrents.rows, 12)

	m := searchFor(full, "x86_64")

	assert.Equal(t, "x86_64", m.torrents.listView.search)
	assert.Equal(t, []int64{3, 5, 8, 9}, rowIDs(m.torrents.rows), "only the matches are left")

	for _, row := range m.torrents.rows {
		assert.Contains(t, strings.ToLower(row.Name), "x86_64")
	}
}

// TestSearchMatchingNothingEmptiesTheList checks a query nothing matches leaves
// no rows at all, rather than leaving the cursor parked.
func TestSearchMatchingNothingEmptiesTheList(t *testing.T) {
	m := searchFor(newTestModel(120, 40), "zzz")

	assert.Empty(t, m.torrents.rows)
	assert.Equal(t, "search: zzz (0)", m.torrents.searchStatus())
}

// TestSearchComposesWithTheSidebarFilter checks the sidebar narrows first and
// the search narrows what is left, the order `snatcher list` applies them in.
func TestSearchComposesWithTheSidebarFilter(t *testing.T) {
	filtered := completedFiltered(t, newTestModel(120, 40))
	seeding := rowIDs(filtered.torrents.rows)
	require.NotEmpty(t, seeding)

	m := searchFor(filtered, "gentoo")
	require.NotEmpty(t, m.torrents.rows, "the fixture has a seeding gentoo torrent")

	for _, row := range m.torrents.rows {
		assert.Contains(t, strings.ToLower(row.Name), "gentoo")
		assert.Contains(t, seeding, row.ID, "the search never reaches past the filter")
	}

	assert.Less(t, len(m.torrents.rows), len(seeding), "the search narrowed the filtered set")
}

// TestSearchKeepsTheCursorOnARow checks the cursor lands on a visible row when
// the search hides the one it was on.
func TestSearchKeepsTheCursorOnARow(t *testing.T) {
	m := searchFor(newTestModel(120, 40), "x86_64")

	row, ok := m.torrents.sel.row(m.torrents.rows)
	require.True(t, ok, "the cursor is on a row that is still shown")
	assert.Contains(t, strings.ToLower(row.Name), "x86_64")
	assert.Less(t, m.torrents.sel.cursor, len(m.torrents.rows), "the cursor is in range")
}

// TestEscBlursThenClearsSearch checks the first esc blurs the field keeping its
// text, and the second clears the search and widens the rows back.
func TestEscBlursThenClearsSearch(t *testing.T) {
	m := searchFor(newTestModel(120, 40), "deb")
	narrowed := len(m.torrents.rows)
	require.Less(t, narrowed, 12)

	m, rung := m.escape()
	require.Equal(t, "blur input", rung)
	assert.False(t, m.inputFocused)
	assert.Equal(t, ownerNone, m.owner)
	assert.Equal(t, "deb", m.torrents.listView.search)
	assert.Len(t, m.torrents.rows, narrowed, "blurring keeps the search in force")
	assert.Contains(t, m.torrents.footer(), "search: deb")

	m, rung = m.escape()
	require.Equal(t, "clear search", rung)
	assert.Empty(t, m.torrents.listView.search)
	assert.Len(t, m.torrents.rows, 12, "clearing the search widens the rows back")
	assert.Empty(t, m.torrents.footer())
	assert.Equal(t, newTestModel(120, 40).torrents.list.table.Height(), m.torrents.list.table.Height())
}

// TestSearchClearsMarks checks a search drops marks, as a filter change does: a
// mark the search has hidden would otherwise be acted on invisibly.
func TestSearchClearsMarks(t *testing.T) {
	marked := press(newTestModel(120, 40), "space")
	require.Equal(t, 1, marked.torrents.sel.markCount())

	m := press(searchFor(marked, "gentoo"), "enter")

	assert.Zero(t, m.torrents.sel.markCount())
}

// TestClearingTheSearchKeepsMarks checks marks made while the search was narrow
// survive widening. Clearing hides nothing, so the rule that drops marks does
// not apply, and the ladder's next rung is what clears them.
func TestClearingTheSearchKeepsMarks(t *testing.T) {
	m := press(press(searchFor(newTestModel(120, 40), "x86_64"), "enter"), "space")
	require.Equal(t, 1, m.torrents.sel.markCount())

	m, rung := m.escape()
	require.Equal(t, "clear search", rung)

	assert.Equal(t, 1, m.torrents.sel.markCount(), "marks survive the rows widening")
	assert.Len(t, m.torrents.rows, 12)

	m, rung = m.escape()
	require.Equal(t, "clear marks", rung)
	assert.Zero(t, m.torrents.sel.markCount())
}

// TestSearchStatusCounts checks the readout names how many rows the search left.
func TestSearchStatusCounts(t *testing.T) {
	m := press(searchFor(newTestModel(120, 40), "x86_64"), "enter")

	assert.Equal(t, "search: x86_64 (4)", m.torrents.searchStatus())

	none := press(searchFor(newTestModel(120, 40), "zzz"), "enter")
	assert.Equal(t, "search: zzz (0)", none.torrents.searchStatus())
}

// TestSearchEmptyPlaceholder checks the empty table says which of the filter and
// the search emptied it.
func TestSearchEmptyPlaceholder(t *testing.T) {
	m := press(searchFor(newTestModel(120, 40), "zzz"), "enter")
	require.Empty(t, m.torrents.rows)

	ph := m.listPlaceholder()
	require.Len(t, ph.lines, 2)
	assert.Equal(t, "no torrents match", ph.lines[0])
	assert.Contains(t, ph.lines[1], "search: zzz")

	both := press(searchFor(completedFiltered(t, newTestModel(120, 40)), "zzz"), "enter")
	assert.Contains(t, both.listPlaceholder().lines[1], "filter: ")
	assert.Contains(t, both.listPlaceholder().lines[1], "search: zzz")
}

// TestSlashAgainEditsTheSearch checks a second `/` starts from the search in
// force rather than an empty field.
func TestSlashAgainEditsTheSearch(t *testing.T) {
	m := press(searchFor(newTestModel(120, 40), "deb"), "enter", "/")

	assert.Equal(t, "deb", m.torrents.searchInput.Value())
}

// TestSettingsInputStillDiscardsOnBlur guards the ownership refactor: the
// settings input keeps its rule that esc discards the draft.
func TestSettingsInputStillDiscardsOnBlur(t *testing.T) {
	m := press(networkTab(settingsModel(120, 40)), "enter", "ctrl+u")
	require.Equal(t, ownerSettings, m.owner)

	m = press(typeText(m, "999"), "esc")

	assert.False(t, m.inputFocused)
	assert.Equal(t, ownerNone, m.owner)
	assert.Equal(t, modeNav, m.settings.mode)
	assert.Zero(t, m.settings.dirtyCount(), "the draft was discarded")
	assert.Empty(t, m.torrents.listView.search, "the settings input's keys never reached the search")
}

// TestSearchInputKeyMapHasNoBannedKeys checks the field's live keymap against
// the terminal constraints, as the registry's keys are.
func TestSearchInputKeyMapHasNoBannedKeys(t *testing.T) {
	v := reflect.ValueOf(newTestModel(120, 40).torrents.searchInput.KeyMap)

	for i := range v.NumField() {
		b, ok := v.Field(i).Interface().(key.Binding)
		if !ok {
			continue
		}

		for _, k := range b.Keys() {
			assert.Falsef(t, isBannedKey(k), "%s binds the banned key %q", v.Type().Field(i).Name, k)
		}
	}
}

// TestTorrentsSearchGolden pins the footer while searching: the field and the
// row count, beside a filter.
func TestTorrentsSearchGolden(t *testing.T) {
	m := completedFiltered(t, newTestModel(120, 40))

	assertGolden(t, "torrents_search", searchFor(m, "gentoo").render())
}
