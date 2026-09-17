package tui

import (
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/table"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/prowlarr"
)

// releaseColumn describes one column of the results table.
type releaseColumn struct {
	// key names the column in the c modal.
	key string

	// colSpec is the heading, width, and alignment. Only title flexes.
	colSpec

	// render turns a release into the cell's text. Every server-derived string
	// it returns must already be sanitized.
	render func(r prowlarr.Release) string
}

// releaseTitleKey is the results table's one flex column.
const releaseTitleKey = "title"

// releaseColumnDefs mirrors searchColumns (internal/commands/search.go) less its
// "#" column: the same columns, in the same order, with the same renderers and
// alignment, so the results table and `snatcher search` draw a release the same
// way. The TUI sorts its rows, so a position number would name a row the CLI
// does not. internal/tui does not import internal/commands, so this is a copy
// pinned by TestReleaseColumnsMirrorSearch.
var releaseColumnDefs = []releaseColumn{
	{key: "age", colSpec: colSpec{title: "Age", width: 5, right: true}, render: func(r prowlarr.Release) string {
		return searchAge(r.Age)
	}},
	{key: releaseTitleKey, colSpec: colSpec{title: "Title"}, render: func(r prowlarr.Release) string {
		return output.SanitizeTerminal(r.Title)
	}},
	{key: "indexer", colSpec: colSpec{title: "Indexer", width: 16}, render: func(r prowlarr.Release) string {
		return output.SanitizeTerminal(r.Indexer)
	}},
	{key: "size", colSpec: colSpec{title: "Size", width: 9, right: true}, render: func(r prowlarr.Release) string {
		return output.Bytes(r.Size)
	}},
	{key: "grabs", colSpec: colSpec{title: "Grabs", width: 5, right: true}, render: func(r prowlarr.Release) string {
		return searchCount(r.Grabs)
	}},
	{key: "seeders", colSpec: colSpec{title: "Seeders", width: 7, right: true}, render: func(r prowlarr.Release) string {
		return searchCount(r.Seeders)
	}},
	{key: "leechers", colSpec: colSpec{title: "Leechers", width: 8, right: true}, render: func(r prowlarr.Release) string {
		return searchCount(r.Leechers)
	}},
}

// allReleaseColumnKeys is every results column, in registry order, and the set
// a Prowlarr window starts with.
var allReleaseColumnKeys = []string{"age", "title", "indexer", "size", "grabs", "seeders", "leechers"}

// searchAge renders a release's age in days. It is internal/commands' searchAge.
func searchAge(days int) string {
	if days < 0 {
		return "-"
	}

	return strconv.Itoa(days) + "d"
}

// searchCount renders a nullable count, a dash for one the indexer does not
// publish. It is internal/commands' searchCount.
func searchCount(n *int) string {
	if n == nil {
		return "-"
	}

	return strconv.Itoa(*n)
}

// releaseColumnDef looks a column up by key.
func releaseColumnDef(key string) (releaseColumn, bool) {
	for _, c := range releaseColumnDefs {
		if c.key == key {
			return c, true
		}
	}

	return releaseColumn{}, false
}

// releaseColumnsFor is the columns for a list of keys, unknown keys skipped.
func releaseColumnsFor(keys []string) []releaseColumn {
	cols := make([]releaseColumn, 0, len(keys))

	for _, k := range keys {
		if c, ok := releaseColumnDef(k); ok {
			cols = append(cols, c)
		}
	}

	return cols
}

// releaseSpecs is the shapes of a set of results columns.
func releaseSpecs(cols []releaseColumn) []colSpec {
	specs := make([]colSpec, 0, len(cols))
	for _, c := range cols {
		specs = append(specs, c.colSpec)
	}

	return specs
}

// releaseRows renders releases into table rows.
func releaseRows(cols []releaseColumn, releases []prowlarr.Release) []table.Row {
	out := make([]table.Row, 0, len(releases))

	for _, r := range releases {
		cells := make(table.Row, 0, len(cols))
		for _, c := range cols {
			cells = append(cells, c.align(c.render(r)))
		}

		out = append(out, cells)
	}

	return out
}

