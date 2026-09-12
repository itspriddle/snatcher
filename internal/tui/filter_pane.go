package tui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// filterPane is the torrents window's left sidebar: a sectioned list of filter
// groups with counts, and a cursor that skips everything unselectable.
//
// It is hand-rolled rather than built on bubbles/v2/list. The list component's
// value is its fuzzy filter, pagination, and status line, none of which a
// fixed-length sectioned index wants; what it would cost is its keymap, which
// binds q, esc, h, l, u, d, f, and b — seven keys that mean something else in
// vision.md — plus a status line and a paginator to style away. Cursor
// arithmetic is the cheaper half of that trade. See docs/TUI-PLAN.md's
// component table.
type filterPane struct {
	// items are the rows, headings included, in display order.
	items []filterItem

	// cursor indexes items. It never rests on an unselectable row.
	cursor int

	// applied is the index of the item whose filter is in force, or -1 for
	// none. T1 records and marks it; T5 makes it filter the table.
	applied int
}

// itemKind distinguishes the three sorts of row the sidebar draws.
type itemKind int

const (
	// itemHeading is a section heading: not selectable, styled as a title.
	itemHeading itemKind = iota

	// itemFilter is a selectable filter with a count.
	itemFilter

	// itemNote is unselectable placeholder text for a section with no content
	// yet. It is styled muted rather than as a heading, because it is neither.
	itemNote
)

// filterItem is one row of the sidebar.
type filterItem struct {
	// label is the displayed text.
	label string

	// count is how many torrents the filter matches. Only itemFilter shows it.
	count int

	// kind decides whether the row is selectable and how it is styled.
	kind itemKind

	// filter is the domain filter this row applies. Label rows have none: a
	// label is not a transmission.Filter, and T5 gives them their own path.
	filter transmission.Filter
}

// selectable reports whether the cursor may rest on this row.
func (i filterItem) selectable() bool {
	return i.kind == itemFilter
}

// statusFilters are the status section's rows, in display order. The vocabulary
// is transmission's own — the values --filter takes and FilterByStatus
// understands — rather than a second, TUI-only set of names. vision.md sketches
// the sidebar with labels like "Completed" and "Waiting"; those would be a
// vocabulary the CLI cannot express, and the parity rule says the TUI and the
// CLI name a selection the same way.
var statusFilters = []transmission.Filter{
	transmission.FilterAll,
	transmission.FilterDownloading,
	transmission.FilterSeeding,
	transmission.FilterActive,
	transmission.FilterStopped,
	transmission.FilterVerifying,
	transmission.FilterQueuedToDownload,
	transmission.FilterQueuedToSeed,
	transmission.FilterQueuedToVerify,
	transmission.FilterError,
}

// noLabel is the label section's catch-all row, for torrents carrying none.
const noLabel = "No label"

// newFilterPane builds the sidebar for a set of torrents: the status section
// with live counts, a label section derived from the rows themselves, and a
// tracker section that stays a placeholder until tracker data lands in T5.
func newFilterPane(rows []transmission.Torrent) filterPane {
	p := filterPane{items: filterItems(rows), applied: -1}

	return p.top()
}

// filterItems builds the sidebar's rows for a set of torrents.
func filterItems(rows []transmission.Torrent) []filterItem {
	items := []filterItem{{label: "Status", kind: itemHeading}}

	for _, f := range statusFilters {
		items = append(items, filterItem{
			label:  filterLabel(f),
			count:  len(transmission.FilterByStatus(rows, f)),
			kind:   itemFilter,
			filter: f,
		})
	}

	items = append(items, filterItem{label: "Labels", kind: itemHeading})
	items = append(items, labelItems(rows)...)
	items = append(items, filterItem{label: "Trackers", kind: itemHeading})
	items = append(items, filterItem{label: "(none yet)", kind: itemNote})

	return items
}

// rebuild recomputes the sidebar for a new set of torrents. The cursor and the
// applied filter are carried by what they name rather than by index, because
// the label section grows and shrinks with the torrents; one whose row is gone
// falls back to the top and to no filter.
func (p filterPane) rebuild(rows []transmission.Torrent) filterPane {
	cursor := p.current()

	var applied filterItem

	hasApplied := p.applied >= 0 && p.applied < len(p.items)
	if hasApplied {
		applied = p.items[p.applied]
	}

	next := filterPane{items: filterItems(rows), applied: -1}.top()

	for i, item := range next.items {
		if !item.selectable() {
			continue
		}

		if item.sameAs(cursor) {
			next.cursor = i
		}

		if hasApplied && item.sameAs(applied) {
			next.applied = i
		}
	}

	return next
}

