package tui

import (
	"context"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/prowlarr"
)

// catalogLoad is where the catalog fetch stands.
type catalogLoad int

const (
	// catalogIdle is not fetched yet.
	catalogIdle catalogLoad = iota

	// catalogLoading is a fetch in flight.
	catalogLoading

	// catalogLoaded is a fetch that succeeded; it is kept for the session.
	catalogLoaded

	// catalogFailed is a fetch that failed; the next f retries it.
	catalogFailed
)

// catalog is the indexers and categories a Prowlarr server offers, fetched the
// first time f is pressed and kept until the server is switched.
type catalog struct {
	state      catalogLoad
	indexers   []prowlarr.Indexer
	categories []flatCategory
	err        error
}

// flatCategory is one category of the tree, with its depth for indentation.
type flatCategory struct {
	id    int
	name  string
	depth int
}

// flattenCategories walks the category tree depth-first, each category just
// before its own sub-categories, keeping the server's order at every level. It
// is internal/commands' flattenCategories, reduced to what the modal draws.
func flattenCategories(tree []prowlarr.Category) []flatCategory {
	rows := make([]flatCategory, 0, len(tree))

	var walk func(categories []prowlarr.Category, depth int)
	walk = func(categories []prowlarr.Category, depth int) {
		for _, c := range categories {
			rows = append(rows, flatCategory{id: c.ID, name: c.Name, depth: depth})
			walk(c.SubCategories, depth+1)
		}
	}

	walk(tree, 0)

	return rows
}

// catalogMsg is the answer to fetchCatalog.
type catalogMsg struct {
	gen        int
	indexers   []prowlarr.Indexer
	categories []prowlarr.Category
	err        error
}

// fetchCatalog reads the indexers and the category tree in one command.
func fetchCatalog(ctx context.Context, client *prowlarr.Client, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, prowlarrTimeout)
		defer cancel()

		indexers, err := client.Indexers(ctx)
		if err != nil {
			return catalogMsg{gen: gen, err: err}
		}

		categories, err := client.Categories(ctx)
		if err != nil {
			return catalogMsg{gen: gen, err: err}
		}

		return catalogMsg{gen: gen, indexers: indexers, categories: categories}
	}
}

// catalogRowKind is what a catalog modal row is.
type catalogRowKind int

const (
	catalogHeading catalogRowKind = iota
	catalogNote
	catalogCategory
	catalogIndexer
)

// catalogRow is one row of the modal.
type catalogRow struct {
	kind  catalogRowKind
	id    int
	label string

	// on is a checkbox's state; disabled rows are drawn but never hold the
	// cursor.
	on       bool
	disabled bool
}

// selectable reports whether the cursor may rest on the row.
func (r catalogRow) selectable() bool {
	return (r.kind == catalogCategory || r.kind == catalogIndexer) && !r.disabled
}

// catalogPickKind names the categories and indexers modal.
const catalogPickKind = "catalog"

// catalogPickOverlay is f in the Prowlarr window: a checkbox list of the
// categories and indexers the next search restricts to. Nothing checked in a
// section means that section is not restricted.
type catalogPickOverlay struct {
	rows   []catalogRow
	cursor int
}

// newCatalogPick builds the modal over a catalog, with the current restriction
// checked.
func newCatalogPick(c catalog, params prowlarr.SearchParams) catalogPickOverlay {
	var rows []catalogRow

	switch c.state {
	case catalogIdle, catalogLoading:
		rows = []catalogRow{{kind: catalogNote, label: "loading…"}}
	case catalogFailed:
		rows = []catalogRow{
			{kind: catalogNote, label: errorLine(c.err)},
			{kind: catalogNote, label: "press f again to retry"},
		}
	case catalogLoaded:
		rows = catalogRows(c, params)
	}

	o := catalogPickOverlay{rows: rows}

	return o.move(0)
}

// catalogRows is a loaded catalog's rows: categories, then indexers.
func catalogRows(c catalog, params prowlarr.SearchParams) []catalogRow {
	rows := []catalogRow{{kind: catalogHeading, label: "Categories"}}

	for _, cat := range c.categories {
		rows = append(rows, catalogRow{
			kind:  catalogCategory,
			id:    cat.id,
			label: strings.Repeat("  ", cat.depth) + output.SanitizeTerminal(cat.name) + " (" + strconv.Itoa(cat.id) + ")",
			on:    slices.Contains(params.Categories, cat.id),
		})
	}

	rows = append(rows, catalogRow{kind: catalogHeading, label: "Indexers"})

	if len(c.indexers) == 0 {
		rows = append(rows, catalogRow{kind: catalogNote, label: "no indexers configured"})
	}

	for _, ix := range c.indexers {
		label := output.SanitizeTerminal(ix.Name) + "  " + output.SanitizeTerminal(ix.Protocol+"/"+ix.Privacy)
		if !ix.Enable {
			label += "  (disabled)"
		}

		rows = append(rows, catalogRow{
			kind:     catalogIndexer,
			id:       ix.ID,
			label:    label,
			on:       slices.Contains(params.IndexerIDs, ix.ID),
			disabled: !ix.Enable,
		})
	}

	return rows
}

// kind implements overlay.
func (catalogPickOverlay) kind() string { return catalogPickKind }

// contexts implements overlay.
func (catalogPickOverlay) contexts() []Context { return []Context{ctxCatalogPick} }

