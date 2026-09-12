package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// bespokeSessionResponse is a session-get payload on the legacy (bespoke) wire
// scheme covering every setting, copied from internal/commands' fixture. The
// download-dir carries an escape sequence for the sanitize rule, and encryption
// is "tolerated" on the wire, which the rpc layer normalizes to "allowed".
const bespokeSessionResponse = `{
	"arguments": {
		"version": "4.0.6 (38c164933e)",
		"rpc-version": 17,
		"rpc-version-semver": "5.3.0",
		"download-dir": "/data\u001b[2Jdownloads",
		"start-added-torrents": true,
		"rename-partial-files": true,
		"incomplete-dir-enabled": false,
		"incomplete-dir": "",
		"seedRatioLimited": true,
		"seedRatioLimit": 2.5,
		"idle-seeding-limit-enabled": false,
		"idle-seeding-limit": 30,
		"cache-size-mb": 8,
		"peer-port": 31129,
		"peer-port-random-on-start": false,
		"port-forwarding-enabled": true,
		"encryption": "tolerated",
		"peer-limit-global": 240,
		"peer-limit-per-torrent": 60,
		"pex-enabled": true,
		"dht-enabled": true,
		"lpd-enabled": false,
		"utp-enabled": true,
		"blocklist-enabled": false,
		"blocklist-url": "http://list.example/blocklist",
		"blocklist-size": 1234,
		"speed-limit-down-enabled": false,
		"speed-limit-down": 100,
		"speed-limit-up-enabled": false,
		"speed-limit-up": 100,
		"alt-speed-enabled": false,
		"alt-speed-down": 50,
		"alt-speed-up": 50,
		"alt-speed-time-enabled": false,
		"alt-speed-time-begin": 540,
		"alt-speed-time-end": 1020,
		"alt-speed-time-day": 127,
		"download-queue-enabled": true,
		"download-queue-size": 5,
		"seed-queue-enabled": false,
		"seed-queue-size": 10,
		"queue-stalled-enabled": true,
		"queue-stalled-minutes": 30
	},
	"result": "success",
	"tag": 1
}`

// sessionIDHeader is Transmission's CSRF header.
const sessionIDHeader = "X-Transmission-Session-Id"

// sessionStub is a stateless Transmission server on the bespoke path. It
// records every request body, and can be switched into failing modes while a
// test runs.
type sessionStub struct {
	mu     sync.Mutex
	bodies []string

	// status, when non-zero, is returned for every request instead of an
	// answer.
	status atomic.Int32

	// failSet makes session-set answer with an RPC error.
	failSet atomic.Bool
}

// newSessionStub starts a stub server for the test.
func newSessionStub(t *testing.T) (*sessionStub, *httptest.Server) {
	t.Helper()

	stub := &sessionStub{}
	srv := httptest.NewServer(http.HandlerFunc(stub.serve))
	t.Cleanup(srv.Close)

	return stub, srv
}

// serve answers one request: the 409 handshake, then session-get with the
// fixture and everything else with success.
func (s *sessionStub) serve(w http.ResponseWriter, r *http.Request) {
	if code := s.status.Load(); code != 0 {
		w.WriteHeader(int(code))

		return
	}

	if r.Header.Get(sessionIDHeader) == "" {
		w.Header().Set(sessionIDHeader, "session-token")
		w.WriteHeader(http.StatusConflict)

		return
	}

	body, _ := io.ReadAll(r.Body)

	s.mu.Lock()
	s.bodies = append(s.bodies, string(body))
	s.mu.Unlock()

	var envelope struct {
		Method string `json:"method"`
	}

	_ = json.Unmarshal(body, &envelope)

	switch {
	case envelope.Method == "session-get":
		_, _ = w.Write([]byte(bespokeSessionResponse))
	case envelope.Method == "session-set" && s.failSet.Load():
		_, _ = w.Write([]byte(`{"result": "permission denied", "arguments": {}, "tag": 1}`))
	default:
		_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
	}
}

// settingSetArgs returns the arguments of every session-set the stub received,
// merged — or nil when none was sent.
func (s *sessionStub) settingSetArgs(t *testing.T) map[string]any {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	var merged map[string]any

	for _, body := range s.bodies {
		if !strings.Contains(body, "session-set") {
			continue
		}

		var envelope struct {
			Arguments map[string]any `json:"arguments"`
		}

		require.NoError(t, json.Unmarshal([]byte(body), &envelope))

		if merged == nil {
			merged = map[string]any{}
		}

		for k, v := range envelope.Arguments {
			merged[k] = v
		}
	}

	return merged
}

