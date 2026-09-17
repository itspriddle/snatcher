package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/prowlarr"
)

// prowlarrTimeout bounds one Prowlarr round trip: the client transport's own
// default, so a search the CLI would wait for, the window waits for too.
const prowlarrTimeout = 30 * time.Second

// defaultReleaseLimit is how many results one search asks for, and how far + steps
// the offset: the CLI's --limit default.
const defaultReleaseLimit = 100

// defaultReleaseType is the torznab search type the window searches with, the
// CLI's --search-type default.
const defaultReleaseType = "search"

// releaseKey names a release exactly, independent of where it sorts: the cursor
// follows it across a re-sort and an appended page.
type releaseKey struct {
	indexerID int
	guid      string
}

// keyOf is a release's key.
func keyOf(r prowlarr.Release) releaseKey {
	return releaseKey{indexerID: r.IndexerID, guid: r.GUID}
}

// prowlarrWindow is the Prowlarr search window: a search pane, a results table,
// and a release detail pane.
//
// It owns its own client and generation, apart from the refresh spine's: the
// Transmission poll keeps running while this window is open, and switching one
// server never touches the other, as default_server and default_prowlarr_server
// are independent in the CLI.
type prowlarrWindow struct {
	// client is the dialed Prowlarr client, nil until the first dial lands.
	client *prowlarr.Client

	// serverName and serverURL are the alias the client was dialed as and its
	// base URL. pending is the alias a dial in flight is for.
	serverName, serverURL, pending string

	// gen bumps on every dial, retiring every reply from before it.
	gen int

	// dialing is set while a dial is in flight; dialErr is the last failed
	// first dial; unconfigured says there is no Prowlarr server to dial.
	dialing      bool
	dialErr      error
	unconfigured bool

	// status is the dialed server's self-description, for the status bar.
	status prowlarr.SystemStatus

	// opened reports that the window has been shown once.
	opened bool

	// queryInput is the search pane's query field. Its text is a draft until
	// it is submitted into params.
	queryInput textinput.Model

	// params is the search last submitted, and the categories and indexers the
	// next one restricts to.
	params prowlarr.SearchParams

	// results are the releases in Prowlarr's order, byte-exact, and rows are
	// the sorted view the table shows.
	results []prowlarr.Release
	rows    []prowlarr.Release

	// searched says a search has been submitted; inFlight that its reply, or a
	// page's, is still out; exhausted that the last page came back short.
	searched, inFlight, exhausted bool

	// seq retires a superseded search's reply; nextOffset is where + asks from.
	seq, nextOffset int

	// err is the last search's error, cleared by the next submit.
	err error

	// catalog is the indexers and categories the f modal lists.
	catalog catalog

	// sort and columns are the results view, for this session only.
	sort    releaseSort
	columns []string

	// table is the results table, cols its columns, and width its inner width
	// as of the last resize.
	table table.Model
	cols  []releaseColumn
	width int

	// detail is the release detail pane, and detailOpen its toggle.
	detail     releaseDetail
	detailOpen bool

	// focus is the pane that owns the keyboard.
	focus Pane

	// cursor is the release under the table's cursor, when there is one.
	cursor    releaseKey
	hasCursor bool
}

// newProwlarrWindow builds an empty window: nothing dialed, nothing searched,
// the results pane focused.
func newProwlarrWindow(st Styles) prowlarrWindow {
	cols := releaseColumnsFor(allReleaseColumnKeys)

	t := table.New(
		table.WithColumns(specColumns(releaseSpecs(cols))),
		table.WithKeyMap(tableKeyMap()),
		table.WithStyles(st.Table),
		table.WithFocused(true),
	)

	return prowlarrWindow{
		queryInput: newQueryInput(st),
		params:     prowlarr.SearchParams{Type: defaultReleaseType, Limit: defaultReleaseLimit},
		columns:    append([]string{}, allReleaseColumnKeys...),
		sort:       releaseSort{field: releaseSortServer},
		table:      t,
		cols:       cols,
		detail:     newReleaseDetail(),
		focus:      paneResults,
	}
}

// setStyles re-themes the components that keep styles of their own.
func (w prowlarrWindow) setStyles(st Styles) prowlarrWindow {
	w.table.SetStyles(st.Table)
	w.queryInput.SetStyles(st.Input)

	return w
}

