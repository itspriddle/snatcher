package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helpText renders the help overlay's content for the model's current focus.
func helpText(t *testing.T, m Model) string {
	t.Helper()

	require.True(t, m.hasOverlay(helpOverlayKind), "the help overlay is not open")

	top, _ := m.topOverlay()

	return ansi.Strip(top.view(m, m.width, m.height))
}

// TestHelpDescribesTheFocusedPane checks help lists the active context's keys —
// the T1 done-condition. The filter pane's help says "apply filter" and does not
// say "start", because the torrent verbs are not bound there.
func TestHelpDescribesTheFocusedPane(t *testing.T) {
	m := press(newTestModel(120, 40).setFocus(paneFilter), "?")

	text := helpText(t, m)

	assert.Contains(t, text, "apply filter")
	assert.Contains(t, text, "start group")
	assert.NotContains(t, text, "open detail")
}

// TestHelpForTheListPaneListsTheVerbs is the same check from the other side.
func TestHelpForTheListPaneListsTheVerbs(t *testing.T) {
	text := helpText(t, press(newTestModel(120, 40), "?"))

	assert.Contains(t, text, "open detail")
	assert.Contains(t, text, "action menu")
	assert.NotContains(t, text, "apply filter")
}

// TestHelpListsEveryBindingInTheStackExactlyOnce checks the overlay's content
// is a complete, non-repeating view of what is live. It is the reason the keymap
// is data: a hand-written cheat sheet drifts, and this cannot.
func TestHelpListsEveryBindingInTheStackExactlyOnce(t *testing.T) {
	m := press(newTestModel(200, 80), "?")

	rows := map[string]int{}
	for _, b := range m.helpBlocks() {
		for _, r := range b.rows {
			rows[r.key+"\x00"+r.desc]++
		}
	}

	for _, ctx := range m.stackBeneathHelp() {
		for _, b := range contextBindings(ctx) {
			help := b.Help()

			assert.Equalf(t, 1, rows[help.Key+"\x00"+help.Desc], "%q / %q is not listed exactly once", help.Key, help.Desc)
		}
	}
}

// TestHelpLayoutDropsNothing checks every row survives the column flow, at
// widths that give one column, two, and more. A layout that fit by truncating
// is exactly what bubbles/help does and what this overlay exists to avoid.
func TestHelpLayoutDropsNothing(t *testing.T) {
	m := newTestModel(200, 80)
	blocks := m.helpBlocks()

	for _, width := range []int{40, 80, 120, 200} {
		layout := layoutHelp(blocks, width, m.styles)
		rows := helpLayoutRows(layout)

		for _, b := range blocks {
			for _, r := range b.rows {
				assert.Equalf(t, 1, rows[r.key+"\x00"+r.desc], "width %d: %q / %q is not listed exactly once", width, r.key, r.desc)
			}
		}
	}
}

// helpLayoutRows counts the key/description pairs in a layout, cutting each line
// at the column boundaries first so one column's row is never read as part of
// another's.
func helpLayoutRows(layout helpLayout) map[string]int {
	var cells []string

	stride := layout.colWidth + helpColumnGap

	for _, line := range layout.lines {
		line = ansi.Strip(line)
		for i := range layout.columns {
			cells = append(cells, ansi.Cut(line, i*stride, i*stride+layout.colWidth))
		}
	}

	return helpRows(strings.Join(cells, "\n"))
}

// TestHelpUsesColumnsWhenWide checks a wide terminal gets more than one column,
// and that the columns make the help shorter.
func TestHelpUsesColumnsWhenWide(t *testing.T) {
	m := newTestModel(200, 80)
	blocks := m.helpBlocks()

	wide := layoutHelp(blocks, 200, m.styles)
	narrow := layoutHelp(blocks, 40, m.styles)

	assert.Greater(t, wide.columns, 1)
	assert.Equal(t, 1, narrow.columns)
	assert.Less(t, len(wide.lines), len(narrow.lines))
}

// TestHelpFallsBackToOneColumn checks a terminal too narrow for two columns gets
// the stacked list, blocks separated by one blank line and nothing padded out.
func TestHelpFallsBackToOneColumn(t *testing.T) {
	m := newTestModel(200, 80)
	blocks := m.helpBlocks()
	layout := layoutHelp(blocks, 40, m.styles)

	require.Equal(t, 1, layout.columns)

	want := len(blocks) - 1
	for _, b := range blocks {
		want += 1 + len(b.rows)
	}

	assert.Len(t, layout.lines, want)
	assert.NotContains(t, ansi.Strip(strings.Join(layout.lines, "\n")), "(cont.)")
}

// TestHelpNeverOrphansATitle checks the flow never leaves a title at the foot of
// a column with its rows in the next, never opens a column with a blank line,
// and repeats the title of a block it splits.
func TestHelpNeverOrphansATitle(t *testing.T) {
	m := newTestModel(200, 80)
	blocks := m.helpBlocks()

	for n := 1; n <= 8; n++ {
		for ci, col := range flowHelp(blocks, n) {
			require.NotEmpty(t, col)
			assert.GreaterOrEqualf(t, col[0].block, 0, "n=%d column %d opens with a blank", n, ci)
			assert.Equalf(t, -1, col[0].row, "n=%d column %d does not open with a title", n, ci)
			assert.Equalf(t, ci > 0 && col[0].cont, col[0].cont, "n=%d column %d: the first column cannot continue a block", n, ci)

			last := col[len(col)-1]
			assert.Falsef(t, last.block >= 0 && last.row < 0, "n=%d column %d ends on a title", n, ci)
		}
	}
}

