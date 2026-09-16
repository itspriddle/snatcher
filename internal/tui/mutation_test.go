package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// polledModel is a live model after one poll: rows 2, 3, 1 in queue order, with
// the cursor on 2.
func polledModel(t *testing.T) (*rpcStub, Model) {
	t.Helper()

	stub, srv := newRPCStub(t)

	return stub, pollOnce(t, liveModel(t, srv, 120, 40))
}

// emptyModel is a live model after one poll of a server with no torrents, so
// nothing is under the cursor.
func emptyModel(t *testing.T) (*rpcStub, Model) {
	t.Helper()

	stub, srv := newRPCStub(t)
	stub.respond("torrent-get", torrentsBody())

	return stub, pollOnce(t, liveModel(t, srv, 120, 40))
}

// ids is the ids argument of a request, as JSON decoded it.
func ids(args map[string]any) []any {
	v, _ := args["ids"].([]any)

	return v
}

// TestStartOneTorrentActsAtOnce checks one torrent needs no confirm: s sends
// torrent-start for the cursor row, reports it in the CLI's words, and refetches.
func TestStartOneTorrentActsAtOnce(t *testing.T) {
	stub, m := polledModel(t)

	m, cmd := pressCmd(m, "s")

	assert.Empty(t, m.overlays)
	assert.Equal(t, "Starting 1 torrent…", m.toast)
	assert.Zero(t, stub.calls("torrent-start"), "nothing is sent until the command runs")

	m, cmd = runCmd(t, m, cmd)

	require.Len(t, stub.args(t, "torrent-start"), 1)
	assert.Equal(t, []any{float64(2)}, ids(stub.args(t, "torrent-start")[0]))
	assert.Equal(t, "Started 1 torrent", m.toast)

	m, cmd = runCmd(t, m, cmd)
	settle(t, m, cmd)

	assert.Equal(t, 2, stub.calls("torrent-get"), "the reply polls at once")
}

// TestStartManyConfirmsWithTheCount checks marks act together, after a confirm
// that states how many.
func TestStartManyConfirmsWithTheCount(t *testing.T) {
	stub, m := polledModel(t)

	m, cmd := pressCmd(press(m, "space", "down", "space"), "s")

	assert.Nil(t, cmd)
	require.True(t, m.hasOverlay(confirmOverlayKind))
	assert.Contains(t, ansi.Strip(m.render()), "Start 2 torrents?")
	assert.Zero(t, stub.calls("torrent-start"))

	declined := press(m, "n")
	assert.False(t, declined.hasOverlay(confirmOverlayKind))
	assert.Zero(t, stub.calls("torrent-start"))

	m, cmd = pressCmd(m, "y")
	m, _ = runCmd(t, m, cmd)

	assert.Equal(t, []any{float64(2), float64(3)}, ids(stub.args(t, "torrent-start")[0]))
	assert.Equal(t, "Started 2 torrents", m.toast)
}

// TestVerbsHitTheirMethods checks each list-pane verb sends its own RPC.
func TestVerbsHitTheirMethods(t *testing.T) {
	for key, method := range map[string]string{
		"s": "torrent-start",
		"p": "torrent-stop",
		"v": "torrent-verify",
		"r": "torrent-reannounce",
		"J": "queue-move-down",
		"K": "queue-move-up",
	} {
		t.Run(key, func(t *testing.T) {
			stub, m := polledModel(t)

			m, cmd := pressCmd(m, key)
			runCmd(t, m, cmd)

			assert.Equal(t, 1, stub.calls(method))
		})
	}
}

// TestDeleteAsksTwice checks d always confirms, then asks about data as a
// separate, strict question.
func TestDeleteAsksTwice(t *testing.T) {
	prompted := func(t *testing.T) (*rpcStub, Model) {
		t.Helper()

		stub, m := polledModel(t)

		m, cmd := pressCmd(m, "d")
		require.Nil(t, cmd)
		require.Contains(t, ansi.Strip(m.render()), "Remove 1 torrent?")

		m = press(m, "y")
		require.Contains(t, ansi.Strip(m.render()), "Also delete downloaded data?")

		return stub, m
	}

	t.Run("yes deletes data", func(t *testing.T) {
		stub, m := prompted(t)

		m, cmd := pressCmd(m, "y")
		m, _ = runCmd(t, m, cmd)

		args := stub.args(t, "torrent-remove")
		require.Len(t, args, 1)
		assert.Equal(t, true, args[0]["delete-local-data"])
		assert.Equal(t, []any{float64(2)}, ids(args[0]))
		assert.Equal(t, "Removed 1 torrent and their data", m.toast)
	})

	t.Run("no keeps data", func(t *testing.T) {
		stub, m := prompted(t)

		m, cmd := pressCmd(m, "n")
		m, _ = runCmd(t, m, cmd)

		assert.Equal(t, false, stub.args(t, "torrent-remove")[0]["delete-local-data"])
		assert.Equal(t, "Removed 1 torrent", m.toast)
	})

	t.Run("esc cancels", func(t *testing.T) {
		stub, m := prompted(t)

		m, cmd := pressCmd(m, "esc")

		assert.Nil(t, cmd)
		assert.Empty(t, m.overlays)
		assert.Zero(t, stub.calls("torrent-remove"))
	})

	t.Run("enter does not delete data", func(t *testing.T) {
		stub, m := prompted(t)

		m, cmd := pressCmd(m, "enter")

		assert.Nil(t, cmd)
		assert.True(t, m.hasOverlay(confirmOverlayKind), "the question stays open")
		assert.Equal(t, "press y to confirm, or n or esc", m.toast)
		assert.Zero(t, stub.calls("torrent-remove"))
	})
}

