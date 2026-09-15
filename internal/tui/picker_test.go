package tui

import (
	"errors"
	"net/http/httptest"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// pickerServers is the configured set the picker tests list: the live model is
// on "home", and "seedbox" is the default.
func pickerServers() []ServerEntry {
	return []ServerEntry{
		{Name: "home", URL: "http://nas:9091/transmission/rpc"},
		{Name: "office", URL: "http://10.0.0.5:9091/transmission/rpc"},
		{Name: "seedbox", URL: "http://sb.example:9091/transmission/rpc", Default: true},
	}
}

// withServers gives a model a server list.
func withServers(m Model, servers []ServerEntry) Model {
	m.deps.Servers = func() ([]ServerEntry, error) { return servers, nil }

	return m
}

// withDialer gives a model a dialer that connects every alias to srv, recording
// the aliases it was asked for.
func withDialer(m Model, srv *httptest.Server, warning string, dialed *[]string) Model {
	m.deps.Dial = func(name string) (DialResult, error) {
		*dialed = append(*dialed, name)

		return DialResult{
			Client:  transmission.New(srv.URL),
			Name:    name,
			URL:     "http://sb.example:9091/transmission/rpc",
			Warning: warning,
		}, nil
	}

	return m
}

// pickerModel is a polled live model with a server list, on "home".
func pickerModel(t *testing.T, srv *httptest.Server) Model {
	t.Helper()

	return withServers(pollOnce(t, liveModel(t, srv, 120, 40)), pickerServers())
}

// TestPickerOpensOnTheCurrentServer checks S opens the picker on the current
// alias, and that S and esc both close it.
func TestPickerOpensOnTheCurrentServer(t *testing.T) {
	_, srv := newRPCStub(t)

	m := press(pickerModel(t, srv), "S")

	p, ok := m.topPicker()
	require.True(t, ok)
	assert.Equal(t, "home", p.servers[p.cursor].Name)

	frame := ansi.Strip(m.render())
	assert.Contains(t, frame, "> home")
	assert.Contains(t, frame, "(current)")
	assert.Contains(t, frame, "(default)")

	assert.False(t, press(m, "S").hasOverlay(pickerOverlayKind))
	assert.False(t, press(m, "esc").hasOverlay(pickerOverlayKind))
}

// TestPickerCursorStopsAtTheEnds checks the cursor clamps rather than wrapping.
func TestPickerCursorStopsAtTheEnds(t *testing.T) {
	_, srv := newRPCStub(t)

	m := press(pickerModel(t, srv), "S", "k")

	p, _ := m.topPicker()
	assert.Zero(t, p.cursor)

	p, _ = press(m, "j", "j", "j", "j").topPicker()
	assert.Equal(t, 2, p.cursor)
}

// TestPickerRefusals checks every reason S cannot open a picker says so.
func TestPickerRefusals(t *testing.T) {
	_, srv := newRPCStub(t)

	live := pollOnce(t, liveModel(t, srv, 120, 40))

	assert.Equal(t, "no server list", press(live, "S").toast)
	assert.Equal(t, "only one server configured", press(withServers(live, pickerServers()[:1]), "S").toast)

	failing := live
	failing.deps.Servers = func() ([]ServerEntry, error) { return nil, errors.New("config: bad json") }
	assert.Equal(t, "server list: config: bad json", press(failing, "S").toast)

	switching := withServers(live, pickerServers())
	switching.live.switching = true

	got := press(switching, "S")
	assert.False(t, got.hasOverlay(pickerOverlayKind))
	assert.Equal(t, "switching servers…", got.toast)
}

// TestPickerSwitchesServers checks enter dials the chosen alias off the update
// loop, then starts that server from nothing and polls it.
func TestPickerSwitchesServers(t *testing.T) {
	_, homeSrv := newRPCStub(t)
	seedbox, seedboxSrv := newRPCStub(t)
	seedbox.respond("torrent-get", torrentsBody(stubTorrent{9, "remote.iso", 0}))

	var dialed []string

	m := withDialer(press(pickerModel(t, homeSrv), "space"), seedboxSrv, "", &dialed)
	require.Equal(t, 1, m.torrents.sel.markCount())

	m, cmd := pressCmd(press(m, "S", "j", "j"), "enter")

	assert.False(t, m.hasOverlay(pickerOverlayKind))
	assert.True(t, m.live.switching)
	assert.Equal(t, "connecting to seedbox…", m.toast)
	assert.Len(t, m.torrents.rows, 3, "the old rows stay until the dial lands")

	m, cmd = runCmd(t, m, cmd)

	assert.Equal(t, []string{"seedbox"}, dialed)
	assert.False(t, m.live.switching)
	assert.Equal(t, "seedbox", m.serverName)
	assert.Equal(t, "switched to seedbox", m.toast)
	assert.Empty(t, m.torrents.rows)
	assert.Zero(t, m.torrents.sel.markCount())
	assert.False(t, m.live.loaded)
	assert.Contains(t, ansi.Strip(m.render()), "\nseedbox ", "the status bar names the new alias")

	m, cmd = runCmd(t, m, cmd)
	m = settle(t, m, cmd)

	assert.Equal(t, []int64{9}, rowIDs(m.torrents.rows))
	assert.Equal(t, 1, seedbox.calls("torrent-get"))
	assert.Equal(t, probeSessionGets+1, seedbox.calls("session-get"), "the first poll carries the slow refresh")
}

// TestPickerDropsTheOldServersReplies checks a poll the old server answers after
// a switch has started changes nothing.
func TestPickerDropsTheOldServersReplies(t *testing.T) {
	_, homeSrv := newRPCStub(t)
	_, seedboxSrv := newRPCStub(t)

	var dialed []string

	m := withDialer(pickerModel(t, homeSrv), seedboxSrv, "", &dialed)

	m, poll := m.pollNow()
	m, pollBatch := runCmd(t, m, poll)

	m, _ = pressCmd(press(m, "S", "j", "j"), "enter")
	m = settle(t, m, pollBatch)

	assert.True(t, m.live.switching)
	assert.Len(t, m.torrents.rows, 3)

	_, cmd := m.update(tickMsg{gen: 0, seq: m.live.tickSeq})
	assert.Nil(t, cmd, "a tick from before the switch")
}

// TestPickerConfirmsUnsavedSettings checks a switch that would discard settings
// asks first, and switches only on yes.
func TestPickerConfirmsUnsavedSettings(t *testing.T) {
	_, homeSrv := newRPCStub(t)
	_, seedboxSrv := newRPCStub(t)

	var dialed []string

	m := withDirtySetting(withDialer(pickerModel(t, homeSrv), seedboxSrv, "", &dialed))

	prompted, cmd := pressCmd(press(m, "S", "j", "j"), "enter")

	assert.Nil(t, cmd)
	require.True(t, prompted.hasOverlay(confirmOverlayKind))
	assert.Contains(t, ansi.Strip(prompted.render()), "Discard 1 unsaved change and switch to seedbox?")

	declined := press(prompted, "n")
	assert.False(t, declined.live.switching)
	assert.Equal(t, 1, declined.settings.dirtyCount())

	accepted, cmd := pressCmd(prompted, "y")
	assert.True(t, accepted.live.switching)

	accepted, _ = runCmd(t, accepted, cmd)
	assert.Zero(t, accepted.settings.dirtyCount())
	assert.Equal(t, []string{"seedbox"}, dialed)
}

// TestPickerDialFailureKeepsTheServer checks a failed dial leaves the TUI on the
// server it was on, with its rows, and resumes polling it.
func TestPickerDialFailureKeepsTheServer(t *testing.T) {
	stub, srv := newRPCStub(t)

	m := pickerModel(t, srv)
	m.deps.Dial = func(string) (DialResult, error) {
		return DialResult{}, errors.New(`unknown server "office"`)
	}

	m, cmd := pressCmd(press(m, "S", "j"), "enter")
	m, cmd = runCmd(t, m, cmd)

	assert.False(t, m.live.switching)
	assert.Equal(t, "home", m.serverName)
	assert.Len(t, m.torrents.rows, 3)
	assert.Equal(t, `switch failed: unknown server "office"`, m.toast)

	m, cmd = runCmd(t, m, cmd)
	settle(t, m, cmd)

	assert.Equal(t, 2, stub.calls("torrent-get"), "polling resumes on the old server")
}

// TestPickerAlreadyOnTheServer checks picking the current alias dials nothing.
func TestPickerAlreadyOnTheServer(t *testing.T) {
	_, srv := newRPCStub(t)

	m, cmd := pressCmd(press(pickerModel(t, srv), "S"), "enter")

	assert.Nil(t, cmd)
	assert.Equal(t, "already on home", m.toast)
}

// TestPickerShowsTheDialWarning checks a warning the dial captured is the toast a
// successful switch leaves, rather than a line under the alt screen.
func TestPickerShowsTheDialWarning(t *testing.T) {
	_, homeSrv := newRPCStub(t)
	_, seedboxSrv := newRPCStub(t)

	var dialed []string

	m := withDialer(pickerModel(t, homeSrv), seedboxSrv, "Warning: the OS secret store could not be reached", &dialed)

	m, cmd := pressCmd(press(m, "S", "j", "j"), "enter")
	m, _ = runCmd(t, m, cmd)

	assert.Equal(t, "Warning: the OS secret store could not be reached", m.toast)
}

// TestPickerSwitchReloadsTheSettingsWindow checks a switch made from the
// settings window loads the new server's settings.
func TestPickerSwitchReloadsTheSettingsWindow(t *testing.T) {
	_, homeSrv := newRPCStub(t)
	_, seedboxSrv := newRPCStub(t)

	var dialed []string

	m := withDialer(pickerModel(t, homeSrv), seedboxSrv, "", &dialed)
	m, load := pressCmd(m, ",")
	m = run(t, m, load)
	require.Equal(t, stateLoaded, m.settings.state)

	m, cmd := pressCmd(press(m, "S", "j", "j"), "enter")
	m, _ = runCmd(t, m, cmd)

	assert.Equal(t, windowSettings, m.window)
	assert.Equal(t, stateLoading, m.settings.state)
}

// TestCtrlCQuitsUnderThePicker checks the picker cannot trap a user.
func TestCtrlCQuitsUnderThePicker(t *testing.T) {
	_, srv := newRPCStub(t)

	_, cmd := pressCmd(press(pickerModel(t, srv), "S"), "ctrl+c")
	require.NotNil(t, cmd)

	_, quit := cmd().(tea.QuitMsg)
	assert.True(t, quit)
}