// sameAs reports whether two rows name the same filter. The label alone is not
// enough: a torrent label could be spelled like a status row.
func (i filterItem) sameAs(other filterItem) bool {
	return i.kind == other.kind && i.label == other.label && i.filter == other.filter
}

// filterLabel is how a domain filter is spelled in the sidebar: its own name,
// capitalized, with hyphens opened out.
func filterLabel(f transmission.Filter) string {
	name := strings.ReplaceAll(string(f), "-", " ")

	return strings.ToUpper(name[:1]) + name[1:]
}

// labelItems builds the label section from the torrents themselves, sorted, with
// a catch-all row for torrents carrying no label. Labels are server-derived, so
// they are sanitized here rather than wherever they are eventually drawn.
func labelItems(rows []transmission.Torrent) []filterItem {
	counts := map[string]int{}
	unlabeled := 0

	for _, t := range rows {
		if len(t.Labels) == 0 {
			unlabeled++

			continue
		}

		for _, l := range t.Labels {
			counts[output.SanitizeTerminal(l)]++
		}
	}

	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}

	slices.Sort(names)

	items := make([]filterItem, 0, len(names)+1)
	for _, name := range names {
		items = append(items, filterItem{label: name, count: counts[name], kind: itemFilter})
	}

	return append(items, filterItem{label: noLabel, count: unlabeled, kind: itemFilter})
}

// move steps the cursor by delta rows, skipping unselectable rows and stopping
// at the ends rather than wrapping.
func (p filterPane) move(delta int) filterPane {
	step := 1
	if delta < 0 {
		step = -1
	}

	for range abs(delta) {
		next := p.cursor + step
		for next >= 0 && next < len(p.items) && !p.items[next].selectable() {
			next += step
		}

		if next < 0 || next >= len(p.items) {
			break
		}

		p.cursor = next
	}

	return p
}

// top and bottom jump the cursor to the first and last selectable rows.
func (p filterPane) top() filterPane {
	for i, item := range p.items {
		if item.selectable() {
			p.cursor = i

			break
		}
	}

	return p
}

func (p filterPane) bottom() filterPane {
	for i := len(p.items) - 1; i >= 0; i-- {
		if p.items[i].selectable() {
			p.cursor = i

			break
		}
	}

	return p
}

// current returns the item under the cursor.
func (p filterPane) current() filterItem {
	if p.cursor < 0 || p.cursor >= len(p.items) {
		return filterItem{}
	}

	return p.items[p.cursor]
}

// apply records the cursor row as the filter in force and reports it. The caller
// clears marks: applying a filter can hide a marked torrent, and a mark acted on
// invisibly is exactly what the mark model exists to prevent.
func (p filterPane) apply() (filterPane, filterItem) {
	p.applied = p.cursor

	return p, p.current()
}

// view renders the sidebar into r, focused or not.
//
// A focused filter pane gets the loud border. vision.md is explicit about why:
// s/p/v/d here act on every torrent in the group, and focus alone must not be
// the only thing standing between one torrent and four hundred.
func (p filterPane) view(st Styles, r rect, focused bool) string {
	inner := r.inner()

	lines := make([]string, 0, len(p.items))
	for i, item := range p.items {
		lines = append(lines, p.itemView(st, item, i, inner.w))
	}

	return paneBox(st, paneFilter, focused).
		Width(r.w).
		Height(r.h).
		Render(paneContent(lines, r))
}

// itemView renders one sidebar row into exactly width cells.
func (p filterPane) itemView(st Styles, item filterItem, i, width int) string {
	switch item.kind {
	case itemHeading:
		return st.Title.Render(truncate(item.label, width))
	case itemNote:
		return st.Muted.Render(truncate("  "+item.label, width))
	case itemFilter:
	}

	marker := " "
	if i == p.applied {
		marker = ">"
	}

	line := pad(marker+" "+item.label, strconv.Itoa(item.count), width)

	if i == p.cursor {
		return st.Cursor.Render(line)
	}

	return line
}

// abs is the absolute value of an int.
func abs(v int) int {
	if v < 0 {
		return -v
	}

	return v
}
