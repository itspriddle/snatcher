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

// matchCountWidth is room for the widest row count the search input's width
// leaves space for, " (999999)".
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

// rowCount is "(12)": how many rows the search left. The rows are already the
// searched set, so this is simply their number.
func (w torrentsWindow) rowCount() string {
	return fmt.Sprintf("(%d)", len(w.rows))
}

// searchStatus is the search as the footer reads while the field is blurred:
// "search: foo (7)".
func (w torrentsWindow) searchStatus() string {
	return "search: " + output.SanitizeTerminal(w.listView.search) + " " + w.rowCount()
}

// narrowedBy names whatever emptied the list — the filter, the search, or both
// — so the empty table can say why it is empty rather than just that it is.
func (w torrentsWindow) narrowedBy() string {
	var parts []string

	if w.listView.filter.set() {
		parts = append(parts, "filter: "+w.listView.filter.text())
	}

	if w.listView.search != "" {
		parts = append(parts, "search: "+output.SanitizeTerminal(w.listView.search))
	}

	return strings.Join(parts, footerGap)
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
		parts = append(parts, st.Muted.Render(w.rowCount()+" ")+w.searchInput.View())
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

	return max(1, m.torrentsLayout().list.inner().w-used)
}

// searchForward hands a message to the field. When it changes the text, the
// rows are narrowed to the matches as they are typed. A key that leaves the
// text alone — a cursor move inside the field — changes nothing, so it does not
// disturb the marks.
func (m Model) searchForward(msg tea.Msg) (Model, tea.Cmd) {
	input, cmd := m.torrents.searchInput.Update(msg)
	m.torrents.searchInput = input

	if input.Value() == m.torrents.listView.search {
		return m, cmd
	}

	// Editing the query narrows the rows, so it clears marks for the reason
	// applyFilterItem does: a mark the search has hidden would be acted on
	// invisibly, and with exactly one hidden mark an action would skip the cursor
	// row with nothing to say so.
	m.torrents.sel = m.torrents.sel.clearMarks()

	return m.setSearch(input.Value()), cmd
}

// setSearch installs a query and rederives the rows through setView, so the
// cursor follows its torrent by ID and the sidebar's counts stay on the
// unfiltered set.
//
// It leaves marks alone: clearing them belongs to whoever narrows the rows, and
// widening them can hide nothing.
func (m Model) setSearch(query string) Model {
	v := m.torrents.listView
	v.search = query

	m.torrents = m.torrents.setView(v, m.icons)

	return m
}

// searchBlur blurs the field, keeping its text: the search is the text, and n
// and N go on using it. An empty search leaves no footer, so it resizes.
func (m Model) searchBlur() Model {
	m.torrents.searchInput.Blur()
	m.torrents.searching = false

	return m.resize(m.width, m.height)
}

// clearSearch is the ladder's "clear search" rung: the rows widen back to the
// filtered set. It resizes because an empty search leaves no footer.
//
// Marks survive it. Widening hides nothing, so the rule that clears them does
// not apply, and marks made while the search was narrow are exactly what the
// next rung ("clear marks") is there to drop.
func (m Model) clearSearch() Model {
	m.torrents.searchInput.SetValue("")

	return m.setSearch("").resize(m.width, m.height)
}