// TestMutationErrorToasts checks a refused RPC is reported with its verb, and
// that the rows the last poll drew stay.
func TestMutationErrorToasts(t *testing.T) {
	stub, m := polledModel(t)
	stub.failMethod("torrent-stop")

	m, cmd := pressCmd(m, "p")
	m, next := runCmd(t, m, cmd)

	assert.Nil(t, next)
	assert.Contains(t, m.toast, "stop: server error")
	assert.Len(t, m.torrents.rows, 3)
}

// TestStaleMutationRepliesAreDropped checks a reply from before a server switch
// changes nothing.
func TestStaleMutationRepliesAreDropped(t *testing.T) {
	_, m := polledModel(t)

	got, cmd := m.update(mutationMsg{gen: 99, kind: mutateStart, count: 1})

	assert.Nil(t, cmd)
	assert.Empty(t, got.toast)
}

// TestMutationRefetchIsQueuedBehindAPoll checks a mutation that lands while a
// poll is in flight polls again as soon as that poll answers, rather than a whole
// interval later.
func TestMutationRefetchIsQueuedBehindAPoll(t *testing.T) {
	stub, m := polledModel(t)

	m, tick := m.pollNow()
	m, batch := runCmd(t, m, tick)
	require.True(t, m.live.inFlight)

	m, cmd := pressCmd(m, "s")
	m, next := runCmd(t, m, cmd)

	assert.Nil(t, next, "no poll starts over the one in flight")
	assert.True(t, m.live.pollAgain)

	// A poll with no slow refresh due is a single command, which tea.Batch
	// returns unwrapped.
	reply := batch()
	if replies, ok := reply.(tea.BatchMsg); ok {
		reply = replies[0]()
	}

	_, isPoll := reply.(pollMsg)
	require.True(t, isPoll)

	m, follow := m.update(reply)
	require.False(t, m.live.pollAgain)

	done := make(chan tea.Msg, 1)

	go func() { done <- follow() }()

	select {
	case msg := <-done:
		got, isTick := msg.(tickMsg)
		require.True(t, isTick)
		assert.Equal(t, m.live.tickSeq, got.seq)
	case <-time.After(2 * time.Second):
		t.Fatal("the refetch waited for the poll interval")
	}

	assert.Equal(t, 1, stub.calls("torrent-start"))
}

// TestMutationRefusalsWithoutAServer checks s says why it cannot act, and sends
// nothing.
func TestMutationRefusalsWithoutAServer(t *testing.T) {
	m, cmd := pressCmd(newTestModel(120, 40), "s")
	assert.Nil(t, cmd)
	assert.Equal(t, "no server", m.toast)

	stub, live := polledModel(t)
	live.live.switching = true

	m, cmd = pressCmd(live, "s")
	assert.Nil(t, cmd)
	assert.Equal(t, "switching servers…", m.toast)
	assert.Zero(t, stub.calls("torrent-start"))
}

// TestNothingSelectedToasts checks an empty list acts on nothing, rather than
// falling back to anything wider.
func TestNothingSelectedToasts(t *testing.T) {
	stub, m := emptyModel(t)

	for _, key := range []string{"s", "p", "d", "J"} {
		got, cmd := pressCmd(m, key)

		assert.Nilf(t, cmd, "%s sent something", key)
		assert.Emptyf(t, got.overlays, "%s asked a question", key)
		assert.Equalf(t, "nothing selected", got.toast, "%s", key)
	}

	assert.Zero(t, stub.calls("torrent-start")+stub.calls("torrent-stop")+stub.calls("torrent-remove"))
}

// TestConfirmDeleteDataGolden pins remove's second question: the strict help
// line and the line saying what each answer does.
func TestConfirmDeleteDataGolden(t *testing.T) {
	_, m := polledModel(t)

	assertGolden(t, "confirm_delete_data", press(m, "d", "y").render())
}
