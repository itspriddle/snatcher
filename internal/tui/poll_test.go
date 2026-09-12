package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// liveModel is a model with no static rows, polling a stub server. The URL the
// status bar shows is fixed rather than the stub's, so a golden does not change
// with the port the stub was given.
func liveModel(t *testing.T, srv *httptest.Server, w, h int) Model {
	t.Helper()

	deps := testDeps()
	deps.Torrents = nil
	deps.Config = newFakeConfig()
	deps.PollInterval = time.Hour

	m := New(deps).applyTheme(true).resize(w, h)
	m.client = transmission.New(srv.URL)
	m.serverURL = "http://nas:9091/transmission/rpc"
	m.serverName = "home"
	m.ctx = t.Context()

	return m
}

// settle runs a command and feeds its message back through update, flattening
// one level of tea.BatchMsg. The follow-up commands — the next scheduled tick —
// are discarded, so no test waits on a timer.
func settle(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()

	require.NotNil(t, cmd, "expected a command to run")

	msg := cmd()

	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		m, _ = m.update(msg)

		return m
	}

	for _, c := range batch {
		if c != nil {
			m, _ = m.update(c())
		}
	}

	return m
}

// pollOnce runs one poll to completion: the tick, then its batch.
func pollOnce(t *testing.T, m Model) Model {
	t.Helper()

	m, cmd := m.pollNow()
	m, cmd = runCmd(t, m, cmd)

	return settle(t, m, cmd)
}

// rowIDs is the IDs of rows, in order.
func rowIDs(rows []transmission.Torrent) []int64 {
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}

	return ids
}

// TestFirstPollFillsTheWindow checks the first poll installs the rows in queue
// order and carries the slow refresh along.
func TestFirstPollFillsTheWindow(t *testing.T) {
	stub, srv := newRPCStub(t)

	m := pollOnce(t, liveModel(t, srv, 120, 40))

	require.NoError(t, m.live.err)
	assert.True(t, m.live.loaded)
	assert.Equal(t, []int64{2, 3, 1}, rowIDs(m.torrents.rows))
	assert.Equal(t, int64(2097152), m.live.stats.DownloadSpeed)

	assert.True(t, m.live.hasSession)
	assert.True(t, m.live.hasFree)
	assert.Equal(t, int64(128849018880), m.live.free.SizeBytes)

	for _, method := range []string{"torrent-get", "session-stats", "free-space"} {
		assert.Equalf(t, 1, stub.calls(method), "%s", method)
	}

	assert.Equal(t, probeSessionGets+1, stub.calls("session-get"))

	assert.False(t, m.live.inFlight)
	assert.Contains(t, ansi.Strip(m.render()), "ubuntu-24.04.iso")
	assert.Contains(t, ansi.Strip(m.render()), "linux", "the sidebar is rebuilt from the polled labels")
}

// TestPollKeepsTheCursorOnItsTorrent checks a poll that reorders the rows moves
// the cursor with its torrent rather than leaving it on an index.
func TestPollKeepsTheCursorOnItsTorrent(t *testing.T) {
	stub, srv := newRPCStub(t)

	m := press(pollOnce(t, liveModel(t, srv, 120, 40)), "down")

	row, ok := m.torrents.sel.row(m.torrents.rows)
	require.True(t, ok)
	require.Equal(t, int64(3), row.ID)

	stub.respond("torrent-get", torrentsBody(stubTorrent{3, "c", 0}, stubTorrent{1, "a", 1}, stubTorrent{2, "b", 2}))

	m = pollOnce(t, m)

	row, ok = m.torrents.sel.row(m.torrents.rows)
	require.True(t, ok)
	assert.Equal(t, int64(3), row.ID)
	assert.Equal(t, 0, m.torrents.list.cursor(), "the table's cursor follows the selection")
}

// TestPollPrunesVanishedMarks checks a mark on a torrent that is gone is
// dropped, and a mark on one that is still there survives.
func TestPollPrunesVanishedMarks(t *testing.T) {
	stub, srv := newRPCStub(t)

	m := press(pollOnce(t, liveModel(t, srv, 120, 40)), "space", "down", "space")
	require.Equal(t, 2, m.torrents.sel.markCount())

	stub.respond("torrent-get", torrentsBody(stubTorrent{1, "a", 0}, stubTorrent{3, "c", 1}))

	m = pollOnce(t, m)

	assert.Equal(t, 1, m.torrents.sel.markCount())
	assert.True(t, m.torrents.sel.isMarked(3))
}

// TestStalePollRepliesAreDropped checks a reply from another generation changes
// nothing.
func TestStalePollRepliesAreDropped(t *testing.T) {
	_, srv := newRPCStub(t)

	m := pollOnce(t, liveModel(t, srv, 120, 40))

	got, cmd := m.update(pollMsg{gen: 99, torrents: []transmission.Torrent{{ID: 42}}})
	assert.Nil(t, cmd)
	assert.Equal(t, rowIDs(m.torrents.rows), rowIDs(got.torrents.rows))

	got, _ = got.update(sessionMsg{gen: 99, session: transmission.SessionSettings{AltSpeedEnabled: true}})
	assert.False(t, got.live.session.AltSpeedEnabled)
}

