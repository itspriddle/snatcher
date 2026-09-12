package tui

import (
	"slices"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// selection is the one selection model, per the cross-cutting rule in
// docs/tui-todo.md: a marked set plus a cursor, owned by the window, that every
// action consumes. Nothing reaches for "the current row" on its own.
//
// The rule it encodes is vision.md's: an action operates on the marked set if
// any marks exist, and on the cursor row otherwise. That is what makes bulk
// actions explicit rather than focus-dependent, and it renders to a
// transmission.Selector so every TUI action has an exact CLI equivalent.
type selection struct {
	// cursor is the index of the cursor row in the currently rendered rows.
	cursor int

	// active reports whether the cursor names a row at all. A cursor can be
	// deselected (esc ladder rung 6) without the detail pane closing, which is
	// a different state from "cursor at row 0".
	active bool

	// marks are the marked torrents, keyed by torrent ID rather than by row
	// index so a re-sort, a filter, or a refresh cannot silently move a mark to
	// a different torrent.
	marks map[int64]struct{}
}

// newSelection returns a selection with the cursor on the first row.
func newSelection() selection {
	return selection{active: true, marks: map[int64]struct{}{}}
}

// Selector renders the selection to the same transmission.Selector every CLI
// mutation command takes. rows is the currently rendered list, which is what
// makes the cursor meaningful.
//
// Marks win over the cursor, and an inactive cursor with no marks yields the
// zero Selector — targeting nothing. It deliberately never returns All: "every
// torrent on the server" is a thing a user asks for with a filter group action
// or the palette, never something a selection falls back to.
func (s selection) Selector(rows []transmission.Torrent) transmission.Selector {
	if len(s.marks) > 0 {
		ids := make([]int64, 0, len(s.marks))
		for id := range s.marks {
			ids = append(ids, id)
		}

		slices.Sort(ids)

		return transmission.Selector{IDs: ids}
	}

	if row, ok := s.row(rows); ok {
		return transmission.Selector{IDs: []int64{row.ID}}
	}

	return transmission.Selector{}
}

// row returns the torrent under the cursor, and whether there is one.
func (s selection) row(rows []transmission.Torrent) (transmission.Torrent, bool) {
	if !s.active || s.cursor < 0 || s.cursor >= len(rows) {
		return transmission.Torrent{}, false
	}

	return rows[s.cursor], true
}

// toggleMark marks or unmarks the torrent under the cursor. It does not move the
// cursor: vision.md binds space to "toggle a mark on the cursor row", and a
// cursor that advanced on its own would make marking a run of rows depend on
// where the run started.
func (s selection) toggleMark(rows []transmission.Torrent) selection {
	row, ok := s.row(rows)
	if !ok {
		return s
	}

	marks := make(map[int64]struct{}, len(s.marks)+1)
	for id := range s.marks {
		marks[id] = struct{}{}
	}

	if _, marked := marks[row.ID]; marked {
		delete(marks, row.ID)
	} else {
		marks[row.ID] = struct{}{}
	}

	s.marks = marks

	return s
}

// clearMarks drops every mark. It is esc ladder rung 5, and it also runs
// whenever the filter changes: a mark hidden by a filter would otherwise be
// acted on invisibly.
func (s selection) clearMarks() selection {
	s.marks = map[int64]struct{}{}

	return s
}

// isMarked reports whether a torrent ID is marked.
func (s selection) isMarked(id int64) bool {
	_, ok := s.marks[id]

	return ok
}

// markCount is how many torrents are marked. The status bar shows it only when
// it is non-zero, which is the only tell a user has that an action is about to
// hit more than the row they are looking at.
func (s selection) markCount() int {
	return len(s.marks)
}

// setCursor moves the cursor to an index and reactivates it. It is called with
// the focused table's own cursor after a nav key is forwarded, so the table and
// the selection cannot disagree about which row is current.
func (s selection) setCursor(i int) selection {
	s.cursor = i
	s.active = true

	return s
}

// deselect clears the cursor without touching marks. It is esc ladder rung 6;
// the detail pane stays open and goes empty.
func (s selection) deselect() selection {
	s.active = false

	return s
}
