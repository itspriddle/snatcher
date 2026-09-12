package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// slowRefreshEvery is how many fast polls pass between slow refreshes. A fast
// poll is the torrent list and the session stats; a slow refresh is the session
// (versions, limits, alt-speed) and free space, which change rarely enough that
// fetching them every tick would be load for no information.
const slowRefreshEvery = 6

// defaultPollInterval stands in for a zero Deps.PollInterval. The command layer
// always resolves one, so this only keeps a hand-built Deps from polling in a
// tight loop.
const defaultPollInterval = 5 * time.Second

// tickMsg asks for a poll. gen and seq retire ticks that no longer apply:
// tea.Tick cannot be canceled, so a tick scheduled before a refresh or a server
// switch is dropped when it fires rather than stopped.
type tickMsg struct {
	gen, seq int
}

// pollMsg is the answer to pollTorrents.
type pollMsg struct {
	gen      int
	torrents []transmission.Torrent
	stats    transmission.Stats
	err      error
}

// sessionMsg is the answer to pollSession.
type sessionMsg struct {
	gen     int
	info    transmission.ServerInfo
	session transmission.SessionSettings
	free    transmission.FreeSpace
	hasFree bool
	err     error
}

// liveData is everything the refresh spine owns. A server switch replaces it
// wholesale, so nothing from one server can leak into another's status bar.
type liveData struct {
	// gen bumps on every server switch, and tickSeq on every scheduled tick.
	// A reply from another generation, or a tick from another sequence, is
	// dropped.
	gen, tickSeq int

	// inFlight is set while a fast poll is running, so polls never overlap.
	inFlight bool

	// polls counts fast polls started; the first, and every slowRefreshEvery
	// after it, carries a slow refresh along.
	polls int

	// slowDue makes the next poll carry a slow refresh whatever the count: R, a
	// server switch, and anything that changed the session set it.
	slowDue bool

	// switching is set while a server switch's dial is in flight.
	switching bool

	// loaded reports that at least one fast poll has succeeded.
	loaded bool

	// err is the last reply's error, cleared by the next successful poll.
	err error

	// stats is the last fast poll's session stats.
	stats transmission.Stats

	// info and session are the last slow refresh; hasSession says there was one.
	info       transmission.ServerInfo
	session    transmission.SessionSettings
	hasSession bool

	// free is the free space in the session's download directory; hasFree says
	// the server reported it.
	free    transmission.FreeSpace
	hasFree bool
}

// polling reports whether the spine should run: there is a server, and the rows
// are not a static set a test supplied.
func (m Model) polling() bool {
	return m.client != nil && m.deps.Torrents == nil
}

// pollTorrents fetches every torrent's full record and the session stats. The
// first error wins: a list without its stats, or stats without a list, would
// draw a status bar that disagrees with the table.
func pollTorrents(ctx context.Context, client *transmission.Client, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
		defer cancel()

		rows, err := client.ListTorrents(ctx, transmission.Selector{All: true}, transmission.AllFields...)
		if err != nil {
			return pollMsg{gen: gen, err: err}
		}

		stats, err := client.Stats(ctx)
		if err != nil {
			return pollMsg{gen: gen, err: err}
		}

		return pollMsg{gen: gen, torrents: rows, stats: stats}
	}
}

// pollSession fetches the session and the free space in its download directory.
// A session with no download directory has nowhere to measure, so free space is
// skipped rather than asked about the server's working directory.
func pollSession(ctx context.Context, client *transmission.Client, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
		defer cancel()

		info, session, err := client.Session(ctx)
		if err != nil {
			return sessionMsg{gen: gen, err: err}
		}

		msg := sessionMsg{gen: gen, info: info, session: session}

		if session.DownloadDir == "" {
			return msg
		}

		free, err := client.FreeSpace(ctx, session.DownloadDir)
		if err != nil {
			return sessionMsg{gen: gen, err: err}
		}

		msg.free, msg.hasFree = free, true

		return msg
	}
}

