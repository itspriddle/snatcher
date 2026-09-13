package tui

import (
	"net/http"
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

	_, srv := newRPCStub(t)
	failing, failingSrv := newRPCStub(t)
	failing.status.Store(http.StatusInternalServerError)

	live := pollOnce(t, pollOnce(t, liveModel(t, srv, w, h)))
	liveToast := live
	liveToast.toast = "saved 3 settings"

	return []frameState{
		{"live", live},
		{"live toast", liveToast},
		{"picker", press(withServers(live, pickerServers()), "S")},
		{"loading", liveModel(t, srv, w, h)},
		{"error", pollOnce(t, liveModel(t, failingSrv, w, h))},
		{"default", base},
		{"no filter", press(base, "{")},
		{"detail open", press(base, "}")},
		{"detail only", press(base, "{", "}")},
		{"detail focused", press(base, "enter")},
		{"marked", press(base, "space", "down", "space")},
		{"help", press(base, "?")},
		{"settings", press(base, ",")},
		{"settings loaded", networkTab(settingsModel(w, h))},
		{"settings editing", press(networkTab(settingsModel(w, h)), "enter")},
		{"settings select", press(networkTab(settingsModel(w, h)), "tab", "tab", "tab", "enter")},
		{"settings confirm", press(networkTab(settingsModel(w, h)), "tab", "enter", "esc")},
		{"sort", press(base, "o")},
		{"columns", press(base, "c")},
		{"columns bottom", press(base, "c", "j", "j", "j", "j", "j", "j", "j", "j", "j", "j", "j", "j", "j", "j")},
		{"filter", press(base, "f")},
		{"filtered", seedingFiltered(t, base)},
		{"filtered detail", seedingFiltered(t, press(base, "}"))},
		{"search", searchFor(base, "x86")},
		{"search blurred", press(searchFor(seedingFiltered(t, base), "x86"), "enter")},
		{"toast", press(base, "s")},
		{"chord", press(base, "y")},
	}
}

// TestTorrentsGolden pins the default frame at both reference sizes.
func TestTorrentsGolden(t *testing.T) {
	assertGolden(t, "torrents_120x40", newTestModel(120, 40).render())
	assertGolden(t, "torrents_80x24", newTestModel(80, 24).render())
}

// TestTorrentsLiveGolden pins a polled frame at both reference sizes: the rows in
// queue order, and a status bar with two samples in each sparkline, degraded to
// fit at 80 columns.
func TestTorrentsLiveGolden(t *testing.T) {
	_, srv := newRPCStub(t)

	assertGolden(t, "torrents_live_120x40", pollOnce(t, pollOnce(t, liveModel(t, srv, 120, 40))).render())
	assertGolden(t, "torrents_live_80x24", pollOnce(t, pollOnce(t, liveModel(t, srv, 80, 24))).render())
}

