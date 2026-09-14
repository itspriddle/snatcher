package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// paneBox returns the border style a pane draws itself in. The filter pane is
// the one pane whose focused style is the loud one; see filterPane.view.
func paneBox(st Styles, p Pane, focused bool) lipgloss.Style {
	switch {
	case focused && p == paneFilter:
		return st.PaneLoud
	case focused:
		return st.PaneFocused
	}

	return st.Pane
}

// truncate cuts s to at most width cells, marking the cut with an ellipsis. It
// measures in display cells rather than runes, so a wide glyph or a styled
// string is cut where it actually overflows.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}

	if lipgloss.Width(s) <= width {
		return s
	}

	if width == 1 {
		return "…"
	}

	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}

	return string(runes) + "…"
}

// padLine pads s to exactly width cells, truncating when it overflows. Every
// line the UI emits goes through here or through a lipgloss box of a fixed
// width, which is what makes "the frame fills the terminal exactly" testable.
func padLine(s string, width int) string {
	s = truncate(s, width)

	if gap := width - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}

	return s
}

// pad lays left and right out on one line of exactly width cells with the gap
// between them, truncating left when the two do not fit. At least one space
// always separates them, so a truncated label never runs into its own count.
func pad(left, right string, width int) string {
	gap := width - lipgloss.Width(right)
	if gap < 2 {
		return padLine(truncate(left, width), width)
	}

	left = truncate(left, gap-1)

	return left + strings.Repeat(" ", gap-lipgloss.Width(left)) + right
}

// paneContent squares a pane's rendered lines off to exactly the region inside
// its border: every line padded or truncated to the inner width, and the whole
// clipped or padded to the inner height.
//
// It exists because the components inside a pane size themselves independently —
// a table whose columns do not fit wraps its header onto a second line, which
// would grow the pane's box by a row and push the frame past the terminal. Every
// pane goes through here, so a component that misbehaves distorts its own
// content rather than the layout.
func paneContent(lines []string, r rect) string {
	inner := r.inner()

	out := clipLines(lines, inner.h)
	for i, line := range out {
		out[i] = padLine(line, inner.w)
	}

	return strings.Join(out, "\n")
}

// clipLines trims lines to at most height entries, padding with blanks when
// there are too few. A pane hands its content to a fixed-height lipgloss box, so
// content longer than the box would otherwise push the frame past the terminal.
func clipLines(lines []string, height int) []string {
	if height <= 0 {
		return nil
	}

	if len(lines) > height {
		return lines[:height]
	}

	for len(lines) < height {
		lines = append(lines, "")
	}

	return lines
}

// overlayContentWidth is the widest an overlay's content may be in a frame of w
// cells: the frame less the margin on both sides, less the box's own border and
// padding.
func overlayContentWidth(w int) int {
	return max(1, w-overlayMargin*2-borderSize*2)
}
