package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// addedMsg is the answer to addTorrent.
type addedMsg struct {
	gen    int
	result transmission.AddResult
	err    error
}

// addTorrent adds one source: a local .torrent file, an HTTP(S) URL, or a magnet
// link, classified as `snatcher torrent add` classifies it. A file is read here,
// inside the command, so a slow disk never blocks a frame; like the CLI it is
// read as typed, with no tilde expansion.
func addTorrent(ctx context.Context, client *transmission.Client, gen int, source string, req transmission.AddRequest) tea.Cmd {
	return func() tea.Msg {
		switch transmission.ClassifySource(source) {
		case transmission.SourceFile:
			data, err := os.ReadFile(source)
			if err != nil {
				return addedMsg{gen: gen, err: fmt.Errorf("reading %s: %w", source, err)}
			}

			req.Metainfo = data
		case transmission.SourceURL:
			req.URL = source
		case transmission.SourceMagnet:
			req.Magnet = source
		}

		ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
		defer cancel()

		res, err := client.AddTorrent(ctx, req)

		return addedMsg{gen: gen, result: res, err: err}
	}
}

// refuseAdd says why an add cannot run, or reports that it can. An add names no
// existing torrents, so there is no selection to check.
func (m Model) refuseAdd() (string, bool) {
	switch {
	case m.client == nil:
		return "no server", true
	case m.live.switching:
		return "switching servers…", true
	}

	return "", false
}

// startAdd sends an add. The guards run again: the prompt or form that asked
// may have been answered after the state it opened in changed.
func (m Model) startAdd(source string, req transmission.AddRequest) (Model, tea.Cmd) {
	if why, refused := m.refuseAdd(); refused {
		m.toast = why

		return m, nil
	}

	m.toast = "adding torrent…"

	return m, addTorrent(m.ctx, m.client, m.live.gen, source, req)
}

// added settles an add, in the CLI's words: a duplicate is a success, not an
// error. Either way the list is refetched, so a new torrent appears at once.
func (m Model) added(msg addedMsg) (Model, tea.Cmd) {
	if msg.gen != m.live.gen {
		return m, nil
	}

	if msg.err != nil {
		m.toast = "add: " + errorLine(msg.err)

		return m, nil
	}

	name := output.SanitizeTerminal(msg.result.Name)

	m.toast = "Added " + name
	if msg.result.Duplicate {
		m.toast = "Already added: " + name
	}

	return m.requestPoll()
}

// openAddPrompt is a: add one source with the CLI's defaults, which include
// adding it paused.
func (m Model) openAddPrompt() (Model, tea.Cmd) {
	if why, refused := m.refuseAdd(); refused {
		m.toast = why

		return m, nil
	}

	return m.openPrompt(newPrompt(m.styles, "Add torrent", "file path, URL, or magnet link; added paused", "",
		func(m Model, value string) (Model, tea.Cmd) {
			source := strings.TrimSpace(value)
			if source == "" {
				return m.rejectPrompt(errors.New("a file path, URL, or magnet link is required")), nil
			}

			return m.pop().startAdd(source, transmission.AddRequest{Paused: true})
		}))
}

// openAddForm is A: add one source with a download directory, labels, and
// whether to start it. The directory starts from the session's, when a slow
// refresh has reported one.
func (m Model) openAddForm() (Model, tea.Cmd) {
	if why, refused := m.refuseAdd(); refused {
		m.toast = why

		return m, nil
	}

	dir := ""
	if m.live.hasSession {
		dir = m.live.session.DownloadDir
	}

	return m.push(newAddForm(m.styles, dir)), nil
}

// addFormSubmit is the add form's button. It adds paused unless "Start now" is
// checked, as `snatcher torrent add` does unless --start is given, and it sends
// no download directory while the row still holds the session's own, so the
// server's default applies rather than the sanitized copy the row displays.
func addFormSubmit(m Model, o formOverlay) (Model, tea.Cmd) {
	source := strings.TrimSpace(o.rows[addRowSource].text)
	if source == "" {
		return m.rejectForm(errors.New("a source is required")), nil
	}

	labels, err := parseLabelList(o.rows[addRowLabels].text)
	if err != nil {
		return m.rejectForm(err), nil
	}

	req := transmission.AddRequest{Paused: !o.rows[addRowStart].on}

	if dir := strings.TrimSpace(o.rows[addRowDir].text); dir != o.defaultDir {
		req.DownloadDir = dir
	}

	if len(labels) > 0 {
		req.Labels = labels
	}

	return m.pop().startAdd(source, req)
}

// addHandlers are a, A, and the form's own keys.
func addHandlers() map[Action]handler {
	return map[Action]handler{
		actionTorrentAdd:     func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.openAddPrompt() },
		actionTorrentAddOpts: func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.openAddForm() },

		actionFormNext:   stepTop(func(o formOverlay) formOverlay { return o.move(1) }),
		actionFormPrev:   stepTop(func(o formOverlay) formOverlay { return o.move(-1) }),
		actionFormToggle: stepTop(formOverlay.toggle),
		actionFormEnter:  func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.formEnter() },
	}
}