// liveSettingsModel is a model pointed at a stub server, in the settings window
// with its session loaded through the real command.
func liveSettingsModel(t *testing.T, srv *httptest.Server, w, h int) (Model, *fakeConfig) {
	t.Helper()

	cfg := newFakeConfig()

	deps := testDeps()
	deps.Config = cfg

	m := New(deps).applyTheme(true).resize(w, h)
	m.client = transmission.New(srv.URL)
	m.ctx = t.Context()

	m, cmd := pressCmd(m, ",")

	require.Equal(t, stateLoading, m.settings.state)

	return run(t, m, cmd), cfg
}

// run executes a command synchronously and feeds its message back through
// update, discarding any follow-up command.
func run(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()

	m, _ = runCmd(t, m, cmd)

	return m
}

// runCmd is run, returning the follow-up command.
func runCmd(t *testing.T, m Model, cmd tea.Cmd) (Model, tea.Cmd) {
	t.Helper()

	require.NotNil(t, cmd, "expected a command to run")

	return m.update(cmd())
}

// pressSave moves to the Save button on the current tab and presses it.
func pressSave(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()

	for m.settings.current().key != buttonSave {
		m = press(m, "tab")
	}

	return pressCmd(m, "enter")
}

// TestSettingsLoadOverRPC checks opening the window fetches the session and
// draws it, normalized and sanitized.
func TestSettingsLoadOverRPC(t *testing.T) {
	_, srv := newSessionStub(t)

	m, _ := liveSettingsModel(t, srv, 120, 40)

	require.Equal(t, stateLoaded, m.settings.state)

	net := networkTab(m)
	assert.Equal(t, "peer-port", cursorKey(net), "a fresh load starts each server tab on its first row")
	assert.Equal(t, 31129, net.settings.current().value)

	encryption, ok := net.settings.fieldValue("encryption")
	require.True(t, ok)
	assert.Equal(t, "allowed", encryption)

	frame := gotoTab(m, tabDownload).render()
	assert.NotContains(t, frame, "\x1b[2J")
	assert.Contains(t, ansi.Strip(frame), "downloads")
}

// TestSettingsReentryDoesNotReload checks a loaded window is not re-fetched when
// it is opened again.
func TestSettingsReentryDoesNotReload(t *testing.T) {
	_, srv := newSessionStub(t)

	m, _ := liveSettingsModel(t, srv, 120, 40)

	_, cmd := pressCmd(press(m, "ctrl+t"), ",")

	assert.Nil(t, cmd)
}

// TestSettingsLoadingKeepsClientTabsUsable checks a slow load does not lock the
// client tabs, and that the server tabs say they are loading.
func TestSettingsLoadingKeepsClientTabsUsable(t *testing.T) {
	_, srv := newSessionStub(t)

	deps := testDeps()
	deps.Config = newFakeConfig()

	m := New(deps).applyTheme(true).resize(120, 40)
	m.client = transmission.New(srv.URL)
	m.ctx = t.Context()
	m = press(m, ",")

	require.Equal(t, stateLoading, m.settings.state)

	assert.Equal(t, themeDracula, press(m, "down").settings.current().value)

	loading := networkTab(m)
	assert.Contains(t, ansi.Strip(loading.render()), "loading settings…")

	_, cmd := pressSave(t, loading)
	assert.Nil(t, cmd, "save must wait for the load")

	assertGolden(t, "settings_loading", loading.render())
}

// TestSettingsSaveSendsExactlyTheChangedSettings checks a save writes only what
// changed — one sparse session-set and one client write — then settles clean,
// and that a theme change is applied to the running UI.
func TestSettingsSaveSendsExactlyTheChangedSettings(t *testing.T) {
	stub, srv := newSessionStub(t)

	m, cfg := liveSettingsModel(t, srv, 120, 40)

	m = press(m, "down")
	m = editField(networkTab(m), "31500")
	m = press(m, "tab", "tab", "tab", "down", "down")

	require.Equal(t, 3, m.settings.dirtyCount())

	m, cmd := pressSave(t, m)
	require.Equal(t, stateSaving, m.settings.state)

	m = run(t, m, cmd)

	assert.Equal(t, map[string]any{"peer-port": float64(31500), "encryption": "required"}, stub.settingSetArgs(t))
	assert.Equal(t, []string{clientKeyTheme}, cfg.sets)

	assert.Equal(t, stateLoaded, m.settings.state)
	assert.Zero(t, m.settings.dirtyCount(), "the re-read settles every saved row")
	assert.Equal(t, "saved 3 settings", m.toast)
	assert.Equal(t, buttonSave, cursorKey(m), "a save leaves the cursor where it was")

	assert.Equal(t, themeDracula, m.deps.Theme)
	assert.Equal(t, themeFor(themeDracula, true).Accent, m.styles.Title.GetForeground(), "the saved theme applies live")
}

