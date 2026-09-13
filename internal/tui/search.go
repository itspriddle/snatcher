package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/itspriddle/snatcher/internal/output"
)

// footerGap separates the footer's filter and search parts.
const footerGap = "   "

// matchCountWidth is room for the widest match count the search input's width
// leaves space for, " (999/999)".
const matchCountWidth = 10

// newSearchInput builds the `/` field with the same readline keymap as the
// settings input.
func newSearchInput(st Styles) textinput.Model {
	input := textinput.New()
	input.Prompt = "/ "
	input.KeyMap = textInputKeyMap()
	input.SetStyles(st.Input)

	return input
}

// matchIndexes are the rows whose name contains the search, case-insensitively,
// in row order. The search never hides a row; these are the rows it jumps
// between.
func (w torrentsWindow) matchIndexes() []int {
	if w.listView.search == "" {
		return nil
	}

	query := strings.ToLower(w.listView.search)

	var out []int

	for i, t := range w.rows {
		if strings.Contains(strings.ToLower(t.Name), query) {
			out = append(out, i)
		}
	}

	return out
}

// matchCount is "(k/n)": the cursor row's place among the matches, 0 when the
// cursor is not on one, or "(0)" when nothing matches.
func (w torrentsWindow) matchCount() string {
	matches := w.matchIndexes()
	if len(matches) == 0 {
		return "(0)"
	}

	ordinal := 0

	for k, i := range matches {
		if w.sel.active && i == w.sel.cursor {
			ordinal = k + 1
		}
	}

	return fmt.Sprintf("(%d/%d)", ordinal, len(matches))
}

// searchStatus is the search as the footer reads while the field is blurred:
// "search: foo (2/7)".
func (w torrentsWindow) searchStatus() string {
	return "search: " + output.SanitizeTerminal(w.listView.search) + " " + w.matchCount()
}

// footerView draws the footer: the filter, and the search as a live input while
// searching or as its readout afterwards. While searching, the match count leads
// the field: textinput pads its text out to the field's width, so a count after
// it would sit at the far end of the pane rather than beside the query.
func (w torrentsWindow) footerView(st Styles) string {
	var parts []string

	if w.listView.filter.set() {
		parts = append(parts, st.Muted.Render("filter: "+w.listView.filter.text()))
	}

	switch {
	case w.searching:
		parts = append(parts, st.Muted.Render(w.matchCount()+" ")+w.searchInput.View())
	case w.listView.search != "":
		parts = append(parts, st.Muted.Render(w.searchStatus()))
	}

	return strings.Join(parts, footerGap)
}

// beginSearch is `/`: focus the field on the search in force, so a second `/`
// edits the query rather than starting over.
func (m Model) beginSearch() (Model, tea.Cmd) {
	m.torrents.searchInput.SetValue(m.torrents.listView.search)
	m.torrents.searchInput.CursorEnd()
	m.torrents.searching = true

	cmd := m.torrents.searchInput.Focus()
	m.inputFocused, m.owner = true, ownerSearch

	m = m.resize(m.width, m.height)
	m.torrents.searchInput.SetWidth(m.searchInputWidth())

	return m, cmd
}

// searchInputWidth is the cells the field's text gets: the list pane less the
// filter part, the prompt, the match count, and the one cell textinput draws
// past its width for the cursor.
func (m Model) searchInputWidth() int {
	used := lipgloss.Width(m.torrents.searchInput.Prompt) + matchCountWidth + 1

	if f := m.torrents.listView.filter; f.set() {
		used += lipgloss.Width("filter: " + f.text() + footerGap)
	}

	return max(1, m.layout().list.inner().w-used)
}

// searchForward hands a message to the field. When it changes the text, the
// cursor jumps to the first match from the top.
func (m Model) searchForward(msg tea.Msg) (Model, tea.Cmd) {
	input, cmd := m.torrents.searchInput.Update(msg)
	m.torrents.searchInput = input

	if input.Value() == m.torrents.listView.search {
		return m, cmd
	}

	m.torrents.listView.search = input.Value()

	if matches := m.torrents.matchIndexes(); len(matches) > 0 {
		m = m.jumpTo(matches[0])
	}

	return m, cmd
}

// searchBlur blurs the field, keeping its text: the search is the text, and n
// and N go on using it. An empty search leaves no footer, so it resizes.
func (m Model) searchBlur() Model {
	m.torrents.searchInput.Blur()
	m.torrents.searching = false

	return m.resize(m.width, m.height)
}

// clearSearch is the ladder's "clear search" rung.
func (m Model) clearSearch() Model {
	m.torrents.listView.search = ""
	m.torrents.searchInput.SetValue("")

	return m.syncTorrents().resize(m.width, m.height)
}

// jumpTo moves the cursor to a row, keeping the table and the selection in step.
func (m Model) jumpTo(i int) Model {
	m.torrents.sel = m.torrents.sel.setCursor(i)
	m.torrents.list.table.SetCursor(i)

	return m.syncTorrents()
}

// searchStep is n (delta 1) and N (delta -1): the next or previous match from
// the cursor, wrapping.
func (m Model) searchStep(delta int) Model {
	w := m.torrents

	if w.listView.search == "" {
		m.toast = "no search"

		return m
	}

	matches := w.matchIndexes()
	if len(matches) == 0 {
		m.toast = "no match for " + w.listView.search

		return m
	}

	return m.jumpTo(nextMatch(matches, w.sel.cursor, w.sel.active, delta))
}

// nextMatch picks the match after (or before) the cursor, wrapping. With no
// cursor it starts from the matching end.
func nextMatch(matches []int, cursor int, active bool, delta int) int {
	first, last := matches[0], matches[len(matches)-1]

	if delta > 0 {
		for _, i := range matches {
			if active && i > cursor {
				return i
			}
		}

		return first
	}

	for k := len(matches) - 1; k >= 0; k-- {
		if active && matches[k] < cursor {
			return matches[k]
		}
	}

	return last
}
