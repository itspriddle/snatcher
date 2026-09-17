package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// detailTabs are the detail pane's tabs, in the order the 1–5 keys select them.
var detailTabs = []string{"Overview", "Files", "Trackers", "Peers", "Pieces"}

// The detail pane's tab indices, named so the 1–5 handlers and the tab cycling
// refer to the same thing.
const (
	tabOverview = iota
	tabFiles
	tabTrackers
	tabPeers
	tabPieces
)

// detailPane is the torrents window's bottom pane: a tab strip over a scrollable
// body describing the torrent under the cursor.
//
// It carries no torrent verbs. vision.md is explicit: action verbs live in a
// window's main pane, because the files tab has marks and a cursor of its own
// and a `d` next to a file cursor that might mean "delete the torrent" is
// exactly the ambiguity marks were introduced to remove.
type detailPane struct {
	// tab is the selected tab index.
	tab int

	// vp scrolls the tab body.
	vp viewport.Model

	// torrent is the torrent being described, or nil for none.
	torrent *transmission.Torrent

	// detail is the fetched detail of torrent; hasDetail says there is one. A
	// detail is only ever installed for the torrent it describes.
	detail    transmission.TorrentDetail
	hasDetail bool

	// err is the last fetch's error for this torrent, cleared by a success.
	err error

	// fetches reports that a detail will arrive: the rows come from a server
	// rather than a static set, so an empty tab is "loading", not "nothing".
	fetches bool

	// files, peers, and trackers are the tabs' tables.
	files, peers, trackers detailTable

	// fileSel is the files tab's selection: the same model as the torrent
	// list's, keyed by file index. Its cursor follows the files table's.
	fileSel fileSelection

	// icons draws the files table's mark cell.
	icons Icons

	// focused is whether the pane has focus, kept so a tab switch can hand
	// the table focus to the tab being shown.
	focused bool
}

// newDetailPane builds the pane with the registry's navigation keys.
//
// viewport.New installs DefaultKeyMap and offers no option to override it, so
// the keymap is assigned after construction. Its defaults bind space, f, b, u,
// d, h, and l — every one of which means something else in vision.md.
func newDetailPane(st Styles, icons Icons, fetches bool) detailPane {
	vp := viewport.New()
	vp.KeyMap = viewportKeyMap()

	return detailPane{
		vp:       vp,
		fetches:  fetches,
		icons:    icons,
		fileSel:  newFileSelection(),
		files:    newDetailTable(st, fileSpecs, 1, "no files"),
		peers:    newDetailTable(st, peerSpecs, 0, "no peers"),
		trackers: newDetailTable(st, trackerSpecs, 0, "no trackers"),
	}
}

// viewportKeyMap builds the detail viewport's keymap from the registry. Left and
// Right stay unbound deliberately: in this pane h/l and left/right switch tabs,
// so horizontal scrolling would shadow the tab keys.
func viewportKeyMap() viewport.KeyMap {
	return viewport.KeyMap{
		Up:           bindingFor(ctxNav, actionNavUp),
		Down:         bindingFor(ctxNav, actionNavDown),
		PageUp:       bindingFor(ctxNav, actionNavPageUp),
		PageDown:     bindingFor(ctxNav, actionNavPageDown),
		HalfPageUp:   bindingFor(ctxNav, actionNavHalfPageUp),
		HalfPageDown: bindingFor(ctxNav, actionNavHalfPageDown),
	}
}

// setTorrent points the pane at a torrent, or at nothing.
//
// Every poll calls it, so the same torrent keeps everything the user has done
// with it — the detail, the scroll position — and only a change of subject
// resets them. Resetting on every call would throw the user back to the top of
// the tab each tick.
func (p detailPane) setTorrent(t *transmission.Torrent) detailPane {
	same := t != nil && p.torrent != nil && t.ID == p.torrent.ID
	p.torrent = t

	if !same {
		p.detail, p.hasDetail, p.err = transmission.TorrentDetail{}, false, nil
		p.peers, p.trackers = p.peers.clear(), p.trackers.clear()

		// Another torrent's files are another index space: a mark on index 3
		// would name an unrelated file.
		p.files, p.fileSel = p.files.clear(), newFileSelection()
		p = p.refresh()
		p.vp.SetYOffset(0)

		return p
	}

	return p.refresh()
}