// resize lays the panes out for a frame.
func (w prowlarrWindow) resize(l layout) prowlarrWindow {
	inner := l.list.inner()

	w.width = inner.w
	w.table.SetColumns(fitSpecs(releaseSpecs(w.cols), 0, inner.w))
	w.table.SetWidth(inner.w)
	w.table.SetHeight(max(1, inner.h-1))

	used := lipgloss.Width(w.queryInput.Prompt) + lipgloss.Width(w.selectionSummary()) + len(footerGap) + 1
	w.queryInput.SetWidth(max(1, l.search.inner().w-used))

	if w.detailOpen {
		w.detail = w.detail.resize(l.detail)
	}

	return w
}

// setColumns switches the table to the columns named by keys, emptying the rows
// first as listPane.setColumns does, then rebuilding them.
func (w prowlarrWindow) setColumns(keys []string) prowlarrWindow {
	w.columns = keys
	w.cols = releaseColumnsFor(keys)
	w.table.SetRows(nil)
	w.table.SetColumns(fitSpecs(releaseSpecs(w.cols), 0, w.width))

	return w.reapply()
}

// setSort installs a sort and rederives the rows.
func (w prowlarrWindow) setSort(s releaseSort) prowlarrWindow {
	w.sort = s

	return w.reapply()
}

// setResults installs releases in the server's order and rederives the rows.
func (w prowlarrWindow) setResults(results []prowlarr.Release) prowlarrWindow {
	w.results = results

	return w.reapply()
}

// reapply rederives the rows from the results and the sort, keeping the cursor
// on its release. A release no longer shown puts the cursor at the top.
func (w prowlarrWindow) reapply() prowlarrWindow {
	w.rows = w.sort.apply(w.results)
	w.table.SetRows(markRows(releaseRows(w.cols, w.rows)))

	cursor := 0

	if w.hasCursor {
		for i, r := range w.rows {
			if keyOf(r) == w.cursor {
				cursor = i

				break
			}
		}
	}

	w.table.SetCursor(cursor)

	return w.syncCursor()
}

// syncCursor pulls the table's cursor back into the window and repoints the
// detail pane at the release under it.
func (w prowlarrWindow) syncCursor() prowlarrWindow {
	r, ok := w.cursorRelease()

	w.hasCursor = ok
	if ok {
		w.cursor = keyOf(r)
		w.detail = w.detail.setRelease(w.redact(r), true)

		return w
	}

	w.detail = w.detail.setRelease(prowlarr.Release{}, false)

	return w
}

// cursorRelease is the release under the table's cursor, exactly as the server
// sent it: what a grab or a download acts on.
func (w prowlarrWindow) cursorRelease() (prowlarr.Release, bool) {
	i := w.table.Cursor()
	if i < 0 || i >= len(w.rows) {
		return prowlarr.Release{}, false
	}

	return w.rows[i], true
}

// redact is a release as it may be drawn, yanked, or opened: the API key
// stripped by the dialed client, which knows its value, or by the URL rewrite
// alone before one is dialed.
func (w prowlarrWindow) redact(r prowlarr.Release) prowlarr.Release {
	if w.client == nil {
		return r.Redacted()
	}

	return w.client.Redact(r)
}

// redactURL is one URL as redact leaves it.
func (w prowlarrWindow) redactURL(raw string) string {
	return w.redact(prowlarr.Release{InfoURL: raw}).InfoURL
}

// reset empties the window for a different server: no results, no catalog, no
// category or indexer restriction, the cursor at the top. The query text, sort,
// columns, and pane toggles are the user's, not the server's, so they stay.
func (w prowlarrWindow) reset() prowlarrWindow {
	w.results, w.rows = nil, nil
	w.searched, w.inFlight, w.exhausted = false, false, false
	w.nextOffset, w.err = 0, nil
	w.catalog = catalog{}
	w.params.Query, w.params.Categories, w.params.IndexerIDs = "", nil, nil
	w.hasCursor = false

	return w.reapply()
}

// openPanes lists the window's open panes in cycle order, top to bottom.
func (w prowlarrWindow) openPanes() []Pane {
	if w.detailOpen {
		return []Pane{paneSearch, paneResults, paneResultDetail}
	}

	return []Pane{paneSearch, paneResults}
}

// neighbor answers the ctrl+w chord. The panes are stacked, so only up and down
// move, one pane at a time.
func (w prowlarrWindow) neighbor(dir Action) Pane {
	panes := w.openPanes()

	for i, p := range panes {
		if p != w.focus {
			continue
		}

		switch {
		case dir == actionFocusUp && i > 0:
			return panes[i-1]
		case dir == actionFocusDown && i < len(panes)-1:
			return panes[i+1]
		}
	}

	return w.focus
}

