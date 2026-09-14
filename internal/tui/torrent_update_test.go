package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// menuOtherDown is the presses from the menu's first row down to "other ▸".
var menuOtherDown = []string{"j", "j", "j", "j", "j", "j", "j", "j", "j", "j", "j", "j"}

// openOtherMenu presses m, walks to "other ▸", and opens it.
func openOtherMenu(m Model) Model {
	return press(press(press(m, "m"), menuOtherDown...), "enter")
}

// runAction dispatches an action as its menu leaf or palette entry would.
func runAction(m Model, a Action) (Model, tea.Cmd) {
	return m.dispatch(a, keyMsg("enter"))
}

// lastTorrentSet is the arguments of the last torrent-set, in their bespoke wire
// spelling.
func lastTorrentSet(t *testing.T, stub *rpcStub) map[string]any {
	t.Helper()

	sets := stub.args(t, "torrent-set")
	require.NotEmpty(t, sets, "no torrent-set was sent")

	return sets[len(sets)-1]
}

// submitPrompt answers the prompt on top with value.
func submitPrompt(t *testing.T, m Model, value string) (Model, tea.Cmd) {
	t.Helper()

	return topPrompt(t, m).submit(m, value)
}

// TestMenuOtherSubmenu checks other opens the per-torrent field leaves in
// vision.md's order, with the three choices as submenus.
func TestMenuOtherSubmenu(t *testing.T) {
	_, m := polledModel(t)

	m = openOtherMenu(m)

	o, ok := topAs[menuOverlay](m)
	require.True(t, ok)
	assert.Equal(t, "Actions (1 torrent) / other", o.title)
	assert.Equal(t, []string{
		"honor session limits", "download limit…", "upload limit…", "peer limit…", "seed ratio limit…",
		"seed idle limit…", "sequential download", "bandwidth priority", "bandwidth group…",
	}, menuLabels(o.items))

	assert.Equal(t, []string{"on", "off"}, menuLabels(o.items[0].sub))
	assert.Equal(t, []string{"on", "off"}, menuLabels(o.items[6].sub))
	assert.Equal(t, "(4.1+)", o.items[6].note)
	assert.Equal(t, []string{"high", "normal", "low"}, menuLabels(o.items[7].sub))
	assert.Contains(t, ansi.Strip(m.render()), "sequential download ▸  (4.1+)")
}

// TestUpdateLeavesHitTorrentSet checks each fixed leaf writes its field, in the
// bespoke wire spelling, for the cursor torrent.
func TestUpdateLeavesHitTorrentSet(t *testing.T) {
	cases := []struct {
		action Action
		field  string
		want   any
	}{
		{actionTorrentHonorLimitsOn, "honorsSessionLimits", true},
		{actionTorrentHonorLimitsOff, "honorsSessionLimits", false},
		{actionTorrentSequentialOn, "sequentialDownload", true},
		{actionTorrentSequentialOff, "sequentialDownload", false},
		{actionTorrentPriorityHigh, "bandwidthPriority", float64(1)},
		{actionTorrentPriorityNormal, "bandwidthPriority", float64(0)},
		{actionTorrentPriorityLow, "bandwidthPriority", float64(-1)},
	}

	for _, tc := range cases {
		t.Run(string(tc.action), func(t *testing.T) {
			stub, m := polledModel(t)

			m, cmd := runAction(m, tc.action)
			assert.Empty(t, m.overlays, "one torrent acts at once")

			runCmd(t, m, cmd)

			sent := lastTorrentSet(t, stub)
			assert.Equal(t, tc.want, sent[tc.field])
			assert.Equal(t, []any{float64(2)}, ids(sent))
		})
	}
}

