package tui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestColumnsOverlayListsTheShownColumnsFirst checks c opens with the shown
// columns checked and in order, then the rest in registry order, and that c and
// esc both close it.
func TestColumnsOverlayListsTheShownColumnsFirst(t *testing.T) {
	m := press(newTestModel(120, 40), "c")

	o, ok := topAs[columnsOverlay](m)
	require.True(t, ok)
	require.Len(t, o.rows, len(allColumnKeys))

	assert.Equal(t, defaultColumnKeys, o.selected())
	assert.Equal(t, columnRow{key: "hash"}, o.rows[len(defaultColumnKeys)])

	frame := ansi.Strip(m.render())
	assert.Contains(t, frame, "> [x] id")
	assert.Contains(t, frame, "[ ] hash")

	assert.False(t, press(m, "c").hasOverlay(columnsOverlayKind))
	assert.False(t, press(m, "esc").hasOverlay(columnsOverlayKind))
}

// TestColumnsOverlayReordersAndPersists checks K moves a column up, space shows
// a hidden one, and enter installs and writes the ordered set.
func TestColumnsOverlayReordersAndPersists(t *testing.T) {
	m, cfg := prefsModel(t)

	m = press(m, "c", "j", "K")

	for range allColumnKeys {
		m = press(m, "j")
	}

	m = press(m, "space", "enter")

	want := []string{"name", "id", "status", "done", "size", "down", "up", "ratio", "eta", "labels"}

	assert.False(t, m.hasOverlay(columnsOverlayKind))
	assert.Equal(t, want, m.torrents.listView.columns)
	assert.Equal(t, want, cfg.value(clientKeyListColumns))
	assert.Equal(t, []string{clientKeyListColumns}, cfg.sets)
	assert.Equal(t, "columns: name, id, status, done, size, down, up, ratio, eta, labels", m.toast)
	assert.Contains(t, ansi.Strip(m.render()), "Labels")
}

// TestColumnsOverlayMovesStopAtTheEnds checks J and K do nothing past the ends.
func TestColumnsOverlayMovesStopAtTheEnds(t *testing.T) {
	o, _ := topAs[columnsOverlay](press(newTestModel(120, 40), "c", "K"))

	assert.Zero(t, o.cursor)
	assert.Equal(t, defaultColumnKeys, o.selected())

	o = o.move(len(o.rows)).swap(1)
	assert.Equal(t, len(o.rows)-1, o.cursor)
}

// TestColumnsOverlayRefusesEmpty checks an empty selection is refused and the
// modal stays open with what the user had.
func TestColumnsOverlayRefusesEmpty(t *testing.T) {
	m := press(newTestModel(120, 40), "c")

	for range defaultColumnKeys {
		m = press(m, "space", "j")
	}

	m = press(m, "enter")

	assert.True(t, m.hasOverlay(columnsOverlayKind))
	assert.Equal(t, "pick at least one column", m.toast)
	assert.Equal(t, defaultColumnKeys, m.torrents.listView.columns)
}

// TestColumnsOverlayScrollsOnAShortTerminal checks the fifteen rows scroll with
// the cursor rather than overflowing a short frame.
func TestColumnsOverlayScrollsOnAShortTerminal(t *testing.T) {
	m := press(newTestModel(60, 16), "c")

	for range allColumnKeys {
		m = press(m, "j")
	}

	frame := ansi.Strip(m.render())

	assert.Contains(t, frame, "> [ ] labels")
	assert.NotContains(t, frame, "[x] id", "the top rows scrolled away")
}

// TestColumnsOverlayGolden pins the modal: checked and unchecked rows, the
// cursor marker, and the help line.
func TestColumnsOverlayGolden(t *testing.T) {
	assertGolden(t, "columns_overlay", press(newTestModel(120, 40), "c", "j", "space").render())
}
