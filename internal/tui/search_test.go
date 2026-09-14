package tui

import (
	"reflect"
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

// cursorID is the ID of the torrent under the cursor.
func cursorID(t *testing.T, m Model) int64 {
	t.Helper()

	row, ok := m.torrents.sel.row(m.torrents.rows)
	require.True(t, ok, "the cursor is on no row")

	return row.ID
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

// TestTypingJumpsToTheFirstMatch checks each keystroke jumps the cursor to the
// first match, and hides nothing.
func TestTypingJumpsToTheFirstMatch(t *testing.T) {
	m := searchFor(newTestModel(120, 40), "gentoo")

	assert.Equal(t, int64(10), cursorID(t, m))
	assert.Equal(t, "gentoo", m.torrents.listView.search)
	assert.Len(t, m.torrents.rows, 12)

	m = typeText(searchFor(newTestModel(120, 40), "zzz"), "")
	assert.Equal(t, int64(1), cursorID(t, m), "no match leaves the cursor")
}

// TestNextAndPrevWrap checks n and N walk the matches from the cursor and wrap
// at both ends.
func TestNextAndPrevWrap(t *testing.T) {
	m := press(searchFor(newTestModel(120, 40), "x86_64"), "enter")
	require.False(t, m.inputFocused)
	require.Equal(t, int64(3), cursorID(t, m))

	var visited []int64

	for range 4 {
		m = press(m, "n")
		visited = append(visited, cursorID(t, m))
	}

	assert.Equal(t, []int64{5, 8, 9, 3}, visited)
	assert.Equal(t, int64(9), cursorID(t, press(m, "N")), "N wraps backwards")
}

// TestEscBlursThenClearsSearch checks the first esc blurs the field keeping its
// text, and the second clears the search.
func TestEscBlursThenClearsSearch(t *testing.T) {
	m := searchFor(newTestModel(120, 40), "deb")

	m, rung := m.escape()
	require.Equal(t, "blur input", rung)
	assert.False(t, m.inputFocused)
	assert.Equal(t, ownerNone, m.owner)
	assert.Equal(t, "deb", m.torrents.listView.search)
	assert.Contains(t, m.torrents.footer(), "search: deb")

	m, rung = m.escape()
	require.Equal(t, "clear search", rung)
	assert.Empty(t, m.torrents.listView.search)
	assert.Empty(t, m.torrents.footer())
	assert.Equal(t, newTestModel(120, 40).torrents.list.table.Height(), m.torrents.list.table.Height())
}

// TestSearchKeepsMarks checks a search leaves marks alone, unlike a filter.
func TestSearchKeepsMarks(t *testing.T) {
	m := press(searchFor(press(newTestModel(120, 40), "space"), "gentoo"), "enter")

	assert.Equal(t, 1, m.torrents.sel.markCount())
	assert.Equal(t, int64(10), cursorID(t, m))
}

// TestSearchStatusCounts checks the readout's place among the matches, 0 off a
// match, and "(0)" when nothing matches.
func TestSearchStatusCounts(t *testing.T) {
	m := press(searchFor(newTestModel(120, 40), "x86_64"), "enter", "n")

	assert.Equal(t, "search: x86_64 (2/4)", m.torrents.searchStatus())
	assert.Equal(t, "search: x86_64 (0/4)", press(m, "g").torrents.searchStatus())

	none := press(searchFor(newTestModel(120, 40), "zzz"), "enter")
	assert.Equal(t, "search: zzz (0)", none.torrents.searchStatus())
}

// TestSearchStepWithoutAMatch checks n says why it did nothing.
func TestSearchStepWithoutAMatch(t *testing.T) {
	assert.Equal(t, "no search", press(newTestModel(120, 40), "n").toast)
	assert.Equal(t, "no match for zzz", press(searchFor(newTestModel(120, 40), "zzz"), "enter", "N").toast)
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
// match count, beside a filter.
func TestTorrentsSearchGolden(t *testing.T) {
	m := seedingFiltered(t, newTestModel(120, 40))

	assertGolden(t, "torrents_search", searchFor(m, "gentoo").render())
}