// TestUpdateLeavesConfirmAboveOneTorrent checks a fixed leaf over marks asks
// first, in its own words and with the count.
func TestUpdateLeavesConfirmAboveOneTorrent(t *testing.T) {
	stub, m := polledModel(t)

	m, cmd := runAction(press(m, "space", "down", "space"), actionTorrentHonorLimitsOn)

	assert.Nil(t, cmd)
	require.True(t, m.hasOverlay(confirmOverlayKind))
	assert.Contains(t, ansi.Strip(m.render()), "Honor session limits on 2 torrents?")
	assert.Zero(t, stub.calls("torrent-set"))

	m, cmd = pressCmd(m, "y")
	runCmd(t, m, cmd)

	assert.Len(t, ids(lastTorrentSet(t, stub)), 2)
}

// TestDownloadLimitPrompt checks the download limit prompt: off disables the
// limit without touching its value, a number sets both, and a bad value is
// refused in place with nothing sent.
func TestDownloadLimitPrompt(t *testing.T) {
	stub, m := polledModel(t)

	m, _ = runAction(m, actionTorrentLimitDownload)

	o := topPrompt(t, m)
	assert.Equal(t, "Set download limit on 1 torrent", o.title)
	assert.Equal(t, "off, or KB/s", o.hint)

	rejected, cmd := submitPrompt(t, m, "fast")
	assert.Nil(t, cmd)
	assert.Equal(t, "must be a non-negative integer (kB/s) or 'off'", topPrompt(t, rejected).err)
	assert.Zero(t, stub.calls("torrent-set"))

	done, cmd := submitPrompt(t, m, "off")
	assert.Empty(t, done.overlays)
	runCmd(t, done, cmd)

	sent := lastTorrentSet(t, stub)
	assert.Equal(t, false, sent["downloadLimited"])
	assert.NotContains(t, sent, "downloadLimit")

	done, cmd = submitPrompt(t, m, "500")
	runCmd(t, done, cmd)

	sent = lastTorrentSet(t, stub)
	assert.Equal(t, float64(500), sent["downloadLimit"])
	assert.Equal(t, true, sent["downloadLimited"])
}

// TestUploadLimitPrompt checks the upload prompt writes the upload pair.
func TestUploadLimitPrompt(t *testing.T) {
	stub, m := polledModel(t)

	m, _ = runAction(m, actionTorrentLimitUpload)
	assert.Equal(t, "Set upload limit on 1 torrent", topPrompt(t, m).title)

	m, cmd := submitPrompt(t, m, "250")
	runCmd(t, m, cmd)

	sent := lastTorrentSet(t, stub)
	assert.Equal(t, float64(250), sent["uploadLimit"])
	assert.Equal(t, true, sent["uploadLimited"])
}

// TestPeerLimitPromptRejectsZero checks the peer limit floor, as
// `torrent update --peer-limit` enforces it.
func TestPeerLimitPromptRejectsZero(t *testing.T) {
	stub, m := polledModel(t)

	m, _ = runAction(m, actionTorrentLimitPeers)

	rejected, cmd := submitPrompt(t, m, "0")
	assert.Nil(t, cmd)
	assert.Equal(t, "must be 1 or greater", topPrompt(t, rejected).err)

	m, cmd = submitPrompt(t, m, " 40 ")
	runCmd(t, m, cmd)

	assert.Equal(t, float64(40), lastTorrentSet(t, stub)["peer-limit"], "peer-limit keeps its kebab spelling on the bespoke path")
}

// TestSeedRatioPrompt checks the ratio's word and number forms.
func TestSeedRatioPrompt(t *testing.T) {
	stub, m := polledModel(t)

	m, _ = runAction(m, actionTorrentLimitSeedRatio)

	done, cmd := submitPrompt(t, m, "unlimited")
	runCmd(t, done, cmd)

	sent := lastTorrentSet(t, stub)
	assert.Equal(t, float64(2), sent["seedRatioMode"])
	assert.NotContains(t, sent, "seedRatioLimit")

	done, cmd = submitPrompt(t, m, "2.5")
	runCmd(t, done, cmd)

	sent = lastTorrentSet(t, stub)
	assert.Equal(t, 2.5, sent["seedRatioLimit"])
	assert.Equal(t, float64(1), sent["seedRatioMode"])
}

