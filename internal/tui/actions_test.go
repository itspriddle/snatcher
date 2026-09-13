package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryBindingHasAHandler checks the registry and the handler table are two
// views of one set. A binding with no handler is a key that appears in the help
// and does nothing.
func TestEveryBindingHasAHandler(t *testing.T) {
	for _, b := range registry {
		_, ok := handlers[b.Action]

		assert.Truef(t, ok, "%s is bound but has no handler", b.Action)
	}
}

// TestEveryHandlerHasABinding checks the other direction. A handler with no
// binding is either dead code or an action that was meant to be reachable and is
// not; T7's palette will list actions by name, and a name nothing can reach is a
// bug either way.
func TestEveryHandlerHasABinding(t *testing.T) {
	bound := map[Action]bool{}

	for _, b := range registry {
		bound[b.Action] = true
	}

	for action := range handlers {
		assert.Truef(t, bound[action], "%s has a handler but no binding", action)
	}
}

// TestNotYetHandlersReportThemselves checks the placeholder handlers say so in
// the status bar. A registered key that silently did nothing would be
// indistinguishable from a broken one.
func TestNotYetHandlersReportThemselves(t *testing.T) {
	m := press(newTestModel(120, 40), ":")

	assert.Contains(t, m.toast, actionPalette)
	assert.Contains(t, m.toast, "not implemented yet")
}

// TestTheNextKeyClearsTheToast checks a toast is acknowledged by the next key
// press rather than by a timer, which is what keeps T1 free of a clock.
func TestTheNextKeyClearsTheToast(t *testing.T) {
	m := press(newTestModel(120, 40), "s")
	require.NotEmpty(t, m.toast)

	assert.Empty(t, press(m, "j").toast)
}

// TestPaneTogglesOpenAndClose checks { and } against the layout, not just the
// flags: a toggle that did not reflow would leave the panes drawn at the old
// geometry.
func TestPaneTogglesOpenAndClose(t *testing.T) {
	m := newTestModel(120, 40)
	require.True(t, m.torrents.filterOpen)
	require.False(t, m.torrents.detailOpen)

	m = press(m, "{")
	assert.False(t, m.torrents.filterOpen)
	assert.Equal(t, rect{}, m.layout().filter)

	m = press(m, "}")
	assert.True(t, m.torrents.detailOpen)
	assert.NotEqual(t, rect{}, m.layout().detail)
}

// TestEnterOpensAndFocusesTheDetailPane pins the list pane's enter: it opens the
// pane and moves the keyboard into it.
func TestEnterOpensAndFocusesTheDetailPane(t *testing.T) {
	m := press(newTestModel(120, 40), "enter")

	assert.True(t, m.torrents.detailOpen)
	assert.Equal(t, paneDetail, m.focusedPane())
	assert.Equal(t, tabOverview, m.torrents.detail.tab)
}

// TestNumberKeysOpenTheDetailPaneWithoutStealingFocus checks 1–5. They are bound
// in the torrents window rather than the list pane, so they work from the list
// without taking the keyboard away from it.
func TestNumberKeysOpenTheDetailPaneWithoutStealingFocus(t *testing.T) {
	for key, want := range map[string]int{"1": tabOverview, "2": tabFiles, "3": tabTrackers, "4": tabPeers, "5": tabPieces} {
		m := press(newTestModel(120, 40), key)

		assert.Truef(t, m.torrents.detailOpen, "%s did not open the pane", key)
		assert.Equalf(t, want, m.torrents.detail.tab, "%s opened the wrong tab", key)
		assert.Equalf(t, paneList, m.focusedPane(), "%s stole focus", key)
	}
}

// TestDetailTabsCycleInBothDirections checks [ and ] wrap, and that h/l and the
// arrows are the same actions rather than a second implementation.
func TestDetailTabsCycleInBothDirections(t *testing.T) {
	m := press(newTestModel(120, 40), "enter")

	for _, key := range []string{"]", "l", "right"} {
		assert.Equal(t, tabFiles, press(m, key).torrents.detail.tab)
	}

	for _, key := range []string{"[", "h", "left"} {
		assert.Equal(t, tabPieces, press(m, key).torrents.detail.tab, "the first tab must wrap backwards")
	}

	assert.Equal(t, tabOverview, press(m, "[", "]").torrents.detail.tab)
}

