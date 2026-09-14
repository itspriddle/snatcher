package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// portTestBody is a bespoke port-test answer.
func portTestBody(open bool) string {
	if open {
		return `{"arguments": {"port-is-open": true}, "result": "success", "tag": 1}`
	}

	return `{"arguments": {"port-is-open": false}, "result": "success", "tag": 1}`
}

// runAll runs a command and every command it batches, feeding each message back
// through update, and returns the model. The messages' own follow-ups are not
// run.
func runAll(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()

	require.NotNil(t, cmd, "expected a command to run")

	msg := cmd()

	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				m = runAll(t, m, c)
			}
		}

		return m
	}

	m, _ = m.update(msg)

	return m
}

// settingDef is a registry setting, failing the test when the key is unknown.
func settingDef(t *testing.T, key string) transmission.SettingDef {
	t.Helper()

	def, ok := transmission.LookupSetting(key)
	require.Truef(t, ok, "no setting %q", key)

	return def
}

// TestPortTestToastsOpen checks test port runs from the palette, says so while
// it runs, and reports the answer.
func TestPortTestToastsOpen(t *testing.T) {
	stub, m := polledModel(t)
	stub.respond("port-test", portTestBody(true))

	m, cmd := pressCmd(typeText(press(m, ":"), "test port"), "enter")
	assert.Empty(t, m.overlays)
	assert.Equal(t, "testing port…", m.toast)

	m, _ = runCmd(t, m, cmd)

	assert.Equal(t, 1, stub.calls("port-test"))
	assert.Equal(t, "port is open", m.toast)
}

// TestPortTestToastsClosed checks a closed port is reported as closed.
func TestPortTestToastsClosed(t *testing.T) {
	stub, m := polledModel(t)
	stub.respond("port-test", portTestBody(false))

	m, cmd := runAction(m, actionServerPortTest)
	m, _ = runCmd(t, m, cmd)

	assert.Equal(t, "port is closed", m.toast)
}

// TestPortTestNamesTheProtocol checks a server that names the IP protocol it
// tested over has it shown, sanitized.
func TestPortTestNamesTheProtocol(t *testing.T) {
	_, m := polledModel(t)

	got, cmd := m.update(portTestMsg{gen: m.live.gen, result: transmission.PortTest{Open: true, IPProtocol: "ipv4"}})

	assert.Nil(t, cmd)
	assert.Equal(t, "port is open (ipv4)", got.toast)

	got, _ = m.update(portTestMsg{gen: m.live.gen, result: transmission.PortTest{Open: true, IPProtocol: "ipv\x1b[2J6"}})
	assert.NotContains(t, got.toast, "\x1b")
}

// TestPortTestRefusals checks a port test with no server, or mid-switch, says
// why and sends nothing.
func TestPortTestRefusals(t *testing.T) {
	m, cmd := runAction(newTestModel(120, 40), actionServerPortTest)
	assert.Nil(t, cmd)
	assert.Equal(t, "no server", m.toast)

	stub, live := polledModel(t)
	live.live.switching = true

	m, cmd = runAction(live, actionServerPortTest)
	assert.Nil(t, cmd)
	assert.Equal(t, "switching servers…", m.toast)
	assert.Zero(t, stub.calls("port-test"))
}

// TestPortTestFailure checks a failed test is reported as one.
func TestPortTestFailure(t *testing.T) {
	stub, m := polledModel(t)
	stub.failMethod("port-test")

	m, cmd := runAction(m, actionServerPortTest)
	m, _ = runCmd(t, m, cmd)

	assert.True(t, strings.HasPrefix(m.toast, "port test: "), m.toast)
}

// TestStalePortTestIsDropped checks an answer from before a server switch
// changes nothing.
func TestStalePortTestIsDropped(t *testing.T) {
	_, m := polledModel(t)

	got, cmd := m.update(portTestMsg{gen: 99, result: transmission.PortTest{Open: true}})

	assert.Nil(t, cmd)
	assert.Empty(t, got.toast)
}

// TestResetSettingListsWritableSettings checks the chooser lists every setting
// but the read-only one, by key, with its description as the note.
func TestResetSettingListsWritableSettings(t *testing.T) {
	entries := resetSettingEntries()
	labels := entryLabels(entries)

	assert.NotContains(t, labels, "blocklist-size")
	assert.Len(t, entries, len(transmission.SettingDefs())-1)

	i := slices.Index(labels, "speed-limit-down")
	require.GreaterOrEqual(t, i, 0)
	assert.Equal(t, settingDef(t, "speed-limit-down").Desc, entries[i].note)
	assert.Empty(t, entries[i].action, "a chooser row runs no registered action")
}

// TestResetSettingConfirmsWithTheDefault checks picking a setting closes the
// chooser and asks, naming the value it will write.
func TestResetSettingConfirmsWithTheDefault(t *testing.T) {
	_, m := polledModel(t)

	m = press(typeText(press(m, ":"), "reset a"), "enter")
	assert.Equal(t, resetChooserTitle, topPalette(t, m).title)
	assert.True(t, m.inputFocused, "the chooser has the keyboard")

	m = press(typeText(m, "speed-limit-down"), "enter")

	require.Len(t, m.overlays, 1)
	assert.True(t, m.hasOverlay(confirmOverlayKind))
	assert.False(t, m.inputFocused)
	assert.Contains(t, ansi.Strip(m.render()), "Reset speed-limit-down to 100?")
}

