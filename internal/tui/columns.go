package tui

import (
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// listColumn describes one column of the torrent table: the CLI field key that
// names it, its heading, its width, its alignment, and how a torrent renders
// into it.
type listColumn struct {
	// key is the CLI --fields key, or empty for the two fixed prefix cells.
	key string

	// title is the column heading.
	title string

	// width is the column's width in cells, excluding the table's own cell
	// padding. Zero means flex: the column takes whatever the others leave. Only
	// name flexes.
	width int

	// right right-aligns the cell. bubbles/v2/table has no alignment, so the
	// cell is left-padded to width when the row is built — which is safe only
	// because every non-flex width is fixed before rows are built.
	right bool

	// render turns a torrent into the cell's text. Every server-derived string
	// it returns must already be sanitized.
	render func(t transmission.Torrent, icons Icons) string
}

// nameKey is the one flex column.
const nameKey = "name"

// prefixColumns are the mark and status-glyph cells every row starts with,
// whatever columns the user chose. Both are one cell wide, so the ASCII and Nerd
// Font glyph sets cannot move any column to their right.
var prefixColumns = []listColumn{
	{title: " ", width: 1, render: func(_ transmission.Torrent, _ Icons) string { return "" }},
	{title: " ", width: 1, render: func(t transmission.Torrent, i Icons) string { return i.status(t.Status) }},
}

// The prefix cells' indices. The mark cell is filled from the selection rather
// than from its render func.
const (
	markColumn = iota
	glyphColumn
)

// columnDefs mirrors the CLI's listFields (internal/commands/list.go): the same
// keys, headings, and renderers, so `c` in the TUI and --fields in `snatcher
// list` name and draw a column the same way. internal/tui does not import
// internal/commands, so it is a copy pinned by TestColumnKeysMirrorTheCLI rather
// than a shared registry.
var columnDefs = []listColumn{
	{key: "id", title: "ID", width: 4, right: true, render: func(t transmission.Torrent, _ Icons) string {
		return strconv.FormatInt(t.ID, 10)
	}},
	{key: "hash", title: "Hash", width: 40, render: func(t transmission.Torrent, _ Icons) string { return t.Hash }},
	{key: nameKey, title: "Name", render: func(t transmission.Torrent, _ Icons) string {
		return output.SanitizeTerminal(t.Name)
	}},
	{key: "status", title: "Status", width: 11, render: func(t transmission.Torrent, _ Icons) string {
		return t.Status.String()
	}},
	{key: "done", title: "Done", width: 4, right: true, render: func(t transmission.Torrent, _ Icons) string {
		return output.Percent(t.PercentDone)
	}},
	{key: "size", title: "Size", width: 9, right: true, render: func(t transmission.Torrent, _ Icons) string {
		return output.Bytes(t.TotalSize)
	}},
	{key: "down", title: "Down", width: 11, right: true, render: func(t transmission.Torrent, _ Icons) string {
		return output.Speed(t.RateDownload)
	}},
	{key: "up", title: "Up", width: 11, right: true, render: func(t transmission.Torrent, _ Icons) string {
		return output.Speed(t.RateUpload)
	}},
	{key: "ratio", title: "Ratio", width: 6, right: true, render: func(t transmission.Torrent, _ Icons) string {
		return output.Ratio(t.UploadRatio)
	}},
	{key: "eta", title: "ETA", width: 7, right: true, render: func(t transmission.Torrent, _ Icons) string {
		return output.ETA(t.ETA)
	}},
	{key: "peers", title: "Peers", width: 5, right: true, render: func(t transmission.Torrent, _ Icons) string {
		return strconv.FormatInt(t.PeersConnected, 10)
	}},
	{key: "added", title: "Added", width: 10, render: func(t transmission.Torrent, _ Icons) string {
		return output.Date(t.AddedDate)
	}},
	{key: "download_dir", title: "Download Dir", width: 24, render: func(t transmission.Torrent, _ Icons) string {
		return output.SanitizeTerminal(t.DownloadDir)
	}},
	{key: "error", title: "Error", width: 24, render: func(t transmission.Torrent, _ Icons) string {
		return output.SanitizeTerminal(t.Error)
	}},
	{key: "labels", title: "Labels", width: 16, render: func(t transmission.Torrent, _ Icons) string {
		return output.SanitizeTerminal(strings.Join(t.Labels, ","))
	}},
}

// allColumnKeys is every column key in registry order, and defaultColumnKeys the
// set shown when client.list_columns is unset. Both equal the CLI's
// allFieldKeys and defaultFieldKeys.
var (
	allColumnKeys     = []string{"id", "hash", "name", "status", "done", "size", "down", "up", "ratio", "eta", "peers", "added", "download_dir", "error", "labels"}
	defaultColumnKeys = []string{"id", "name", "status", "done", "size", "down", "up", "ratio", "eta"}
)

// columnDef looks a column up by key.
func columnDef(key string) (listColumn, bool) {
	for _, c := range columnDefs {
		if c.key == key {
			return c, true
		}
	}

	return listColumn{}, false
}

// columnsFor is the table's columns for a list of keys: the two prefix cells,
// then each known key in the order given. An unknown key is skipped rather than
// failing, because the keys arrive from a config file.
func columnsFor(keys []string) []listColumn {
	cols := append([]listColumn{}, prefixColumns...)

	for _, k := range keys {
		if c, ok := columnDef(k); ok {
			cols = append(cols, c)
		}
	}

	return cols
}

// cellText renders one torrent into one column, left-padded to the column's
// width when it is right-aligned.
func (c listColumn) cellText(t transmission.Torrent, icons Icons) string {
	return c.align(c.render(t, icons))
}

// align left-pads s to the column's width when the column is right-aligned. A
// value wider than the column is left for the table to truncate.
func (c listColumn) align(s string) string {
	if !c.right {
		return s
	}

	if gap := c.width - lipgloss.Width(s); gap > 0 {
		return strings.Repeat(" ", gap) + s
	}

	return s
}

// minNameWidth is the narrowest the name column is allowed to get before a
// column is dropped to feed it.
const minNameWidth = 16

// cellPadding is the trailing padding every table cell carries (Styles.Cell in
// newStyles). It is one rather than the component default's two because nine
// columns of padding on both sides costs eighteen cells, which is most of a
// torrent name.
const cellPadding = 1

// fitColumns sizes the columns for a pane of the given inner width: the name
// column takes whatever the others leave, and when that would squeeze it below
// minNameWidth — or, with no name column, when the columns overflow — the
// rightmost droppable column is dropped and the sum recomputed. The prefix cells
// and the name are never dropped.
//
// A dropped column is given a width of zero rather than removed. The component
// skips a zero-width column when rendering both headers and rows, so the rows
// built by setRows stay one cell per column however narrow the pane gets, and
// nothing has to rebuild them on a resize.
func fitColumns(cols []listColumn, width int) []table.Column {
	out := tableColumns(cols)
	name := -1

	for i, c := range cols {
		if c.key == nameKey {
			name = i
		}
	}

	for next := len(out) - 1; ; next-- {
		spent := 0

		for i, c := range out {
			if i != name && c.Width > 0 {
				spent += c.Width + cellPadding
			}
		}

		remaining := width - spent - cellPadding
		fits := remaining >= minNameWidth
		if name < 0 {
			fits = spent <= width
		}

		for next >= len(prefixColumns) && next == name {
			next--
		}

		if fits || next < len(prefixColumns) {
			if name >= 0 {
				out[name].Width = max(1, remaining)
			}

			return out
		}

		out[next].Width = 0
	}
}

// tableColumns converts the column descriptions into the component's own type. A
// right-aligned heading is padded like its cells, so it sits over them.
func tableColumns(cols []listColumn) []table.Column {
	out := make([]table.Column, 0, len(cols))

	for _, c := range cols {
		out = append(out, table.Column{Title: c.align(c.title), Width: c.width})
	}

	return out
}

// columnsOverlayKind names the columns modal.
const columnsOverlayKind = "columns"

// columnsMaxWidth caps the columns modal, wide enough for its help line.
const columnsMaxWidth = 60

// columnRow is one row of the columns modal: a column key and whether it is
// shown.
type columnRow struct {
	key string
	on  bool
}

// columnsOverlay is c: choose which columns the list shows, and in what order.
// Applying it writes client.list_columns, so `snatcher list` shows the same set.
//
// The rows are the shown columns first, in their order, then the rest in
// registry order. Reordering is part of the modal rather than left out, because
// client.list_columns is ordered: a checklist that wrote registry order would
// silently rewrite a user's "name,id".
type columnsOverlay struct {
	rows   []columnRow
	cursor int
}

// newColumnsOverlay builds the modal for the columns currently shown.
func newColumnsOverlay(keys []string) columnsOverlay {
	rows := make([]columnRow, 0, len(allColumnKeys))

	for _, k := range keys {
		rows = append(rows, columnRow{key: k, on: true})
	}

	for _, k := range allColumnKeys {
		if !slices.Contains(keys, k) {
			rows = append(rows, columnRow{key: k})
		}
	}

	return columnsOverlay{rows: rows}
}

// kind implements overlay.
func (columnsOverlay) kind() string { return columnsOverlayKind }

// contexts implements overlay.
func (columnsOverlay) contexts() []Context { return []Context{ctxColumns} }

// move steps the cursor, stopping at the ends.
func (o columnsOverlay) move(delta int) columnsOverlay {
	o.cursor = clamp(o.cursor+delta, 0, len(o.rows)-1)

	return o
}

// toggle shows or hides the cursor row's column.
func (o columnsOverlay) toggle() columnsOverlay {
	o.rows = slices.Clone(o.rows)
	o.rows[o.cursor].on = !o.rows[o.cursor].on

	return o
}

// swap moves the cursor row delta places, carrying the cursor with it and
// stopping at the ends.
func (o columnsOverlay) swap(delta int) columnsOverlay {
	next := o.cursor + delta
	if next < 0 || next >= len(o.rows) {
		return o
	}

	o.rows = slices.Clone(o.rows)
	o.rows[o.cursor], o.rows[next] = o.rows[next], o.rows[o.cursor]
	o.cursor = next

	return o
}

// selected is the shown column keys, in order.
func (o columnsOverlay) selected() []string {
	keys := make([]string, 0, len(o.rows))

	for _, r := range o.rows {
		if r.on {
			keys = append(keys, r.key)
		}
	}

	return keys
}

// view implements overlay. A row reads "[x] name  Name": the key as
// client.list_columns spells it, and the heading the table draws.
func (o columnsOverlay) view(m Model, w, h int) string {
	width := min(overlayContentWidth(w), columnsMaxWidth)

	keyW := 0
	for _, r := range o.rows {
		keyW = max(keyW, len(r.key))
	}

	rows := make([]string, 0, len(o.rows))

	for i, r := range o.rows {
		box := "[ ] "
		if r.on {
			box = "[x] "
		}

		def, _ := columnDef(r.key)
		rows = append(rows, cursorRow(m.styles, i == o.cursor, box+padLine(r.key, keyW)+"  "+def.title, width))
	}

	return m.modalBox(modal{title: "Columns", rows: rows, focus: o.cursor, ctx: ctxColumns, width: width}, h)
}

// toggleColumns is c: open the modal, or close it when it is the overlay on top.
func (m Model) toggleColumns() Model {
	if top, ok := m.topOverlay(); ok && top.kind() == columnsOverlayKind {
		return m.pop()
	}

	return m.push(newColumnsOverlay(m.torrents.listView.columns))
}

// applyColumns is enter in the modal. An empty selection is refused and the
// modal stays open: the CLI rejects an empty client.list_columns, and a table of
// nothing is not a choice worth persisting.
func (m Model) applyColumns() Model {
	o, ok := topAs[columnsOverlay](m)
	if !ok {
		return m
	}

	keys := o.selected()
	if len(keys) == 0 {
		m.toast = "pick at least one column"

		return m
	}

	m = m.pop()

	prev := m.torrents.listView
	v := prev
	v.columns = keys

	m.torrents = m.torrents.setView(v, m.icons)
	m.toast = "columns: " + strings.Join(keys, ", ")

	if !slices.Equal(keys, prev.columns) {
		m = m.persistPref(clientKeyListColumns, keys)
	}

	return m
}
