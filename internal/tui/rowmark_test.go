package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRowMarkIsZeroWidth checks the marker costs no cells, so a marked row
// measures and pads exactly as an unmarked one.
func TestRowMarkIsZeroWidth(t *testing.T) {
	assert.Zero(t, ansi.StringWidth(rowMark(1234)))
	assert.Equal(t, 3, ansi.StringWidth(rowMark(7)+"abc"))
	assert.Equal(t, "abc", ansi.Strip(rowMark(7)+"abc"))
}

// TestRowAtRecoversEveryVisibleRow renders the torrent table through setRows
// and View, scrolled past its height, and reads every visible line back to the
// torrent drawn on it — the case geometry alone cannot answer.
func TestRowAtRecoversEveryVisibleRow(t *testing.T) {
	m := newTestModel(60, 16)
	list := m.torrents.list

	for range 11 {
		list.table.MoveDown(1)
	}

	lines := strings.Split(list.table.View(), "\n")
	require.Greater(t, len(m.torrents.rows), list.table.Height(), "the table must scroll for this test to mean anything")

	seen := map[int]bool{}

	for line := 1; line < len(lines); line++ {
		i, ok := rowAt(list.table.View(), line)
		if !ok {
			continue
		}

		seen[i] = true

		// The ID column is the first field after the two glyph cells.
		fields := strings.Fields(ansi.Strip(lines[line]))
		require.GreaterOrEqual(t, len(fields), 2)
		assert.Equal(t, strconv.FormatInt(m.torrents.rows[i].ID, 10), fields[1], "line %d names row %d", line, i)
	}

	assert.True(t, seen[11], "the cursor row is visible and marked")
	assert.False(t, seen[0], "the first row has scrolled out of view")
}

// TestRowAtIgnoresTheHeader checks the header line and lines past the table are
// not rows.
func TestRowAtIgnoresTheHeader(t *testing.T) {
	view := newTestModel(120, 40).torrents.list.table.View()

	_, ok := rowAt(view, 0)
	assert.False(t, ok)

	_, ok = rowAt(view, 1000)
	assert.False(t, ok)

	i, ok := rowAt(view, 1)
	assert.True(t, ok)
	assert.Zero(t, i)
}

// TestMarkersSurviveAHiddenFirstColumn checks a row stays readable when its
// first cells are not drawn: every cell carries the marker.
func TestMarkersSurviveAHiddenFirstColumn(t *testing.T) {
	m := newTestModel(120, 40)

	cols := m.torrents.list.table.Columns()
	cols[markColumn].Width, cols[glyphColumn].Width = 0, 0
	m.torrents.list.table.SetColumns(cols)

	i, ok := rowAt(m.torrents.list.table.View(), 2)
	assert.True(t, ok)
	assert.Equal(t, 1, i)
}

// TestStripMarkersLeavesStylesAlone checks stripping removes markers and only
// markers.
func TestStripMarkersLeavesStylesAlone(t *testing.T) {
	styled := "\x1b[1;90m" + rowMark(3) + "name\x1b[m " + rowMark(12) + "\x1b[2Jx"

	assert.Equal(t, "\x1b[1;90mname\x1b[m \x1b[2Jx", stripMarkers(styled))
	assert.Equal(t, "plain", stripMarkers("plain"))
}

// TestRenderEmitsNoMarkers checks no marker reaches the terminal, in every
// window and with an overlay composited over the rows.
func TestRenderEmitsNoMarkers(t *testing.T) {
	_, results := searchModel(t, 120, 40)

	frames := map[string]Model{
		"torrents":      newTestModel(120, 40),
		"detail files":  staticFiles(),
		"under overlay": newTestModel(120, 40).push(torrentMenu(1)),
		"prowlarr":      results,
	}

	for name, m := range frames {
		assert.NotRegexp(t, rowMarkPattern, m.render(), name)
	}
}