// TestSettingsNothingToSave checks Save with no changes sends nothing.
func TestSettingsNothingToSave(t *testing.T) {
	stub, srv := newSessionStub(t)

	m, _ := liveSettingsModel(t, srv, 120, 40)

	m, cmd := pressSave(t, m)

	assert.Nil(t, cmd)
	assert.Equal(t, "nothing to save", m.toast)
	assert.Nil(t, stub.settingSetArgs(t))
}

// TestSettingsSaveThenLeave checks esc → y saves, then leaves once the save has
// succeeded.
func TestSettingsSaveThenLeave(t *testing.T) {
	_, srv := newSessionStub(t)

	m, _ := liveSettingsModel(t, srv, 120, 40)
	m = press(networkTab(m), "tab", "enter", "esc")

	m, cmd := pressCmd(m, "y")
	require.Equal(t, windowSettings, m.window, "the window stays until the save lands")

	m = run(t, m, cmd)

	assert.Equal(t, windowTorrents, m.window)
	assert.Zero(t, m.settings.dirtyCount())
}

// TestSettingsSaveThenQuit checks q → y saves, then quits.
func TestSettingsSaveThenQuit(t *testing.T) {
	_, srv := newSessionStub(t)

	m, _ := liveSettingsModel(t, srv, 120, 40)
	m = press(m, "down", "q")

	m, cmd := pressCmd(m, "y")

	_, next := runCmd(t, m, cmd)
	require.NotNil(t, next)

	_, quit := next().(tea.QuitMsg)
	assert.True(t, quit)
}

// TestSettingsClientSaveFailureKeepsTheRowDirty checks a failed client write
// marks its row, keeps it unsaved, and cancels the prompt's follow-up.
func TestSettingsClientSaveFailureKeepsTheRowDirty(t *testing.T) {
	_, srv := newSessionStub(t)

	m, cfg := liveSettingsModel(t, srv, 120, 40)
	cfg.failKey = clientKeyTheme

	m = press(m, "down", "esc")

	m, cmd := pressCmd(m, "y")
	m = run(t, m, cmd)

	assert.Equal(t, windowSettings, m.window, "a failed save must not leave")
	assert.Equal(t, 1, m.settings.dirtyCount())
	assert.Contains(t, m.settings.tabs[tabGeneral].fields[0].err, "disk full")
	assert.Contains(t, m.toast, "save failed")
	assert.Equal(t, "default", m.deps.Theme, "an unsaved theme must not be applied")
}

// TestSettingsSessionSaveFailureStays checks an RPC error on session-set keeps
// the change and reports it.
func TestSettingsSessionSaveFailureStays(t *testing.T) {
	stub, srv := newSessionStub(t)

	m, _ := liveSettingsModel(t, srv, 120, 40)
	stub.failSet.Store(true)

	m = press(networkTab(m), "tab", "enter")

	m, cmd := pressSave(t, m)
	m = run(t, m, cmd)

	assert.Equal(t, 1, m.settings.dirtyCount())
	require.Error(t, m.settings.saveErr)
	assert.Contains(t, m.toast, "save failed: server error")
	assert.Contains(t, ansi.Strip(m.render()), "save failed: server error")
}

// TestSettingsLoadErrors checks a failed load names what to check, and that R
// retries it.
func TestSettingsLoadErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   string
	}{
		{name: "unreachable", status: http.StatusInternalServerError, want: "server unreachable"},
		{name: "credentials", status: http.StatusUnauthorized, want: "authentication failed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub, srv := newSessionStub(t)
			stub.status.Store(int32(tc.status))

			m, _ := liveSettingsModel(t, srv, 120, 40)

			require.Equal(t, stateError, m.settings.state)

			frame := ansi.Strip(networkTab(m).render())
			assert.Contains(t, frame, tc.want)
			assert.Contains(t, frame, "press R to retry")

			stub.status.Store(0)

			m, cmd := pressCmd(m, "R")
			require.Equal(t, stateLoading, m.settings.state)

			m = run(t, m, cmd)
			assert.Equal(t, stateLoaded, m.settings.state)
		})
	}
}

