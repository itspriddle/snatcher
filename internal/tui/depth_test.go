package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEffectiveProfile(t *testing.T) {
	tests := []struct {
		depth    string
		detected colorprofile.Profile
		want     colorprofile.Profile
	}{
		{depth: "", detected: colorprofile.TrueColor, want: colorprofile.TrueColor},
		{depth: colorDepthAuto, detected: colorprofile.ANSI256, want: colorprofile.ANSI256},
		{depth: "bogus", detected: colorprofile.ANSI, want: colorprofile.ANSI},
		{depth: colorDepth16, detected: colorprofile.TrueColor, want: colorprofile.ANSI},
		{depth: colorDepth256, detected: colorprofile.TrueColor, want: colorprofile.ANSI256},
		{depth: colorDepthTrueColor, detected: colorprofile.ANSI256, want: colorprofile.TrueColor},
		{depth: colorDepthTrueColor, detected: colorprofile.ANSI, want: colorprofile.TrueColor},
		{depth: colorDepthTrueColor, detected: colorprofile.Ascii, want: colorprofile.Ascii},
		{depth: colorDepth16, detected: colorprofile.NoTTY, want: colorprofile.NoTTY},
		{depth: colorDepth16, detected: colorprofile.Unknown, want: colorprofile.Unknown},
	}

	for _, tt := range tests {
		assert.Equalf(t, tt.want, effectiveProfile(tt.depth, tt.detected), "depth=%q detected=%s", tt.depth, tt.detected)
	}
}

// TestColorDepthStartsTheModelAtItsProfile checks the first frame is already
// drawn at the configured depth, before bubbletea reports anything.
func TestColorDepthStartsTheModelAtItsProfile(t *testing.T) {
	deps := testDeps()
	deps.ColorDepth = colorDepth16
	deps.Theme = "solarized"

	m := New(deps)

	assert.Equal(t, colorprofile.ANSI, m.profile)
	assert.Equal(t, lipgloss.Blue, m.styles.Title.GetForeground())
}

// TestColorDepthOverrideRoundTrip checks a detection the depth overrides is
// sent back to the program, and that the echo is recognized rather than
// recorded as a detection.
func TestColorDepthOverrideRoundTrip(t *testing.T) {
	deps := testDeps()
	deps.ColorDepth = colorDepth16

	m, cmd := New(deps).update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})

	assert.Equal(t, colorprofile.TrueColor, m.detected)
	assert.Equal(t, colorprofile.ANSI, m.profile)
	require.NotNil(t, cmd, "the renderer must be told to draw at the configured depth")

	echo := cmd()
	require.Equal(t, tea.ColorProfileMsg{Profile: colorprofile.ANSI}, echo)

	// A capability reply upgrading the detection while the echo is in flight
	// does not send a second override.
	m, again := m.update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
	assert.Nil(t, again)
	assert.Equal(t, colorprofile.ANSI, m.profile)

	m, cmd = m.update(echo)

	assert.Nil(t, cmd, "an echo is not answered")
	assert.Equal(t, colorprofile.TrueColor, m.detected, "an echo is not a detection")
	assert.Equal(t, colorprofile.ANSI, m.profile)
	assert.Equal(t, colorprofile.Unknown, m.forcedProfile)
}

// TestColorDepthNeverUpgradesMonochrome checks NO_COLOR's forced Ascii
// profile wins over any depth.
func TestColorDepthNeverUpgradesMonochrome(t *testing.T) {
	deps := testDeps()
	deps.ColorDepth = colorDepthTrueColor

	m, cmd := New(deps).update(tea.ColorProfileMsg{Profile: colorprofile.Ascii})

	assert.Nil(t, cmd)
	assert.Equal(t, colorprofile.Ascii, m.profile)
	assert.NotRegexp(t, colorPattern, m.resize(120, 40).render())
}

// TestSavedColorDepthAppliesLive checks a color depth saved in the settings
// window re-profiles the model and the renderer without a restart, and that
// going back to auto restores the detected profile.
func TestSavedColorDepthAppliesLive(t *testing.T) {
	deps := testDeps()
	deps.Config = newFakeConfig()
	deps.Theme = "solarized"

	m := press(New(deps).applyTheme(true).resize(120, 40), ",")
	m = press(m, "tab", "tab")
	require.Equal(t, clientKeyColorDepth, cursorKey(m))

	m = press(m, "up")
	require.Equal(t, colorDepth16, m.settings.current().value)

	m, cmd := pressSave(t, m)
	m, cmd = runCmd(t, m, cmd)

	assert.Equal(t, colorDepth16, m.deps.ColorDepth)
	assert.Equal(t, colorprofile.ANSI, m.profile)
	assert.Equal(t, lipgloss.Blue, m.styles.Title.GetForeground(), "the [ansi] section applies live")

	m, cmd = runCmd(t, m, cmd)
	assert.Nil(t, cmd)
	assert.Equal(t, colorprofile.Unknown, m.forcedProfile, "the echo cleared the override")

	for cursorKey(m) != clientKeyColorDepth {
		m = press(m, "tab")
	}

	m = press(m, "down")
	require.Equal(t, colorDepthAuto, m.settings.current().value)

	m, cmd = pressSave(t, m)
	m, cmd = runCmd(t, m, cmd)

	assert.Equal(t, colorprofile.TrueColor, m.profile)
	assert.Equal(t, lipgloss.Color("#268bd2"), m.styles.Title.GetForeground())
	require.NotNil(t, cmd)
	assert.Equal(t, tea.ColorProfileMsg{Profile: colorprofile.TrueColor}, cmd())
}
