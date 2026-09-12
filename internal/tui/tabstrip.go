package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// renderTabs draws a tab strip of exactly width cells, with the active tab
// styled. It is shared by the detail pane and the settings window — and by T3's
// form — so the two never drift into looking like different widgets.
func renderTabs(st Styles, tabs []string, active, width int) string {
	rendered := make([]string, 0, len(tabs))

	for i, name := range tabs {
		if i == active {
			rendered = append(rendered, st.TabActive.Render(name))

			continue
		}

		rendered = append(rendered, st.Tab.Render(name))
	}

	return padLine(lipgloss.JoinHorizontal(lipgloss.Top, rendered...), width)
}

// tabRule is the horizontal rule drawn under a tab strip.
func tabRule(st Styles, width int) string {
	if width <= 0 {
		return ""
	}

	return st.Muted.Render(strings.Repeat("─", width))
}
