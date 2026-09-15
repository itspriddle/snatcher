package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAltSpeedToggles checks t writes the flipped switch in one session-set,
// shows it at once, and then re-reads the session for what the server kept.
func TestAltSpeedToggles(t *testing.T) {
	stub, srv := newRPCStub(t)

	m := pollOnce(t, liveModel(t, srv, 200, 40))
	require.False(t, m.live.session.AltSpeedEnabled)

	m, cmd := pressCmd(m, "t")
	assert.Equal(t, "alt-speed: switching…", m.toast)
	assert.Nil(t, stub.settingSetArgs(t), "nothing is written until the command runs")

	m, cmd = runCmd(t, m, cmd)

	assert.Equal(t, map[string]any{"alt-speed-enabled": true}, stub.settingSetArgs(t))
	assert.Equal(t, "alt-speed on", m.toast)
	assert.True(t, m.live.session.AltSpeedEnabled, "the bar changes before the re-read lands")
	assert.Contains(t, barText(m, 200), "alt-speed")
	assert.Contains(t, barText(m, 200), "(50.0 KB/s)")

	stub.respond("session-get", strings.Replace(bespokeSessionResponse, `"alt-speed-enabled": false`, `"alt-speed-enabled": true`, 1))

	before := stub.calls("session-get")
	m = run(t, m, cmd)

	assert.Equal(t, before+1, stub.calls("session-get"))
	assert.True(t, m.live.session.AltSpeedEnabled)
}

// TestAltSpeedTogglesOff checks t turns a running alt-speed off.
func TestAltSpeedTogglesOff(t *testing.T) {
	stub, srv := newRPCStub(t)

	m := pollOnce(t, liveModel(t, srv, 120, 40))
	m.live.session.AltSpeedEnabled = true

	m, cmd := pressCmd(m, "t")
	m, _ = runCmd(t, m, cmd)

	assert.Equal(t, map[string]any{"alt-speed-enabled": false}, stub.settingSetArgs(t))
	assert.Equal(t, "alt-speed off", m.toast)
	assert.False(t, m.live.session.AltSpeedEnabled)
}

// TestAltSpeedRefusals checks every reason t cannot act says so, without a
// round trip.
func TestAltSpeedRefusals(t *testing.T) {
	stub, srv := newRPCStub(t)

	m, cmd := pressCmd(newTestModel(120, 40), "t")
	assert.Nil(t, cmd)
	assert.Equal(t, "no server", m.toast)

	m, cmd = pressCmd(liveModel(t, srv, 120, 40), "t")
	assert.Nil(t, cmd)
	assert.Equal(t, "alt-speed: waiting for the server", m.toast)

	switching := pollOnce(t, liveModel(t, srv, 120, 40))
	switching.live.switching = true

	m, cmd = pressCmd(switching, "t")
	assert.Nil(t, cmd)
	assert.Equal(t, "switching servers…", m.toast)

	assert.Nil(t, stub.settingSetArgs(t))
}

// TestAltSpeedFailureChangesNothing checks a refused write is reported and the
// bar keeps what the server last said.
func TestAltSpeedFailureChangesNothing(t *testing.T) {
	stub, srv := newRPCStub(t)
	stub.failMethod("session-set")

	m := pollOnce(t, liveModel(t, srv, 120, 40))

	m, cmd := pressCmd(m, "t")
	m, next := runCmd(t, m, cmd)

	assert.Nil(t, next)
	assert.Contains(t, m.toast, "alt-speed: server error")
	assert.False(t, m.live.session.AltSpeedEnabled)
}

// TestStaleAltSpeedRepliesAreDropped checks a reply from before a server switch
// changes nothing.
func TestStaleAltSpeedRepliesAreDropped(t *testing.T) {
	_, srv := newRPCStub(t)

	m := pollOnce(t, liveModel(t, srv, 120, 40))

	got, cmd := m.update(altSpeedMsg{gen: 99, on: true})

	assert.Nil(t, cmd)
	assert.False(t, got.live.session.AltSpeedEnabled)
	assert.Empty(t, got.toast)
}
