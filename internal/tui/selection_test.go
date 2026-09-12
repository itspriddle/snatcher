package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// TestSelectorPrefersMarks checks the selection rule from vision.md: an action
// hits the marked set when marks exist, and the cursor row otherwise.
func TestSelectorPrefersMarks(t *testing.T) {
	rows := mockTorrents()

	sel := newSelection().setCursor(0)
	assert.Equal(t, transmission.Selector{IDs: []int64{rows[0].ID}}, sel.Selector(rows))

	sel = sel.setCursor(2).toggleMark(rows).setCursor(5).toggleMark(rows)
	assert.Equal(t, transmission.Selector{IDs: []int64{rows[2].ID, rows[5].ID}}, sel.Selector(rows))
}

// TestSelectorIsSortedAndStable checks the IDs come out in a fixed order, since
// they are built from a map. An unstable Selector would make every command's
// output order depend on Go's map iteration.
func TestSelectorIsSortedAndStable(t *testing.T) {
	rows := mockTorrents()

	sel := newSelection()
	for _, i := range []int{7, 1, 4} {
		sel = sel.setCursor(i).toggleMark(rows)
	}

	assert.Equal(t, []int64{rows[1].ID, rows[4].ID, rows[7].ID}, sel.Selector(rows).IDs)
}

// TestSelectorWithNoCursorTargetsNothing checks the deselected state. It must
// never fall back to All: "every torrent on the server" is something a user asks
// for explicitly, not something a selection decays into.
func TestSelectorWithNoCursorTargetsNothing(t *testing.T) {
	rows := mockTorrents()

	sel := newSelection().deselect().Selector(rows)

	assert.Equal(t, transmission.Selector{}, sel)
	assert.False(t, sel.All)
}

// TestSelectorOutOfRangeCursorTargetsNothing checks a cursor past the end of a
// shortened list, which is what a filter or a refresh produces.
func TestSelectorOutOfRangeCursorTargetsNothing(t *testing.T) {
	assert.Equal(t, transmission.Selector{}, newSelection().setCursor(99).Selector(mockTorrents()))
}

// TestToggleMarkDoesNotMoveTheCursor pins the binding's wording: space toggles a
// mark on the cursor row. A cursor that advanced would make marking a run depend
// on where the run started.
func TestToggleMarkDoesNotMoveTheCursor(t *testing.T) {
	m := newTestModel(120, 40)

	m = press(m, "down", "down", "space")

	assert.Equal(t, 2, m.torrents.sel.cursor)
	assert.Equal(t, 2, m.torrents.list.cursor())
	assert.True(t, m.torrents.sel.isMarked(m.torrents.rows[2].ID))
}

// TestMarksSurviveAReorder checks that marks are keyed by torrent ID rather than
// by row index: a sort, filter, or refresh must not move a mark to a different
// torrent.
func TestMarksSurviveAReorder(t *testing.T) {
	rows := mockTorrents()

	sel := newSelection().setCursor(0).toggleMark(rows)
	marked := rows[0].ID

	reordered := transmission.SortTorrents(rows, transmission.SortName, true)

	require.NotEqual(t, marked, reordered[0].ID, "the fixture must actually reorder")
	assert.Equal(t, []int64{marked}, sel.Selector(reordered).IDs)
}

// TestApplyingAFilterClearsMarks pins the rule from vision.md: a mark hidden by a
// filter would be acted on invisibly, and with exactly one hidden mark an action
// would skip the cursor row with nothing to say so.
func TestApplyingAFilterClearsMarks(t *testing.T) {
	m := press(newTestModel(120, 40), "space")
	require.Equal(t, 1, m.torrents.sel.markCount())

	m = press(m.setFocus(paneFilter), "down", "enter")

	assert.Zero(t, m.torrents.sel.markCount())
	assert.Equal(t, m.torrents.filter.cursor, m.torrents.filter.applied)
}

// TestMarkCountDrivesTheStatusBar checks the only tell a user has that an action
// will hit more than the row in front of them.
func TestMarkCountDrivesTheStatusBar(t *testing.T) {
	m := newTestModel(120, 40)

	assert.NotContains(t, m.statusBarView(120), "marks:")

	m = press(m, "space", "down", "space")

	assert.Contains(t, m.statusBarView(120), "marks: 2")
}

// TestFilterPaneCursorSkipsHeadings checks the hand-rolled sidebar's one piece of
// real logic: the cursor never rests on a section heading or a placeholder.
func TestFilterPaneCursorSkipsHeadings(t *testing.T) {
	p := newFilterPane(mockTorrents())

	require.True(t, p.current().selectable())

	for range len(p.items) + 5 {
		p = p.move(1)
		assert.True(t, p.current().selectable(), "cursor landed on %q", p.current().label)
	}

	for range len(p.items) + 5 {
		p = p.move(-1)
		assert.True(t, p.current().selectable())
	}
}

// TestFilterPaneEndsStopRatherThanWrap checks the sidebar's ends, including that
// bottom lands on the last selectable row rather than on the tracker placeholder
// beneath it.
func TestFilterPaneEndsStopRatherThanWrap(t *testing.T) {
	p := newFilterPane(mockTorrents())

	assert.Equal(t, filterLabel(transmission.FilterAll), p.top().current().label)
	assert.Equal(t, noLabel, p.bottom().current().label)
	assert.Equal(t, p.top().current(), p.top().move(-5).current())
	assert.Equal(t, p.bottom().current(), p.bottom().move(5).current())
}

// TestFilterPaneCountsMatchTheDomainFilters checks the sidebar's counts come from
// transmission's own filter, not from a second implementation that can drift.
func TestFilterPaneCountsMatchTheDomainFilters(t *testing.T) {
	rows := mockTorrents()
	p := newFilterPane(rows)

	for _, item := range p.items {
		if item.filter == "" {
			continue
		}

		assert.Equalf(t, len(transmission.FilterByStatus(rows, item.filter)), item.count, "%s", item.filter)
	}
}

// TestFilterPaneLabelsComeFromTheRows checks the label section is derived, with a
// catch-all for torrents carrying none.
func TestFilterPaneLabelsComeFromTheRows(t *testing.T) {
	labels := map[string]int{}

	for _, item := range newFilterPane(mockTorrents()).items {
		if item.kind == itemFilter && item.filter == "" {
			labels[item.label] = item.count
		}
	}

	assert.Equal(t, map[string]int{"linux": 3, "seed": 3, noLabel: 7}, labels)
}
