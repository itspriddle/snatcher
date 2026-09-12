package tui

import (
	"reflect"
	"slices"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cursorKey is the key of the settings row under the cursor.
func cursorKey(m Model) string {
	return m.settings.current().key
}

// TestSettingsTabsWrapAndMarkDirtyTabs checks [ and ] cycle both ways, and that
// a tab holding an unsaved change says so in its label.
func TestSettingsTabsWrapAndMarkDirtyTabs(t *testing.T) {
	m := settingsModel(120, 40)

	require.Equal(t, tabGeneral, m.settings.tab)
	assert.Equal(t, tabQueue, press(m, "[").settings.tab, "[ wraps backwards")
	assert.Equal(t, tabGeneral, press(m, "]", "]", "]", "]", "]", "]").settings.tab, "] wraps forwards")

	m = editField(networkTab(m), "31500")

	names := m.settings.tabNames()

	assert.Equal(t, "Network*", names[tabNetwork])
	assert.Equal(t, "Download", names[tabDownload])
}

// TestSettingsFieldCyclingSkipsReadOnly checks tab visits every editable row and
// both buttons, wraps, and never rests on blocklist-size.
func TestSettingsFieldCyclingSkipsReadOnly(t *testing.T) {
	m := networkTab(settingsModel(120, 40))

	require.Equal(t, "peer-port", cursorKey(m))

	var visited []string

	for range 14 {
		m = press(m, "tab")
		visited = append(visited, cursorKey(m))
	}

	assert.NotContains(t, visited, "blocklist-size")
	assert.Contains(t, visited, buttonSave)
	assert.Contains(t, visited, buttonRevert)
	assert.Equal(t, "peer-port", visited[len(visited)-1], "tab wraps back to the first row")

	assert.Equal(t, buttonRevert, cursorKey(press(networkTab(settingsModel(120, 40)), "shift+tab")))
}

// TestSettingsFlagOverriddenThemeIsReadOnly checks a client key set by a flag
// for this run is skipped, and says why.
func TestSettingsFlagOverriddenThemeIsReadOnly(t *testing.T) {
	cfg := newFakeConfig()
	cfg.entries[2] = ClientEntry{Key: clientKeyTheme, Value: "nord", Source: "flag", SetBy: "--theme", Allowed: builtinThemes}

	deps := testDeps()
	deps.Config = cfg

	m := settingsModelWith(deps, 120, 40)

	require.Equal(t, clientKeyNerdFont, cursorKey(m), "the cursor must not rest on the overridden row")

	for range 3 {
		m = press(m, "tab")
		assert.NotEqual(t, clientKeyTheme, cursorKey(m))
	}

	assert.Contains(t, ansi.Strip(m.render()), "(set by --theme for this run)")
}

// TestSettingsNilConfigIsReadOnly checks the window without a config store: the
// client rows show what this run resolved and cannot be edited.
func TestSettingsNilConfigIsReadOnly(t *testing.T) {
	m := press(newTestModel(120, 40), ",")

	assert.Equal(t, buttonSave, cursorKey(m))
	assert.Contains(t, ansi.Strip(m.render()), "(no config store)")
}

// TestSettingsNoServer checks the server tabs without a dialed client.
func TestSettingsNoServer(t *testing.T) {
	m := gotoTab(press(newTestModel(120, 40), ","), tabDownload)

	assert.True(t, m.settings.noServer)
	assert.Equal(t, buttonSave, cursorKey(m))
	assert.Contains(t, ansi.Strip(m.render()), "(no server)")
}

// TestSettingsToggleFlipsOnEnterUpAndDown checks all three keys flip a toggle in
// place.
func TestSettingsToggleFlipsOnEnterUpAndDown(t *testing.T) {
	m := press(networkTab(settingsModel(120, 40)), "tab")

	require.Equal(t, "peer-port-random-on-start", cursorKey(m))
	require.Equal(t, false, m.settings.current().value)

	m = press(m, "enter")
	assert.Equal(t, true, m.settings.current().value)
	assert.Equal(t, 1, m.settings.dirtyCount())

	m = press(m, "down")
	assert.Equal(t, false, m.settings.current().value)
	assert.Zero(t, m.settings.dirtyCount(), "flipping back is not a change")

	assert.Equal(t, true, press(m, "up").settings.current().value)
}

// TestSettingsSelect covers the dropdown — open, move, commit, cancel — and
// cycling an option in place without opening it.
func TestSettingsSelect(t *testing.T) {
	m := settingsModel(120, 40)

	require.Equal(t, clientKeyTheme, cursorKey(m))

	opened := press(m, "enter")
	require.Equal(t, modeSelect, opened.settings.mode)
	assert.Equal(t, 0, opened.settings.selectIdx)

	moved := press(opened, "down", "down", "down")
	assert.Equal(t, len(builtinThemes)-1, moved.settings.selectIdx, "the dropdown cursor is clamped")

	committed := press(opened, "down", "enter")
	assert.Equal(t, modeNav, committed.settings.mode)
	assert.Equal(t, themeDracula, committed.settings.current().value)

	canceled := press(opened, "down", "esc")
	assert.Equal(t, modeNav, canceled.settings.mode)
	assert.Equal(t, windowSettings, canceled.window, "esc closes the dropdown, not the window")
	assert.Equal(t, "default", canceled.settings.current().value)

	assert.Equal(t, themeDracula, press(m, "down").settings.current().value)
	assert.Equal(t, themeNord, press(m, "up").settings.current().value, "up wraps")

	closedByTab := press(opened, "tab")
	assert.Equal(t, modeNav, closedByTab.settings.mode)
}

// TestSettingsTextCommitsPerType checks a submitted draft is parsed into the
// setting's own type, and that an invalid one keeps the input open with the
// reason on the hint line.
func TestSettingsTextCommitsPerType(t *testing.T) {
	tests := []struct {
		name  string
		tab   int
		moves int
		key   string
		input string
		want  any
	}{
		{name: "int", tab: tabNetwork, key: "peer-port", input: "31500", want: 31500},
		{name: "int64", tab: tabBandwidth, moves: 1, key: "speed-limit-down", input: "512", want: int64(512)},
		{name: "float", tab: tabDownload, moves: 6, key: "seed-ratio-limit", input: "1.5", want: 1.5},
		{name: "string", tab: tabDownload, key: "download-dir", input: " /srv/dl ", want: "/srv/dl"},
		{name: "duration", tab: tabPolling, key: clientKeyPollInterval, input: "30s", want: "30s"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := gotoTab(settingsModel(120, 40), tc.tab)

			for range tc.moves {
				m = press(m, "tab")
			}

			require.Equal(t, tc.key, cursorKey(m))

			m = editField(m, tc.input)

			assert.Equal(t, modeNav, m.settings.mode)
			assert.False(t, m.inputFocused)
			assert.Equal(t, tc.want, m.settings.current().value)
			assert.True(t, m.settings.current().dirty())
		})
	}

	invalid := []struct {
		name  string
		tab   int
		input string
	}{
		{name: "int", tab: tabNetwork, input: "abc"},
		{name: "duration out of range", tab: tabPolling, input: "2h"},
	}

	for _, tc := range invalid {
		t.Run("invalid "+tc.name, func(t *testing.T) {
			m := gotoTab(settingsModel(120, 40), tc.tab)
			before := m.settings.current().value

			m = editField(m, tc.input)

			assert.Equal(t, modeEdit, m.settings.mode, "an invalid value keeps the input open")
			assert.True(t, m.inputFocused)
			assert.NotEmpty(t, m.settings.current().err)
			assert.Equal(t, before, m.settings.current().value)
			assert.Contains(t, ansi.Strip(m.render()), "error: ")
		})
	}
}