// TestPickerGolden pins the server picker over a live frame: the cursor marker,
// the current and default notes, and the picker's own help line.
func TestPickerGolden(t *testing.T) {
	_, srv := newRPCStub(t)

	m := withServers(pollOnce(t, liveModel(t, srv, 120, 40)), pickerServers())

	assertGolden(t, "picker", press(m, "S").render())
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

// TestSettingsNoServerGolden pins the settings window with no dialed client and
// no config store: read-only client rows, and no server rows to draw.
func TestSettingsNoServerGolden(t *testing.T) {
	assertGolden(t, "settings_noserver_120x40", press(newTestModel(120, 40), ",").render())
}

// TestSettingsLoadedGolden pins a loaded server tab at both reference sizes:
// the cursor marker, the read-only row, and the button row, with Network's
// thirteen rows fitting at 80x24.
func TestSettingsLoadedGolden(t *testing.T) {
	assertGolden(t, "settings_loaded_120x40", networkTab(settingsModel(120, 40)).render())
	assertGolden(t, "settings_loaded_80x24", networkTab(settingsModel(80, 24)).render())
}

// TestSettingsDirtyGolden pins the three places an unsaved change shows: the
// row's "*", the tab's "*", and the counts on Save and in the status bar.
func TestSettingsDirtyGolden(t *testing.T) {
	m := editField(networkTab(settingsModel(120, 40)), "31500")
	m = press(m, "tab", "tab", "tab", "down")

	assertGolden(t, "settings_dirty", m.render())
}

// TestSettingsEditingGolden pins a row with the text input in place of its value.
func TestSettingsEditingGolden(t *testing.T) {
	assertGolden(t, "settings_editing", press(networkTab(settingsModel(120, 40)), "enter").render())
}

// TestSettingsConfirmGolden pins the save prompt composited over the window.
func TestSettingsConfirmGolden(t *testing.T) {
	m := press(networkTab(settingsModel(120, 40)), "tab", "enter", "esc")

	assertGolden(t, "settings_confirm", m.render())
}

// TestSettingsSelectOpenGolden pins the inline dropdown, pushing the rows below
// it down.
func TestSettingsSelectOpenGolden(t *testing.T) {
	m := press(networkTab(settingsModel(120, 40)), "tab", "tab", "tab", "enter")

	assertGolden(t, "settings_select_open", m.render())
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
// so switching spellings must not be able to move a column.
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
// It asserts that two colors appear rather than which two. Naming them would
// pin one of the three palettes into a test about focus; which color each slot
// holds is theme_test.go's business.
func TestFocusedPaneIsStyledDifferently(t *testing.T) {
	m := newTestModel(120, 40)

	require.True(t, m.torrents.filterOpen)

	colors := borderColors(m.setFocus(paneList))

	require.Len(t, colors, 2, "the filter and list borders must both be colored")
	assert.NotEqual(t, colors[0], colors[1], "the focused pane's border is not distinguishable")

	assert.Len(t, borderColors(press(m, "{")), 1, "one pane means one border color")
}

// TestFocusedPaneGetsTheThickBorder pins the other half of decision 3: focus is
// a border shape, not only a border color, so it survives NO_COLOR, a
// sixteen-color terminal, a colorblind user — and the ANSI-stripped goldens,
// which record which pane has focus for the first time because of it.
func TestFocusedPaneGetsTheThickBorder(t *testing.T) {
	thick := lipgloss.ThickBorder().Top

	for _, pane := range []Pane{paneList, paneDetail} {
		m := press(newTestModel(120, 40), "}").setFocus(pane)

		assert.Containsf(t, m.render(), thick, "%s has focus and no thick border", pane)
	}

	unfocused := press(newTestModel(120, 40), "}").setFocus(paneFilter).render()

	assert.NotContains(t, unfocused, thick, "a thick border appeared with no ordinary pane focused")
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

// colorPattern matches an SGR sequence carrying a color parameter in any of the
// four spellings a terminal accepts: basic, bright, 256-color, and truecolor,
// in either the foreground or the background role.
//
// A truecolor-only pattern was enough while every theme was a placeholder hex
// palette. The default theme is now the terminal's own sixteen colors, which
// lipgloss writes as \x1b[34m — and merges with any attribute set alongside it,
// so the parameter can arrive with neighbors on either side.
//
// theme_test.go asserts the monochrome styles against the same pattern, which
// is the point of having one: "emits a color" must mean the same thing in the
// test that requires one and the test that forbids one.
var colorPattern = regexp.MustCompile(`\x1b\[(?:\d+;)*(?:[34][0-7]|9[0-7]|10[0-7]|[34]8;5;\d+|[34]8;2;\d+;\d+;\d+)(?:;\d+)*m`)

// borderColors returns the color escape sequences on the frame's top line, which
// is where each open pane's top border is drawn.
func borderColors(m Model) []string {
	return colorPattern.FindAllString(strings.Split(m.render(), "\n")[0], -1)
}
