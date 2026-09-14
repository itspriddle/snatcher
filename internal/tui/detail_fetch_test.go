package tui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// detailModel is a polled live model whose stub answers a detail read with
// detailResponse, the detail of torrent 2 — the cursor row.
func detailModel(t *testing.T) (*rpcStub, Model) {
	t.Helper()

	stub, m := polledModel(t)
	stub.respondWhen("torrent-get", isDetailRequest, detailResponse)

	return stub, m
}

// updateKeys sends keys through update, the path a running program takes, so
// ensureDetail runs after each; the commands are discarded.
func updateKeys(m Model, keys ...string) Model {
	for _, k := range keys {
		m, _ = m.update(keyMsg(k))
	}

	return m
}

// fireDetailTick fires the current debounce timer and runs the fetch it starts.
func fireDetailTick(t *testing.T, m Model) Model {
	t.Helper()

	m, cmd := m.update(detailTickMsg{gen: m.live.gen, seq: m.live.detailSeq})

	return run(t, m, cmd)
}

// failedDetail is a live model whose detail fetch for the cursor torrent failed.
func failedDetail(m Model) Model {
	m = press(m, "enter")
	id, _ := m.wantedDetailID()
	m, _ = m.update(detailMsg{gen: m.live.gen, id: id, err: transmission.ErrUnreachable})

	return m
}

// TestOpeningTheDetailFetchesAfterADebounce checks enter fetches nothing at once,
// and one detail read for the cursor torrent once the debounce fires.
func TestOpeningTheDetailFetchesAfterADebounce(t *testing.T) {
	stub, m := detailModel(t)

	m, cmd := m.update(keyMsg("enter"))

	require.NotNil(t, cmd, "the debounce timer")
	assert.Equal(t, int64(2), m.live.detailWant)
	assert.Zero(t, stub.detailCalls(t), "nothing is fetched before the debounce")
	assert.Contains(t, m.torrents.detail.body(Styles{}), overviewLine("Comment", "loading…"))

	m = fireDetailTick(t, m)

	require.Equal(t, 1, stub.detailCalls(t))

	requests := stub.args(t, "torrent-get")
	assert.Equal(t, []any{float64(2)}, ids(requests[len(requests)-1]))

	assert.True(t, m.torrents.detail.hasDetail)
	assert.Equal(t, int64(2), m.torrents.detail.detail.ID)
	assert.Zero(t, m.live.detailInFlight)
}

// TestCursorMoveRefetchesTheNewTorrent checks a run of cursor moves costs one
// fetch, for the torrent the run ended on: every superseded timer is dropped.
func TestCursorMoveRefetchesTheNewTorrent(t *testing.T) {
	stub, m := detailModel(t)

	m = updateKeys(m, "1", "j", "j", "k")
	require.Equal(t, int64(3), m.live.detailWant)

	for seq := 1; seq < m.live.detailSeq; seq++ {
		_, cmd := m.update(detailTickMsg{gen: m.live.gen, seq: seq})
		assert.Nilf(t, cmd, "superseded timer %d started a fetch", seq)
	}

	fireDetailTick(t, m)

	require.Equal(t, 1, stub.detailCalls(t))

	requests := stub.args(t, "torrent-get")
	assert.Equal(t, []any{float64(3)}, ids(requests[len(requests)-1]))
}

// TestPollCarriesTheDetailWhileOpen checks the poll brings a detail read along
// while the pane is open, and only then.
func TestPollCarriesTheDetailWhileOpen(t *testing.T) {
	stub, closed := detailModel(t)

	m, cmd := closed.startPoll()
	assert.Zero(t, m.live.detailInFlight)
	settle(t, m, cmd)
	assert.Zero(t, stub.detailCalls(t), "a closed pane fetches nothing")

	m, cmd = press(closed, "enter").startPoll()
	assert.Equal(t, int64(2), m.live.detailInFlight)

	m = settle(t, m, cmd)

	assert.Equal(t, 1, stub.detailCalls(t))
	assert.True(t, m.torrents.detail.hasDetail)

	_, again := m.ensureDetail(nil)
	assert.Nil(t, again, "the poll's fetch satisfied the want, so no debounce follows")
}

