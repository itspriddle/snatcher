package tui

import (
	"strings"

	"charm.land/bubbles/v2/table"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// listColumn describes one column of the torrent table: its heading, its width,
// and how a torrent renders into it.
type listColumn struct {
	// title is the column heading.
	title string

	// width is the column's width in cells, excluding the table's own cell
	// padding.
	width int

	// render turns a torrent into the cell's text. Every server-derived string
	// it returns must already be sanitized.
	render func(t transmission.Torrent, icons Icons) string
}

// listColumns is T1's fixed column set. T5 makes it configurable (the `c` key,
// and client.list_columns, which the CLI already honors).
//
// The mark and status glyph columns are one cell wide and separate. A Nerd Font
// private-use glyph measures one cell, so the ASCII fallback in T2 cannot move
// any column to its right.
var listColumns = []listColumn{
	{title: " ", width: 1, render: func(_ transmission.Torrent, _ Icons) string { return "" }},
	{title: " ", width: 1, render: func(t transmission.Torrent, i Icons) string { return i.status(t.Status) }},
	{title: "Name", width: 40, render: func(t transmission.Torrent, _ Icons) string {
		return output.SanitizeTerminal(t.Name)
	}},
	{title: "Status", width: 11, render: func(t transmission.Torrent, _ Icons) string { return t.Status.String() }},
	{title: "Done", width: 4, render: func(t transmission.Torrent, _ Icons) string {
		return output.Percent(t.PercentDone)
	}},
	{title: "Size", width: 9, render: func(t transmission.Torrent, _ Icons) string {
		return output.Bytes(t.TotalSize)
	}},
	{title: "Down", width: 11, render: func(t transmission.Torrent, _ Icons) string {
		return output.Speed(t.RateDownload)
	}},
	{title: "Up", width: 11, render: func(t transmission.Torrent, _ Icons) string {
		return output.Speed(t.RateUpload)
	}},
	{title: "ETA", width: 7, render: func(t transmission.Torrent, _ Icons) string { return output.ETA(t.ETA) }},
}

// The column indices, named so the mark column can be filled from the selection
// and the drop order can be written as a list of columns rather than of numbers.
const (
	markColumn = iota
	glyphColumn
	nameColumn
	statusColumn
	doneColumn
	sizeColumn
	downColumn
	upColumn
	etaColumn
)

// dropOrder is the order columns are hidden as the pane narrows, least
// informative first. The mark glyph, the status glyph, and the name are never
// dropped: the first two are one cell each, and a list of torrents with no names
// is not a list of torrents.
var dropOrder = []int{etaColumn, upColumn, downColumn, sizeColumn, doneColumn, statusColumn}

// minNameWidth is the narrowest the name column is allowed to get before a
// column is dropped to feed it.
const minNameWidth = 16

// cellPadding is the trailing padding every table cell carries (Styles.Cell in
// newStyles). It is one rather than the component default's two because nine
// columns of padding on both sides costs eighteen cells, which is most of a
// torrent name.
const cellPadding = 1

// listPane is the torrents window's main pane: the torrent table.
type listPane struct {
	// table is the bubbles table. It is constructed with a keymap built from
	// the registry and nothing else — its default keymap binds f, b, space, u,
	// d, g, and G, six of which mean something else in vision.md.
	table table.Model
}

// newListPane builds the table with the registry's navigation keys and the
// theme's styles.
func newListPane(st Styles) listPane {
	t := table.New(
		table.WithColumns(tableColumns(listColumns)),
		table.WithKeyMap(tableKeyMap()),
		table.WithStyles(st.Table),
		table.WithFocused(true),
	)

	return listPane{table: t}
}

// tableKeyMap builds the table's keymap from the registry, field by field. Every
// field is filled from a registry row, so a key the table responds to is a key
// the help view advertises and the collision test has seen. A field with no
// registry row stays the zero Binding — unbound — rather than keeping the
// component's default.
func tableKeyMap() table.KeyMap {
	return table.KeyMap{
		LineUp:       bindingFor(ctxNav, actionNavUp),
		LineDown:     bindingFor(ctxNav, actionNavDown),
		PageUp:       bindingFor(ctxNav, actionNavPageUp),
		PageDown:     bindingFor(ctxNav, actionNavPageDown),
		HalfPageUp:   bindingFor(ctxNav, actionNavHalfPageUp),
		HalfPageDown: bindingFor(ctxNav, actionNavHalfPageDown),
		GotoTop:      bindingFor(ctxNav, actionNavTop),
		GotoBottom:   bindingFor(ctxNav, actionNavBottom),
	}
}

// fitColumns sizes the columns for a pane of the given inner width: the name
// column takes whatever the others leave, and when that would squeeze it below
// minNameWidth the least informative column is dropped and the sum recomputed.
//
// A dropped column is given a width of zero rather than removed. The component
// skips a zero-width column when rendering both headers and rows, so the rows
// built by setRows stay one cell per listColumns entry however narrow the pane
// gets, and nothing has to rebuild them on a resize.
func fitColumns(width int) []table.Column {
	cols := tableColumns(listColumns)

	for dropped := 0; ; dropped++ {
		spent := 0

		for i, c := range cols {
			if i != nameColumn && c.Width > 0 {
				spent += c.Width + cellPadding
			}
		}

		nameWidth := width - spent - cellPadding

		if nameWidth >= minNameWidth || dropped == len(dropOrder) {
			cols[nameColumn].Width = max(1, nameWidth)

			return cols
		}

		cols[dropOrder[dropped]].Width = 0
	}
}

// tableColumns converts the column descriptions into the component's own type.
func tableColumns(cols []listColumn) []table.Column {
	out := make([]table.Column, 0, len(cols))

	for _, c := range cols {
		out = append(out, table.Column{Title: c.title, Width: c.width})
	}

	return out
}

// setRows rebuilds the table's rows from the torrents and the current marks.
//
// Neither table in the Charm stack sorts or filters; the data is transformed by
// internal/transmission's own helpers and the rows are rebuilt. That is also why
// marks are keyed by torrent ID: the row index under a mark is not stable across
// a rebuild.
func (p listPane) setRows(rows []transmission.Torrent, sel selection, icons Icons) listPane {
	out := make([]table.Row, 0, len(rows))

	for _, t := range rows {
		cells := make(table.Row, 0, len(listColumns))

		for i, c := range listColumns {
			if i == markColumn {
				cells = append(cells, icons.mark(sel.isMarked(t.ID)))

				continue
			}

			cells = append(cells, c.render(t, icons))
		}

		out = append(out, cells)
	}

	p.table.SetRows(out)

	return p
}

// resize fits the table to r, giving the name column whatever width the fixed
// columns do not claim.
func (p listPane) resize(r rect) listPane {
	inner := r.inner()

	p.table.SetColumns(fitColumns(inner.w))
	p.table.SetWidth(inner.w)

	// One row of the pane goes to the table's own header.
	p.table.SetHeight(inner.h - 1)

	return p
}

// setStyles re-themes the table. It is a pointer-receiver method on the
// component, so it is called on the copy this pane returns rather than on a
// temporary.
func (p listPane) setStyles(st Styles) listPane {
	p.table.SetStyles(st.Table)

	return p
}

// focus and blur drive the component's own focus flag. table.Update is a no-op
// unless the table is focused, so a nav key forwarded to an unfocused table
// would silently do nothing.
func (p listPane) focus(on bool) listPane {
	if on {
		p.table.Focus()
	} else {
		p.table.Blur()
	}

	return p
}

// cursor is the table's current row index.
func (p listPane) cursor() int {
	return p.table.Cursor()
}

// placeholder is what an empty table says instead of nothing: why it is empty.
type placeholder struct {
	// lines are drawn centered under the header; empty means no placeholder.
	lines []string

	// isErr styles the first line as an error.
	isErr bool
}

// listPlaceholder is what the torrent table says while it has no rows.
func (m Model) listPlaceholder() placeholder {
	switch {
	case len(m.torrents.rows) > 0 || m.deps.Torrents != nil:
		return placeholder{}
	case m.client == nil:
		return placeholder{lines: []string{noServerLabel}}
	case !m.live.loaded && m.live.err == nil:
		return placeholder{lines: []string{"loading…"}}
	case !m.live.loaded:
		return placeholder{lines: []string{errorLine(m.live.err), errorHint(m.live.err)}, isErr: true}
	}

	return placeholder{lines: []string{"no torrents"}}
}

// view renders the table into r, with the placeholder under its header when the
// table has no rows.
func (p listPane) view(st Styles, r rect, focused bool, ph placeholder) string {
	lines := strings.Split(p.table.View(), "\n")

	if len(ph.lines) > 0 && len(p.table.Rows()) == 0 {
		lines = placeholderLines(st, lines[0], ph, r.inner())
	}

	return paneBox(st, paneList, focused).
		Width(r.w).
		Height(r.h).
		Render(paneContent(lines, r))
}

// placeholderLines lays the placeholder out centered in the rows under the
// table's header. Every line is truncated before it is styled, so paneContent
// never cuts through an escape sequence.
func placeholderLines(st Styles, header string, ph placeholder, inner rect) []string {
	lines := []string{header}

	for range max(0, (inner.h-1-len(ph.lines))/2) {
		lines = append(lines, "")
	}

	for i, text := range ph.lines {
		style := st.Muted
		if i == 0 && ph.isErr {
			style = st.FieldError
		}

		lines = append(lines, style.Render(centered(truncate(text, inner.w), inner.w)))
	}

	return lines
}
