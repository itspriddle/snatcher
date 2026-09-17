package tui

import (
	"cmp"
	"slices"
	"strings"

	"github.com/itspriddle/snatcher/internal/prowlarr"
)

// releaseSortField names a way to order the results.
type releaseSortField string

// The release sort fields. releaseSortServer is Prowlarr's own order, which is
// the order `snatcher search` prints and the one a window starts in.
const (
	releaseSortServer   releaseSortField = "server"
	releaseSortAge      releaseSortField = "age"
	releaseSortTitle    releaseSortField = "title"
	releaseSortIndexer  releaseSortField = "indexer"
	releaseSortSize     releaseSortField = "size"
	releaseSortGrabs    releaseSortField = "grabs"
	releaseSortSeeders  releaseSortField = "seeders"
	releaseSortLeechers releaseSortField = "leechers"
)

// releaseSortFields are the release sort modal's rows.
var releaseSortFields = []releaseSortField{
	releaseSortServer, releaseSortAge, releaseSortTitle, releaseSortIndexer,
	releaseSortSize, releaseSortGrabs, releaseSortSeeders, releaseSortLeechers,
}

// releaseSort is the order the results are shown in. It is session-only.
type releaseSort struct {
	field   releaseSortField
	reverse bool
}

// apply orders a copy of releases. The sort is stable, so releases that tie keep
// the server's order, and a count the indexer does not publish sorts last in
// either direction: a dash is not a small number.
func (s releaseSort) apply(releases []prowlarr.Release) []prowlarr.Release {
	out := slices.Clone(releases)

	if s.field == releaseSortServer || s.field == "" {
		if s.reverse {
			slices.Reverse(out)
		}

		return out
	}

	slices.SortStableFunc(out, func(a, b prowlarr.Release) int {
		if c, ok := nilsLast(s.count(a), s.count(b)); ok {
			return c
		}

		c := s.compare(a, b)
		if s.reverse {
			return -c
		}

		return c
	})

	return out
}

// count is the nullable count a field sorts by, or nil for a field that is not
// one.
func (s releaseSort) count(r prowlarr.Release) *int {
	switch s.field {
	case releaseSortGrabs:
		return r.Grabs
	case releaseSortSeeders:
		return r.Seeders
	case releaseSortLeechers:
		return r.Leechers
	case releaseSortServer, releaseSortAge, releaseSortTitle, releaseSortIndexer, releaseSortSize:
	}

	return nil
}

// nilsLast orders two counts when either is missing, and reports whether it
// did. A field that is not a count has neither, and falls through.
func nilsLast(a, b *int) (int, bool) {
	switch {
	case a == nil && b == nil:
		return 0, false
	case a == nil:
		return 1, true
	case b == nil:
		return -1, true
	}

	return 0, false
}

// compare orders two releases by the field, ascending.
func (s releaseSort) compare(a, b prowlarr.Release) int {
	switch s.field {
	case releaseSortAge:
		return cmp.Compare(a.Age, b.Age)
	case releaseSortTitle:
		return cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
	case releaseSortIndexer:
		return cmp.Compare(strings.ToLower(a.Indexer), strings.ToLower(b.Indexer))
	case releaseSortSize:
		return cmp.Compare(a.Size, b.Size)
	case releaseSortGrabs, releaseSortSeeders, releaseSortLeechers:
		if a, b := s.count(a), s.count(b); a != nil && b != nil {
			return cmp.Compare(*a, *b)
		}
	case releaseSortServer:
	}

	return 0
}

// releaseSortOverlayKind names the release sort modal.
const releaseSortOverlayKind = "release-sort"

// releaseSortOverlay is o in the Prowlarr window: the torrent sort modal's shape
// over the release fields.
type releaseSortOverlay struct {
	cursor  int
	current releaseSortField
	reverse bool
}

// newReleaseSortOverlay builds the modal with the cursor on the sort in force.
func newReleaseSortOverlay(s releaseSort) releaseSortOverlay {
	o := releaseSortOverlay{current: s.field, reverse: s.reverse}

	for i, f := range releaseSortFields {
		if f == s.field {
			o.cursor = i
		}
	}

	return o
}

// kind implements overlay.
func (releaseSortOverlay) kind() string { return releaseSortOverlayKind }

