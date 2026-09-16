package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/prowlarr"
)

// releaseLabel names a release for a human, as "title [indexer]", falling back
// to the guid and the indexer id. It is internal/commands' releaseLabel; the
// caller sanitizes it.
func releaseLabel(release prowlarr.Release) string {
	name := release.Title
	if name == "" {
		name = release.GUID
	}

	indexer := release.Indexer
	if indexer == "" {
		indexer = "indexer " + strconv.Itoa(release.IndexerID)
	}

	return fmt.Sprintf("%s [%s]", name, indexer)
}

// actionRelease is the release a verb acts on: the cursor release, or why there
// is none to act on.
func (m Model) actionRelease() (prowlarr.Release, string, bool) {
	if why, refused := m.refuseRelease(); refused {
		return prowlarr.Release{}, why, false
	}

	r, ok := m.prowlarr.cursorRelease()
	if !ok {
		return prowlarr.Release{}, "nothing selected", false
	}

	return r, "", true
}

// displayLabel is a release's label as the terminal may draw it.
func (m Model) displayLabel(r prowlarr.Release) string {
	return output.SanitizeTerminal(releaseLabel(m.prowlarr.redact(r)))
}

// grabMsg is the answer to grabRelease.
type grabMsg struct {
	gen   int
	label string
	err   error
}

// grabRelease hands a release to Prowlarr, byte-exact.
func grabRelease(ctx context.Context, client *prowlarr.Client, gen int, r prowlarr.Release, label string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, prowlarrTimeout)
		defer cancel()

		_, err := client.Grab(ctx, r)

		return grabMsg{gen: gen, label: label, err: err}
	}
}

// grab is a: ask, then hand the release to Prowlarr. The question says where it
// goes, because Prowlarr's download client is not necessarily the server the
// torrents window is on.
func (m Model) grab() (Model, tea.Cmd) {
	r, why, ok := m.actionRelease()
	if !ok {
		m.toast = why

		return m, nil
	}

	label := m.displayLabel(r)
	gen := m.prowlarr.gen

	o := newConfirm("Grab "+label+"?", func(m Model) (Model, tea.Cmd) {
		if m.prowlarr.gen != gen || m.prowlarr.client == nil {
			m.toast = "the Prowlarr server changed; nothing grabbed"

			return m, nil
		}

		m.toast = "Grabbing: " + label

		return m, grabRelease(m.ctx, m.prowlarr.client, gen, r, label)
	}, nil)
	o.secondary = "It goes to Prowlarr's download client, not this server."

	return m.push(o), nil
}

// grabbed settles a grab.
func (m Model) grabbed(msg grabMsg) Model {
	if msg.gen != m.prowlarr.gen {
		return m
	}

	if msg.err != nil {
		m.toast = "grab failed: " + errorLine(msg.err)

		return m
	}

	m.toast = "Grabbed: " + msg.label

	return m
}

// writeMsg is the answer to writeRelease.
type writeMsg struct {
	gen  int
	path string
	n    int
	err  error
}

// writeRelease downloads a release's .torrent and writes it to path.
func writeRelease(ctx context.Context, client *prowlarr.Client, gen int, r prowlarr.Release, path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, prowlarrTimeout)
		defer cancel()

		data, err := client.Download(ctx, r)
		if err != nil {
			return writeMsg{gen: gen, path: path, err: err}
		}

		return writeMsg{gen: gen, path: path, n: len(data), err: writeTorrentFile(path, data)}
	}
}

// writeTorrent is w: ask where to write the release's .torrent, suggesting the
// name `snatcher search download` would derive, in the working directory.
func (m Model) writeTorrent() (Model, tea.Cmd) {
	r, why, ok := m.actionRelease()
	if !ok {
		m.toast = why

		return m, nil
	}

	if strings.TrimSpace(r.DownloadURL) == "" {
		m.toast = "no download url"
		if r.MagnetURL != "" {
			m.toast += "; y m yanks the magnet"
		}

		return m, nil
	}

	gen := m.prowlarr.gen
	prefill := output.SanitizeTerminal(torrentFilename(r.Title))

	return m.openPrompt(newPrompt(m.styles, "Write .torrent", "an existing file is never overwritten", prefill,
		func(m Model, value string) (Model, tea.Cmd) {
			path := strings.TrimSpace(value)
			if path == "" {
				return m.rejectPrompt(errors.New("a file name is required")), nil
			}

			if err := checkTorrentPath(path); err != nil {
				return m.rejectPrompt(err), nil
			}

			m = m.pop()

			if m.prowlarr.gen != gen || m.prowlarr.client == nil {
				m.toast = "the Prowlarr server changed; nothing written"

				return m, nil
			}

			m.toast = "Writing " + path + "…"

			return m, writeRelease(m.ctx, m.prowlarr.client, gen, r, path)
		}))
}

