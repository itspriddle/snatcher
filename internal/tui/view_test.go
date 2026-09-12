package tui

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRenderFillsTheTerminalExactly is the invariant every other view test rests
// on: the frame is exactly h lines of exactly w cells.
//
// A frame one cell too wide wraps and pushes the status bar off the bottom, and
// one line too short leaves the previous frame's last row on screen. Both are
// invisible in a golden — the golden would simply record the wrong shape — so
// they are asserted as geometry, at every size the tests draw at.
func TestRenderFillsTheTerminalExactly(t *testing.T) {
	sizes := []struct{ w, h int }{{120, 40}, {80, 24}, {60, 16}, {200, 60}}

	for _, size := range sizes {
		for _, state := range frameStates(t, size.w, size.h) {
			t.Run(state.name, func(t *testing.T) {
				lines := strings.Split(state.model.render(), "\n")

				require.Lenf(t, lines, size.h, "%dx%d: wrong line count", size.w, size.h)

				for i, line := range lines {
					assert.Equalf(t, size.w, lipgloss.Width(line), "%dx%d: line %d is the wrong width", size.w, size.h, i)
				}
			})
		}
	}
}

// frameState is one named model state a frame is rendered from.
type frameState struct {
	name  string
	model Model
}

// frameStates are the shapes the frame can take, so the fill invariant is
// checked against every pane combination rather than only the default one.
func frameStates(t *testing.T, w, h int) []frameState {
	t.Helper()

	base := newTestModel(w, h)

	return []frameState{
		{"default", base},
		{"no filter", press(base, "{")},
		{"detail open", press(base, "}")},
		{"detail only", press(base, "{", "}")},
		{"detail focused", press(base, "enter")},
		{"marked", press(base, "space", "down", "space")},
		{"help", press(base, "?")},
		{"settings", press(base, ",")},
		{"toast", press(base, "s")},
		{"chord", press(base, "y")},
	}
}

// TestTorrentsGolden pins the default frame at both reference sizes.
func TestTorrentsGolden(t *testing.T) {
	assertGolden(t, "torrents_120x40", newTestModel(120, 40).render())
	assertGolden(t, "torrents_80x24", newTestModel(80, 24).render())
}

// TestTorrentsFilterFocusedGolden pins the sidebar's focused rendering, whose
// loud border is a safety feature rather than decoration.
func TestTorrentsFilterFocusedGolden(t *testing.T) {
	assertGolden(t, "torrents_filter_focused", newTestModel(120, 40).setFocus(paneFilter).render())
}

// TestTorrentsDetailOpenGolden pins the three-pane layout with the detail pane
// focused on its overview tab.
func TestTorrentsDetailOpenGolden(t *testing.T) {
	assertGolden(t, "torrents_detail_open", press(newTestModel(120, 40), "enter").render())
}

// TestTorrentsMarkedGolden pins the mark glyphs and the status bar's count.
func TestTorrentsMarkedGolden(t *testing.T) {
	assertGolden(t, "torrents_marked", press(newTestModel(120, 40), "space", "down", "space", "down").render())
}

// TestSettingsGolden pins the mock settings window and its tab strip.
func TestSettingsGolden(t *testing.T) {
	assertGolden(t, "settings", press(newTestModel(120, 40), ",").render())
}

// TestHelpOverlayGolden pins the overlay composited over the frame.
func TestHelpOverlayGolden(t *testing.T) {
	assertGolden(t, "help_overlay", press(newTestModel(120, 40), "?").render())
}

// TestTooSmallGolden pins what a terminal below the floor shows.
func TestTooSmallGolden(t *testing.T) {
	assertGolden(t, "too_small_40x10", newTestModel(40, 10).render())
}

// TestNerdFontGolden is the one golden rendered with the Nerd Font glyph set. It
// exists to pin the glyph column's width: a private-use glyph measures one cell,
// so T2's fallback work must not be able to move a column.
func TestNerdFontGolden(t *testing.T) {
	deps := testDeps()
	deps.NerdFont = true

	m := New(deps).applyTheme(true).resize(120, 40)

	assertGolden(t, "torrents_nerd_120x40", m.render())
}

// TestHostileNamesAreSanitized checks the untrusted-string rule on the path it
// matters most: a torrent name is server-derived, and an escape sequence in one
// must not reach the terminal.
func TestHostileNamesAreSanitized(t *testing.T) {
	m := newTestModel(120, 40)

	require.Contains(t, m.torrents.rows[11].Name, "\x1b", "the fixture must carry an escape sequence")

	frame := m.render()

	assert.Contains(t, ansi.Strip(frame), "hostile")
	assert.NotContains(t, frame, "\x1b[31m", "the torrent name's escape sequence reached the frame")
	assert.NotContains(t, frame, "\x07", "the torrent name's bell reached the frame")
}

// TestFocusedPaneIsStyledDifferently is the narrow style assertion the
// ANSI-stripped goldens deliberately cannot make: the focused pane's border is
// drawn in a different color from its neighbour's.
//
// It asserts that two colors appear rather than which two. Naming the colors
// would pin the palette, and filling the palette in is T2's job.
func TestFocusedPaneIsStyledDifferently(t *testing.T) {
	m := newTestModel(120, 40)

	require.True(t, m.torrents.filterOpen)

	colors := borderColors(m.setFocus(paneList))

	require.Len(t, colors, 2, "the filter and list borders must both be colored")
	assert.NotEqual(t, colors[0], colors[1], "the focused pane's border is not distinguishable")

	assert.Len(t, borderColors(press(m, "{")), 1, "one pane means one border color")
}

// TestOnlyTheFilterPaneGetsTheLoudBorder pins vision.md's blast-radius styling:
// the loud style appears when the filter pane has focus and never otherwise,
// because the filter pane's group actions act on every torrent in a group.
func TestOnlyTheFilterPaneGetsTheLoudBorder(t *testing.T) {
	loud := lipgloss.DoubleBorder().Top

	assert.Contains(t, newTestModel(120, 40).setFocus(paneFilter).render(), loud)

	for _, pane := range []Pane{paneList, paneDetail} {
		frame := press(newTestModel(120, 40), "}").setFocus(pane).render()

		assert.NotContainsf(t, frame, loud, "the loud border appeared with %s focused", pane)
	}
}

// colorPattern matches a truecolor foreground escape sequence.
var colorPattern = regexp.MustCompile(`\x1b\[38;2;\d+;\d+;\d+m`)

// borderColors returns the color escape sequences on the frame's top line, which
// is where each open pane's top border is drawn.
func borderColors(m Model) []string {
	return colorPattern.FindAllString(strings.Split(m.render(), "\n")[0], -1)
}