// contexts implements overlay.
func (releaseSortOverlay) contexts() []Context { return []Context{ctxReleaseSort} }

// move steps the cursor, stopping at the ends.
func (o releaseSortOverlay) move(delta int) releaseSortOverlay {
	o.cursor = clamp(o.cursor+delta, 0, len(releaseSortFields)-1)

	return o
}

// flipReverse is r.
func (o releaseSortOverlay) flipReverse() releaseSortOverlay {
	o.reverse = !o.reverse

	return o
}

// view implements overlay.
func (o releaseSortOverlay) view(m Model, w, h int) string {
	return m.modalBox(o.modal(m, w, h), h)
}

// modal implements modalOverlay.
func (o releaseSortOverlay) modal(m Model, w, _ int) modal {
	width := min(overlayContentWidth(w), sortMaxWidth)

	rows := make([]string, 0, len(releaseSortFields))

	for i, f := range releaseSortFields {
		label := string(f)
		if f == releaseSortServer {
			label += " order"
		}

		if f == o.current {
			label += "  (current)"
		}

		rows = append(rows, cursorRow(m.styles, i == o.cursor, label, width))
	}

	return modal{
		title:  "Sort releases",
		rows:   rows,
		focus:  o.cursor,
		footer: []string{padLine("  reverse: "+onOff(o.reverse), width)},
		ctx:    ctxReleaseSort,
		width:  width,
	}
}

// pointAt implements modalOverlay.
func (o releaseSortOverlay) pointAt(i int) (overlay, bool) {
	if i < 0 || i >= len(releaseSortFields) {
		return o, false
	}

	o.cursor = i

	return o, true
}

// clickAction implements modalOverlay: a click applies the field, as enter does.
func (releaseSortOverlay) clickAction() Action { return actionReleaseSortSelect }

// wheelActions implements modalOverlay.
func (releaseSortOverlay) wheelActions() (up, down Action) {
	return actionReleaseSortUp, actionReleaseSortDown
}

// toggleReleaseSort is o: open the modal, or close it when it is on top.
func (m Model) toggleReleaseSort() Model {
	if top, ok := m.topOverlay(); ok && top.kind() == releaseSortOverlayKind {
		return m.pop()
	}

	return m.push(newReleaseSortOverlay(m.prowlarr.sort))
}

// applyReleaseSort is enter in the modal.
func (m Model) applyReleaseSort() Model {
	o, ok := topAs[releaseSortOverlay](m)
	if !ok {
		return m
	}

	return m.pop().setReleaseSort(releaseSort{field: releaseSortFields[o.cursor], reverse: o.reverse})
}

// setReleaseSort installs a results sort. The modal and a click on a results
// heading both land here.
func (m Model) setReleaseSort(s releaseSort) Model {
	m.prowlarr = m.prowlarr.setSort(s)
	m.toast = "sort: " + string(s.field)

	if s.reverse {
		m.toast += ", reversed"
	}

	return m
}

// sortReleasesByColumn is a click on a results heading. Every results column is
// named for the sort field that orders it, so the heading is the field; the same
// heading again flips the direction.
func (m Model) sortReleasesByColumn(col int) Model {
	cols := m.prowlarr.cols
	if col < 0 || col >= len(cols) {
		return m
	}

	field := releaseSortField(cols[col].key)
	if !slices.Contains(releaseSortFields, field) || field == releaseSortServer {
		m.toast = "no sort for " + cols[col].key

		return m
	}

	current := m.prowlarr.sort

	return m.setReleaseSort(releaseSort{field: field, reverse: field == current.field && !current.reverse})
}

// releaseSortHandlers are o and the modal's own keys.
func releaseSortHandlers() map[Action]handler {
	return map[Action]handler{
		actionReleaseSort:        plain(Model.toggleReleaseSort),
		actionReleaseSortUp:      stepTop(func(o releaseSortOverlay) releaseSortOverlay { return o.move(-1) }),
		actionReleaseSortDown:    stepTop(func(o releaseSortOverlay) releaseSortOverlay { return o.move(1) }),
		actionReleaseSortReverse: stepTop(releaseSortOverlay.flipReverse),
		actionReleaseSortSelect:  plain(Model.applyReleaseSort),
	}
}
