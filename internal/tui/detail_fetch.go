package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// detailDebounce is how long the cursor must rest on a torrent before its detail
// is fetched. A held j walks a row every key repeat, and without the wait each
// row would cost a round trip nobody reads.
const detailDebounce = 150 * time.Millisecond

// detailMsg is the answer to fetchDetail. id is the torrent that was asked for,
// which a reply for a torrent no longer under the cursor is dropped by.
type detailMsg struct {
	gen    int
	id     int64
	detail transmission.TorrentDetail
	err    error
}

// detailTickMsg is the debounce timer firing. Like tickMsg, it cannot be
// canceled, so a timer a later cursor move superseded is dropped by seq.
type detailTickMsg struct {
	gen, seq int
}

// fetchDetail reads one torrent's full detail: files, peers, trackers, pieces,
// and the overview's scalars, none of which the poll carries.
func fetchDetail(ctx context.Context, client *transmission.Client, gen int, id int64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
		defer cancel()

		detail, err := client.TorrentDetail(ctx, id)

		return detailMsg{gen: gen, id: id, detail: detail, err: err}
	}
}

// wantedDetailID is the torrent the detail pane should be describing: the cursor
// row, while the pane is open and there is a server to ask.
func (m Model) wantedDetailID() (int64, bool) {
	if !m.torrents.detailOpen || !m.polling() {
		return 0, false
	}

	row, ok := m.torrents.sel.row(m.torrents.rows)

	return row.ID, ok
}

// ensureDetail follows every transition that can move the cursor or open the
// pane: when the torrent the pane wants has changed, it starts the debounce. It
// batches onto the command the transition already returned.
func (m Model) ensureDetail(cmd tea.Cmd) (Model, tea.Cmd) {
	id, ok := m.wantedDetailID()
	if !ok {
		m.live.detailWant = 0

		return m, cmd
	}

	if id == m.live.detailWant {
		return m, cmd
	}

	m.live.detailWant = id

	m, debounce := m.debounceDetail()

	return m, tea.Batch(cmd, debounce)
}

// debounceDetail schedules a detail fetch one debounce from now, retiring any
// timer already scheduled.
func (m Model) debounceDetail() (Model, tea.Cmd) {
	m.live.detailSeq++

	msg := detailTickMsg{gen: m.live.gen, seq: m.live.detailSeq}

	return m, tea.Tick(detailDebounce, func(time.Time) tea.Msg { return msg })
}

// detailTick fetches the wanted detail once the cursor has rested, unless the
// timer is stale or a fetch for that torrent is already running. A fetch running
// for another torrent is left to finish: its reply sees the want has moved and
// debounces again.
func (m Model) detailTick(msg detailTickMsg) (Model, tea.Cmd) {
	if msg.gen != m.live.gen || msg.seq != m.live.detailSeq || m.live.switching {
		return m, nil
	}

	id, ok := m.wantedDetailID()
	if !ok || id != m.live.detailWant || m.live.detailInFlight != 0 {
		return m, nil
	}

	return m.startDetailFetch(id)
}

// startDetailFetch runs a detail fetch and records it as the one in flight.
func (m Model) startDetailFetch(id int64) (Model, tea.Cmd) {
	m.live.detailWant = id
	m.live.detailInFlight = id

	return m, fetchDetail(m.ctx, m.client, m.live.gen, id)
}

// detailFetched installs a detail. A reply for a torrent the cursor has left is
// dropped, and the torrent it is on now is debounced instead: a fetch that
// outlived its cursor must not draw one torrent's files under another's name.
func (m Model) detailFetched(msg detailMsg) (Model, tea.Cmd) {
	if msg.gen != m.live.gen {
		return m, nil
	}

	m.live.detailInFlight = 0

	id, ok := m.wantedDetailID()

	switch {
	case !ok:
		return m, nil
	case id != msg.id:
		return m.debounceDetail()
	case msg.err != nil:
		m.torrents.detail = m.torrents.detail.setErr(msg.id, msg.err)
	default:
		m.torrents.detail = m.torrents.detail.setDetail(msg.detail)
	}

	return m, nil
}
