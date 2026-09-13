package tui

import (
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/table"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// listColumn describes one column of the torrent table: the CLI field key that
// names it, its shape, and how a torrent renders into it.
type listColumn struct {
	// key is the CLI --fields key, or empty for the two fixed prefix cells.
	key string

	// colSpec is the heading, width, and alignment. Only name flexes.
	colSpec

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
	{colSpec: colSpec{title: " ", width: 1}, render: func(_ transmission.Torrent, _ Icons) string { return "" }},
	{colSpec: colSpec{title: " ", width: 1}, render: func(t transmission.Torrent, i Icons) string { return i.status(t.Status) }},
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
	{key: "id", colSpec: colSpec{title: "ID", width: 4, right: true}, render: func(t transmission.Torrent, _ Icons) string {
		return strconv.FormatInt(t.ID, 10)
	}},
	{key: "hash", colSpec: colSpec{title: "Hash", width: 40}, render: func(t transmission.Torrent, _ Icons) string { return t.Hash }},
	{key: nameKey, colSpec: colSpec{title: "Name"}, render: func(t transmission.Torrent, _ Icons) string {
		return output.SanitizeTerminal(t.Name)
	}},
	{key: "status", colSpec: colSpec{title: "Status", width: 11}, render: func(t transmission.Torrent, _ Icons) string {
		return t.Status.String()
	}},
	{key: "done", colSpec: colSpec{title: "Done", width: 4, right: true}, render: func(t transmission.Torrent, _ Icons) string {
		return output.Percent(t.PercentDone)
	}},
	{key: "size", colSpec: colSpec{title: "Size", width: 9, right: true}, render: func(t transmission.Torrent, _ Icons) string {
		return output.Bytes(t.TotalSize)
	}},
	{key: "down", colSpec: colSpec{title: "Down", width: 11, right: true}, render: func(t transmission.Torrent, _ Icons) string {
		return output.Speed(t.RateDownload)
	}},
	{key: "up", colSpec: colSpec{title: "Up", width: 11, right: true}, render: func(t transmission.Torrent, _ Icons) string {
		return output.Speed(t.RateUpload)
	}},
	{key: "ratio", colSpec: colSpec{title: "Ratio", width: 6, right: true}, render: func(t transmission.Torrent, _ Icons) string {
		return output.Ratio(t.UploadRatio)
	}},
	{key: "eta", colSpec: colSpec{title: "ETA", width: 7, right: true}, render: func(t transmission.Torrent, _ Icons) string {
		return output.ETA(t.ETA)
	}},
	{key: "peers", colSpec: colSpec{title: "Peers", width: 5, right: true}, render: func(t transmission.Torrent, _ Icons) string {
		return strconv.FormatInt(t.PeersConnected, 10)
	}},
	{key: "added", colSpec: colSpec{title: "Added", width: 10}, render: func(t transmission.Torrent, _ Icons) string {
		return output.Date(t.AddedDate)
	}},
	{key: "download_dir", colSpec: colSpec{title: "Download Dir", width: 24}, render: func(t transmission.Torrent, _ Icons) string {
		return output.SanitizeTerminal(t.DownloadDir)
	}},
	{key: "error", colSpec: colSpec{title: "Error", width: 24}, render: func(t transmission.Torrent, _ Icons) string {
		return output.SanitizeTerminal(t.Error)
	}},
	{key: "labels", colSpec: colSpec{title: "Labels", width: 16}, render: func(t transmission.Torrent, _ Icons) string {
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

// fitColumns sizes the torrent table's columns for a pane of the given inner
// width, never dropping the two prefix cells. See fitSpecs.
func fitColumns(cols []listColumn, width int) []table.Column {
	return fitSpecs(specsOf(cols), len(prefixColumns), width)
}

// tableColumns converts the column descriptions into the component's own type.
func tableColumns(cols []listColumn) []table.Column {
	return specColumns(specsOf(cols))
}

// specsOf is the shapes of the torrent table's columns.
func specsOf(cols []listColumn) []colSpec {
	specs := make([]colSpec, 0, len(cols))
	for _, c := range cols {
		specs = append(specs, c.colSpec)
	}

	return specs
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
