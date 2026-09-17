package theme

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuiltinsParse checks every embedded file parses and the set is exactly the
// four names --theme has always accepted. Builtins panics on a broken file, so
// this is what keeps that panic unreachable.
func TestBuiltinsParse(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"default", "dracula", "nord", "solarized"}, Builtins().Names())
	assert.Empty(t, Builtins().Warnings())
}

// TestBuiltinSectionsAreComplete checks the sections every fallback chain ends
// in set every slot, so no builtin leans on the default by accident.
func TestBuiltinSectionsAreComplete(t *testing.T) {
	t.Parallel()

	complete := map[string][]string{
		"default":   {SectionANSI},
		"dracula":   {SectionDark, SectionLight, SectionANSI},
		"nord":      {SectionDark, SectionLight, SectionANSI},
		"solarized": {SectionDark, SectionLight, SectionANSI},
	}

	for name, sections := range complete {
		spec, err := Builtins().Lookup(name)
		require.NoError(t, err)

		for _, section := range sections {
			for _, slot := range Slots {
				_, ok := spec.Color(section, slot)
				assert.Truef(t, ok, "%s [%s] does not set %s", name, section, slot)
			}
		}
	}
}

// pinned is the truecolor palette each fixed theme drew when themes were Go
// functions, copied from internal/tui/theme.go before those were deleted. The
// port to files was verbatim, and this is what says so.
var pinned = map[string]map[bool]Theme{
	"dracula": {
		true: {
			Text: lipgloss.Color("#f8f8f2"), Muted: lipgloss.Color("#6272a4"), Accent: lipgloss.Color("#bd93f9"),
			Loud: lipgloss.Color("#ff5555"), Cursor: lipgloss.Color("#44475a"), CursorText: lipgloss.Color("#f8f8f2"),
			Border: lipgloss.Color("#6272a4"), StatusBar: lipgloss.Color("#44475a"), StatusText: lipgloss.Color("#f8f8f2"),
			Warn:    lipgloss.Color("#ffb86c"),
			IconAll: lipgloss.Color("#f8f8f2"), IconDownloading: lipgloss.Color("#8be9fd"), IconCompleted: lipgloss.Color("#50fa7b"),
			IconActive: lipgloss.Color("#bd93f9"), IconInactive: lipgloss.Color("#f8f8f2"), IconStopped: lipgloss.Color("#6272a4"),
			IconError: lipgloss.Color("#ff5555"), IconWaiting: lipgloss.Color("#f1fa8c"),
		},
		false: {
			Text: lipgloss.Color("#1f1f1f"), Muted: lipgloss.Color("#6c664b"), Accent: lipgloss.Color("#644ac9"),
			Loud: lipgloss.Color("#cb3a2a"), Cursor: lipgloss.Color("#cfcfde"), CursorText: lipgloss.Color("#1f1f1f"),
			Border: lipgloss.Color("#6c664b"), StatusBar: lipgloss.Color("#cfcfde"), StatusText: lipgloss.Color("#1f1f1f"),
			Warn:    lipgloss.Color("#a34d14"),
			IconAll: lipgloss.Color("#1f1f1f"), IconDownloading: lipgloss.Color("#036a96"), IconCompleted: lipgloss.Color("#14710a"),
			IconActive: lipgloss.Color("#644ac9"), IconInactive: lipgloss.Color("#1f1f1f"), IconStopped: lipgloss.Color("#6c664b"),
			IconError: lipgloss.Color("#cb3a2a"), IconWaiting: lipgloss.Color("#846e15"),
		},
	},
	"nord": {
		true: {
			Text: lipgloss.Color("#d8dee9"), Muted: lipgloss.Color("#4c566a"), Accent: lipgloss.Color("#88c0d0"),
			Loud: lipgloss.Color("#bf616a"), Cursor: lipgloss.Color("#434c5e"), CursorText: lipgloss.Color("#eceff4"),
			Border: lipgloss.Color("#4c566a"), StatusBar: lipgloss.Color("#3b4252"), StatusText: lipgloss.Color("#d8dee9"),
			Warn:    lipgloss.Color("#ebcb8b"),
			IconAll: lipgloss.Color("#d8dee9"), IconDownloading: lipgloss.Color("#88c0d0"), IconCompleted: lipgloss.Color("#a3be8c"),
			IconActive: lipgloss.Color("#81a1c1"), IconInactive: lipgloss.Color("#d8dee9"), IconStopped: lipgloss.Color("#4c566a"),
			IconError: lipgloss.Color("#bf616a"), IconWaiting: lipgloss.Color("#ebcb8b"),
		},
		false: {
			Text: lipgloss.Color("#2e3440"), Muted: lipgloss.Color("#4c566a"), Accent: lipgloss.Color("#5e81ac"),
			Loud: lipgloss.Color("#bf616a"), Cursor: lipgloss.Color("#d8dee9"), CursorText: lipgloss.Color("#2e3440"),
			Border: lipgloss.Color("#4c566a"), StatusBar: lipgloss.Color("#e5e9f0"), StatusText: lipgloss.Color("#2e3440"),
			Warn:    lipgloss.Color("#d08770"),
			IconAll: lipgloss.Color("#2e3440"), IconDownloading: lipgloss.Color("#5e81ac"), IconCompleted: lipgloss.Color("#a3be8c"),
			IconActive: lipgloss.Color("#81a1c1"), IconInactive: lipgloss.Color("#2e3440"), IconStopped: lipgloss.Color("#4c566a"),
			IconError: lipgloss.Color("#bf616a"), IconWaiting: lipgloss.Color("#d08770"),
		},
	},
	"solarized": {
		true: {
			Text: lipgloss.Color("#93a1a1"), Muted: lipgloss.Color("#657b83"), Accent: lipgloss.Color("#268bd2"),
			Loud: lipgloss.Color("#dc322f"), Cursor: lipgloss.Color("#073642"), CursorText: lipgloss.Color("#eee8d5"),
			Border: lipgloss.Color("#586e75"), StatusBar: lipgloss.Color("#073642"), StatusText: lipgloss.Color("#93a1a1"),
			Warn:    lipgloss.Color("#b58900"),
			IconAll: lipgloss.Color("#93a1a1"), IconDownloading: lipgloss.Color("#2aa198"), IconCompleted: lipgloss.Color("#859900"),
			IconActive: lipgloss.Color("#268bd2"), IconInactive: lipgloss.Color("#93a1a1"), IconStopped: lipgloss.Color("#586e75"),
			IconError: lipgloss.Color("#dc322f"), IconWaiting: lipgloss.Color("#b58900"),
		},
		false: {
			Text: lipgloss.Color("#586e75"), Muted: lipgloss.Color("#839496"), Accent: lipgloss.Color("#268bd2"),
			Loud: lipgloss.Color("#dc322f"), Cursor: lipgloss.Color("#eee8d5"), CursorText: lipgloss.Color("#073642"),
			Border: lipgloss.Color("#93a1a1"), StatusBar: lipgloss.Color("#eee8d5"), StatusText: lipgloss.Color("#586e75"),
			Warn:    lipgloss.Color("#cb4b16"),
			IconAll: lipgloss.Color("#586e75"), IconDownloading: lipgloss.Color("#2aa198"), IconCompleted: lipgloss.Color("#859900"),
			IconActive: lipgloss.Color("#268bd2"), IconInactive: lipgloss.Color("#586e75"), IconStopped: lipgloss.Color("#93a1a1"),
			IconError: lipgloss.Color("#dc322f"), IconWaiting: lipgloss.Color("#b58900"),
		},
	},
}