// TestStaleDetailIsDropped checks a reply from another generation, or for a
// torrent the cursor has left, installs nothing — and that the latter debounces
// the torrent the cursor is on now.
func TestStaleDetailIsDropped(t *testing.T) {
	_, m := detailModel(t)
	m = press(m, "enter")

	got, cmd := m.update(detailMsg{gen: 99, id: 2, detail: transmission.TorrentDetail{Torrent: transmission.Torrent{ID: 2}}})
	assert.Nil(t, cmd)
	assert.False(t, got.torrents.detail.hasDetail)

	got, cmd = m.update(detailMsg{gen: m.live.gen, id: 3, detail: transmission.TorrentDetail{Torrent: transmission.Torrent{ID: 3}}})
	assert.False(t, got.torrents.detail.hasDetail)
	assert.NotNil(t, cmd, "the torrent under the cursor is debounced instead")
	assert.Equal(t, m.live.detailSeq+1, got.live.detailSeq)
}

// TestDetailErrorShowsRetryHint checks a failed fetch says what went wrong and
// how to retry, on the overview and on a detail-only tab.
func TestDetailErrorShowsRetryHint(t *testing.T) {
	_, m := detailModel(t)
	m = failedDetail(m)

	for _, tab := range []string{"1", "3"} {
		frame := ansi.Strip(press(m, tab).render())

		assert.Containsf(t, frame, "server unreachable", "tab %s", tab)
		assert.Containsf(t, frame, "press R to retry", "tab %s", tab)
	}

	fixed := fireDetailTick(t, updateKeys(m, "}", "}"))
	assert.NoError(t, fixed.torrents.detail.err, "the next success clears the error")
}

// TestSameTorrentKeepsScroll checks a poll of the same torrent keeps the detail
// and the scroll position, and a different torrent drops both.
func TestSameTorrentKeepsScroll(t *testing.T) {
	m := withMockDetail(press(newTestModel(120, 40), "enter"))
	m = press(m, "j", "j")

	require.Equal(t, 2, m.torrents.detail.vp.YOffset())

	m.torrents = m.torrents.setAll(mockTorrents(), m.icons)

	assert.Equal(t, 2, m.torrents.detail.vp.YOffset(), "a poll of the same torrent keeps the scroll")
	assert.True(t, m.torrents.detail.hasDetail, "a poll of the same torrent keeps the detail")

	m = press(m.setFocus(paneList), "j")

	assert.Zero(t, m.torrents.detail.vp.YOffset(), "another torrent starts at the top")
	assert.False(t, m.torrents.detail.hasDetail, "another torrent's detail is not this one's")
}

// TestServerSwitchClearsTheDetail checks a successful switch leaves nothing of
// the old server's detail behind.
func TestServerSwitchClearsTheDetail(t *testing.T) {
	stub, srv := newRPCStub(t)
	stub.respondWhen("torrent-get", isDetailRequest, detailResponse)

	m := fireDetailTick(t, updateKeys(pollOnce(t, liveModel(t, srv, 120, 40)), "enter"))
	require.True(t, m.torrents.detail.hasDetail)

	m.live.gen++
	m.live.switching = true
	m.live.detailInFlight = 2

	m, _ = m.update(dialedMsg{gen: m.live.gen, name: "other", result: DialResult{Client: transmission.New(srv.URL), Name: "other"}})

	assert.False(t, m.torrents.detail.hasDetail)
	assert.Zero(t, m.live.detailWant)
	assert.Zero(t, m.live.detailInFlight)
}

// TestDetailLoadingGolden pins a detail-only tab while its first fetch is on its
// way.
func TestDetailLoadingGolden(t *testing.T) {
	_, m := detailModel(t)

	assertGolden(t, "detail_loading", press(m, "enter", "2").render())
}

// TestDetailErrorGolden pins the pane after a failed fetch.
func TestDetailErrorGolden(t *testing.T) {
	_, m := detailModel(t)

	assertGolden(t, "detail_error", failedDetail(m).render())
}
