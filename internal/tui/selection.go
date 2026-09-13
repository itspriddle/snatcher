package tui

import (
	"cmp"
	"slices"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// rowKey names what identifies a row across a refresh of the rows it lives in.
// It is a type parameter rather than a func field, so two selections compare
// equal when their state does — a func value never compares equal to another.
type rowKey[T any, K cmp.Ordered] interface {
	key(i int, row T) K
}

// torrentID keys a torrent by its ID, which survives a sort, a filter, and a
// poll.
type torrentID struct{}

func (torrentID) key(_ int, t transmission.Torrent) int64 { return t.ID }

// fileIndex keys a file by its position in the torrent's file list: exactly the
// index space transmission.FileChanges addresses, and stable within a torrent.
type fileIndex struct{}

func (fileIndex) key(i int, _ transmission.File) int { return i }

// selection is the one selection model, per the cross-cutting rule in
// docs/tui-todo.md: a marked set plus a cursor, owned by whoever draws the rows,
// that every action consumes. Nothing reaches for "the current row" on its own.
// The torrent list and the detail pane's files tab are both one.
//
// The rule it encodes is vision.md's: an action operates on the marked set if
// any marks exist, and on the cursor row otherwise. That is what makes bulk
// actions explicit rather than focus-dependent, and it renders to a
// transmission.Selector (or a file index set) so every TUI action has an exact
// CLI equivalent.
type selection[T any, K cmp.Ordered, R rowKey[T, K]] struct {
	// cursor is the index of the cursor row in the currently rendered rows.
	cursor int

	// active reports whether the cursor names a row at all. A cursor can be
	// deselected (esc ladder rung "deselect cursor") without the detail pane
	// closing, which is a different state from "cursor at row 0".
	active bool

	// marks are the marked rows, keyed by R rather than by row index so a
	// re-sort, a filter, or a refresh cannot silently move a mark to a
	// different row.
	marks map[K]struct{}
}

// torrentSelection is the torrent list's selection, and fileSelection the files
// tab's.
type (
	torrentSelection = selection[transmission.Torrent, int64, torrentID]
	fileSelection    = selection[transmission.File, int, fileIndex]
)

// newSelection returns a selection with the cursor on the first row.
func newSelection[T any, K cmp.Ordered, R rowKey[T, K]]() selection[T, K, R] {
	return selection[T, K, R]{active: true, marks: map[K]struct{}{}}
}

// newTorrentSelection and newFileSelection are the two selections the TUI keeps.
func newTorrentSelection() torrentSelection { return newSelection[transmission.Torrent, int64, torrentID]() }

func newFileSelection() fileSelection { return newSelection[transmission.File, int, fileIndex]() }

// keyOf is a row's key.
func (s selection[T, K, R]) keyOf(i int, row T) K {
	var r R

	return r.key(i, row)
}

// keys is what an action acts on: the marks, sorted, when there are any, and the
// cursor row's key otherwise. An inactive or out-of-range cursor with no marks
// yields nothing — never "every row", which is a thing a user asks for, not
// something a selection decays into.
func (s selection[T, K, R]) keys(rows []T) []K {
	if len(s.marks) > 0 {
		keys := make([]K, 0, len(s.marks))
		for k := range s.marks {
			keys = append(keys, k)
		}

		slices.Sort(keys)

		return keys
	}

	if row, ok := s.row(rows); ok {
		return []K{s.keyOf(s.cursor, row)}
	}

	return nil
}

// torrentSelector renders the list's selection to the same transmission.Selector
// every CLI mutation command takes. rows is the currently rendered list, which is
// what makes the cursor meaningful. It never sets All.
func torrentSelector(sel torrentSelection, rows []transmission.Torrent) transmission.Selector {
	ids := sel.keys(rows)
	if len(ids) == 0 {
		return transmission.Selector{}
	}

	return transmission.Selector{IDs: ids}
}

// listSelector is the torrent list's selection as a Selector.
func (m Model) listSelector() transmission.Selector {
	return torrentSelector(m.torrents.sel, m.torrents.rows)
}

// row returns the row under the cursor, and whether there is one.
func (s selection[T, K, R]) row(rows []T) (T, bool) {
	if !s.active || s.cursor < 0 || s.cursor >= len(rows) {
		var zero T

		return zero, false
	}

	return rows[s.cursor], true
}

// toggleMark marks or unmarks the row under the cursor. It does not move the
// cursor: vision.md binds space to "toggle a mark on the cursor row", and a
// cursor that advanced on its own would make marking a run of rows depend on
// where the run started.
func (s selection[T, K, R]) toggleMark(rows []T) selection[T, K, R] {
	row, ok := s.row(rows)
	if !ok {
		return s
	}

	k := s.keyOf(s.cursor, row)

	marks := make(map[K]struct{}, len(s.marks)+1)
	for m := range s.marks {
		marks[m] = struct{}{}
	}

	if _, marked := marks[k]; marked {
		delete(marks, k)
	} else {
		marks[k] = struct{}{}
	}

	s.marks = marks

	return s
}

// clearMarks drops every mark. It is an esc ladder rung, and it also runs
// whenever the list's filter changes: a mark hidden by a filter would otherwise
// be acted on invisibly.
func (s selection[T, K, R]) clearMarks() selection[T, K, R] {
	s.marks = map[K]struct{}{}

	return s
}

// isMarked reports whether a key is marked.
func (s selection[T, K, R]) isMarked(k K) bool {
	_, ok := s.marks[k]

	return ok
}

// markCount is how many rows are marked. The status bar shows it only when it
// is non-zero, which is the only tell a user has that an action is about to hit
// more than the row they are looking at.
func (s selection[T, K, R]) markCount() int {
	return len(s.marks)
}

// setCursor moves the cursor to an index and reactivates it. It is called with
// the focused table's own cursor after a nav key is forwarded, so the table and
// the selection cannot disagree about which row is current.
func (s selection[T, K, R]) setCursor(i int) selection[T, K, R] {
	s.cursor = i
	s.active = true

	return s
}

// deselect clears the cursor without touching marks; the detail pane stays open
// and goes empty.
func (s selection[T, K, R]) deselect() selection[T, K, R] {
	s.active = false

	return s
}

// track carries the selection across a refresh of the rows. The cursor follows
// the row it was on, by key, and stays at its index, clamped, when that row is
// gone. Marks on rows that are gone are dropped: a mark nothing on screen
// carries would be acted on invisibly.
func (s selection[T, K, R]) track(prev K, had bool, rows []T) selection[T, K, R] {
	present := make(map[K]struct{}, len(rows))

	for i, row := range rows {
		k := s.keyOf(i, row)
		present[k] = struct{}{}

		if had && k == prev {
			s.cursor = i
		}
	}

	s.cursor = clamp(s.cursor, 0, max(0, len(rows)-1))

	marks := make(map[K]struct{}, len(s.marks))
	for k := range s.marks {
		if _, ok := present[k]; ok {
			marks[k] = struct{}{}
		}
	}

	s.marks = marks

	return s
}
