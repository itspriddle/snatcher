package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// magnetMsg is the answer to fetchMagnet.
type magnetMsg struct {
	gen  int
	link string
	err  error
}

// fetchMagnet reads one torrent's magnet link. The poll does not carry it — it
// is a detail field, not a list one — so a yank fetches it on demand.
func fetchMagnet(ctx context.Context, client *transmission.Client, gen int, id int64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
		defer cancel()

		detail, err := client.TorrentDetail(ctx, id)
		if err != nil {
			return magnetMsg{gen: gen, err: err}
		}

		return magnetMsg{gen: gen, link: detail.MagnetLink}
	}
}

// yankMagnet is y m: fetch the selected torrent's magnet link, then yank it. It
// names exactly one torrent — the cursor row, or a single mark — because a
// clipboard holds one link, and yanking an arbitrary one of several marks would
// hide which.
func (m Model) yankMagnet() (Model, tea.Cmd) {
	sel := m.listSelector()

	switch {
	case m.client == nil:
		m.toast = "no server"
	case m.live.switching:
		m.toast = "switching servers…"
	case len(sel.IDs) == 0:
		m.toast = "nothing selected"
	case len(sel.IDs) > 1:
		m.toast = "yank magnet: one torrent at a time"
	default:
		m.toast = "yank magnet: fetching…"

		return m, fetchMagnet(m.ctx, m.client, m.live.gen, sel.IDs[0])
	}

	return m, nil
}

// magnetFetched settles y m. Like every yank it reports what was sent rather
// than claiming it arrived: OSC 52 is write-only and not every terminal honors
// it.
func (m Model) magnetFetched(msg magnetMsg) (Model, tea.Cmd) {
	if msg.gen != m.live.gen {
		return m, nil
	}

	switch {
	case msg.err != nil:
		m.toast = "yank magnet: " + errorLine(msg.err)

		return m, nil
	case msg.link == "":
		m.toast = "yank magnet: the server reported no magnet link"

		return m, nil
	}

	m.toast = "yanked magnet link"

	return m, tea.SetClipboard(output.SanitizeTerminal(msg.link))
}