// wrote settles a write.
func (m Model) wrote(msg writeMsg) Model {
	if msg.gen != m.prowlarr.gen {
		return m
	}

	if msg.err != nil {
		m.toast = "write failed: " + errorLine(msg.err)

		return m
	}

	m.toast = fmt.Sprintf("Wrote %s (%d bytes)", msg.path, msg.n)

	return m
}

// openedMsg is the answer to the browser opener; only a failure is reported.
type openedMsg struct{ err error }

// openURL opens a redacted URL in the browser, or yanks it when there is no
// browser to open.
func (m Model) openURL(url, what string) (Model, tea.Cmd) {
	if m.deps.OpenURL == nil {
		m.toast = "no browser here; yanked " + what

		return m, m.yank(output.SanitizeTerminal(url))
	}

	open, ctx := m.deps.OpenURL, m.ctx
	m.toast = "opening " + what + "…"

	return m, func() tea.Msg { return openedMsg{err: open(ctx, url)} }
}

// urlOpened settles an open.
func (m Model) urlOpened(msg openedMsg) Model {
	if msg.err != nil {
		m.toast = "open failed: " + msg.err.Error()
	}

	return m
}

// openReleasePage is O: the release's page on its indexer, or its comments page
// when it has no page of its own.
func (m Model) openReleasePage() (Model, tea.Cmd) {
	r, why, ok := m.actionRelease()
	if !ok {
		m.toast = why

		return m, nil
	}

	red := m.prowlarr.redact(r)

	switch {
	case red.InfoURL != "":
		return m.openURL(red.InfoURL, "the release page")
	case red.CommentURL != "":
		return m.openURL(red.CommentURL, "the comments page")
	}

	m.toast = "no info or comment URL"

	return m, nil
}

// openProwlarrUI is P: the Prowlarr server's own web UI.
func (m Model) openProwlarrUI() (Model, tea.Cmd) {
	if why, refused := m.refuseRelease(); refused {
		m.toast = why

		return m, nil
	}

	return m.openURL(m.prowlarr.redactURL(m.prowlarr.serverURL), "Prowlarr")
}

// releaseYank is one of the y chord's second keys: a field of the cursor
// release, redacted.
func releaseYank(what string, value func(prowlarr.Release) string, note string) handler {
	return func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) {
		r, why, ok := m.actionRelease()
		if !ok {
			m.toast = why

			return m, nil
		}

		v := value(m.prowlarr.redact(r))
		if v == "" {
			m.toast = "no " + what

			return m, nil
		}

		m.toast = "yanked " + what + note

		return m, m.yank(output.SanitizeTerminal(v))
	}
}

// releaseMenu is m in the Prowlarr window: every release verb, including
// the yanks and P.
func releaseMenu() menuOverlay {
	return menuOverlay{
		title: "Release",
		items: []menuItem{
			{label: "grab", action: actionReleaseGrab, note: "via Prowlarr"},
			{label: "write .torrent", action: actionReleaseWrite},
			{label: "open in browser", action: actionReleaseOpen},
			{label: "yank", sub: []menuItem{
				{label: "info URL", action: actionYankInfoURL},
				{label: "magnet link", action: actionYankReleaseMagnet},
				{label: "download URL", action: actionYankDownloadURL},
			}},
			{label: "open Prowlarr", action: actionProwlarrOpen},
		},
	}
}

// openReleaseMenu is m: open the menu over the cursor release. The menu's own m
// closes it.
func (m Model) openReleaseMenu() Model {
	if _, why, ok := m.actionRelease(); !ok {
		m.toast = why

		return m
	}

	return m.push(releaseMenu())
}

// releaseActionHandlers are the release verbs and the y chord.
func releaseActionHandlers() map[Action]handler {
	return map[Action]handler{
		actionReleaseGrab:      func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.grab() },
		actionReleaseWrite:     func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.writeTorrent() },
		actionReleaseOpen:      func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.openReleasePage() },
		actionProwlarrOpen:     func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.openProwlarrUI() },
		actionReleaseMenu:      plain(Model.openReleaseMenu),
		actionReleaseYankChord: plain(func(m Model) Model { m.chord = chordYankRelease; return m }),

		actionYankInfoURL: releaseYank("info URL", func(r prowlarr.Release) string { return r.InfoURL }, ""),
		actionYankReleaseMagnet: releaseYank("magnet link", func(r prowlarr.Release) string {
			return r.MagnetURL
		}, ""),
		actionYankDownloadURL: releaseYank("download URL", func(r prowlarr.Release) string {
			return r.DownloadURL
		}, " without its API key; w writes the file"),
	}
}
