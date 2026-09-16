package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

// The client.color_depth values, spelled as the CLI spells them. internal/tui
// does not import internal/commands, so they are written out here, under a
// pinning test there.
const (
	colorDepthAuto      = "auto"
	colorDepthTrueColor = "truecolor"
	colorDepth256       = "256"
	colorDepth16        = "16"
)

// builtinColorDepths are the color depth choices, in the order the settings
// window offers them.
var builtinColorDepths = []string{colorDepthAuto, colorDepthTrueColor, colorDepth256, colorDepth16}

// effectiveProfile is the color profile to render with, for a configured depth
// and the profile bubbletea detected.
//
// A monochrome detection is never upgraded: NO_COLOR, TERM=dumb, and a pipe all
// arrive as one, and a depth setting is a statement about the terminal's
// palette, not a reason to paint where color was refused. Auto, and a value
// this package does not know, keep the detection.
func effectiveProfile(depth string, detected colorprofile.Profile) colorprofile.Profile {
	if monochrome(detected) || detected == colorprofile.Unknown {
		return detected
	}

	switch depth {
	case colorDepthTrueColor:
		return colorprofile.TrueColor
	case colorDepth256:
		return colorprofile.ANSI256
	case colorDepth16:
		return colorprofile.ANSI
	default:
		return detected
	}
}

// setProfile hands a profile to the program as a tea.ColorProfileMsg.
// bubbletea's event loop sets its renderer's profile from that message before
// Update sees it, so the one message re-themes the renderer's downsampling and
// the model's palette together. The model recognizes the message as its own
// echo by Model.forcedProfile.
func setProfile(p colorprofile.Profile) tea.Cmd {
	return func() tea.Msg { return tea.ColorProfileMsg{Profile: p} }
}

// profileReported handles a tea.ColorProfileMsg: bubbletea's detection, a later
// upgrade it made on a terminal capability reply, or the echo of setProfile.
//
// A detection is recorded, and when the configured depth overrides it, the
// override is sent back so the renderer draws at that depth too. One override
// is in flight at a time: a second detection while one is pending does not
// send another, so the one echo that arrives clears it.
func (m Model) profileReported(p colorprofile.Profile) (Model, tea.Cmd) {
	if m.forcedProfile != colorprofile.Unknown && p == m.forcedProfile {
		m.forcedProfile = colorprofile.Unknown
		m.profile = p

		return m.applyTheme(m.isDark), nil
	}

	m.detected = p
	want := effectiveProfile(m.deps.ColorDepth, p)
	m.profile = want
	m = m.applyTheme(m.isDark)

	if want == p || m.forcedProfile == want {
		return m, nil
	}

	m.forcedProfile = want

	return m, setProfile(want)
}

// applyColorDepth makes a saved client.color_depth take effect in this session,
// returning the command that re-profiles the renderer, or nil when the
// effective profile does not change.
func (m Model) applyColorDepth(depth string) (Model, tea.Cmd) {
	m.deps.ColorDepth = depth

	want := effectiveProfile(depth, m.detected)
	if want == m.profile {
		return m, nil
	}

	m.profile = want
	m.forcedProfile = want

	return m.applyTheme(m.isDark), setProfile(want)
}
