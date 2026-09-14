package tui

import (
	"github.com/itspriddle/snatcher/internal/transmission"
)

// sortOverlayKind names the sort modal.
const sortOverlayKind = "sort"

// sortMaxWidth caps the sort modal, as confirmMaxWidth caps the confirm. It is
// as wide as the confirm rather than as wide as its rows, so the help line can
// say how to apply.
const sortMaxWidth = 60

// sortFields are the sort modal's rows: the fields --sort takes, in the order
// transmission lists them.
var sortFields = []transmission.SortField{
	transmission.SortQueue,
	transmission.SortName,
	transmission.SortAdded,
	transmission.SortSize,
	transmission.SortProgress,
	transmission.SortRatio,
	transmission.SortSpeed,
}

// sortOverlay is o: pick a sort field and a direction. It is sort and nothing
// else, per vision.md; applying it writes client.list_sort and
// client.list_reverse, so `snatcher list` sorts the same way.
//
// It is modal. Its stack is ctxSort alone, so q and the window keys do nothing
// while it is open, and ctrl+c still quits.
type sortOverlay struct {
	// cursor indexes sortFields.
	cursor int

	// current is the field in force when the modal opened.
	current transmission.SortField

	// reverse is the direction the modal will apply.
	reverse bool
}

// newSortOverlay builds the modal over a view, with the cursor on its field.
func newSortOverlay(v listView) sortOverlay {
	o := sortOverlay{current: v.sort, reverse: v.reverse}

	for i, f := range sortFields {
		if f == v.sort {
			o.cursor = i
		}
	}

	return o
}

// kind implements overlay.
func (sortOverlay) kind() string { return sortOverlayKind }

// contexts implements overlay.
func (sortOverlay) contexts() []Context { return []Context{ctxSort} }

// move steps the cursor, stopping at the ends.
func (o sortOverlay) move(delta int) sortOverlay {
	o.cursor = clamp(o.cursor+delta, 0, len(sortFields)-1)

	return o
}

// flipReverse is r.
func (o sortOverlay) flipReverse() sortOverlay {
	o.reverse = !o.reverse

	return o
}

// view implements overlay.
func (o sortOverlay) view(m Model, w, h int) string {
	width := min(overlayContentWidth(w), sortMaxWidth)

	rows := make([]string, 0, len(sortFields))

	for i, f := range sortFields {
		label := string(f)
		if f == o.current {
			label += "  (current)"
		}

		rows = append(rows, cursorRow(m.styles, i == o.cursor, label, width))
	}

	return m.modalBox(modal{
		title:  "Sort torrents",
		rows:   rows,
		focus:  o.cursor,
		footer: []string{padLine("  reverse: "+onOff(o.reverse), width)},
		ctx:    ctxSort,
		width:  width,
	}, h)
}

// onOff spells a switch.
func onOff(on bool) string {
	if on {
		return "on"
	}

	return "off"
}

// toggleSort is o: open the modal, or close it when it is the overlay on top.
func (m Model) toggleSort() Model {
	if top, ok := m.topOverlay(); ok && top.kind() == sortOverlayKind {
		return m.pop()
	}

	return m.push(newSortOverlay(m.torrents.listView))
}

// applySort is enter in the modal: install the sort, then persist whichever of
// the two keys changed. The session value applies whether or not the write
// succeeds.
func (m Model) applySort() Model {
	o, ok := topAs[sortOverlay](m)
	if !ok {
		return m
	}

	m = m.pop()

	prev := m.torrents.listView
	v := prev
	v.sort, v.reverse = sortFields[o.cursor], o.reverse

	m.torrents = m.torrents.setView(v, m.icons)
	m.toast = "sort: " + string(v.sort)

	if v.reverse {
		m.toast += ", reversed"
	}

	if v.sort != prev.sort {
		m = m.persistPref(clientKeyListSort, string(v.sort))
	}

	if v.reverse != prev.reverse {
		m = m.persistPref(clientKeyListReverse, v.reverse)
	}

	return m
}
