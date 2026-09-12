package tui

import (
	"charm.land/lipgloss/v2"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// torrentsWindow is the default window: a filter sidebar, a torrent table, and a
// detail pane, with one selection shared between them.
type torrentsWindow struct {
	// rows are the torrents currently rendered, after any filter, search, and
	// sort. T4 renders the polled set in queue order.
	rows []transmission.Torrent

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
	sel selection

	// search is the active `/` query. It is always empty in T1 — `/` is
	// registered but not implemented — and exists so the esc ladder's
	// clear-search rung is a real rung with a real test rather than a hole.
	search string
}

// newTorrentsWindow builds the window around a set of torrents.
//
// The filter sidebar starts open and the detail pane starts closed: the sidebar
// is how a user narrows a list they are looking at, and the detail pane
// describes a row they have chosen, which they have not yet done.
func newTorrentsWindow(st Styles, icons Icons, rows []transmission.Torrent) torrentsWindow {
	w := torrentsWindow{
		rows:       rows,
		filter:     newFilterPane(rows),
		list:       newListPane(st),
		detail:     newDetailPane(),
		filterOpen: true,
		focus:      paneList,
		sel:        newSelection(),
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

// setRows installs a new set of rows, keeping the user's place in them: the
// cursor follows its torrent by ID, marks on torrents that are gone are dropped,
// and the sidebar's counts are rebuilt.
func (w torrentsWindow) setRows(rows []transmission.Torrent, icons Icons) torrentsWindow {
	prev, had := w.sel.row(w.rows)

	w.rows = rows
	w.sel = w.sel.track(prev.ID, had, rows)
	w.filter = w.filter.rebuild(rows)
	w = w.sync(icons)
	w.list.table.SetCursor(w.sel.cursor)

	return w
}

// resize lays the panes out for a frame.
func (w torrentsWindow) resize(l layout) torrentsWindow {
	w.list = w.list.resize(l.list)

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
	top := w.list.view(st, l.list, w.focus == paneList, ph)

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