// setFocus moves focus, driving the table's own flag: an unfocused table
// ignores the nav keys.
func (w prowlarrWindow) setFocus(p Pane) prowlarrWindow {
	w.focus = p

	if p == paneResults {
		w.table.Focus()
	} else {
		w.table.Blur()
	}

	return w
}

// selectionSummary is the search pane's note on what the next search is
// restricted to.
func (w prowlarrWindow) selectionSummary() string {
	return "categories: " + countOrAll(len(w.params.Categories)) + "  indexers: " + countOrAll(len(w.params.IndexerIDs))
}

// countOrAll is a restriction's size, or "all" for none.
func countOrAll(n int) string {
	if n == 0 {
		return "all"
	}

	return strconv.Itoa(n)
}

// view renders the window: the search pane, the results, and, when open, the
// detail pane beneath them.
func (w prowlarrWindow) view(st Styles, l layout, ph placeholder) string {
	panes := []string{w.searchView(st, l.search), w.resultsView(st, l.list, ph)}

	if w.detailOpen {
		panes = append(panes, w.detail.view(st, l.detail, w.focus == paneResultDetail))
	}

	return lipgloss.JoinVertical(lipgloss.Left, panes...)
}

// searchView draws the search pane: the query field, and the restriction
// summary right of it.
func (w prowlarrWindow) searchView(st Styles, r rect) string {
	line := pad(w.queryInput.View(), st.Muted.Render(w.selectionSummary()), r.inner().w)

	return paneBox(st, paneSearch, w.focus == paneSearch).
		Width(r.w).
		Height(r.h).
		Render(paneContent([]string{line}, r))
}

// resultsView draws the results table, with the placeholder under its header
// while it has no rows.
func (w prowlarrWindow) resultsView(st Styles, r rect, ph placeholder) string {
	lines := highlightRow(strings.Split(w.table.View(), "\n"), w.table.Cursor(), st.Table.Selected)

	if len(ph.lines) > 0 && len(w.rows) == 0 {
		lines = placeholderLines(st, lines[0], ph, r.inner())
	}

	return paneBox(st, paneResults, w.focus == paneResults).
		Width(r.w).
		Height(r.h).
		Render(paneContent(lines, r))
}

// resultsPlaceholder is what the results table says while it has no rows.
func (m Model) resultsPlaceholder() placeholder {
	w := m.prowlarr

	switch {
	case w.unconfigured:
		return placeholder{lines: []string{"no Prowlarr server configured", "run: snatcher server add --type prowlarr"}}
	case w.dialing:
		return placeholder{lines: []string{"connecting to " + output.SanitizeTerminal(w.pending) + "…"}}
	case w.client == nil && w.dialErr != nil:
		return placeholder{lines: []string{errorLine(w.dialErr), errorHint(w.dialErr)}, isErr: true}
	case len(w.rows) > 0:
		return placeholder{}
	case w.inFlight:
		return placeholder{lines: []string{"searching…"}}
	case w.err != nil:
		return placeholder{lines: []string{errorLine(w.err), errorHint(w.err)}, isErr: true}
	case !w.searched:
		return placeholder{lines: []string{"press / to search"}}
	}

	return placeholder{lines: []string{"no results"}}
}

// prowlarrDialedMsg is the answer to dialProwlarrServer.
type prowlarrDialedMsg struct {
	gen    int
	name   string
	result ProwlarrDialResult
	status prowlarr.SystemStatus
	err    error
}

// dialProwlarrServer dials a Prowlarr server off the update loop, as dialServer
// does, then asks it for its status: that verifies the API key before the first
// search, and supplies the version the status bar shows.
func dialProwlarrServer(ctx context.Context, dial func(string) (ProwlarrDialResult, error), name string, gen int) tea.Cmd {
	return func() tea.Msg {
		res, err := dial(name)
		if err != nil {
			return prowlarrDialedMsg{gen: gen, name: name, err: err}
		}

		ctx, cancel := context.WithTimeout(ctx, prowlarrTimeout)
		defer cancel()

		status, err := res.Client.SystemStatus(ctx)
		if err != nil {
			return prowlarrDialedMsg{gen: gen, name: name, err: err}
		}

		return prowlarrDialedMsg{gen: gen, name: name, result: res, status: status}
	}
}

