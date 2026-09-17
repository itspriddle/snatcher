package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// overlay is something drawn on top of a window: the help view, the confirm
// dialog, the action menu, the command palette, and the rest.
//
// Overlays are a stack rather than a field, because the esc ladder's first real
// rung is "close the topmost overlay" and a confirm dialog opened from the
// action menu has to close back onto the menu.
type overlay interface {
	// kind names the overlay, so a caller can ask whether a particular one is
	// already open without a type switch.
	kind() string

	// contexts is the context stack while this overlay is topmost. It replaces
	// the pane's stack rather than extending it: an overlay swallows the keys
	// it does not bind.
	contexts() []Context

	// view renders the overlay's box, given the frame it is being centered in.
	view(m Model, w, h int) string
}

// push adds an overlay to the stack, making it topmost.
func (m Model) push(o overlay) Model {
	m.overlays = append(append([]overlay{}, m.overlays...), o)

	return m
}

// inputHolder is an overlay that holds a text input, and names it.
type inputHolder interface {
	ownsInput() inputOwner
}

// pop removes the topmost overlay. Popping an empty stack is a no-op, so the
// esc ladder can call it without first asking.
//
// An overlay that held the focused input takes the focus with it: a prompt
// closed by esc, or by a submit that acted, must not leave the router sending
// keys to an input nothing draws.
func (m Model) pop() Model {
	if len(m.overlays) == 0 {
		return m
	}

	top := m.overlays[len(m.overlays)-1]
	m.overlays = m.overlays[:len(m.overlays)-1]

	if holder, ok := top.(inputHolder); ok && m.owner == holder.ownsInput() {
		m.inputFocused, m.owner = false, ownerNone
	}

	return m
}

// replaceTopOverlay swaps the topmost overlay for an updated copy. The stack is
// copied rather than written through, so a Model handed to a test and then
// updated cannot mutate the stack the caller still holds.
func (m Model) replaceTopOverlay(o overlay) Model {
	if len(m.overlays) == 0 {
		return m
	}

	stack := append([]overlay{}, m.overlays...)
	stack[len(stack)-1] = o
	m.overlays = stack

	return m
}

// topOverlay returns the topmost overlay and whether there is one.
func (m Model) topOverlay() (overlay, bool) {
	if len(m.overlays) == 0 {
		return nil, false
	}

	return m.overlays[len(m.overlays)-1], true
}

// topAs returns the topmost overlay as a concrete type, and whether it is one.
func topAs[T overlay](m Model) (T, bool) {
	var zero T

	top, ok := m.topOverlay()
	if !ok {
		return zero, false
	}

	o, ok := top.(T)
	if !ok {
		return zero, false
	}

	return o, true
}

// stepTop adapts a pure transition of one overlay type into a handler that
// applies it to the topmost overlay, when that overlay is of the type.
func stepTop[T overlay](f func(T) T) handler {
	return func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) {
		o, ok := topAs[T](m)
		if !ok {
			return m, nil
		}

		return m.replaceTopOverlay(f(o)), nil
	}
}

// modal is the shape every list-style modal draws: a title, a scrolled run of
// rows, an optional footer that never scrolls, and the modal's own help line.
type modal struct {
	title string

	// head lines are drawn between the title and the rows, outside the scroll:
	// the palette's input line.
	head []string

	// rows are the selectable lines, already styled and padded to width.
	rows []string

	// focus is the row the scrolled window keeps in view.
	focus int

	// footer lines are drawn under the rows, outside the scroll.
	footer []string

	// ctx is the context whose keys the help line lists.
	ctx Context

	// help, when non-nil, replaces ctx's bindings on the help line, for a modal
	// that words its keys itself.
	help []key.Binding

	// width is the content width.
	width int
}

// overlayContentHeight is the tallest an overlay's content may be in a terminal
// of h rows: the body above the chrome, less the box's border, less a row of
// margin on each side, so a centered box never reaches the help line.
func overlayContentHeight(h int) int {
	return max(1, h-chromeHeight-borderSize*2)
}

