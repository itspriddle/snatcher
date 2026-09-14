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

// altSpeedMsg is the answer to setAltSpeed.
type altSpeedMsg struct {
	gen int
	on  bool
	err error
}

// dialedMsg is the answer to dialServer.
type dialedMsg struct {
	gen    int
	name   string
	result DialResult
	err    error
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

	// pollAgain is a refetch asked for while a poll was in flight. A tick that
	// arrives in flight is dropped, so without it the refetch after a mutation
	// would wait a whole interval; the reply polls again at once instead.
	pollAgain bool

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

	// stats is the last fast poll's session stats, and down and up the recent
	// rates the status bar's sparklines draw.
	stats    transmission.Stats
	down, up rateRing

	// info and session are the last slow refresh; hasSession says there was one.
	info       transmission.ServerInfo
	session    transmission.SessionSettings
	hasSession bool

	// free is the free space in the session's download directory; hasFree says
	// the server reported it.
	free    transmission.FreeSpace
	hasFree bool

	// detailWant is the torrent the detail pane last asked for, detailSeq
	// retires superseded debounce timers, and detailInFlight is the torrent a
	// detail fetch is running for, 0 for none. Transmission IDs start at 1.
	detailWant     int64
	detailSeq      int
	detailInFlight int64
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

	// The detail rides along while the pane is open, so it refreshes at the
	// poll's cadence, under its single-flight, and on R, for free.
	if id, ok := m.wantedDetailID(); ok && m.live.detailInFlight == 0 {
		var detail tea.Cmd

		m, detail = m.startDetailFetch(id)
		cmds = append(cmds, detail)
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

		return m.afterPoll()
	}

	m.live.err = nil
	m.live.loaded = true
	m.live.stats = msg.stats
	m.live.down = m.live.down.push(msg.stats.DownloadSpeed)
	m.live.up = m.live.up.push(msg.stats.UploadSpeed)
	m = m.refreshTorrents(msg.torrents)

	// A poll can take the cursor's torrent away, which moves the detail's want.
	m, next := m.afterPoll()

	return m.ensureDetail(next)
}

// afterPoll is what a poll's reply schedules: the queued refetch, when one was
// asked for while it ran, and the next tick otherwise.
func (m Model) afterPoll() (Model, tea.Cmd) {
	if m.live.pollAgain {
		m.live.pollAgain = false

		return m.pollNow()
	}

	return m.scheduleTick()
}

// requestPoll is the refetch after a mutation: poll now, or once the poll in
// flight answers.
func (m Model) requestPoll() (Model, tea.Cmd) {
	switch {
	case !m.polling() || m.live.switching:
		return m, nil
	case m.live.inFlight:
		m.live.pollAgain = true

		return m, nil
	}

	return m.pollNow()
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

// refreshTorrents installs polled torrents; the window's view filters and sorts
// them into rows.
func (m Model) refreshTorrents(rows []transmission.Torrent) Model {
	m.torrents = m.torrents.setAll(rows, m.icons)

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

// dialServer dials a server off the update loop: resolving its secret can open
// the OS keyring, and a Keychain prompt would otherwise freeze the frame.
func dialServer(dial func(string) (DialResult, error), name string, gen int) tea.Cmd {
	return func() tea.Msg {
		res, err := dial(name)

		return dialedMsg{gen: gen, name: name, result: res, err: err}
	}
}

// switchServer starts a switch to a named server. The generation bump retires
// every reply and tick the old server still has in flight, and a second switch
// is refused until this one lands, which is what keeps the dialer's single
// error-writer swap safe.
func (m Model) switchServer(name string) (Model, tea.Cmd) {
	if m.deps.Dial == nil {
		m.toast = "no dialer"

		return m, nil
	}

	m.live.gen++
	m.live.switching = true
	m.live.inFlight = false
	m.live.pollAgain = false
	m.live.detailInFlight = 0
	m.toast = "connecting to " + name + "…"

	return m, dialServer(m.deps.Dial, name, m.live.gen)
}

// dialed settles a switch. A failed dial keeps the old server and resumes its
// polling, whose ticks the generation bump retired. A successful one tears the
// old client down and starts the new server from nothing: empty rows, a fresh
// settings window, and an immediate poll that carries the slow refresh.
func (m Model) dialed(msg dialedMsg) (Model, tea.Cmd) {
	if msg.gen != m.live.gen {
		return m, nil
	}

	m.live.switching = false

	if msg.err != nil {
		m.toast = "switch failed: " + errorLine(msg.err)

		return m.pollNow()
	}

	if m.client != nil {
		_ = m.client.Close()
	}

	m.client, m.serverURL, m.serverName = msg.result.Client, msg.result.URL, msg.result.Name
	m.live = liveData{gen: msg.gen}
	m.torrents = m.torrents.reset(m.icons)

	// The reset blurred the search field; the keyboard is released with it.
	if m.owner == ownerSearch {
		m.inputFocused, m.owner = false, ownerNone
	}
	m.settings = newSettingsWindow(m.styles, m.deps)

	// The reset cleared the filter, so the list pane may have lost its footer.
	m = m.resize(m.width, m.height)

	var settingsCmd tea.Cmd
	if m.window == windowSettings {
		m, settingsCmd = m.openSettings()
	}

	m.toast = "switched to " + msg.result.Name
	if msg.result.Warning != "" {
		m.toast = msg.result.Warning
	}

	m, poll := m.pollNow()

	return m, tea.Batch(settingsCmd, poll)
}

// setAltSpeed turns alt-speed on or off.
func setAltSpeed(ctx context.Context, client *transmission.Client, gen int, on bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
		defer cancel()

		err := client.SetSession(ctx, map[string]any{"alt-speed-enabled": on})

		return altSpeedMsg{gen: gen, on: on, err: err}
	}
}

// toggleAltSpeed is t: flip alt-speed from what the last slow refresh said. It
// does not confirm — it is one switch, and t again undoes it. Before the first
// slow refresh there is nothing to flip from, and guessing could turn on the
// mode the user meant to turn off.
func (m Model) toggleAltSpeed() (Model, tea.Cmd) {
	switch {
	case m.client == nil:
		m.toast = "no server"

		return m, nil
	case m.live.switching:
		m.toast = "switching servers…"

		return m, nil
	case !m.live.hasSession:
		m.toast = "alt-speed: waiting for the server"

		return m, nil
	}

	m.toast = "alt-speed: switching…"

	return m, setAltSpeed(m.ctx, m.client, m.live.gen, !m.live.session.AltSpeedEnabled)
}

// altSpeedSet settles t. A success shows at once, then re-reads the session so
// the bar's limits are what the server actually kept. The settings window's
// loaded Bandwidth tab is left alone; R there reloads it.
func (m Model) altSpeedSet(msg altSpeedMsg) (Model, tea.Cmd) {
	if msg.gen != m.live.gen {
		return m, nil
	}

	if msg.err != nil {
		m.toast = "alt-speed: " + errorLine(msg.err)

		return m, nil
	}

	m.live.session.AltSpeedEnabled = msg.on

	m.toast = "alt-speed off"
	if msg.on {
		m.toast = "alt-speed on"
	}

	return m, pollSession(m.ctx, m.client, m.live.gen)
}