// releaseColumnsOverlayKind names the release columns modal.
const releaseColumnsOverlayKind = "release-columns"

// releaseColumnsOverlay is c in the Prowlarr window: the torrent columns modal's
// rows and keys, over the results columns. Applying it is for this session
// only; there is no client key for the results table.
type releaseColumnsOverlay struct {
	columnsOverlay
}

// newReleaseColumnsOverlay builds the modal for the columns currently shown.
func newReleaseColumnsOverlay(keys []string) releaseColumnsOverlay {
	return releaseColumnsOverlay{columnsOverlay{rows: columnRows(keys, allReleaseColumnKeys)}}
}

// kind implements overlay.
func (releaseColumnsOverlay) kind() string { return releaseColumnsOverlayKind }

// contexts implements overlay.
func (releaseColumnsOverlay) contexts() []Context { return []Context{ctxReleaseColumns} }

// view implements overlay.
func (o releaseColumnsOverlay) view(m Model, w, h int) string {
	return m.modalBox(o.modal(m, w, h), h)
}

// modal implements modalOverlay.
func (o releaseColumnsOverlay) modal(m Model, w, _ int) modal {
	return o.render(m, w, "Release columns", ctxReleaseColumns, func(key string) string {
		def, _ := releaseColumnDef(key)

		return def.title
	})
}

// pointAt implements modalOverlay. It is spelled out rather than promoted: the
// embedded modal's would hand back a columnsOverlay, which is not this overlay.
func (o releaseColumnsOverlay) pointAt(i int) (overlay, bool) {
	inner, ok := o.point(i)
	o.columnsOverlay = inner

	return o, ok
}

// clickAction implements modalOverlay.
func (releaseColumnsOverlay) clickAction() Action { return actionReleaseColumnsToggle }

// wheelActions implements modalOverlay.
func (releaseColumnsOverlay) wheelActions() (up, down Action) {
	return actionReleaseColumnsUp, actionReleaseColumnsDown
}

// step applies a columnsOverlay transition to the embedded modal.
func (o releaseColumnsOverlay) step(f func(columnsOverlay) columnsOverlay) releaseColumnsOverlay {
	o.columnsOverlay = f(o.columnsOverlay)

	return o
}

// toggleReleaseColumns is c: open the modal, or close it when it is on top.
func (m Model) toggleReleaseColumns() Model {
	if top, ok := m.topOverlay(); ok && top.kind() == releaseColumnsOverlayKind {
		return m.pop()
	}

	return m.push(newReleaseColumnsOverlay(m.prowlarr.columns))
}

// applyReleaseColumns is enter in the modal. An empty selection is refused, as
// the torrent modal refuses one.
func (m Model) applyReleaseColumns() Model {
	o, ok := topAs[releaseColumnsOverlay](m)
	if !ok {
		return m
	}

	keys := o.selected()
	if len(keys) == 0 {
		m.toast = "pick at least one column"

		return m
	}

	m = m.pop()

	if !slices.Equal(keys, m.prowlarr.columns) {
		m.prowlarr = m.prowlarr.setColumns(keys)
	}

	m.toast = "columns: " + strings.Join(keys, ", ")

	return m
}

// releaseColumnsHandlers are c and the modal's own keys.
func releaseColumnsHandlers() map[Action]handler {
	step := func(f func(columnsOverlay) columnsOverlay) handler {
		return stepTop(func(o releaseColumnsOverlay) releaseColumnsOverlay { return o.step(f) })
	}

	return map[Action]handler{
		actionReleaseColumns:         plain(Model.toggleReleaseColumns),
		actionReleaseColumnsUp:       step(func(o columnsOverlay) columnsOverlay { return o.move(-1) }),
		actionReleaseColumnsDown:     step(func(o columnsOverlay) columnsOverlay { return o.move(1) }),
		actionReleaseColumnsToggle:   step(columnsOverlay.toggle),
		actionReleaseColumnsMoveUp:   step(func(o columnsOverlay) columnsOverlay { return o.swap(-1) }),
		actionReleaseColumnsMoveDown: step(func(o columnsOverlay) columnsOverlay { return o.swap(1) }),
		actionReleaseColumnsApply:    plain(Model.applyReleaseColumns),
	}
}
