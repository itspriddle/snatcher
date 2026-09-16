package tui

import (
	"strings"

	"charm.land/bubbles/v2/table"
	"github.com/charmbracelet/x/ansi"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// listPane is the torrents window's main pane: the torrent table.
type listPane struct {
	// table is the bubbles table. It is constructed with a keymap built from
	// the registry and nothing else — its default keymap binds f, b, space, u,
	// d, g, and G, six of which mean something else in vision.md.
	table table.Model

	// cols are the columns drawn, prefix cells first.
	cols []listColumn

	// width is the table's inner width as of the last resize, so a column
	// change can be fitted without waiting for the next one.
	width int
}

// newListPane builds the table with the registry's navigation keys, the theme's
// styles, and the columns named by keys.
func newListPane(st Styles, keys []string) listPane {
	cols := columnsFor(keys)

	t := table.New(
		table.WithColumns(tableColumns(cols)),
		table.WithKeyMap(tableKeyMap()),
		table.WithStyles(st.Table),
		table.WithFocused(true),
	)

	return listPane{table: t, cols: cols}
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

// setColumns switches the table to the columns named by keys. The rows are
// emptied first: the component renders each row's cells against its columns,
// and a row with more cells than columns would index past them. The caller
// rebuilds the rows.
func (p listPane) setColumns(keys []string) listPane {
	p.table.SetRows(nil)
	p.cols = columnsFor(keys)
	p.table.SetColumns(fitColumns(p.cols, p.width))

	return p
}

// setRows rebuilds the table's rows from the torrents and the current marks.
//
// Neither table in the Charm stack sorts or filters; the data is transformed by
// internal/transmission's own helpers and the rows are rebuilt. That is also why
// marks are keyed by torrent ID: the row index under a mark is not stable across
// a rebuild.
func (p listPane) setRows(rows []transmission.Torrent, sel torrentSelection, icons Icons) listPane {
	out := make([]table.Row, 0, len(rows))

	for _, t := range rows {
		cells := make(table.Row, 0, len(p.cols))

		for i, c := range p.cols {
			if i == markColumn {
				cells = append(cells, icons.mark(sel.isMarked(t.ID)))

				continue
			}

			cells = append(cells, c.cellText(t, icons))
		}

		out = append(out, cells)
	}

	p.table.SetRows(out)

	return p
}

// resize fits the table to r, giving the name column whatever width the fixed
// columns do not claim. footer reserves the pane's last row for the footer line.
func (p listPane) resize(r rect, footer bool) listPane {
	inner := r.inner()

	p.width = inner.w
	p.table.SetColumns(fitColumns(p.cols, inner.w))
	p.table.SetWidth(inner.w)

	// One row of the pane goes to the table's own header, and one to the
	// footer while there is one.
	height := inner.h - 1
	if footer {
		height--
	}

	p.table.SetHeight(max(1, height))

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
	case len(m.torrents.rows) > 0:
		return placeholder{}
	case len(m.torrents.all) > 0:
		return placeholder{lines: []string{"no torrents match", m.torrents.narrowedBy()}}
	case m.deps.Torrents != nil:
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
// table has no rows, and the footer on the pane's last row when there is one. The
// footer arrives styled — it can hold the search input — so it is truncated
// escape-aware.
func (p listPane) view(st Styles, r rect, focused bool, ph placeholder, footer string) string {
	inner := r.inner()
	lines := strings.Split(p.table.View(), "\n")

	if len(ph.lines) > 0 && len(p.table.Rows()) == 0 {
		lines = placeholderLines(st, lines[0], ph, inner)
	}

	if footer != "" {
		lines = append(clipLines(lines, inner.h-1), ansi.Truncate(footer, inner.w, "…"))
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