// setDetail installs a fetched detail, when it describes the pane's torrent.
func (p detailPane) setDetail(d transmission.TorrentDetail) detailPane {
	if p.torrent == nil || d.ID != p.torrent.ID {
		return p
	}

	p.detail, p.hasDetail, p.err = d, true, nil

	peers, peerKeys := peerRows(d.Peers)
	p.peers = p.peers.setRows(peers, peerKeys)

	trackers, trackerKeys := trackerRows(d.Trackers, time.Now().Unix())
	p.trackers = p.trackers.setRows(trackers, trackerKeys)

	p.fileSel = p.fileSel.track(p.files.cursor(), true, d.Files)

	return p.rebuildFiles().refresh()
}

// rebuildFiles redraws the files table from the detail and the file marks, and
// pulls the table's cursor back into the selection.
func (p detailPane) rebuildFiles() detailPane {
	rows, keys := fileRows(p.detail.Files, p.fileSel, p.icons)
	p.files = p.files.setRows(rows, keys)
	p.fileSel = p.fileSel.setCursor(p.files.cursor())

	return p
}

// toggleFileMark marks or unmarks the file under the cursor, leaving the cursor
// where it is.
func (p detailPane) toggleFileMark() detailPane {
	if !p.hasDetail {
		return p
	}

	p.fileSel = p.fileSel.setCursor(p.files.cursor()).toggleMark(p.detail.Files)

	return p.rebuildFiles()
}

// clearFileMarks drops every file mark.
func (p detailPane) clearFileMarks() detailPane {
	p.fileSel = p.fileSel.clearMarks()

	return p.rebuildFiles()
}

// setErr records a failed fetch for a torrent, when it is the pane's torrent.
// The last detail stays: an error over stale rows says more than an error alone.
func (p detailPane) setErr(id int64, err error) detailPane {
	if p.torrent == nil || id != p.torrent.ID {
		return p
	}

	p.err = err

	return p.refresh()
}

// gotoTab selects a tab by index, clamped to the tabs that exist.
func (p detailPane) gotoTab(i int) detailPane {
	return p.showTab(clamp(i, 0, len(detailTabs)-1))
}

// nextTab and prevTab cycle the tabs, wrapping in both directions.
func (p detailPane) nextTab() detailPane {
	return p.showTab((p.tab + 1) % len(detailTabs))
}

func (p detailPane) prevTab() detailPane {
	return p.showTab((p.tab + len(detailTabs) - 1) % len(detailTabs))
}

// showTab switches tabs. A different tab is a different body, so it starts at
// its top.
func (p detailPane) showTab(tab int) detailPane {
	changed := tab != p.tab
	p.tab = tab
	p = p.focus(p.focused).refresh()

	if changed {
		p.vp.SetYOffset(0)
	}

	return p
}

// refresh rebuilds the viewport's content for the current tab and torrent,
// keeping the scroll position clamped to it.
//
// The stored content is rendered unstyled: it exists so scrolling is bounded by
// the right number of lines. view renders the styled body, which has the same
// lines, because panes are handed their styles at view time.
func (p detailPane) refresh() detailPane {
	p.vp.SetContent(strings.Join(p.body(Styles{}), "\n"))
	p.vp.SetYOffset(p.vp.YOffset())

	return p
}

// body is the current tab's content.
func (p detailPane) body(st Styles) []string {
	if p.torrent == nil {
		return []string{"no torrent selected"}
	}

	if p.tab == tabOverview {
		return p.overviewLines(st)
	}

	if lines, waiting := p.waiting(st); waiting {
		return lines
	}

	if _, ok := p.currentTable(); ok {
		// The tables draw themselves; the viewport has nothing to scroll.
		return nil
	}

	if p.tab == tabPieces {
		d := p.detail

		return pieceLines(d.Pieces, d.PieceCount, d.PieceSize, d.MetadataPercentComplete, p.vp.Width(), p.vp.Height())
	}

	return nil
}

// currentTable is the current tab's table, when the tab is one.
func (p detailPane) currentTable() (detailTable, bool) {
	switch p.tab {
	case tabFiles:
		return p.files, true
	case tabPeers:
		return p.peers, true
	case tabTrackers:
		return p.trackers, true
	}

	return detailTable{}, false
}