// move steps the cursor to the next selectable row in delta's direction,
// stopping at the ends. A delta of zero settles it on the first selectable row
// at or after it.
func (o catalogPickOverlay) move(delta int) catalogPickOverlay {
	from, step := o.cursor+1, 1

	switch {
	case delta == 0:
		from = o.cursor
	case delta < 0:
		from, step = o.cursor-1, -1
	}

	for i := from; i >= 0 && i < len(o.rows); i += step {
		if o.rows[i].selectable() {
			o.cursor = i

			return o
		}
	}

	return o
}

// toggle checks or unchecks the cursor row.
func (o catalogPickOverlay) toggle() catalogPickOverlay {
	if o.cursor >= len(o.rows) || !o.rows[o.cursor].selectable() {
		return o
	}

	o.rows = slices.Clone(o.rows)
	o.rows[o.cursor].on = !o.rows[o.cursor].on

	return o
}

// loaded reports whether the modal lists a catalog.
func (o catalogPickOverlay) loaded() bool {
	return slices.ContainsFunc(o.rows, catalogRow.selectable)
}

// selected is the checked ids of one kind, in row order.
func (o catalogPickOverlay) selected(kind catalogRowKind) []int {
	var ids []int

	for _, r := range o.rows {
		if r.kind == kind && r.on {
			ids = append(ids, r.id)
		}
	}

	return ids
}

// view implements overlay.
func (o catalogPickOverlay) view(m Model, w, h int) string {
	width := min(overlayContentWidth(w), filterPickMaxWidth)

	rows := make([]string, 0, len(o.rows))

	for i, r := range o.rows {
		switch r.kind {
		case catalogHeading:
			rows = append(rows, m.styles.Title.Render(padLine(r.label, width)))
		case catalogNote:
			rows = append(rows, m.styles.Muted.Render(padLine("    "+r.label, width)))
		case catalogCategory, catalogIndexer:
			box := "[ ] "
			if r.on {
				box = "[x] "
			}

			if r.disabled {
				rows = append(rows, m.styles.Muted.Render(padLine("  "+box+r.label, width)))

				continue
			}

			rows = append(rows, cursorRow(m.styles, i == o.cursor, box+r.label, width))
		}
	}

	return m.modalBox(modal{
		title:  "Search categories and indexers",
		rows:   rows,
		focus:  o.cursor,
		footer: []string{m.styles.Muted.Render(padLine("  nothing checked searches all", width))},
		ctx:    ctxCatalogPick,
		width:  width,
	}, h)
}

// toggleCatalogPick is f: open the modal, fetching the catalog the first time,
// or close it when it is on top.
func (m Model) toggleCatalogPick() (Model, tea.Cmd) {
	if top, ok := m.topOverlay(); ok && top.kind() == catalogPickKind {
		return m.pop(), nil
	}

	if why, refused := m.refuseRelease(); refused {
		m.toast = why

		return m, nil
	}

	w := m.prowlarr

	var cmd tea.Cmd

	if w.catalog.state == catalogIdle || w.catalog.state == catalogFailed {
		w.catalog = catalog{state: catalogLoading}
		cmd = fetchCatalog(m.ctx, w.client, w.gen)
	}

	m.prowlarr = w

	return m.push(newCatalogPick(w.catalog, w.params)), cmd
}

// catalogFetched installs the catalog and, when the modal is still open, fills
// it in.
func (m Model) catalogFetched(msg catalogMsg) Model {
	if msg.gen != m.prowlarr.gen {
		return m
	}

	if msg.err != nil {
		m.prowlarr.catalog = catalog{state: catalogFailed, err: msg.err}
	} else {
		m.prowlarr.catalog = catalog{
			state:      catalogLoaded,
			indexers:   msg.indexers,
			categories: flattenCategories(msg.categories),
		}
	}

	if _, ok := topAs[catalogPickOverlay](m); ok {
		m = m.replaceTopOverlay(newCatalogPick(m.prowlarr.catalog, m.prowlarr.params))
	}

	return m
}

// applyCatalogPick is enter in the modal: restrict the next search to what is
// checked, and offer to rerun a search already on screen with it.
func (m Model) applyCatalogPick() (Model, tea.Cmd) {
	o, ok := topAs[catalogPickOverlay](m)
	if !ok {
		return m, nil
	}

	if !o.loaded() {
		if m.prowlarr.catalog.state == catalogLoading {
			m.toast = "still loading"

			return m, nil
		}

		return m.pop(), nil
	}

	m = m.pop()

	m.prowlarr.params.Categories = o.selected(catalogCategory)
	m.prowlarr.params.IndexerIDs = o.selected(catalogIndexer)
	m = m.resize(m.width, m.height)
	m.toast = m.prowlarr.selectionSummary()

	if !m.prowlarr.searched {
		return m, nil
	}

	return m.push(newConfirm("Resubmit the search with the new selection?", Model.resubmitSearch, nil)), nil
}

// catalogHandlers are f and the modal's own keys.
func catalogHandlers() map[Action]handler {
	return map[Action]handler{
		actionReleaseFilter: func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.toggleCatalogPick() },
		actionCatalogUp:     stepTop(func(o catalogPickOverlay) catalogPickOverlay { return o.move(-1) }),
		actionCatalogDown:   stepTop(func(o catalogPickOverlay) catalogPickOverlay { return o.move(1) }),
		actionCatalogToggle: stepTop(catalogPickOverlay.toggle),
		actionCatalogApply:  func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.applyCatalogPick() },
	}
}
