package tui

import (
	"net/http"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/output"
)

// barText is the status bar as plain text, without its trailing padding.
func barText(m Model, width int) string {
	return strings.TrimRight(ansi.Strip(m.statusBarView(width)), " ")
}

// TestStatusBarWithoutAServer checks a model with nothing polled shows only what
// it knows.
func TestStatusBarWithoutAServer(t *testing.T) {
	assert.Equal(t, "snatcher v1.2.3 │ no server", barText(newTestModel(120, 40), 120))
}

// TestStatusBarLive checks every live segment appears once the spine has data.
func TestStatusBarLive(t *testing.T) {
	_, srv := newRPCStub(t)

	m := pollOnce(t, pollOnce(t, liveModel(t, srv, 200, 40)))
	bar := barText(m, 200)

	for _, want := range []string{
		"snatcher v1.2.3",
		"home@nas:9091",
		"Transmission 4.0.6",
		"↓ " + output.Speed(2097152) + " " + sparkline([]int64{2097152, 2097152}, sparkSamples),
		"↑ " + output.Speed(1048576),
		"free " + output.Bytes(128849018880),
		"used " + output.Bytes(1073741824+1073741824+104857600),
	} {
		assert.Contains(t, bar, want)
	}

	assert.NotContains(t, bar, "KB/s", "the fixture enforces no limit")
	assert.NotContains(t, bar, "alt-speed")
	assert.NotContains(t, bar, "!")
}

// TestStatusBarShowsTheLimitInForce checks a global limit shows only while
// enabled, and that alt-speed's limits replace it while alt-speed is on.
func TestStatusBarShowsTheLimitInForce(t *testing.T) {
	_, srv := newRPCStub(t)

	m := pollOnce(t, liveModel(t, srv, 200, 40))
	m.live.session.SpeedLimitDownEnabled = true
	m.live.session.SpeedLimitDown = 5000

	bar := barText(m, 200)
	assert.Contains(t, bar, "(5000 KB/s)")
	assert.Equal(t, 1, strings.Count(bar, "KB/s"), "the upload limit is not enabled")

	m.live.session.AltSpeedEnabled = true

	bar = barText(m, 200)
	assert.Equal(t, 2, strings.Count(bar, "(50 KB/s)"))
	assert.NotContains(t, bar, "5000")
	assert.Contains(t, bar, "alt-speed")
}

// TestStatusBarShowsTheError checks a failing server is named in the bar.
func TestStatusBarShowsTheError(t *testing.T) {
	stub, srv := newRPCStub(t)
	stub.status.Store(http.StatusInternalServerError)

	bar := barText(pollOnce(t, liveModel(t, srv, 120, 40)), 120)

	assert.Contains(t, bar, "! unreachable")
	assert.NotContains(t, bar, "Transmission")
}

// TestFitSegmentsDegradeOrder pins the order segments give way in: highest rank
// first, the least degraded of a rank before the rest, rightmost on a tie, and
// never a rank-zero segment.
func TestFitSegmentsDegradeOrder(t *testing.T) {
	segs := []barSegment{
		{forms: []string{"aaaa", ""}, rank: rankVersion},
		fixed("HOST"),
		{forms: []string{"d-full", "d-mid", "d", ""}, rank: rankRates},
		{forms: []string{"u-full", "u-mid", "u", ""}, rank: rankRates},
		{forms: []string{"used", ""}, rank: rankUsed},
	}

	tests := []struct {
		width int
		want  string
	}{
		{36, "aaaa │ HOST │ d-full │ u-full │ used"},
		{35, "aaaa │ HOST │ d-full │ u-full"},
		{28, "aaaa │ HOST │ d-full │ u-mid"},
		{27, "aaaa │ HOST │ d-mid │ u-mid"},
		{26, "aaaa │ HOST │ d-mid │ u"},
		{22, "aaaa │ HOST │ d │ u"},
		{18, "aaaa │ HOST │ d"},
		{14, "aaaa │ HOST"},
		{10, "HOST"},
		{3, "HOST"},
	}

	for _, tc := range tests {
		got := fitSegments(segs, tc.width)

		assert.Equalf(t, tc.want, got, "width %d", tc.width)
	}
}

// TestStatusBarDegradesAtNarrowWidths checks a live bar fits at 80 and 60
// columns, dropping the least important segments and never the server or an
// error.
func TestStatusBarDegradesAtNarrowWidths(t *testing.T) {
	_, srv := newRPCStub(t)

	m := pollOnce(t, pollOnce(t, liveModel(t, srv, 80, 24)))

	bar := barText(m, 80)
	assert.LessOrEqual(t, lipgloss.Width(bar), 80)
	assert.Contains(t, bar, "home@nas:9091")
	assert.NotContains(t, bar, "Transmission", "the server version is the first to go")

	stub, failingSrv := newRPCStub(t)

	m = pollOnce(t, liveModel(t, failingSrv, 60, 16))

	stub.status.Store(http.StatusInternalServerError)
	m = pollOnce(t, m)
	require.Error(t, m.live.err)

	bar = barText(m, 60)
	assert.LessOrEqual(t, lipgloss.Width(bar), 60)
	assert.Contains(t, bar, "home@nas:9091")
	assert.Contains(t, bar, "! unreachable")
}