// modalBox draws a modal. When the rows do not fit the terminal they scroll,
// keeping the focused row in view: a column list is fifteen rows, and a 60x16
// terminal has room for five of them.
func (m Model) modalBox(b modal, h int) string {
	start, end := modalWindow(b, h)

	lines := make([]string, 0, end-start+4+len(b.footer)+len(b.head))
	lines = append(lines, m.styles.Title.Render(truncate(b.title, b.width)))
	lines = append(lines, b.head...)
	lines = append(lines, "")
	lines = append(lines, b.rows[start:end]...)
	lines = append(lines, "")
	lines = append(lines, b.footer...)

	// The help line is truncated here rather than by help.SetWidth: bubbles'
	// ShortHelpView stops truncating once an item overflows and the ellipsis no
	// longer fits either, and appends every remaining item, which would stretch
	// the box past the frame.
	bindings := b.help
	if bindings == nil {
		bindings = contextBindings(b.ctx)
	}

	lines = append(lines, ansi.Truncate(m.help.ShortHelpView(bindings), b.width, "…"))

	return m.styles.Overlay.Render(strings.Join(lines, "\n"))
}

// modalWindow is the run of a modal's rows modalBox draws in a terminal of h
// rows: from start up to, not including, end.
func modalWindow(b modal, h int) (start, end int) {
	budget := max(1, overlayContentHeight(h)-4-len(b.footer)-len(b.head))
	start = scrollStart(b.focus, len(b.rows), budget)

	return start, min(len(b.rows), start+budget)
}

// modalRowsTop is how many content lines modalBox draws above a modal's first
// row: the title, the head, and a blank line.
func modalRowsTop(b modal) int {
	return 1 + len(b.head) + 1
}

// modalOverlay is an overlay drawn with modalBox, whose rows a click can land
// on. modal is exactly what its view hands modalBox, so the hit-test and the
// frame agree on which row is where.
type modalOverlay interface {
	overlay

	// modal is the modal the overlay draws in a frame of w by h.
	modal(m Model, w, h int) modal

	// pointAt moves the cursor to row i, and reports false for a row that
	// cannot hold it: a heading, a note, a disabled row.
	pointAt(i int) (overlay, bool)

	// clickAction is what a click on a row runs once the cursor is on it: the
	// action the row's own key runs.
	clickAction() Action

	// wheelActions are the actions the overlay's own up and down keys run,
	// which a notch of the wheel runs too.
	wheelActions() (up, down Action)
}

// scrollStart is the first row of a window of height rows over total rows that
// keeps focus in view, centered where it can be.
func scrollStart(focus, total, height int) int {
	if total <= height {
		return 0
	}

	return clamp(focus-height/2, 0, total-height)
}

// cursorRow draws one selectable row: a ">" marker as well as the cursor style
// on the cursor row, so the cursor survives NO_COLOR and a stripped golden.
func cursorRow(st Styles, on bool, text string, width int) string {
	marker := "  "
	if on {
		marker = "> "
	}

	line := padLine(marker+text, width)
	if on {
		return st.Cursor.Render(line)
	}

	return line
}

// hasOverlay reports whether an overlay of the given kind is open.
func (m Model) hasOverlay(kind string) bool {
	for _, o := range m.overlays {
		if o.kind() == kind {
			return true
		}
	}

	return false
}

// placeBox is where composite centers a rendered overlay box in the frame.
func (m Model) placeBox(box string) rect {
	w, h := lipgloss.Width(box), lipgloss.Height(box)

	return rect{x: max(0, (m.width-w)/2), y: max(0, (m.height-h)/2), w: w, h: h}
}

// composite draws the overlay stack over a base frame, each layer centered and
// one Z above the last.
//
// lipgloss v2 composites with a Compositor rather than with Place — Place
// positions content within a box but does not draw one string over another — and
// it has no centering helper, so the offsets are computed from the rendered
// sizes.
func (m Model) composite(base string) string {
	if len(m.overlays) == 0 {
		return base
	}

	layers := []*lipgloss.Layer{lipgloss.NewLayer(base)}

	for i, o := range m.overlays {
		box := o.view(m, m.width, m.height)
		r := m.placeBox(box)

		layers = append(layers, lipgloss.NewLayer(box).X(r.x).Y(r.y).Z(i+1))
	}

	return lipgloss.NewCompositor(layers...).Render()
}
