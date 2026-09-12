package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"

	"github.com/itspriddle/snatcher/internal/output"
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
}

// newDetailPane builds the pane with the registry's navigation keys.
//
// viewport.New installs DefaultKeyMap and offers no option to override it, so
// the keymap is assigned after construction. Its defaults bind space, f, b, u,
// d, h, and l — every one of which means something else in vision.md.
func newDetailPane() detailPane {
	vp := viewport.New()
	vp.KeyMap = viewportKeyMap()

	return detailPane{vp: vp}
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
func (p detailPane) setTorrent(t *transmission.Torrent) detailPane {
	p.torrent = t

	return p.refresh()
}

// gotoTab selects a tab by index, clamped to the tabs that exist.
func (p detailPane) gotoTab(i int) detailPane {
	p.tab = clamp(i, 0, len(detailTabs)-1)

	return p.refresh()
}

// nextTab and prevTab cycle the tabs, wrapping in both directions.
func (p detailPane) nextTab() detailPane {
	p.tab = (p.tab + 1) % len(detailTabs)

	return p.refresh()
}

func (p detailPane) prevTab() detailPane {
	p.tab = (p.tab + len(detailTabs) - 1) % len(detailTabs)

	return p.refresh()
}

// refresh rebuilds the viewport's content for the current tab and torrent.
func (p detailPane) refresh() detailPane {
	p.vp.SetContent(strings.Join(p.body(), "\n"))
	p.vp.SetYOffset(0)

	return p
}

// body is the current tab's content.
//
// Only the overview tab has content in T1. The rest name themselves and say what
// they are waiting for, which is more honest than an empty pane: the data they
// render comes from TorrentDetail, and fetching it is T5's job.
func (p detailPane) body() []string {
	if p.torrent == nil {
		return []string{"no torrent selected"}
	}

	if p.tab != tabOverview {
		return []string{fmt.Sprintf("%s: not implemented yet", detailTabs[p.tab])}
	}

	t := p.torrent

	lines := []string{
		field("Name", output.SanitizeTerminal(t.Name)),
		field("Hash", t.Hash),
		field("Status", t.Status.String()),
		field("Done", fmt.Sprintf("%s of %s", output.Percent(t.PercentDone), output.Bytes(t.TotalSize))),
		field("Rate", fmt.Sprintf("%s down / %s up", output.Speed(t.RateDownload), output.Speed(t.RateUpload))),
		field("Ratio", output.Ratio(t.UploadRatio)),
		field("ETA", output.ETA(t.ETA)),
		field("Peers", fmt.Sprintf("%d connected", t.PeersConnected)),
		field("Location", output.SanitizeTerminal(t.DownloadDir)),
		field("Labels", output.SanitizeTerminal(strings.Join(t.Labels, ", "))),
	}

	if t.Error != "" {
		lines = append(lines, field("Error", output.SanitizeTerminal(t.Error)))
	}

	return lines
}

// field formats one label/value line of the overview.
func field(label, value string) string {
	return fmt.Sprintf("%-10s %s", label, value)
}

// resize fits the pane's viewport to r, leaving room for the tab strip and its
// rule.
func (p detailPane) resize(r rect) detailPane {
	inner := r.inner()

	p.vp.SetWidth(inner.w)
	p.vp.SetHeight(max(1, inner.h-2))

	return p
}

// view renders the tab strip, a rule, and the scrolled body into r.
func (p detailPane) view(st Styles, r rect, focused bool) string {
	inner := r.inner()

	lines := []string{renderTabs(st, detailTabs, p.tab, inner.w), tabRule(st, inner.w)}
	lines = append(lines, strings.Split(p.vp.View(), "\n")...)

	return paneBox(st, paneDetail, focused).
		Width(r.w).
		Height(r.h).
		Render(paneContent(lines, r))
}