// TestDetailPaneCarriesNoTorrentVerbs pins vision.md's rule that action verbs
// live in a window's main pane: s must not start a torrent from the detail pane,
// where the files tab has a cursor and marks of its own.
func TestDetailPaneCarriesNoTorrentVerbs(t *testing.T) {
	m := press(newTestModel(120, 40), "enter")
	require.Equal(t, paneDetail, m.focusedPane())

	for _, key := range []string{"s", "p", "v", "d", "y", "space"} {
		_, ok := lookup(m.contextStack(), keyMsg(key))

		assert.Falsef(t, ok, "%q is bound in the detail pane", key)
	}
}

// TestSettingsTabsCycle checks the settings window's implemented keys.
func TestSettingsTabsCycle(t *testing.T) {
	m := press(newTestModel(120, 40), ",")

	assert.Equal(t, 1, press(m, "]").settings.tab)
	assert.Equal(t, len(m.settings.tabs)-1, press(m, "[").settings.tab)
}

// TestUnboundKeysAreIgnored checks a key nothing binds leaves the model alone
// rather than falling into a default branch.
func TestUnboundKeysAreIgnored(t *testing.T) {
	m := newTestModel(120, 40)

	got, cmd := pressCmd(m, "z")

	assert.Nil(t, cmd)
	assert.Equal(t, m.torrents.sel, got.torrents.sel)
	assert.Empty(t, got.toast)
}

// TestOnlyQuitIsHonoredBelowTheSizeFloor checks the degenerate terminal: there is
// nothing drawn to act on, so every key but ctrl+c is ignored.
func TestOnlyQuitIsHonoredBelowTheSizeFloor(t *testing.T) {
	m := newTestModel(40, 10)
	require.True(t, m.layout().tooSmall)

	got, cmd := pressCmd(m, "}")
	assert.Nil(t, cmd)
	assert.False(t, got.torrents.detailOpen)

	_, cmd = pressCmd(m, "ctrl+c")
	assert.NotNil(t, cmd)
}

// TestBackgroundColorMsgRethemes checks the light/dark reply is applied when it
// arrives, which is the whole reason Init does not block on stdin.
func TestBackgroundColorMsgRethemes(t *testing.T) {
	m := newTestModel(120, 40)
	require.True(t, m.isDark)

	got, _ := m.update(lightBackgroundMsg())

	assert.False(t, got.isDark)
}

// TestColorProfileMsgRethemes checks the terminal's color profile is applied
// when it arrives, and that it is what decides whether the frame carries color
// at all.
//
// Both directions matter. The Ascii message is what NO_COLOR, TERM=dumb, and a
// forced profile all reduce to, and the TrueColor message after it proves the
// monochrome rendering is a function of the profile rather than a latch.
func TestColorProfileMsgRethemes(t *testing.T) {
	m := newTestModel(120, 40)
	require.Regexp(t, colorPattern, m.render(), "the color frame must carry color to begin with")

	m, _ = m.update(tea.ColorProfileMsg{Profile: colorprofile.Ascii})

	frame := m.render()

	assert.NotRegexp(t, colorPattern, frame, "a monochrome profile still painted")
	assert.Regexp(t, reverseSGR, frame, "nothing marks the cursor row without color")

	m, _ = m.update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})

	assert.Regexp(t, colorPattern, m.render(), "color did not come back")
}

// TestWindowSizeMsgReflows checks the resize path runs through Update, not only
// through the test helper.
func TestWindowSizeMsgReflows(t *testing.T) {
	got, _ := New(testDeps()).update(sizeMsg(100, 30))

	assert.Equal(t, 100, got.width)
	assert.Equal(t, 30, got.height)
}
