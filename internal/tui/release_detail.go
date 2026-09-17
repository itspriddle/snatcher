package tui

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/prowlarr"
)

// releaseDetail is the Prowlarr window's bottom pane: every field of the release
// under the cursor, including the URLs the results table leaves out. It has no
// tabs, so it is a viewport and nothing else.
type releaseDetail struct {
	vp viewport.Model

	// release is the release described, already redacted; has says there is
	// one.
	release prowlarr.Release
	has     bool
}

// newReleaseDetail builds the pane with the detail pane's registry keymap.
func newReleaseDetail() releaseDetail {
	vp := viewport.New()
	vp.KeyMap = viewportKeyMap()

	return releaseDetail{vp: vp}
}

// setRelease points the pane at a redacted release, or at nothing. The same
// release keeps its scroll position; another starts at the top.
func (d releaseDetail) setRelease(r prowlarr.Release, has bool) releaseDetail {
	same := has && d.has && keyOf(r) == keyOf(d.release)
	d.release, d.has = r, has

	d.vp.SetContent(strings.Join(d.lines(), "\n"))

	if !same {
		d.vp.SetYOffset(0)
	}

	return d
}

// lines is the pane's content. Every server string is sanitized here, at the
// render boundary; the URLs arrive already redacted.
func (d releaseDetail) lines() []string {
	if !d.has {
		return []string{"no release selected"}
	}

	r := d.release
	s := output.SanitizeTerminal

	return []string{
		overviewLine("Title", s(r.Title)),
		overviewLine("Indexer", s(r.Indexer)),
		overviewLine("Age", searchAge(r.Age)),
		overviewLine("Published", s(r.PublishDate)),
		overviewLine("Size", output.Bytes(r.Size)),
		overviewLine("Files", searchCount(r.Files)),
		overviewLine("Grabs", searchCount(r.Grabs)),
		overviewLine("Seeders", searchCount(r.Seeders)),
		overviewLine("Leechers", searchCount(r.Leechers)),
		overviewLine("Protocol", s(r.Protocol)),
		overviewLine("Categories", s(categoryNames(r.Categories))),
		overviewLine("Info Hash", s(r.InfoHash)),
		overviewLine("Info URL", s(r.InfoURL)),
		overviewLine("Comment URL", s(r.CommentURL)),
		overviewLine("Download URL", s(r.DownloadURL)),
		overviewLine("Magnet", s(r.MagnetURL)),
	}
}

// categoryNames is a release's categories as "Name (id), …".
func categoryNames(cats []prowlarr.Category) string {
	parts := make([]string, 0, len(cats))
	for _, c := range cats {
		parts = append(parts, c.Name+" ("+strconv.Itoa(c.ID)+")")
	}

	return strings.Join(parts, ", ")
}

// resize fits the viewport inside the pane's border.
func (d releaseDetail) resize(r rect) releaseDetail {
	inner := r.inner()

	d.vp.SetWidth(inner.w)
	d.vp.SetHeight(max(1, inner.h))
	d.vp.SetContent(strings.Join(d.lines(), "\n"))

	return d
}

// forward hands a navigation key to the viewport.
func (d releaseDetail) forward(msg tea.Msg) (releaseDetail, tea.Cmd) {
	var cmd tea.Cmd

	d.vp, cmd = d.vp.Update(msg)

	return d, cmd
}

// wheel scrolls the pane by delta notches.
func (d releaseDetail) wheel(delta int) releaseDetail {
	d.vp = wheelViewport(d.vp, delta)

	return d
}

// view renders the scrolled lines into r, each cut escape-aware first.
func (d releaseDetail) view(st Styles, r rect, focused bool) string {
	inner := r.inner()

	body := d.lines()
	for i, line := range body {
		body[i] = ansi.Truncate(line, inner.w, "…")
	}

	vp := d.vp
	vp.SetContent(strings.Join(body, "\n"))
	vp.SetYOffset(d.vp.YOffset())

	return paneBox(st, paneResultDetail, focused).
		Width(r.w).
		Height(r.h).
		Render(paneContent(strings.Split(vp.View(), "\n"), r))
}

// openReleaseDetail is enter on a result: open the detail pane and focus it.
func (m Model) openReleaseDetail() Model {
	if _, ok := m.prowlarr.cursorRelease(); !ok {
		m.toast = "nothing selected"

		return m
	}

	m.prowlarr.detailOpen = true

	return m.resize(m.width, m.height).setFocus(paneResultDetail)
}
