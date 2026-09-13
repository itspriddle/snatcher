package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// overlay is something drawn on top of a window: the help view now, and the
// confirm dialog, action menu, and command palette in T5 and T7.
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

// pop removes the topmost overlay. Popping an empty stack is a no-op, so the
// esc ladder can call it without first asking.
func (m Model) pop() Model {
	if len(m.overlays) == 0 {
		return m
	}

	m.overlays = m.overlays[:len(m.overlays)-1]

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

	// rows are the selectable lines, already styled and padded to width.
	rows []string

	// focus is the row the scrolled window keeps in view.
	focus int

	// footer lines are drawn under the rows, outside the scroll.
	footer []string

	// ctx is the context whose keys the help line lists.
	ctx Context

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
	budget := max(1, overlayContentHeight(h)-4-len(b.footer))
	start := scrollStart(b.focus, len(b.rows), budget)
	end := min(len(b.rows), start+budget)

	lines := make([]string, 0, budget+4+len(b.footer))
	lines = append(lines, m.styles.Title.Render(truncate(b.title, b.width)), "")
	lines = append(lines, b.rows[start:end]...)
	lines = append(lines, "")
	lines = append(lines, b.footer...)

	// The help line is truncated here rather than by help.SetWidth: bubbles'
	// ShortHelpView stops truncating once an item overflows and the ellipsis no
	// longer fits either, and appends every remaining item, which would stretch
	// the box past the frame.
	lines = append(lines, ansi.Truncate(m.help.ShortHelpView(contextBindings(b.ctx)), b.width, "…"))

	return m.styles.Overlay.Render(strings.Join(lines, "\n"))
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

		x := max(0, (m.width-lipgloss.Width(box))/2)
		y := max(0, (m.height-lipgloss.Height(box))/2)

		layers = append(layers, lipgloss.NewLayer(box).X(x).Y(y).Z(i+1))
	}

	return lipgloss.NewCompositor(layers...).Render()
}