// focus hands the pane's focus to the current tab's table, and takes it from
// every other: a table focused on a hidden tab would eat the nav keys.
func (p detailPane) focus(on bool) detailPane {
	p.focused = on
	p.files = p.files.focus(on && p.tab == tabFiles)
	p.peers = p.peers.focus(on && p.tab == tabPeers)
	p.trackers = p.trackers.focus(on && p.tab == tabTrackers)

	return p
}

// forward hands a navigation key to what the current tab scrolls: its table, or
// the viewport.
func (p detailPane) forward(msg tea.Msg) (detailPane, tea.Cmd) {
	var cmd tea.Cmd

	switch p.tab {
	case tabFiles:
		p.files, cmd = p.files.update(msg)
		p.fileSel = p.fileSel.setCursor(p.files.cursor())
	case tabPeers:
		p.peers, cmd = p.peers.update(msg)
	case tabTrackers:
		p.trackers, cmd = p.trackers.update(msg)
	default:
		p.vp, cmd = p.vp.Update(msg)
	}

	return p, cmd
}

// pointAt puts the current tab's table cursor on row i, and the files tab's
// selection cursor with it, as forward does after a key.
func (p detailPane) pointAt(i int) detailPane {
	switch p.tab {
	case tabFiles:
		p.files.table = pointTable(p.files.table, i)
		p.fileSel = p.fileSel.setCursor(p.files.cursor())
	case tabPeers:
		p.peers.table = pointTable(p.peers.table, i)
	case tabTrackers:
		p.trackers.table = pointTable(p.trackers.table, i)
	}

	return p
}

// wheel scrolls what the current tab scrolls by delta: its table's cursor, or
// the viewport by wheelLines a notch.
func (p detailPane) wheel(delta int) detailPane {
	switch p.tab {
	case tabFiles:
		p.files.table = wheelTable(p.files.table, delta)
		p.fileSel = p.fileSel.setCursor(p.files.cursor())
	case tabPeers:
		p.peers.table = wheelTable(p.peers.table, delta)
	case tabTrackers:
		p.trackers.table = wheelTable(p.trackers.table, delta)
	default:
		p.vp = wheelViewport(p.vp, delta)
	}

	return p
}

// setStyles re-themes the tables, which render their rows with styles of
// their own.
func (p detailPane) setStyles(st Styles) detailPane {
	p.files = p.files.setStyles(st)
	p.peers = p.peers.setStyles(st)
	p.trackers = p.trackers.setStyles(st)

	return p
}

// waiting is what a detail-only tab draws before it has a detail to draw: the
// fetch's error and how to retry, "loading…" while one is on its way, or nothing
// when no detail will come.
func (p detailPane) waiting(st Styles) ([]string, bool) {
	switch {
	case p.err != nil:
		return []string{st.FieldError.Render(errorLine(p.err)), st.Muted.Render(errorHint(p.err))}, true
	case p.hasDetail:
		return nil, false
	case p.fetches:
		return []string{st.Muted.Render("loading…")}, true
	}

	return nil, true
}

// resize fits the pane's viewport to r, leaving room for the tab strip and its
// rule.
func (p detailPane) resize(r rect) detailPane {
	inner := r.inner()

	p.vp.SetWidth(inner.w)
	p.vp.SetHeight(max(1, inner.h-2))
	p.files = p.files.resize(inner)
	p.peers = p.peers.resize(inner)
	p.trackers = p.trackers.resize(inner)

	return p.refresh()
}

// view renders the tab strip, a rule, and the scrolled body into r. The body is
// styled here, on a copy of the viewport, at the offset the stored one scrolled
// to. Every line is cut escape-aware first, so paneContent never splits a
// sequence.
func (p detailPane) view(st Styles, r rect, focused bool) string {
	inner := r.inner()

	body := p.body(st)
	for i, line := range body {
		body[i] = ansi.Truncate(line, inner.w, "…")
	}

	vp := p.vp
	vp.SetContent(strings.Join(body, "\n"))
	vp.SetYOffset(p.vp.YOffset())

	lines := []string{renderTabs(st, detailTabs, p.tab, inner.w), tabRule(st, inner.w)}

	if t, ok := p.currentTable(); ok && len(body) == 0 {
		lines = append(lines, t.lines(st)...)
	} else {
		lines = append(lines, strings.Split(vp.View(), "\n")...)
	}

	return paneBox(st, paneDetail, focused).
		Width(r.w).
		Height(r.h).
		Render(paneContent(lines, r))
}