// pollInterval is the interval to schedule the next tick with.
func (m Model) pollInterval() time.Duration {
	if m.deps.PollInterval <= 0 {
		return defaultPollInterval
	}

	return m.deps.PollInterval
}

// scheduleTick schedules the next poll one interval from now, retiring any tick
// already scheduled.
func (m Model) scheduleTick() (Model, tea.Cmd) {
	m.live.tickSeq++

	msg := tickMsg{gen: m.live.gen, seq: m.live.tickSeq}

	return m, tea.Tick(m.pollInterval(), func(time.Time) tea.Msg { return msg })
}

// pollNow polls at once, retiring any tick already scheduled.
func (m Model) pollNow() (Model, tea.Cmd) {
	m.live.tickSeq++

	msg := tickMsg{gen: m.live.gen, seq: m.live.tickSeq}

	return m, func() tea.Msg { return msg }
}

// tick starts a poll, unless the tick is stale or a poll could not start.
func (m Model) tick(msg tickMsg) (Model, tea.Cmd) {
	if msg.gen != m.live.gen || msg.seq != m.live.tickSeq {
		return m, nil
	}

	if !m.polling() || m.live.inFlight || m.live.switching {
		return m, nil
	}

	return m.startPoll()
}

// startPoll runs a fast poll, with a slow refresh riding along when one is due.
func (m Model) startPoll() (Model, tea.Cmd) {
	m.live.inFlight = true
	m.live.polls++

	cmds := []tea.Cmd{pollTorrents(m.ctx, m.client, m.live.gen)}

	if (m.live.polls-1)%slowRefreshEvery == 0 || m.live.slowDue {
		m.live.slowDue = false
		cmds = append(cmds, pollSession(m.ctx, m.client, m.live.gen))
	}

	return m, tea.Batch(cmds...)
}

// polled installs a fast poll's answer and schedules the next tick. The interval
// runs from the reply rather than the request, so a slow server stretches the
// cadence instead of piling polls up behind it. A failed poll keeps the rows it
// already has: stale rows under an error are more useful than none.
func (m Model) polled(msg pollMsg) (Model, tea.Cmd) {
	if msg.gen != m.live.gen {
		return m, nil
	}

	m.live.inFlight = false

	if msg.err != nil {
		m.live.err = msg.err

		return m.scheduleTick()
	}

	m.live.err = nil
	m.live.loaded = true
	m.live.stats = msg.stats
	m = m.refreshTorrents(msg.torrents)

	return m.scheduleTick()
}

// sessionPolled installs a slow refresh's answer. Its error never replaces one
// the fast poll already reported, which is the more fundamental of the two.
func (m Model) sessionPolled(msg sessionMsg) (Model, tea.Cmd) {
	if msg.gen != m.live.gen {
		return m, nil
	}

	if msg.err != nil {
		if m.live.err == nil {
			m.live.err = msg.err
		}

		return m, nil
	}

	m.live.info, m.live.session, m.live.hasSession = msg.info, msg.session, true
	m.live.free, m.live.hasFree = msg.free, msg.hasFree

	return m, nil
}

// refreshTorrents installs polled rows in queue order, the CLI's default
// list_sort.
func (m Model) refreshTorrents(rows []transmission.Torrent) Model {
	m.torrents = m.torrents.setRows(transmission.SortTorrents(rows, transmission.SortQueue, false), m.icons)

	return m
}

// refreshNow is R in the torrents window: poll at once, with a slow refresh.
func (m Model) refreshNow() (Model, tea.Cmd) {
	switch {
	case !m.polling():
		m.toast = "no server"

		return m, nil
	case m.live.switching:
		m.toast = "switching servers…"

		return m, nil
	case m.live.inFlight:
		m.toast = "refresh already in flight"

		return m, nil
	}

	m.live.slowDue = true

	return m.pollNow()
}