// TestSeedIdlePromptAcceptsDurations checks a duration becomes whole minutes and
// a sub-minute one is refused.
func TestSeedIdlePromptAcceptsDurations(t *testing.T) {
	stub, m := polledModel(t)

	m, _ = runAction(m, actionTorrentLimitSeedIdle)

	rejected, cmd := submitPrompt(t, m, "90s")
	assert.Nil(t, cmd)
	assert.NotEmpty(t, topPrompt(t, rejected).err)

	m, cmd = submitPrompt(t, m, "1h30m")
	runCmd(t, m, cmd)

	sent := lastTorrentSet(t, stub)
	assert.Equal(t, float64(90), sent["seedIdleLimit"])
	assert.Equal(t, float64(1), sent["seedIdleMode"])
}

// TestGroupPromptEmptyClears checks a group is set by name and an empty one
// clears it.
func TestGroupPromptEmptyClears(t *testing.T) {
	stub, m := polledModel(t)

	m, _ = runAction(m, actionTorrentGroup)

	done, cmd := submitPrompt(t, m, " lan ")
	runCmd(t, done, cmd)
	assert.Equal(t, "lan", lastTorrentSet(t, stub)["group"])

	done, cmd = submitPrompt(t, m, "")
	runCmd(t, done, cmd)
	assert.Empty(t, lastTorrentSet(t, stub)["group"])
}

// TestUpdateReportsTheCLIsVerb checks a field change is reported in `torrent
// update`'s words.
func TestUpdateReportsTheCLIsVerb(t *testing.T) {
	_, m := polledModel(t)

	m, cmd := runAction(m, actionTorrentPriorityHigh)
	assert.Equal(t, "Updating 1 torrent…", m.toast)

	m, _ = runCmd(t, m, cmd)
	assert.Equal(t, "Updated 1 torrent", m.toast)
}

// TestUpdateFailureNamesWhatWasTried checks a failed field change names the
// field, not only the shared verb.
func TestUpdateFailureNamesWhatWasTried(t *testing.T) {
	stub, m := polledModel(t)
	stub.failMethod("torrent-set")

	m, _ = runAction(m, actionTorrentLimitDownload)
	m, cmd := submitPrompt(t, m, "500")
	m, _ = runCmd(t, m, cmd)

	assert.True(t, strings.HasPrefix(m.toast, "set download limit on: "), m.toast)
}

// TestEveryUpdateLeafIsUnboundAndInTheMenu checks the leaf table, the unbound
// table, and the other submenu name the same thirteen actions.
func TestEveryUpdateLeafIsUnboundAndInTheMenu(t *testing.T) {
	var inMenu []Action

	var walk func(items []menuItem)

	walk = func(items []menuItem) {
		for _, it := range items {
			if len(it.sub) > 0 {
				walk(it.sub)

				continue
			}

			inMenu = append(inMenu, it.action)
		}
	}

	walk(otherMenu())

	require.Len(t, updateLeaves, 13)
	assert.Len(t, inMenu, len(updateLeaves))

	for _, leaf := range updateLeaves {
		assert.Truef(t, isUnbound(leaf.action), "%s is not in unboundActions", leaf.action)
		assert.Truef(t, slices.Contains(inMenu, leaf.action), "%s is not in the other submenu", leaf.action)
		assert.NotEmptyf(t, leaf.ask, "%s has no ask", leaf.action)
		assert.Equalf(t, leaf.parse == nil, leaf.changes != nil, "%s must be exactly one of fixed or prompted", leaf.action)
	}
}

// TestMenuOtherGolden pins the other submenu over the menu.
func TestMenuOtherGolden(t *testing.T) {
	_, m := polledModel(t)

	assertGolden(t, "menu_other", openOtherMenu(m).render())
}

// TestPromptLimitGolden pins a limit prompt: its counted title and its hint.
func TestPromptLimitGolden(t *testing.T) {
	_, m := polledModel(t)

	assertGolden(t, "prompt_limit", press(openOtherMenu(m), "j", "enter").render())
}
