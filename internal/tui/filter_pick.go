package tui

import (
	"strconv"

	"github.com/itspriddle/snatcher/internal/output"
)

// filterPickKind names the filter modal.
const filterPickKind = "filter"

// filterPickMaxWidth caps the filter modal, as confirmMaxWidth caps the confirm.
const filterPickMaxWidth = 60

// filterPickOverlay is f: the sidebar's rows in a modal, so a filter can be
// applied with the sidebar closed. It is the sidebar's own pane underneath —
// the same rows, counts, and cursor rules — rather than a second list that
// could disagree with it.
//
// It is modal, like the sort and columns modals.
type filterPickOverlay struct {
	pane filterPane
}

// newFilterPick builds the modal from the sidebar, with the cursor on the filter
// in force, or on the first row when there is none.
func newFilterPick(p filterPane) filterPickOverlay {
	pick := filterPane{items: p.items, applied: p.applied}.top()

	if p.applied >= 0 && p.applied < len(p.items) {
		pick.cursor = p.applied
	}

	return filterPickOverlay{pane: pick}
}

// kind implements overlay.
func (filterPickOverlay) kind() string { return filterPickKind }

// contexts implements overlay.
func (filterPickOverlay) contexts() []Context { return []Context{ctxFilterPick} }

// move steps the cursor over the selectable rows.
func (o filterPickOverlay) move(delta int) filterPickOverlay {
	o.pane = o.pane.move(delta)

	return o
}

// view implements overlay.
func (o filterPickOverlay) view(m Model, w, h int) string {
	width := min(overlayContentWidth(w), filterPickMaxWidth)

	rows := make([]string, 0, len(o.pane.items))

	for i, item := range o.pane.items {
		rows = append(rows, o.row(m.styles, item, i, width))
	}

	return m.modalBox(modal{title: "Filter torrents", rows: rows, focus: o.pane.cursor, ctx: ctxFilterPick, width: width}, h)
}

// row draws one of the modal's rows in exactly width cells.
func (o filterPickOverlay) row(st Styles, item filterItem, i, width int) string {
	switch item.kind {
	case itemHeading:
		return st.Title.Render(padLine(item.label, width))
	case itemNote:
		return st.Muted.Render(padLine("    "+item.label, width))
	case itemFilter:
	}

	label := item.label
	if i == o.pane.applied {
		label += "  (current)"
	}

	return cursorRow(st, i == o.pane.cursor, pad(label, strconv.Itoa(item.count), width-2), width)
}

// toggleFilterPick is f: open the modal, or close it when it is the overlay on
// top.
func (m Model) toggleFilterPick() Model {
	if top, ok := m.topOverlay(); ok && top.kind() == filterPickKind {
		return m.pop()
	}

	return m.push(newFilterPick(m.torrents.filter))
}

// applyFilterPick is enter in the modal.
func (m Model) applyFilterPick() Model {
	o, ok := topAs[filterPickOverlay](m)
	if !ok {
		return m
	}

	return m.pop().applyFilterItem(o.pane.current())
}

// text is the filter as the footer and the toast name it: a status as the
// sidebar spells it, a label sanitized, or "no label".
func (f viewFilter) text() string {
	switch f.kind {
	case filterByStatus:
		return filterLabel(f.status)
	case filterByLabel:
		if f.label == "" {
			return "no label"
		}

		return "label " + output.SanitizeTerminal(f.label)
	case filterByNone:
	}

	return ""
}

// set reports whether a filter is in force.
func (f viewFilter) set() bool {
	return f.kind != filterByNone
}
