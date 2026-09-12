package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTabCyclesPanesInEveryOpenState walks tab and shift+tab through all four
// combinations of the two pane toggles. Focus cycling is the one behavior every
// other key depends on, and it is exactly the behavior that breaks when a pane
// is opened or closed.
func TestTabCyclesPanesInEveryOpenState(t *testing.T) {
	tests := []struct {
		name       string
		filterOpen bool
		detailOpen bool
		forward    []Pane
	}{
		{"both open", true, true, []Pane{paneDetail, paneFilter, paneList}},
		{"filter only", true, false, []Pane{paneFilter, paneList}},
		{"detail only", false, true, []Pane{paneDetail, paneList}},
		{"neither", false, false, []Pane{paneList}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := paneState(t, tc.filterOpen, tc.detailOpen)

			require.Equal(t, paneList, m.focusedPane())

			for _, want := range tc.forward {
				m = press(m, "tab")
				assert.Equal(t, want, m.focusedPane())
			}

			// shift+tab retraces the same cycle backwards.
			for i := len(tc.forward) - 2; i >= 0; i-- {
				m = press(m, "shift+tab")
				assert.Equal(t, tc.forward[i], m.focusedPane())
			}
		})
	}
}

// paneState builds a model with the two pane toggles in a given state, focused
// on the list pane.
func paneState(t *testing.T, filterOpen, detailOpen bool) Model {
	t.Helper()

	m := newTestModel(120, 40)

	if m.torrents.filterOpen != filterOpen {
		m = press(m, "{")
	}

	if m.torrents.detailOpen != detailOpen {
		m = press(m, "}")
	}

	return m.setFocus(paneList)
}

// TestCtrlWFocusesDirectly pins the ctrl+w table. The rules are positional and
// memoryless on purpose: from the detail pane every vertical move returns to the
// list, so there is no "which pane did I come from" state to get wrong.
func TestCtrlWFocusesDirectly(t *testing.T) {
	tests := []struct {
		name string
		from Pane
		key  string
		want Pane
	}{
		{"list to filter", paneList, "h", paneFilter},
		{"list to detail", paneList, "j", paneDetail},
		{"list right is a no-op", paneList, "l", paneList},
		{"filter to list", paneFilter, "l", paneList},
		{"filter to detail", paneFilter, "j", paneDetail},
		{"filter left is a no-op", paneFilter, "h", paneFilter},
		{"detail to list", paneDetail, "k", paneList},
		{"detail sideways is a no-op", paneDetail, "h", paneDetail},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := paneState(t, true, true).setFocus(tc.from)

			assert.Equal(t, tc.want, press(m, "ctrl+w", tc.key).focusedPane())
		})
	}
}

// TestCtrlWSkipsClosedPanes checks the chord cannot focus a pane that is not
// drawn.
func TestCtrlWSkipsClosedPanes(t *testing.T) {
	m := paneState(t, false, false)

	assert.Equal(t, paneList, press(m, "ctrl+w", "h").focusedPane())
	assert.Equal(t, paneList, press(m, "ctrl+w", "j").focusedPane())
}

// TestClosingTheFocusedPaneReturnsFocusToMain checks the rule that keeps the
// keyboard pointed at something drawn: closing the pane you are standing in
// moves focus to the window's main pane.
func TestClosingTheFocusedPaneReturnsFocusToMain(t *testing.T) {
	m := paneState(t, true, true).setFocus(paneFilter)

	assert.Equal(t, paneList, press(m, "{").focusedPane())

	m = paneState(t, true, true).setFocus(paneDetail)

	assert.Equal(t, paneList, press(m, "}").focusedPane())
}

// TestTabDoesNotCycleInSettings checks the settings window keeps tab for its own
// fields, per vision.md. It is the reason the settings stack excludes ctxNav.
func TestTabDoesNotCycleInSettings(t *testing.T) {
	m := press(newTestModel(120, 40), ",")

	require.Equal(t, windowSettings, m.window)

	require.Equal(t, buttonSave, m.settings.current().key)

	m = press(m, "tab")

	assert.Equal(t, paneSettings, m.focusedPane())
	assert.Equal(t, buttonRevert, m.settings.current().key, "tab must move between fields, not panes")
}

// TestSwitchWindowPopsOverlaysAndCancelsChord checks that neither an overlay nor
// half a chord survives a window switch: both belong to the window that was in
// front, and carrying a half-pressed y across would make the next key mean
// something nobody asked for.
func TestSwitchWindowPopsOverlaysAndCancelsChord(t *testing.T) {
	m := press(newTestModel(120, 40), "?")
	require.Len(t, m.overlays, 1)

	m = press(m, "ctrl+t")
	assert.Empty(t, m.overlays)

	m = newTestModel(120, 40)
	m.chord = chordYank
	m = m.switchWindow(windowSettings)

	assert.Equal(t, chordNone, m.chord)
}

// TestSwitchingBackToTorrentsRepairsFocus checks a window switch cannot leave
// focus on a pane the torrents window has since closed.
func TestSwitchingBackToTorrentsRepairsFocus(t *testing.T) {
	m := paneState(t, true, true).setFocus(paneDetail)
	m.torrents.detailOpen = false

	assert.Equal(t, paneList, m.switchWindow(windowTorrents).focusedPane())
}

// TestContextStackFollowsFocus checks the router resolves against the focused
// pane's stack, which is the whole point of the focus router.
func TestContextStackFollowsFocus(t *testing.T) {
	m := paneState(t, true, true)

	assert.Equal(t, paneContexts(paneList), m.setFocus(paneList).contextStack())
	assert.Equal(t, paneContexts(paneFilter), m.setFocus(paneFilter).contextStack())
	assert.Equal(t, paneContexts(paneDetail), m.setFocus(paneDetail).contextStack())
}

// TestModalStatesReplaceTheStack checks the three states that swallow the
// keyboard — a focused input, a pending chord, an open overlay — replace the
// pane's stack rather than extending it, and in that precedence order.
func TestModalStatesReplaceTheStack(t *testing.T) {
	m := newTestModel(120, 40)

	overlay := press(m, "?")
	assert.Equal(t, []Context{ctxHelp, ctxGlobal}, overlay.contextStack())

	chorded := overlay
	chorded.chord = chordYank
	assert.Equal(t, []Context{ctxChordYank}, chorded.contextStack())

	inputs := chorded
	inputs.inputFocused = true
	assert.Equal(t, []Context{ctxTextInput}, inputs.contextStack())
}