// TestResetSettingFindsADescription checks the chooser matches a description
// when no key does.
func TestResetSettingFindsADescription(t *testing.T) {
	_, m := polledModel(t)

	m, _ = runAction(m, actionServerResetSetting)
	m = typeText(m, "local peer discovery")

	e, ok := topPalette(t, m).current()
	require.True(t, ok)
	assert.Equal(t, "lpd-enabled", e.label)
}

// TestResetSettingWritesTheDefault checks yes writes the default in one
// session-set and reports it.
func TestResetSettingWritesTheDefault(t *testing.T) {
	stub, m := polledModel(t)

	m, _ = runAction(m, actionServerResetSetting)
	m = press(typeText(m, "speed-limit-down"), "enter")

	m, cmd := pressCmd(m, "y")
	assert.Equal(t, "resetting speed-limit-down…", m.toast)
	assert.Nil(t, stub.settingSetArgs(t), "nothing is written until the command runs")

	m, _ = runCmd(t, m, cmd)

	assert.Equal(t, map[string]any{"speed-limit-down": float64(100)}, stub.settingSetArgs(t))
	assert.Equal(t, "Reset speed-limit-down to 100", m.toast)
}

// TestResetSettingRereadsTheSession checks a reset re-reads the session, so the
// status bar shows what the server kept.
func TestResetSettingRereadsTheSession(t *testing.T) {
	stub, m := polledModel(t)

	_, next := m.update(settingResetMsg{gen: m.live.gen, def: settingDef(t, "speed-limit-down")})
	require.NotNil(t, next)

	before := stub.calls("session-get")
	runAll(t, m, next)

	assert.Equal(t, before+1, stub.calls("session-get"))
}

// TestResetReloadsACleanSettingsWindow checks a reset reloads a loaded settings
// window with nothing unsaved, so its rows show the value just written.
func TestResetReloadsACleanSettingsWindow(t *testing.T) {
	_, srv := newRPCStub(t)

	m, _ := liveSettingsModel(t, srv, 120, 40)
	require.Equal(t, stateLoaded, m.settings.state)

	got, cmd := m.update(settingResetMsg{gen: m.live.gen, def: settingDef(t, "speed-limit-down")})

	assert.Equal(t, stateLoading, got.settings.state)
	assert.Equal(t, "Reset speed-limit-down to 100", got.toast)
	assert.Equal(t, stateLoaded, runAll(t, got, cmd).settings.state)
}

// TestResetLeavesADirtySettingsWindow checks a reset leaves a window with
// unsaved changes alone: the draft wins.
func TestResetLeavesADirtySettingsWindow(t *testing.T) {
	_, srv := newRPCStub(t)

	m, _ := liveSettingsModel(t, srv, 120, 40)
	m = press(networkTab(m), "tab", "enter")
	require.Equal(t, 1, m.settings.dirtyCount())

	got, _ := m.update(settingResetMsg{gen: m.live.gen, def: settingDef(t, "speed-limit-down")})

	assert.Equal(t, stateLoaded, got.settings.state)
	assert.Equal(t, 1, got.settings.dirtyCount())
	assert.Equal(t, "Reset speed-limit-down to 100", got.toast)
}

// TestResetRefusals checks the chooser does not open with no server or
// mid-switch, a confirm answered mid-switch writes nothing, and a failed or
// stale reset is reported or dropped.
func TestResetRefusals(t *testing.T) {
	m, cmd := runAction(newTestModel(120, 40), actionServerResetSetting)
	assert.Nil(t, cmd)
	assert.Equal(t, "no server", m.toast)
	assert.Empty(t, m.overlays)

	stub, live := polledModel(t)

	switching := live
	switching.live.switching = true

	m, cmd = runAction(switching, actionServerResetSetting)
	assert.Nil(t, cmd)
	assert.Equal(t, "switching servers…", m.toast)

	m, cmd = switching.resetSetting(settingDef(t, "speed-limit-down"))
	assert.Nil(t, cmd)
	assert.Equal(t, "switching servers…", m.toast)
	assert.Nil(t, stub.settingSetArgs(t))

	stub.failMethod("session-set")

	m, cmd = live.resetSetting(settingDef(t, "speed-limit-down"))
	m, next := runCmd(t, m, cmd)
	assert.Nil(t, next)
	assert.True(t, strings.HasPrefix(m.toast, "reset speed-limit-down: "), m.toast)

	got, cmd := live.update(settingResetMsg{gen: 99, def: settingDef(t, "speed-limit-down")})
	assert.Nil(t, cmd)
	assert.Empty(t, got.toast)
}

// TestPaletteResetGolden pins the reset chooser: its title, the keys, and their
// descriptions, scrolled to fit.
func TestPaletteResetGolden(t *testing.T) {
	_, m := polledModel(t)

	assertGolden(t, "palette_reset", press(typeText(press(m, ":"), "reset a"), "enter").render())
}