// helpRowPattern matches one rendered help row: a key spelling, two or more
// spaces, and the description, once the overlay's border has been trimmed off.
var helpRowPattern = regexp.MustCompile(`^(\S+)\s{2,}(\S.*?)$`)

// helpRows counts the key/description pairs in a rendered help view. Matching
// whole fields rather than substrings is what keeps the count honest: "f" /
// "filter" is a substring of "{" / "toggle filter pane".
func helpRows(text string) map[string]int {
	rows := map[string]int{}

	for _, line := range strings.Split(text, "\n") {
		if m := helpRowPattern.FindStringSubmatch(strings.Trim(line, "│ ")); m != nil {
			rows[m[1]+"\x00"+m[2]]++
		}
	}

	return rows
}

// TestHelpNamesEveryLiveContext checks each context gets a titled block, so a
// user can tell which pane a key belongs to.
func TestHelpNamesEveryLiveContext(t *testing.T) {
	m := press(newTestModel(200, 80), "?")
	text := helpText(t, m)

	for _, ctx := range m.stackBeneathHelp() {
		assert.Contains(t, text, ctx.String())
	}
}

// TestHelpToggles checks ? closes the overlay it opened, rather than stacking a
// second copy on top of the first.
func TestHelpToggles(t *testing.T) {
	m := press(newTestModel(120, 40), "?")
	require.Len(t, m.overlays, 1)

	m = press(m, "?")
	assert.Empty(t, m.overlays)

	assert.Len(t, press(m, "?", "?", "?").overlays, 1)
}

// TestHelpSwallowsUnboundKeys checks an overlay's stack replaces the pane's
// rather than extending it: with help open, a torrent verb must not reach the
// list underneath.
func TestHelpSwallowsUnboundKeys(t *testing.T) {
	m := press(newTestModel(120, 40), "?")

	got := press(m, "s")

	assert.Empty(t, got.toast, "the list pane's start reached through the overlay")
	assert.Len(t, got.overlays, 1)
}

// TestHelpScrolls checks the ctxHelp keys are real: a short terminal cannot show
// the whole keymap, and scrolling is how the rest is reachable.
func TestHelpScrolls(t *testing.T) {
	m := press(newTestModel(80, 24), "?")

	require.Zero(t, helpOffset(t, m))

	m = press(m, "j", "j")
	assert.Positive(t, helpOffset(t, m))

	m = press(m, "k")
	assert.Equal(t, 1, helpOffset(t, m))
}

// TestHelpHalfPages checks ctrl+d and ctrl+u move half the box, as they do in
// the torrent list.
func TestHelpHalfPages(t *testing.T) {
	m := press(newTestModel(80, 16), "?")
	require.Zero(t, helpOffset(t, m))

	m = press(m, "ctrl+d")

	top, _ := m.topOverlay()
	half := top.(helpOverlay).fit(m, m.width, m.height).vp.Height() / 2

	require.Positive(t, half)
	assert.Equal(t, half, helpOffset(t, m))

	m = press(m, "ctrl+u")
	assert.Zero(t, helpOffset(t, m))
}

// helpOffset is the help overlay's current scroll position.
func helpOffset(t *testing.T, m Model) int {
	t.Helper()

	top, ok := m.topOverlay()
	require.True(t, ok)

	o, ok := top.(helpOverlay)
	require.True(t, ok)

	return o.vp.YOffset()
}

// TestShortHelpLineAdvertisesTheLiveContext checks the one-line help above the
// status bar tracks focus too, and always ends with the two keys that work
// everywhere.
func TestShortHelpLineAdvertisesTheLiveContext(t *testing.T) {
	m := newTestModel(200, 40)

	list := ansi.Strip(m.helpLineView(200))
	assert.Contains(t, list, "mark")
	assert.Contains(t, list, "quit")

	filter := ansi.Strip(m.setFocus(paneFilter).helpLineView(200))
	assert.Contains(t, filter, "apply filter")
	assert.NotContains(t, filter, "reannounce")
}

// TestHelpForAFocusedInputDescribesTheInput checks the stack help reads is the
// router's, including the modal one: with an input focused, the only keys that
// work are the input's two.
func TestHelpForAFocusedInputDescribesTheInput(t *testing.T) {
	m := newTestModel(120, 40)
	m.inputFocused = true
	m = m.push(newHelpOverlay())

	text := helpText(t, m)

	assert.Contains(t, text, "submit")
	assert.NotContains(t, text, "reannounce")
}

// TestHelpNamesTheFilesContextOnTheFilesTab checks the files tab's own keys are
// listed on the files tab — in the overlay and on the help line — and nowhere
// else in the pane.
func TestHelpNamesTheFilesContextOnTheFilesTab(t *testing.T) {
	files := helpText(t, press(newTestModel(200, 80), "enter", "2", "?"))
	assert.Contains(t, files, "mark file")
	assert.Contains(t, files, "file menu")

	overview := helpText(t, press(newTestModel(200, 80), "enter", "?"))
	assert.NotContains(t, overview, "mark file")

	line := ansi.Strip(press(newTestModel(200, 40), "enter", "2").helpLineView(200))
	assert.Contains(t, line, "mark file")
	assert.Contains(t, line, "file menu")
}
