package tui

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/theme"
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

// TestHighlightRowRedrawsOnlyTheCursorRow checks the cursor row loses its own
// SGR, keeps its markers, and is drawn in the style, while the header and the
// other rows are left alone.
func TestHighlightRowRedrawsOnlyTheCursorRow(t *testing.T) {
	style := lipgloss.NewStyle().Background(lipgloss.Color("#073642"))
	header := "\x1b[1mID\x1b[m " + rowMark(1)
	other := rowMark(0) + "\x1b[38;2;1;2;3ma\x1b[m"
	cursor := rowMark(1) + "\x1b[38;2;1;2;3mb\x1b[m " + rowMark(1) + "\x1b[32m+\x1b[39m"

	lines := highlightRow([]string{header, other, cursor}, 1, style)

	assert.Equal(t, header, lines[0], "the header is never a row")
	assert.Equal(t, other, lines[1])
	assert.Equal(t, style.Render(rowMark(1)+"b "+rowMark(1)+"+"), lines[2])

	i, ok := rowAt(strings.Join(lines, "\n"), 2)
	assert.True(t, ok, "the redrawn row keeps its marker")
	assert.Equal(t, 1, i)
}

// TestHighlightRowWithoutTheCursorRow checks lines are unchanged when the
// cursor row is not among them.
func TestHighlightRowWithoutTheCursorRow(t *testing.T) {
	style := lipgloss.NewStyle().Reverse(true)
	row := rowMark(0) + "\x1b[1ma\x1b[m"

	assert.Equal(t, []string{"header", row}, highlightRow([]string{"header", row}, 5, style))
	assert.Equal(t, []string{"header"}, highlightRow([]string{"header"}, 0, style))
}

// TestCursorRowBackgroundSpansTheRow is the regression for themes with a text
// color: the table wraps cells that each end in a full reset in its Selected
// style, which cleared the background after the first cell. The cursor row must
// be one styled span under every builtin theme, and the other rows keep the
// text color.
func TestCursorRowBackgroundSpansTheRow(t *testing.T) {
	for _, name := range themeNames {
		t.Run(name, func(t *testing.T) {
			deps := testDeps()
			deps.Theme = name
			deps.Themes = theme.Builtins().Lookup

			m := New(deps).applyTheme(true).resize(100, 20)
			view := m.torrents.list.view(m.styles, rect{w: 100, h: 8}, true, placeholder{}, "")

			var cursor, other string

			for _, line := range strings.Split(view, "\n") {
				switch {
				case strings.Contains(line, rowMark(0)):
					cursor = line
				case strings.Contains(line, rowMark(1)):
					other = line
				}
			}

			require.NotEmpty(t, cursor)
			require.NotEmpty(t, other)

			// Between the row's first and last cell there is no SGR at all.
			row := cursor[strings.Index(cursor, rowMark(0)):strings.LastIndex(cursor, rowMark(0))]
			assert.NotRegexp(t, sgrPattern, row)

			open := sgrOpen(m.styles.Table.Selected)
			require.NotEmpty(t, open)
			assert.Contains(t, cursor, open+rowMark(0), "the cursor style opens the row")

			if text := sgrOpen(m.styles.Table.Cell); text != "" {
				assert.Contains(t, other, text+rowMark(1), "other rows keep the text color")
			}
		})
	}
}

// sgrOpen is the sequence a style opens with, or "" when it sets nothing.
func sgrOpen(s lipgloss.Style) string {
	open, _, _ := strings.Cut(s.UnsetPaddingRight().Render("x"), "x")

	return open
}

// TestRenderEmitsNoMarkers checks no marker reaches the terminal, in every
// window and with an overlay composited over the rows.
func TestRenderEmitsNoMarkers(t *testing.T) {
	frames := map[string]Model{
		"torrents":      newTestModel(120, 40),
		"detail files":  staticFiles(),
		"under overlay": newTestModel(120, 40).push(torrentMenu(1)),
	}

	for name, m := range frames {
		assert.NotRegexp(t, rowMarkPattern, m.render(), name)
	}
}
