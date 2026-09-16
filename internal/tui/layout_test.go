package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestComputeLayoutAt120x40 pins the exact geometry at the reference size, both
// toggles open. Rects rather than properties: a reflow bug shows up as a region
// moving by a row, and a property test that only checks "the panes tile the
// body" would pass through it.
func TestComputeLayoutAt120x40(t *testing.T) {
	l := computeLayout(120, 40, true, true)

	assert.False(t, l.tooSmall)
	assert.Equal(t, rect{w: 120, h: 38}, l.body)
	assert.Equal(t, rect{w: 24, h: 23}, l.filter)
	assert.Equal(t, rect{x: 24, w: 96, h: 23}, l.list)
	assert.Equal(t, rect{y: 23, w: 120, h: 15}, l.detail)
	assert.Equal(t, rect{y: 38, w: 120, h: 1}, l.helpLine)
	assert.Equal(t, rect{y: 39, w: 120, h: 1}, l.statusBar)
}

// TestComputeLayoutAt80x24 pins the small end, where the filter sidebar hits its
// minimum width and the detail pane its proportional height.
func TestComputeLayoutAt80x24(t *testing.T) {
	l := computeLayout(80, 24, true, true)

	assert.Equal(t, rect{w: 80, h: 22}, l.body)
	assert.Equal(t, rect{w: 20, h: 14}, l.filter)
	assert.Equal(t, rect{x: 20, w: 60, h: 14}, l.list)
	assert.Equal(t, rect{y: 14, w: 80, h: 8}, l.detail)
}

// TestComputeLayoutClosedPanes checks a closed pane gets the zero rect and its
// space goes to the list, so a view can ask for a closed pane's region without a
// nil check.
func TestComputeLayoutClosedPanes(t *testing.T) {
	both := computeLayout(120, 40, false, false)

	assert.Equal(t, rect{}, both.filter)
	assert.Equal(t, rect{}, both.detail)
	assert.Equal(t, rect{w: 120, h: 38}, both.list)

	noDetail := computeLayout(120, 40, true, false)

	assert.Equal(t, rect{}, noDetail.detail)
	assert.Equal(t, 38, noDetail.list.h)

	noFilter := computeLayout(120, 40, false, true)

	assert.Equal(t, rect{}, noFilter.filter)
	assert.Equal(t, rect{x: 0, w: 120, h: 23}, noFilter.list)
}

// TestComputeLayoutFilterWidthIsClamped checks both ends of the sidebar clamp: a
// very wide terminal must not spend a third of itself on a fixed-content index.
func TestComputeLayoutFilterWidthIsClamped(t *testing.T) {
	assert.Equal(t, minFilterWidth, computeLayout(60, 40, true, false).filter.w)
	assert.Equal(t, maxFilterWidth, computeLayout(400, 40, true, false).filter.w)
}

// TestComputeLayoutDetailLeavesRoomForTheList checks the detail pane's clamp
// against the list rather than against a constant: at the height floor, two
// fifths of the body would leave the list too short to be a list.
func TestComputeLayoutDetailLeavesRoomForTheList(t *testing.T) {
	l := computeLayout(80, minHeight, true, true)

	assert.Equal(t, minDetailHeight, l.detail.h)
	assert.GreaterOrEqual(t, l.list.h, minListHeight)
}

// TestComputeLayoutFloor checks each axis of the floor independently, one cell
// below it on each.
func TestComputeLayoutFloor(t *testing.T) {
	assert.True(t, computeLayout(minWidth-1, minHeight, true, true).tooSmall)
	assert.True(t, computeLayout(minWidth, minHeight-1, true, true).tooSmall)
	assert.False(t, computeLayout(minWidth, minHeight, true, true).tooSmall)
}

// TestClampInvertedRangeYieldsLow checks the degenerate case a pane clamp can
// actually hit: when the upper bound falls below the lower one, the answer is
// the documented minimum rather than an inverted range.
func TestClampInvertedRangeYieldsLow(t *testing.T) {
	assert.Equal(t, 7, clamp(20, 7, 3))
	assert.Equal(t, 7, clamp(1, 7, 30))
	assert.Equal(t, 30, clamp(99, 7, 30))
}

// TestComputeProwlarrLayout pins the Prowlarr window's rects at the reference
// size, with and without the detail pane.
func TestComputeProwlarrLayout(t *testing.T) {
	l := computeProwlarrLayout(120, 40, true)

	assert.Equal(t, rect{w: 120, h: 38}, l.body)
	assert.Equal(t, rect{w: 120, h: 3}, l.search)
	assert.Equal(t, rect{y: 3, w: 120, h: 20}, l.list)
	assert.Equal(t, rect{y: 23, w: 120, h: 15}, l.detail)
	assert.Equal(t, rect{}, l.filter)

	closed := computeProwlarrLayout(80, 24, false)

	assert.Equal(t, rect{y: 3, w: 80, h: 19}, closed.list)
	assert.Equal(t, rect{}, closed.detail)
}

// TestComputeProwlarrLayoutAtTheFloor checks the detail pane keeps its minimum
// at 60x16 and the three panes still tile the body.
func TestComputeProwlarrLayoutAtTheFloor(t *testing.T) {
	l := computeProwlarrLayout(minWidth, minHeight, true)

	assert.Equal(t, rect{w: 60, h: 3}, l.search)
	assert.Equal(t, rect{y: 3, w: 60, h: 4}, l.list)
	assert.Equal(t, rect{y: 7, w: 60, h: minDetailHeight}, l.detail)
	assert.Equal(t, l.body.h, l.search.h+l.list.h+l.detail.h)

	assert.True(t, computeProwlarrLayout(minWidth-1, minHeight, false).tooSmall)
}
