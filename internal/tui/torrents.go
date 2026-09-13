package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// torrentsWindow is the default window: a filter sidebar, a torrent table, and a
// detail pane, with one selection shared between them.
type torrentsWindow struct {
	// all is every torrent the spine last polled, before any filter. The
	// sidebar counts and the status bar's used space are computed from it, so a
	// filter never changes the numbers it was chosen from.
	all []transmission.Torrent

	// rows are the torrents the table shows: all, filtered and sorted.
	rows []transmission.Torrent

	// listView is the filter, sort, columns, and search behind rows.
	listView listView

	// filter, list, and detail are the window's panes.
	filter filterPane
	list   listPane
	detail detailPane

	// filterOpen and detailOpen are the two pane toggles ({ and }).
	filterOpen bool
	detailOpen bool

	// focus is the pane that owns the keyboard.
	focus Pane

	// sel is the marked set and cursor every action in this window consumes.
	sel torrentSelection

	// searchInput is the `/` field, drawn in the list pane's footer while
	// searching. Its text is the search, so blurring it keeps the text.
	searchInput textinput.Model
	searching   bool
}

// newTorrentsWindow builds the window around a set of torrents and a view.
//
// The filter sidebar starts open and the detail pane starts closed: the sidebar
// is how a user narrows a list they are looking at, and the detail pane
// describes a row they have chosen, which they have not yet done.
func newTorrentsWindow(st Styles, icons Icons, all []transmission.Torrent, view listView) torrentsWindow {
	w := torrentsWindow{
		all:        all,
		rows:       view.apply(all),
		listView:   view,
		filter:     newFilterPane(all),
		list:       newListPane(st, view.columns),
		detail:     newDetailPane(st, icons, all == nil),
		filterOpen: true,
		focus:      paneList,
		sel:        newTorrentSelection(),

		searchInput: newSearchInput(st),
	}

	return w.sync(icons)
}

// sync rebuilds the table's rows and repoints the detail pane, after anything
// that changes the rows, the marks, or the cursor.
func (w torrentsWindow) sync(icons Icons) torrentsWindow {
	w.list = w.list.setRows(w.rows, w.sel, icons)

	if row, ok := w.sel.row(w.rows); ok {
		w.detail = w.detail.setTorrent(&row)

		return w
	}

	return w.applyDetail(nil)
}

// applyDetail points the detail pane at a torrent. It exists so sync has one
// statement per branch rather than a bare assignment the linter reads as a
// duplicated expression.
func (w torrentsWindow) applyDetail(t *transmission.Torrent) torrentsWindow {
	w.detail = w.detail.setTorrent(t)

	return w
}

// setAll installs a newly polled set of torrents.
func (w torrentsWindow) setAll(all []transmission.Torrent, icons Icons) torrentsWindow {
	w.all = all

	return w.reapply(icons)
}

// setView installs a new view: a filter, a sort, or a column set.
func (w torrentsWindow) setView(v listView, icons Icons) torrentsWindow {
	if !slices.Equal(v.columns, w.listView.columns) {
		w.list = w.list.setColumns(v.columns)
	}

	w.listView = v

	return w.reapply(icons)
}

// reapply rederives the rows from all and the view, keeping the user's place in
// them: the cursor follows its torrent by ID, marks on torrents no longer shown
// are dropped, and the sidebar's counts are rebuilt from the unfiltered set.
func (w torrentsWindow) reapply(icons Icons) torrentsWindow {
	prev, had := w.sel.row(w.rows)

	w.rows = w.listView.apply(w.all)
	w.sel = w.sel.track(prev.ID, had, w.rows)
	w.filter = w.filter.rebuild(w.all)
	w = w.sync(icons)
	w.list.table.SetCursor(w.sel.cursor)

	return w
}

// reset empties the window for a different server: no rows, no marks, no filter
// or search, the cursor at the top. The pane toggles, focus, sort, and columns
// are the user's preferences, not the server's, so they stay.
func (w torrentsWindow) reset(icons Icons) torrentsWindow {
	w.all = nil
	w.rows = nil
	w.sel = newTorrentSelection()
	w.filter = newFilterPane(nil)
	w.listView.filter = viewFilter{}
	w.listView.search = ""
	w.searchInput.SetValue("")
	w.searchInput.Blur()
	w.searching = false
	w = w.sync(icons)
	w.list.table.SetCursor(0)

	return w
}

// footer is the list pane's footer line as plain text: the filter in force and
// the search, or empty when there is neither. Whoever changes whether there is a
// footer resizes, so the table gives up or takes back the row. footerView draws
// it.
func (w torrentsWindow) footer() string {
	var parts []string

	if w.listView.filter.set() {
		parts = append(parts, "filter: "+w.listView.filter.text())
	}

	if w.searching || w.listView.search != "" {
		parts = append(parts, w.searchStatus())
	}

	return strings.Join(parts, footerGap)
}

// resize lays the panes out for a frame.
func (w torrentsWindow) resize(l layout) torrentsWindow {
	w.list = w.list.resize(l.list, w.footer() != "")

	if w.detailOpen {
		w.detail = w.detail.resize(l.detail)
	}

	return w
}

// openPanes lists the window's open panes in cycle order, left to right and then
// down. tab cycles panes, so the order it walks is the order they are drawn.
func (w torrentsWindow) openPanes() []Pane {
	panes := make([]Pane, 0, 3)

	if w.filterOpen {
		panes = append(panes, paneFilter)
	}

	panes = append(panes, paneList)

	if w.detailOpen {
		panes = append(panes, paneDetail)
	}

	return panes
}

// neighbor answers the ctrl+w chord: which pane lies in a direction from the
// focused one, or the focused one when there is nothing there.
//
// The rules are deliberately positional and memoryless. From the detail pane,
// both k and any other vertical move return to the list — one rule, no "which
// pane did I come from" state to get wrong.
func (w torrentsWindow) neighbor(dir Action) Pane {
	switch w.focus {
	case paneFilter:
		return w.neighborFromFilter(dir)
	case paneList:
		return w.neighborFromList(dir)
	case paneDetail:
		if dir == actionFocusUp {
			return paneList
		}
	case paneSettings:
	}

	return w.focus
}

// neighborFromFilter moves right into the list and down into the detail pane.
func (w torrentsWindow) neighborFromFilter(dir Action) Pane {
	switch {
	case dir == actionFocusRight:
		return paneList
	case dir == actionFocusDown && w.detailOpen:
		return paneDetail
	}

	return paneFilter
}

// neighborFromList moves left into the sidebar and down into the detail pane.
func (w torrentsWindow) neighborFromList(dir Action) Pane {
	switch {
	case dir == actionFocusLeft && w.filterOpen:
		return paneFilter
	case dir == actionFocusDown && w.detailOpen:
		return paneDetail
	}

	return paneList
}

// view renders the window: the sidebar and table side by side, with the detail
// pane, when open, beneath both. placeholder stands in for an empty table.
func (w torrentsWindow) view(st Styles, l layout, ph placeholder) string {
	top := w.list.view(st, l.list, w.focus == paneList, ph, w.footerView(st))

	if w.filterOpen {
		top = lipgloss.JoinHorizontal(
			lipgloss.Top,
			w.filter.view(st, l.filter, w.focus == paneFilter),
			top,
		)
	}

	if !w.detailOpen {
		return top
	}

	return lipgloss.JoinVertical(lipgloss.Left, top, w.detail.view(st, l.detail, w.focus == paneDetail))
}
