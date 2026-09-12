package tui

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// TestNewNeverDials checks the constructor touches no network. It is what lets
// every other test in this package build a model, and it is also the property
// that keeps the dial where it belongs: above the alt screen, in Run.
func TestNewNeverDials(t *testing.T) {
	dialed := false

	deps := testDeps()
	deps.Dial = func(string) (*transmission.Client, string, error) {
		dialed = true

		return nil, "", nil
	}

	m := New(deps)

	assert.False(t, dialed)
	assert.Nil(t, m.client)
	assert.Empty(t, m.serverURL)
}

// TestRunReturnsTheDialErrorWithoutStartingAProgram pins the decision behind
// bare "snatcher" on an unconfigured machine: the CLI's own error reaches the
// terminal, and no alt screen is ever entered.
//
// If a program had started, this test would block on a terminal that is not
// there; it returning at all is part of the assertion.
func TestRunReturnsTheDialErrorWithoutStartingAProgram(t *testing.T) {
	want := errors.New("not configured: run 'snatcher server add' first")

	deps := testDeps()
	deps.Dial = func(string) (*transmission.Client, string, error) { return nil, "", want }

	err := Run(t.Context(), deps)

	require.Error(t, err)
	assert.ErrorIs(t, err, want)
}

// TestNewSeedsTheMockTorrents checks the T1 shell has something to render
// without a server, and that an explicitly supplied set is used as given — which
// is the seam T4's live poll arrives through.
func TestNewSeedsTheMockTorrents(t *testing.T) {
	assert.Len(t, New(testDeps()).torrents.rows, len(mockTorrents()))

	deps := testDeps()
	deps.Torrents = []transmission.Torrent{{ID: 7, Name: "only one"}}

	assert.Len(t, New(deps).torrents.rows, 1)
}

// TestNewIsUsableBeforeAnyResize checks the model renders before the terminal
// has reported its size. bubbletea sends the first WindowSizeMsg after Init, so
// a model that needed one would panic on its very first frame.
func TestNewIsUsableBeforeAnyResize(t *testing.T) {
	m := New(testDeps())

	require.True(t, m.layout().tooSmall)
	assert.NotPanics(t, func() { _ = m.render() })
}

// TestInitRequestsTheBackgroundColor checks light/dark detection is asked for
// rather than blocked on: the reply arrives as a message, and until it does the
// dark palette is used.
func TestInitRequestsTheBackgroundColor(t *testing.T) {
	m := New(testDeps())

	require.NotNil(t, m.Init())
	assert.True(t, m.isDark)
}

// TestViewIsAnAltScreen pins the two view fields that are program options in
// bubbletea v1 and struct fields in v2.
func TestViewIsAnAltScreen(t *testing.T) {
	v := newTestModel(120, 40).View()

	assert.True(t, v.AltScreen)
	assert.Equal(t, "snatcher", v.WindowTitle)
	assert.NotEmpty(t, v.Content)
}

// TestStatusBarShowsTheHost checks the status bar reports a host rather than a
// full RPC URL, whose path is identical on every Transmission server.
func TestStatusBarShowsTheHost(t *testing.T) {
	m := newTestModel(120, 40)
	m.serverURL = "http://seedbox.example:9091/transmission/rpc"

	bar := m.statusBarView(120)

	assert.Contains(t, bar, "seedbox.example:9091")
	assert.NotContains(t, bar, "/transmission/rpc")
}