// TestSettingsEscDiscardsTheDraft checks esc in the input leaves the row as it
// was: the draft is not the row's state.
func TestSettingsEscDiscardsTheDraft(t *testing.T) {
	m := networkTab(settingsModel(120, 40))

	m = typeText(press(m, "enter", "ctrl+u"), "1")
	require.Equal(t, "1", m.settings.input.Value())

	m = press(m, "esc")

	assert.Equal(t, modeNav, m.settings.mode)
	assert.False(t, m.inputFocused)
	assert.Equal(t, windowSettings, m.window)
	assert.Equal(t, 31129, m.settings.current().value)
	assert.Zero(t, m.settings.dirtyCount())
}

// TestSettingsRevertDiscardsEveryTab checks Revert undoes changes on tabs other
// than the one it is pressed on.
func TestSettingsRevertDiscardsEveryTab(t *testing.T) {
	m := press(settingsModel(120, 40), "down")
	m = press(networkTab(m), "tab", "enter")

	require.Equal(t, 2, m.settings.dirtyCount())

	m = press(m, "shift+tab", "shift+tab")
	require.Equal(t, buttonRevert, cursorKey(m))

	m = press(m, "enter")

	assert.Zero(t, m.settings.dirtyCount())
	assert.Contains(t, m.toast, "reverted 2 changes")
}

// TestSettingsBusyBlocksEdits checks nothing in the window moves while a round
// trip is in flight.
func TestSettingsBusyBlocksEdits(t *testing.T) {
	m := settingsModel(120, 40)
	m.settings.state = stateSaving

	got := press(m, "]")

	assert.Equal(t, m.settings.tab, got.settings.tab)
	assert.Equal(t, "saving…", got.toast)
}