// TestBuiltinPalettesArePinned compares each fixed theme's truecolor resolution
// against the palette it drew as Go code.
func TestBuiltinPalettesArePinned(t *testing.T) {
	t.Parallel()

	for name, byBackground := range pinned {
		spec, err := Builtins().Lookup(name)
		require.NoError(t, err)

		for isDark, want := range byBackground {
			assert.Equalf(t, want, spec.Resolve(colorprofile.TrueColor, isDark), "%s isDark=%v", name, isDark)
		}
	}
}

// TestFixedThemesDifferBetweenLightAndDark checks the fixed themes ship both
// arrangements, which is what stops a light terminal from rendering Dracula's
// near-white text on its own near-white background.
func TestFixedThemesDifferBetweenLightAndDark(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"dracula", "nord", "solarized"} {
		spec, err := Builtins().Lookup(name)
		require.NoError(t, err)

		assert.NotEqualf(t, spec.Resolve(colorprofile.TrueColor, true), spec.Resolve(colorprofile.TrueColor, false),
			"%q has one arrangement for both backgrounds", name)
	}
}

// TestEveryBuiltinIsADistinctPalette checks the names actually differ, under
// truecolor and under sixteen colors.
func TestEveryBuiltinIsADistinctPalette(t *testing.T) {
	t.Parallel()

	for _, p := range []colorprofile.Profile{colorprofile.TrueColor, colorprofile.ANSI} {
		for _, isDark := range []bool{false, true} {
			seen := map[Theme]string{}

			for _, name := range Builtins().Names() {
				spec, err := Builtins().Lookup(name)
				require.NoError(t, err)

				got := spec.Resolve(p, isDark)

				other, dup := seen[got]
				require.Falsef(t, dup, "%s isDark=%v: %q and %q are the same palette", p, isDark, other, name)

				seen[got] = name
			}
		}
	}
}

// TestDefaultIsTheTerminalPalette pins the default theme as the sixteen colors
// the terminal already resolves for every other program: identical under every
// profile and background, with text left to the terminal's own foreground.
func TestDefaultIsTheTerminalPalette(t *testing.T) {
	t.Parallel()

	want := Default().Resolve(colorprofile.TrueColor, true)

	for _, p := range []colorprofile.Profile{colorprofile.TrueColor, colorprofile.ANSI256, colorprofile.ANSI, colorprofile.Ascii} {
		for _, isDark := range []bool{false, true} {
			assert.Equalf(t, want, Default().Resolve(p, isDark), "%s isDark=%v", p, isDark)
		}
	}

	assert.Equal(t, lipgloss.NoColor{}, want.Text, "text must be the terminal's configured foreground")

	for name, c := range map[string]color.Color{
		"Muted": want.Muted, "Accent": want.Accent, "Loud": want.Loud,
		"Cursor": want.Cursor, "CursorText": want.CursorText, "Border": want.Border,
		"StatusBar": want.StatusBar, "StatusText": want.StatusText, "Warn": want.Warn,
	} {
		_, ok := c.(ansi.BasicColor)

		assert.Truef(t, ok, "%s is %T, not one of the terminal's sixteen colors", name, c)
	}
}