// openProwlarr is ctrl+p: bring the window to the front, dialing on first use.
// The first time it opens, the query field takes the keyboard, since a search is
// the only thing an empty window can do.
func (m Model) openProwlarr() (Model, tea.Cmd) {
	first := !m.prowlarr.opened

	m = m.switchWindow(windowProwlarr)
	m.prowlarr.opened = true

	var dial tea.Cmd

	if m.prowlarr.client == nil && !m.prowlarr.dialing {
		m, dial = m.dialDefaultProwlarr()
	}

	m = m.resize(m.width, m.height)

	if !first || m.prowlarr.unconfigured {
		return m, dial
	}

	m, focus := m.beginQuery()

	return m, tea.Batch(dial, focus)
}

// dialDefaultProwlarr dials the Prowlarr server the window starts on: the type's
// default, else the only one, else the first by name. SNATCHER_SERVER is not
// consulted: it names one server, and in a TUI that one is the Transmission
// server the torrents window is on.
func (m Model) dialDefaultProwlarr() (Model, tea.Cmd) {
	if m.deps.DialProwlarr == nil || m.deps.ProwlarrServers == nil {
		m.prowlarr.unconfigured = true

		return m, nil
	}

	servers, err := m.deps.ProwlarrServers()
	if err != nil {
		m.prowlarr.dialErr = err

		return m, nil
	}

	name := defaultAlias(servers)
	if name == "" {
		m.prowlarr.unconfigured = true

		return m, nil
	}

	m.prowlarr.unconfigured = false

	return m.startProwlarrDial(name)
}

// defaultAlias picks the server a list starts on: the one flagged default, else
// the first. The list arrives sorted by alias.
func defaultAlias(servers []ServerEntry) string {
	for _, s := range servers {
		if s.Default {
			return s.Name
		}
	}

	if len(servers) == 0 {
		return ""
	}

	return servers[0].Name
}

// startProwlarrDial dials a Prowlarr alias. The dialers share one error-writer
// swap in the command layer, so a dial never starts while the torrents window's
// server switch is in flight.
func (m Model) startProwlarrDial(name string) (Model, tea.Cmd) {
	if m.live.switching {
		m.toast = "switching servers…"

		return m, nil
	}

	m.prowlarr.gen++
	m.prowlarr.dialing = true
	m.prowlarr.pending = name
	m.toast = "connecting to " + name + "…"

	return m, dialProwlarrServer(m.ctx, m.deps.DialProwlarr, name, m.prowlarr.gen)
}

// prowlarrDialed settles a dial. A failed switch keeps the server the window is
// on; a failed first dial is shown in the results pane. A successful one starts
// the new server from nothing but the query text and the view.
func (m Model) prowlarrDialed(msg prowlarrDialedMsg) Model {
	if msg.gen != m.prowlarr.gen {
		return m
	}

	w := m.prowlarr
	w.dialing = false
	w.pending = ""

	if msg.err != nil {
		if w.client != nil {
			m.toast = "switch failed: " + errorLine(msg.err)
		} else {
			w.dialErr = msg.err
			m.toast = "prowlarr: " + errorShort(msg.err)
		}

		m.prowlarr = w

		return m
	}

	switched := w.client != nil

	w.client, w.serverName, w.serverURL = msg.result.Client, msg.result.Name, msg.result.URL
	w.status, w.dialErr = msg.status, nil
	m.prowlarr = w.reset()

	m.toast = "connected to " + msg.result.Name
	if switched {
		m.toast = "switched to " + msg.result.Name
	}

	if msg.result.Warning != "" {
		m.toast = msg.result.Warning
	}

	return m.resize(m.width, m.height)
}

// switchProwlarr is the picker's switch in the Prowlarr window.
func (m Model) switchProwlarr(name string) (Model, tea.Cmd) {
	if m.deps.DialProwlarr == nil {
		m.toast = "no dialer"

		return m, nil
	}

	return m.startProwlarrDial(name)
}

// refuseRelease is why a Prowlarr action cannot run now, if it cannot.
func (m Model) refuseRelease() (string, bool) {
	w := m.prowlarr

	switch {
	case w.dialing:
		return "connecting to " + w.pending + "…", true
	case w.client == nil:
		return "no Prowlarr server", true
	}

	return "", false
}

// prowlarrWindowHandlers are the Prowlarr window's pane keys.
func prowlarrWindowHandlers() map[Action]handler {
	return map[Action]handler{
		actionWindowProwlarr:     func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.openProwlarr() },
		actionReleaseSearchFocus: func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.beginQuery() },
		actionSearchSubmit:       func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.submitSearch() },
		actionReleaseMore:        func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.fetchMore() },
		actionReleaseDetailOpen:  plain(Model.openReleaseDetail),
	}
}