// TestSettingsSanitizesServerDerivedValues checks a download-dir carrying an
// escape sequence is drawn neutralized, in navigation and in the input.
func TestSettingsSanitizesServerDerivedValues(t *testing.T) {
	m := gotoTab(settingsModel(120, 40), tabDownload)

	require.Contains(t, fixtureSession().DownloadDir, "\x1b")

	frame := m.render()
	assert.NotContains(t, frame, "\x1b[2J")
	assert.Contains(t, ansi.Strip(frame), "downloads")

	editing := press(m, "enter")
	assert.NotContains(t, editing.settings.input.Value(), "\x1b")
	assert.NotContains(t, editing.render(), "\x1b[2J")
}

// TestEditingSwallowsGlobalKeysButCtrlCQuits pins the focused-input rule in the
// settings window: q, the comma, and ctrl+t are text, and ctrl+c still quits.
func TestEditingSwallowsGlobalKeysButCtrlCQuits(t *testing.T) {
	m := press(networkTab(settingsModel(120, 40)), "enter")

	m = press(m, "q", ",", "ctrl+t", "?")

	assert.Equal(t, windowSettings, m.window)
	assert.True(t, m.inputFocused)
	assert.Empty(t, m.overlays)
	assert.Equal(t, "31129q,?", m.settings.input.Value())

	_, cmd := pressCmd(m, "ctrl+c")
	require.NotNil(t, cmd)

	_, quit := cmd().(tea.QuitMsg)
	assert.True(t, quit)
}

// TestCtrlHIsIgnoredWhileEditing checks the banned ctrl+h does nothing in the
// input — legacy terminals send it for backspace, so binding it would make one
// physical key mean two things — while backspace itself deletes.
func TestCtrlHIsIgnoredWhileEditing(t *testing.T) {
	m := press(networkTab(settingsModel(120, 40)), "enter")

	assert.Equal(t, "31129", press(m, "ctrl+h").settings.input.Value())
	assert.Equal(t, "3112", press(m, "backspace").settings.input.Value())
}

// TestSettingsInputKeyMapIsReadlineMinusBanned is the settings input's
// component-keymap check. The input is deliberately not in componentKeyMaps:
// its readline chords are the documented exception to "every key is a registry
// row". What it may not do is bind a banned key, bind one of the keys the
// router reserves, or keep a suggestion or paste default.
func TestSettingsInputKeyMapIsReadlineMinusBanned(t *testing.T) {
	km := textInputKeyMap()
	v := reflect.ValueOf(km)

	reserved := []string{"esc", "enter", "tab", "shift+tab", "ctrl+c"}

	for i := range v.NumField() {
		field := v.Type().Field(i).Name

		binding, ok := v.Field(i).Interface().(key.Binding)
		require.Truef(t, ok, "%s is not a key.Binding", field)

		for _, k := range binding.Keys() {
			assert.Falsef(t, isBannedKey(k), "%s binds the banned key %q", field, k)
			assert.Falsef(t, slices.Contains(reserved, k), "%s binds the reserved key %q", field, k)
		}
	}

	assert.Equal(t, []string{"backspace"}, km.DeleteCharacterBackward.Keys())

	for name, b := range map[string]key.Binding{
		"AcceptSuggestion": km.AcceptSuggestion, "NextSuggestion": km.NextSuggestion,
		"PrevSuggestion": km.PrevSuggestion, "Paste": km.Paste,
	} {
		assert.Emptyf(t, b.Keys(), "%s must stay unbound", name)
	}
}

// TestStatusBarShowsUnsavedCount checks the reminder follows the user out of the
// settings window.
func TestStatusBarShowsUnsavedCount(t *testing.T) {
	m := press(networkTab(settingsModel(120, 40)), "tab", "enter")

	assert.Contains(t, m.statusBarView(120), "unsaved: 1")
	assert.Contains(t, press(m, "ctrl+t").statusBarView(120), "unsaved: 1")
	assert.NotContains(t, settingsModel(120, 40).statusBarView(120), "unsaved")
}

// TestSettingsReentryKeepsEdits checks leaving and re-entering the window does
// not reload over an unsaved change.
func TestSettingsReentryKeepsEdits(t *testing.T) {
	m := press(settingsModel(120, 40), "down")
	require.Equal(t, 1, m.settings.dirtyCount())

	m = press(m, "ctrl+t", ",")

	assert.Equal(t, 1, m.settings.dirtyCount())
	assert.Equal(t, themeDracula, m.settings.tabs[tabGeneral].fields[0].value)
}