// TestSettingsErrorGolden pins the failed-load state of a server tab.
func TestSettingsErrorGolden(t *testing.T) {
	stub, srv := newSessionStub(t)
	stub.status.Store(http.StatusInternalServerError)

	m, _ := liveSettingsModel(t, srv, 120, 40)

	assertGolden(t, "settings_error", networkTab(m).render())
}

// TestSettingsRefresh checks R reloads at once when clean, and asks first when
// a reload would discard changes.
func TestSettingsRefresh(t *testing.T) {
	_, srv := newSessionStub(t)

	m, _ := liveSettingsModel(t, srv, 120, 40)

	clean, cmd := pressCmd(m, "R")
	assert.Equal(t, stateLoading, clean.settings.state)
	assert.NotNil(t, cmd)

	dirty := press(networkTab(m), "tab", "enter")

	prompted, cmd := pressCmd(dirty, "R")
	assert.Nil(t, cmd)
	require.True(t, prompted.hasOverlay(confirmOverlayKind))
	assert.Contains(t, ansi.Strip(prompted.render()), "Discard 1 unsaved change and reload?")

	declined := press(prompted, "n")
	assert.Equal(t, 1, declined.settings.dirtyCount())

	reloaded, cmd := pressCmd(prompted, "y")
	assert.Zero(t, reloaded.settings.dirtyCount())
	assert.Equal(t, stateLoading, reloaded.settings.state)

	assert.Equal(t, stateLoaded, run(t, reloaded, cmd).settings.state)
}

// TestRefreshOutsideSettingsIsNotYetBuilt checks R in the torrents window still
// reports itself until T4's poll exists.
func TestRefreshOutsideSettingsIsNotYetBuilt(t *testing.T) {
	m := press(newTestModel(120, 40), "R")

	assert.Contains(t, m.toast, "not implemented yet")
}

// TestSettingsClientOnlySaveWithoutAServer checks a window with no server saves
// client settings without touching the network, and applies them live.
func TestSettingsClientOnlySaveWithoutAServer(t *testing.T) {
	cfg := newFakeConfig()

	deps := testDeps()
	deps.Config = cfg

	m := press(New(deps).applyTheme(true).resize(120, 40), ",")
	require.True(t, m.settings.noServer)

	m = press(m, "tab", "enter")
	m = editField(press(m, "]"), "30s")

	m, cmd := pressSave(t, m)
	m = run(t, m, cmd)

	assert.Equal(t, []string{clientKeyNerdFont, clientKeyPollInterval}, cfg.sets)
	assert.False(t, m.deps.NerdFont, "the store's default was on, and the saved toggle turned it off")
	assert.Equal(t, newIcons(false).Marked, m.icons.Marked, "the icon set follows the saved toggle")
	assert.Equal(t, "30s", m.deps.PollInterval.String())
	assert.Zero(t, m.settings.dirtyCount())
}

// TestStaleSettingsRepliesAreDropped checks a reply the window is not waiting on
// changes nothing.
func TestStaleSettingsRepliesAreDropped(t *testing.T) {
	m := settingsModel(120, 40)

	got, _ := m.update(settingsLoadedMsg{session: transmission.SessionSettings{PeerPort: 1}})
	got, _ = got.update(settingsSavedMsg{clientWrote: []string{clientKeyTheme}})

	peer, _ := got.settings.fieldValue("peer-port")
	assert.Equal(t, 31129, peer)
	assert.Empty(t, got.toast)
}

// TestErrorLineCoversTheSentinels checks each backend sentinel gets its own
// sentence, and that other text is sanitized.
func TestErrorLineCoversTheSentinels(t *testing.T) {
	assert.Contains(t, errorLine(transmission.ErrAuth), "authentication failed")
	assert.Contains(t, errorLine(transmission.ErrUnreachable), "server unreachable")
	assert.Contains(t, errorLine(transmission.ErrUnsupported), "Transmission 4.0")
	assert.Contains(t, errorLine(transmission.ErrServer), "server error")
	assert.NotContains(t, errorLine(io.ErrUnexpectedEOF), "server")
	assert.NotContains(t, errorLine(&hostileError{}), "\x1b")
	assert.Empty(t, errorLine(nil))
}

// hostileError is an error whose text carries an escape sequence.
type hostileError struct{}

func (*hostileError) Error() string { return "boom\x1b[2J" }
