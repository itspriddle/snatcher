package tui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// menuTrackersDown is the presses from the menu's first row down to
// "trackers ▸".
var menuTrackersDown = []string{"j", "j", "j", "j", "j", "j", "j", "j", "j", "j", "j"}

// openTrackersMenu presses m, walks to "trackers ▸", and opens it.
func openTrackersMenu(m Model) Model {
	return press(press(press(m, "m"), menuTrackersDown...), "enter")
}

// trackerModel is a polled live model whose stub answers a detail read with
// torrent 2's, which carries one tracker.
func trackerModel(t *testing.T) (*rpcStub, Model) {
	t.Helper()

	stub, m := polledModel(t)
	stub.respondWhen("torrent-get", isDetailRequest, detailResponse)

	return stub, m
}

// trackerListSent is the tracker list the last torrent-set wrote, in its bespoke
// wire spelling.
func trackerListSent(t *testing.T, stub *rpcStub) map[string]any {
	t.Helper()

	sets := stub.args(t, "torrent-set")
	require.NotEmpty(t, sets, "no torrent-set was sent")

	last := sets[len(sets)-1]
	require.Contains(t, last, "trackerList", "tracker-list must reach the wire as the bespoke trackerList")

	return last
}

// TestTrackerListGroupsByTier checks trackers render as Transmission's tracker
// list: tiers in order, a newline within one, a blank line between them.
func TestTrackerListGroupsByTier(t *testing.T) {
	trackers := []transmission.Tracker{
		{Announce: "c1", Tier: 1},
		{Announce: "a0", Tier: 0},
		{Announce: "b0", Tier: 0},
		{Announce: "d2", Tier: 2},
	}

	assert.Equal(t, "a0\nb0\n\nc1\n\nd2", trackerList(trackers))
	assert.Empty(t, trackerList(nil))
}

// TestParseAnnounce checks the URLs a tracker edit accepts.
func TestParseAnnounce(t *testing.T) {
	for _, ok := range []string{"https://t.example/announce", "http://t.example:80/a", "udp://t.example:6969", "wss://t.example/ws", " udp://t.example:1 "} {
		_, err := parseAnnounce(ok)
		require.NoErrorf(t, err, "%q", ok)
	}

	for _, bad := range []string{"", "ftp://t.example/", "http://", "not a url", "t.example/announce", "http://a/b c", "http://a/\nb"} {
		_, err := parseAnnounce(bad)
		require.Errorf(t, err, "%q", bad)
	}
}

// TestMenuTrackersSubmenu checks the trackers row opens its three leaves, each
// with a handler.
func TestMenuTrackersSubmenu(t *testing.T) {
	_, m := polledModel(t)

	m = openTrackersMenu(m)

	o, ok := topAs[menuOverlay](m)
	require.True(t, ok)
	assert.Equal(t, "Actions (1 torrent) / trackers", o.title)
	assert.Equal(t, []string{"add", "replace", "clear"}, menuLabels(o.items))

	for _, it := range o.items {
		_, ok := handlers[it.action]
		assert.Truef(t, ok, "%q has no handler", it.label)
	}
}

// TestAddTrackerReadsThenReplaces checks add reads the torrent's trackers, then
// writes them back with the new URL as a tier of its own.
func TestAddTrackerReadsThenReplaces(t *testing.T) {
	stub, m := trackerModel(t)

	m = press(openTrackersMenu(m), "enter")

	o := topPrompt(t, m)
	assert.Equal(t, "Add tracker to debian.iso", o.title)

	m, cmd := o.submit(m, "ftp://nope")
	assert.Nil(t, cmd)
	assert.Contains(t, topPrompt(t, m).err, "announce URLs are")

	m, cmd = o.submit(m, "udp://backup.debian.org:6969/announce")
	require.NotNil(t, cmd)
	assert.Empty(t, m.overlays)
	assert.Zero(t, stub.detailCalls(t), "nothing is read until the command runs")

	m, _ = runCmd(t, m, cmd)

	assert.Equal(t, 1, stub.detailCalls(t))

	sent := trackerListSent(t, stub)
	assert.Equal(t, "https://tracker.debian.org/announce\n\nudp://backup.debian.org:6969/announce", sent["trackerList"])
	assert.Equal(t, []any{float64(2)}, ids(sent))
	assert.Equal(t, "Updated 1 torrent", m.toast)
}