// TestEscWithUnsavedChangesPrompts walks the leave prompt: esc asks, esc on the
// prompt stays with the changes, and no discards them and leaves.
func TestEscWithUnsavedChangesPrompts(t *testing.T) {
	m := press(networkTab(settingsModel(120, 40)), "tab", "enter")
	require.Equal(t, 1, m.settings.dirtyCount())

	m = press(m, "esc")
	require.True(t, m.hasOverlay(confirmOverlayKind))
	assert.Contains(t, ansi.Strip(m.render()), "Save 1 change before leaving?")
	assert.Contains(t, ansi.Strip(m.render()), "unsaved on: Network")

	stayed := press(m, "esc")
	assert.Empty(t, stayed.overlays)
	assert.Equal(t, windowSettings, stayed.window)
	assert.Equal(t, 1, stayed.settings.dirtyCount())

	left := press(m, "n")
	assert.Empty(t, left.overlays)
	assert.Equal(t, windowTorrents, left.window)
	assert.Zero(t, left.settings.dirtyCount())
}

// TestEscWhenCleanLeavesWithoutPrompting checks the prompt is only for unsaved
// changes.
func TestEscWhenCleanLeavesWithoutPrompting(t *testing.T) {
	m := press(settingsModel(120, 40), "esc")

	assert.Empty(t, m.overlays)
	assert.Equal(t, windowTorrents, m.window)
}

// TestQuitWithUnsavedChangesPrompts checks q asks before quitting past unsaved
// settings, from the settings window and from the torrents window, and that no
// quits.
func TestQuitWithUnsavedChangesPrompts(t *testing.T) {
	dirty := press(settingsModel(120, 40), "down")

	for name, m := range map[string]Model{"settings": dirty, "torrents": press(dirty, "ctrl+t")} {
		t.Run(name, func(t *testing.T) {
			m, cmd := pressCmd(m, "q")

			assert.Nil(t, cmd, "q must not quit past unsaved changes")
			require.True(t, m.hasOverlay(confirmOverlayKind))
			assert.Contains(t, ansi.Strip(m.render()), "Save 1 change before quitting?")

			_, cmd = pressCmd(m, "n")
			require.NotNil(t, cmd)

			_, quit := cmd().(tea.QuitMsg)
			assert.True(t, quit)
		})
	}
}

// TestQuitWhenCleanQuits checks q with nothing unsaved quits at once.
func TestQuitWhenCleanQuits(t *testing.T) {
	_, cmd := pressCmd(settingsModel(120, 40), "q")
	require.NotNil(t, cmd)

	_, quit := cmd().(tea.QuitMsg)
	assert.True(t, quit)
}

// TestCtrlCNeverPrompts pins the never-trapped rule: ctrl+c quits past unsaved
// changes and past an open prompt.
func TestCtrlCNeverPrompts(t *testing.T) {
	dirty := press(settingsModel(120, 40), "down")

	for name, m := range map[string]Model{"dirty": dirty, "prompting": press(dirty, "q")} {
		t.Run(name, func(t *testing.T) {
			_, cmd := pressCmd(m, "ctrl+c")
			require.NotNil(t, cmd)

			_, quit := cmd().(tea.QuitMsg)
			assert.True(t, quit)
		})
	}
}

// TestConfirmSwallowsGlobalKeys checks nothing but an answer gets past an open
// prompt.
func TestConfirmSwallowsGlobalKeys(t *testing.T) {
	m := press(press(settingsModel(120, 40), "down"), "esc")
	require.True(t, m.hasOverlay(confirmOverlayKind))

	got, cmd := pressCmd(m, "q")
	assert.Nil(t, cmd)

	got = press(got, ",", "?", "ctrl+t", "]")

	assert.Len(t, got.overlays, 1)
	assert.Equal(t, windowSettings, got.window)
	assert.Equal(t, tabGeneral, got.settings.tab)
}

// TestSettingsScrollKeepsTheCursorVisible checks a tab taller than the region
// scrolls to follow the cursor at the smallest supported size.
func TestSettingsScrollKeepsTheCursorVisible(t *testing.T) {
	m := networkTab(settingsModel(60, 16))

	for range 11 {
		m = press(m, "tab")
	}

	require.Equal(t, "blocklist-url", cursorKey(m))
	assert.Positive(t, m.settings.tabs[tabNetwork].offset)
	assert.Contains(t, ansi.Strip(m.render()), "> blocklist-url")

	for range 11 {
		m = press(m, "shift+tab")
	}

	require.Equal(t, "peer-port", cursorKey(m))
	assert.Zero(t, m.settings.tabs[tabNetwork].offset)
	assert.Contains(t, ansi.Strip(m.render()), "> peer-port")
}
