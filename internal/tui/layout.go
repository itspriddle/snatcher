package tui

// Layout constants. These are the whole geometry vocabulary: everything else is
// derived, so a reflow is one function and one test rather than arithmetic
// spread across the pane views.
const (
	// minWidth and minHeight are the floor below which the UI is not drawn at
	// all. Below it a pane border alone would consume the row it is meant to
	// frame, so render says so plainly instead of drawing something illegible.
	minWidth  = 60
	minHeight = 16

	// chromeHeight is the two lines every window gives up at the bottom: the
	// short-help line and the status bar.
	chromeHeight = 2

	// filterWidthDivisor, minFilterWidth, and maxFilterWidth bound the filter
	// sidebar. It is a fifth of the terminal, clamped: narrower than 20 and the
	// status labels truncate, wider than 30 and it steals from the table for no
	// gain, since its content does not grow with the terminal.
	filterWidthDivisor = 5
	minFilterWidth     = 20
	maxFilterWidth     = 30

	// detailNumerator, detailDenominator, minDetailHeight, and minListHeight
	// bound the detail pane: two fifths of the body, never less than 7 rows (a
	// border, a tab strip, and four content lines) and never leaving the list
	// fewer than 6.
	detailNumerator   = 2
	detailDenominator = 5
	minDetailHeight   = 7
	minListHeight     = 6

	// borderSize is the rows and columns a rounded border consumes on each
	// axis: one line on each side.
	borderSize = 2
)

// rect is a region of the terminal in cells, with its origin at the top left.
// Panes are laid out by joining strings rather than by absolute positioning, so
// a rect is what a pane is told to fill, not where it is blitted — except in
// the compositor, which does take absolute offsets.
type rect struct {
	x, y, w, h int
}

// inner returns the region inside a rounded border: the area a pane's content
// actually gets.
func (r rect) inner() rect {
	return rect{x: r.x + 1, y: r.y + 1, w: r.w - borderSize, h: r.h - borderSize}
}

// layout is one frame's geometry. Panes that are closed have a zero rect, so a
// view can ask for a closed pane's rect without a nil check and get a region it
// cannot draw into.
type layout struct {
	// body is everything above the help line and status bar.
	body rect

	// filter, list, and detail are the torrents window's panes. A closed pane
	// is the zero rect.
	filter rect
	list   rect
	detail rect

	// helpLine and statusBar are the two chrome rows at the bottom.
	helpLine  rect
	statusBar rect

	// tooSmall reports that the terminal is below the floor, in which case
	// every other field is the zero rect.
	tooSmall bool
}

// computeLayout divides a terminal of w by h into the frame's regions.
//
// The order is fixed and one-directional: chrome comes off the bottom first
// because it is a constant, then the sidebar off the left because its width does
// not depend on the detail pane, then the detail pane off the bottom of what
// remains. Nothing feeds back, so the geometry at any size is readable top to
// bottom and pinned by exact rects in layout_test.go.
func computeLayout(w, h int, filterOpen, detailOpen bool) layout {
	if w < minWidth || h < minHeight {
		return layout{tooSmall: true}
	}

	body := rect{w: w, h: h - chromeHeight}

	l := layout{
		body:      body,
		helpLine:  rect{y: body.h, w: w, h: 1},
		statusBar: rect{y: body.h + 1, w: w, h: 1},
	}

	top := body
	if detailOpen {
		detailHeight := clamp(body.h*detailNumerator/detailDenominator, minDetailHeight, body.h-minListHeight)
		top.h = body.h - detailHeight
		l.detail = rect{x: 0, y: top.h, w: w, h: detailHeight}
	}

	if filterOpen {
		l.filter = rect{x: 0, y: 0, w: clamp(w/filterWidthDivisor, minFilterWidth, maxFilterWidth), h: top.h}
	}

	l.list = rect{x: l.filter.w, y: 0, w: w - l.filter.w, h: top.h}

	return l
}

// clamp constrains v to [low, high]. A high below low yields low, which is the
// answer that keeps a pane at its documented minimum rather than inverting it.
func clamp(v, low, high int) int {
	if high < low {
		return low
	}

	if v < low {
		return low
	}

	if v > high {
		return high
	}

	return v
}