// TestReplaceTrackersPrefillsAndJoinsWithNewlines checks replace reads the list
// it is prefilled with, refuses an empty prompt, and writes the URLs as one tier.
func TestReplaceTrackersPrefillsAndJoinsWithNewlines(t *testing.T) {
	stub, m := trackerModel(t)

	m, cmd := pressCmd(press(openTrackersMenu(m), "j"), "enter")
	require.NotNil(t, cmd, "the prompt waits on the detail read")
	assert.Equal(t, "trackers: fetching…", m.toast)

	m, _ = runCmd(t, m, cmd)

	o := topPrompt(t, m)
	assert.Equal(t, "Replace trackers on debian.iso", o.title)
	assert.Equal(t, "https://tracker.debian.org/announce", o.input.Value())

	m, cmd = o.submit(m, "   ")
	assert.Nil(t, cmd)
	assert.Contains(t, topPrompt(t, m).err, "clear removes them all")

	m, cmd = o.submit(m, "http://a.example/announce udp://b.example:80")
	require.NotNil(t, cmd)

	runCmd(t, m, cmd)

	assert.Equal(t, "http://a.example/announce\nudp://b.example:80", trackerListSent(t, stub)["trackerList"])
}

// TestReplaceTrackersUsesTheLoadedDetail checks replace skips the read when the
// detail pane already describes the torrent.
func TestReplaceTrackersUsesTheLoadedDetail(t *testing.T) {
	stub, m := trackerModel(t)

	m = fireDetailTick(t, updateKeys(m, "1"))
	calls := stub.detailCalls(t)

	m, cmd := pressCmd(press(openTrackersMenu(m), "j"), "enter")

	assert.Equal(t, calls, stub.detailCalls(t))
	assert.Equal(t, "https://tracker.debian.org/announce", topPrompt(t, m).input.Value())
	assert.NotNil(t, cmd, "the prompt's cursor blink")
}

// TestClearTrackersConfirmsWithTheCount checks clear asks, naming the count, and
// sends an empty list for exactly the torrents it counted.
func TestClearTrackersConfirmsWithTheCount(t *testing.T) {
	stub, m := trackerModel(t)

	m = press(m, "space", "down", "space")
	m, cmd := pressCmd(press(openTrackersMenu(m), "j", "j"), "enter")

	assert.Nil(t, cmd)
	require.True(t, m.hasOverlay(confirmOverlayKind))
	assert.Contains(t, ansi.Strip(m.render()), "Clear trackers on 2 torrents?")

	m, cmd = pressCmd(m, "y")
	runCmd(t, m, cmd)

	sent := trackerListSent(t, stub)
	assert.Empty(t, sent["trackerList"])
	assert.Equal(t, []any{float64(2), float64(3)}, ids(sent))
}

// TestTrackerEditsNeedOneTorrent checks add and replace refuse a wider
// selection, and send nothing.
func TestTrackerEditsNeedOneTorrent(t *testing.T) {
	for _, down := range [][]string{nil, {"j"}} {
		stub, m := trackerModel(t)

		m = press(m, "space", "down", "space")
		m, cmd := pressCmd(press(openTrackersMenu(m), down...), "enter")

		assert.Nil(t, cmd)
		assert.Equal(t, "trackers: one torrent at a time", m.toast)
		assert.Empty(t, m.overlays)
		assert.Zero(t, stub.detailCalls(t))
	}
}

// TestMenuTrackersGolden pins the trackers submenu over the menu.
func TestMenuTrackersGolden(t *testing.T) {
	_, m := polledModel(t)

	assertGolden(t, "menu_trackers", openTrackersMenu(m).render())
}

// TestPromptAddTrackerGolden pins the add prompt.
func TestPromptAddTrackerGolden(t *testing.T) {
	_, m := polledModel(t)

	assertGolden(t, "prompt_add_tracker", press(openTrackersMenu(m), "enter").render())
}
