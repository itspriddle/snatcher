package tui

import (
	"charm.land/lipgloss/v2"
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