// TestStaleTicksAreDropped checks a tick retired by a later schedule, or from
// another generation, starts nothing.
func TestStaleTicksAreDropped(t *testing.T) {
	_, srv := newRPCStub(t)

	m, _ := liveModel(t, srv, 120, 40).pollNow()

	_, cmd := m.update(tickMsg{seq: 0})
	assert.Nil(t, cmd, "a retired sequence")

	_, cmd = m.update(tickMsg{gen: 1, seq: 1})
	assert.Nil(t, cmd, "another generation")

	_, cmd = m.update(tickMsg{seq: 1})
	assert.NotNil(t, cmd, "the current tick")
}

// TestNothingToPollIsQuiet checks the first tick does nothing for static rows or
// with no server, which is what keeps the rest of the suite off the network.
func TestNothingToPollIsQuiet(t *testing.T) {
	_, cmd := newTestModel(120, 40).update(tickMsg{})
	assert.Nil(t, cmd)

	deps := testDeps()
	deps.Torrents = nil

	_, cmd = New(deps).update(tickMsg{})
	assert.Nil(t, cmd)
}

// TestPollsNeverOverlap checks a tick arriving while a poll is running starts
// nothing.
func TestPollsNeverOverlap(t *testing.T) {
	_, srv := newRPCStub(t)

	m, cmd := liveModel(t, srv, 120, 40).pollNow()
	m, _ = runCmd(t, m, cmd)
	require.True(t, m.live.inFlight)

	m, cmd = m.pollNow()
	_, again := runCmd(t, m, cmd)

	assert.Nil(t, again)
}

// TestPollErrorBeforeData checks a failing server says what is wrong in place
// of rows, and how to retry.
func TestPollErrorBeforeData(t *testing.T) {
	stub, srv := newRPCStub(t)
	stub.status.Store(http.StatusInternalServerError)

	m := pollOnce(t, liveModel(t, srv, 120, 40))

	require.ErrorIs(t, m.live.err, transmission.ErrUnreachable)
	assert.False(t, m.live.loaded)

	frame := ansi.Strip(m.render())
	assert.Contains(t, frame, "server unreachable")
	assert.Contains(t, frame, "press R to retry")

	assertGolden(t, "torrents_error", m.render())
}

// TestPollErrorAfterDataKeepsRows checks a failure after a success keeps the
// rows on screen, and that the next success clears the error.
func TestPollErrorAfterDataKeepsRows(t *testing.T) {
	stub, srv := newRPCStub(t)

	m := pollOnce(t, liveModel(t, srv, 120, 40))

	stub.status.Store(http.StatusInternalServerError)

	m = pollOnce(t, m)

	require.ErrorIs(t, m.live.err, transmission.ErrUnreachable)
	assert.Len(t, m.torrents.rows, 3)

	stub.status.Store(0)

	assert.NoError(t, pollOnce(t, m).live.err)
}

// TestRefreshInTorrentsPolls checks R polls at once with a slow refresh, and
// says why when it cannot.
func TestRefreshInTorrentsPolls(t *testing.T) {
	stub, srv := newRPCStub(t)

	m := pollOnce(t, liveModel(t, srv, 120, 40))

	m, cmd := pressCmd(m, "R")
	assert.Empty(t, m.toast)

	m, cmd = runCmd(t, m, cmd)
	require.True(t, m.live.inFlight)

	blocked, again := pressCmd(m, "R")
	assert.Nil(t, again)
	assert.Equal(t, "refresh already in flight", blocked.toast)

	settle(t, m, cmd)

	assert.Equal(t, 2, stub.calls("torrent-get"))
	assert.Equal(t, probeSessionGets+2, stub.calls("session-get"), "R carries a slow refresh")

	assert.Equal(t, "no server", press(newTestModel(120, 40), "R").toast)
}

// TestSlowRefreshRidesEverySixthPoll checks the session is fetched on the first
// poll and then once every slowRefreshEvery polls.
func TestSlowRefreshRidesEverySixthPoll(t *testing.T) {
	stub, srv := newRPCStub(t)

	m := liveModel(t, srv, 120, 40)

	for range slowRefreshEvery {
		m = pollOnce(t, m)
	}

	assert.Equal(t, probeSessionGets+1, stub.calls("session-get"))

	pollOnce(t, m)

	assert.Equal(t, probeSessionGets+2, stub.calls("session-get"))
	assert.Equal(t, slowRefreshEvery+1, stub.calls("torrent-get"))
}

// TestEmptyDownloadDirSkipsFreeSpace checks a session with no download directory
// does not ask for free space.
func TestEmptyDownloadDirSkipsFreeSpace(t *testing.T) {
	stub, srv := newRPCStub(t)
	stub.respond("session-get", strings.Replace(bespokeSessionResponse, "\"/data\\u001b[2Jdownloads\"", "\"\"", 1))

	m := pollOnce(t, liveModel(t, srv, 120, 40))

	assert.True(t, m.live.hasSession)
	assert.False(t, m.live.hasFree)
	assert.Zero(t, stub.calls("free-space"))
}

// TestTorrentsPlaceholderGoldens pins what the table says before its first poll
// and when the server has no torrents.
func TestTorrentsPlaceholderGoldens(t *testing.T) {
	stub, srv := newRPCStub(t)

	loading := liveModel(t, srv, 120, 40)
	assert.Contains(t, ansi.Strip(loading.render()), "loading…")
	assertGolden(t, "torrents_loading", loading.render())

	stub.respond("torrent-get", torrentsBody())

	empty := pollOnce(t, liveModel(t, srv, 120, 40))
	assert.Contains(t, ansi.Strip(empty.render()), "no torrents")
	assertGolden(t, "torrents_empty", empty.render())
}
