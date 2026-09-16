package tui

import (
	"errors"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
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
	deps.Dial = func(string) (DialResult, error) {
		dialed = true

		return DialResult{}, nil
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
	deps.Dial = func(string) (DialResult, error) { return DialResult{}, want }

	err := Run(t.Context(), deps)

	require.Error(t, err)
	assert.ErrorIs(t, err, want)
}

// TestNewWithoutTorrentsIsEmpty checks there are no mock rows in production: a
// model with no static set starts empty and says why, and a supplied set is used
// as given.
func TestNewWithoutTorrentsIsEmpty(t *testing.T) {
	deps := testDeps()
	deps.Torrents = nil

	m := New(deps).applyTheme(true).resize(120, 40)

	assert.Empty(t, m.torrents.rows)
	assert.Contains(t, ansi.Strip(m.render()), noServerLabel)

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

// TestNewDefaultsToATrueColorProfile checks the model renders in color before
// bubbletea has reported the detected profile — the same trade isDark makes,
// and the reason a color-capable terminal never flashes a monochrome frame.
func TestNewDefaultsToATrueColorProfile(t *testing.T) {
	m := New(testDeps())

	assert.Equal(t, colorprofile.TrueColor, m.profile)
	assert.False(t, monochrome(m.profile))

	deps := testDeps()
	deps.ColorDepth = colorDepth16

	assert.Equal(t, colorprofile.ANSI, New(deps).profile, "a configured depth starts the model at its profile")
}

// TestProgramOptionsForcesAsciiUnderNoColor checks the NO_COLOR branch of the
// program options without starting a program, which this suite has no terminal
// to do.
//
// The options are opaque closures over a *tea.Program, so their count is the
// only observable; what the extra one does is asserted where it lands, on the
// model, in TestColorProfileMsgRethemes.
func TestProgramOptionsForcesAsciiUnderNoColor(t *testing.T) {
	deps := testDeps()

	plain := programOptions(t.Context(), deps)

	deps.NoColor = true

	assert.Len(t, programOptions(t.Context(), deps), len(plain)+1)

	deps.NoColor = false
	deps.ColorDepth = colorDepth16

	assert.Len(t, programOptions(t.Context(), deps), len(plain), "a color depth is applied by the model, not a program option")
}

// TestViewIsAnAltScreen pins the two view fields that are program options in
// bubbletea v1 and struct fields in v2.
func TestViewIsAnAltScreen(t *testing.T) {
	v := newTestModel(120, 40).View()

	assert.True(t, v.AltScreen)
	assert.Equal(t, "snatcher", v.WindowTitle)
	assert.NotEmpty(t, v.Content)
}

// TestStatusBarShowsTheAlias checks the status bar reports the alias alone:
// neither the host beside it nor a full RPC URL, whose path is identical on
// every Transmission server.
func TestStatusBarShowsTheAlias(t *testing.T) {
	m := newTestModel(120, 40)
	m.serverURL = "http://seedbox.example:9091/transmission/rpc"
	m.serverName = "home"

	bar := m.statusBarView(120)

	assert.Contains(t, bar, "home")
	assert.NotContains(t, bar, "seedbox.example")
	assert.NotContains(t, bar, "/transmission/rpc")
}
