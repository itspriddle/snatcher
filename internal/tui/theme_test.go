package tui

import (
	"image/color"
	"testing"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// themeNames is every name --theme and client.theme accept, plus one that is
// neither, since themeFor must draw something for a name it does not know.
var themeNames = []string{"default", themeDracula, themeNord}

// TestEveryThemeNameIsADistinctPalette checks the three names actually differ.
//
// This is the assertion T1 could not make: all three names resolved to one
// placeholder palette, so "--theme nord" was accepted, stored, and resolved and
// then changed nothing a user could see.
func TestEveryThemeNameIsADistinctPalette(t *testing.T) {
	for _, isDark := range []bool{false, true} {
		seen := make(map[Theme]string, len(themeNames))

		for _, name := range themeNames {
			got := themeFor(name, isDark)

			other, dup := seen[got]
			require.Falsef(t, dup, "isDark=%v: %q and %q are the same palette", isDark, other, name)

			seen[got] = name
		}
	}
}

// TestNamedThemesDifferBetweenLightAndDark checks the two fixed palettes ship
// both arrangements, which is what stops a light terminal from rendering
// Dracula's near-white text on its own near-white background.
func TestNamedThemesDifferBetweenLightAndDark(t *testing.T) {
	for _, name := range []string{themeDracula, themeNord} {
		assert.NotEqualf(t, themeFor(name, true), themeFor(name, false), "%q has one arrangement for both backgrounds", name)
	}
}

// TestDefaultThemeIsTheTerminalPalette pins decision 1: the default theme is
// the sixteen colors the terminal already resolves for every other program,
// which is why it needs no light/dark arrangement of its own.
func TestDefaultThemeIsTheTerminalPalette(t *testing.T) {
	dark := themeFor("default", true)

	assert.Equal(t, dark, themeFor("default", false), "the terminal's own palette must not be re-picked per background")
	assert.Equal(t, lipgloss.NoColor{}, dark.Text, "text must be the terminal's configured foreground")

	for name, c := range map[string]color.Color{
		"Muted": dark.Muted, "Accent": dark.Accent, "Loud": dark.Loud,
		"Cursor": dark.Cursor, "CursorText": dark.CursorText, "Border": dark.Border,
		"StatusBar": dark.StatusBar, "StatusText": dark.StatusText, "Warn": dark.Warn,
	} {
		_, ok := c.(ansi.BasicColor)

		assert.Truef(t, ok, "%s is %T, not one of the terminal's sixteen colors", name, c)
	}
}

// TestUnknownThemeNameFallsBackToTheDefault checks a name the validator did not
// catch draws rather than panicking or rendering an empty palette. The TUI must
// not fail to draw over a config value.
func TestUnknownThemeNameFallsBackToTheDefault(t *testing.T) {
	for _, name := range []string{"", "DRACULA", "solarized"} {
		assert.Equal(t, themeFor("default", true), themeFor(name, true))
	}
}

// TestHelpStylesFollowTheTheme checks the help bubble is styled from the
// palette. T1 hardcoded help.DefaultStyles(true), which answered to neither the
// theme name nor the light/dark reply.
func TestHelpStylesFollowTheTheme(t *testing.T) {
	dracula := helpStyles(themeFor(themeDracula, true))

	assert.Equal(t, themeFor(themeDracula, true).Muted, dracula.ShortDesc.GetForeground())
	assert.NotEqual(t, helpStyles(themeFor(themeNord, true)).ShortDesc.GetForeground(), dracula.ShortDesc.GetForeground())
	assert.NotEqual(t, helpStyles(themeFor(themeDracula, false)).ShortKey.GetForeground(), dracula.ShortKey.GetForeground())
}

// TestFocusIsEncodedInTheBorderShape pins decision 3: focus is legible without
// color, so each of the three pane states gets its own border shape.
func TestFocusIsEncodedInTheBorderShape(t *testing.T) {
	styles := newStyles(themeFor("default", true))

	shapes := map[string]lipgloss.Border{
		"unfocused": styles.Pane.GetBorderStyle(),
		"focused":   styles.PaneFocused.GetBorderStyle(),
		"loud":      styles.PaneLoud.GetBorderStyle(),
	}

	assert.Equal(t, lipgloss.RoundedBorder(), shapes["unfocused"])
	assert.Equal(t, lipgloss.ThickBorder(), shapes["focused"])
	assert.Equal(t, lipgloss.DoubleBorder(), shapes["loud"])
}

// allStatuses is every transmission.Status the enum covers, so a status added
// upstream without a glyph fails here rather than rendering "?" in a user's
// list.
func allStatuses() []transmission.Status {
	var statuses []transmission.Status

	for s := transmission.StatusStopped; s <= transmission.StatusError; s++ {
		statuses = append(statuses, s)
	}

	return statuses
}

// TestBothGlyphSetsCoverEveryStatus checks neither spelling can fall through to
// the "?" placeholder for a status the enum names.
func TestBothGlyphSetsCoverEveryStatus(t *testing.T) {
	for _, nerdFont := range []bool{false, true} {
		icons := newIcons(nerdFont)

		for _, s := range allStatuses() {
			_, ok := icons.statuses[s]

			assert.Truef(t, ok, "nerdFont=%v has no glyph for status %d (%s)", nerdFont, s, s)
		}
	}

	assert.Equal(t, "?", newIcons(false).status(transmission.Status(255)))
	assert.Equal(t, "?", newIcons(true).status(transmission.Status(255)))
}

// TestNerdFontGlyphsAreOneCellPrivateUseRunes is the invariant the nerd golden
// cannot state: every Nerd Font glyph is a single private-use rune measuring
// exactly one cell.
//
// T1 shipped this map as eight empty strings — a zero-width column the golden
// faithfully pinned — so "the glyph column cannot move" is asserted on the
// glyphs themselves, not on a frame rendered from them.
func TestNerdFontGlyphsAreOneCellPrivateUseRunes(t *testing.T) {
	glyphs := map[string]string{"mark": nerdFontMark}

	for _, s := range allStatuses() {
		glyphs[s.String()] = nerdFontIcons[s]
	}

	for name, glyph := range glyphs {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, 1, utf8.RuneCountInString(glyph), "not a single rune")

			r, _ := utf8.DecodeRuneInString(glyph)

			assert.Truef(t, r >= 0xE000 && r <= 0xF8FF, "%U is outside the private use area U+E000–U+F8FF", r)
			assert.Equal(t, 1, lipgloss.Width(glyph), "does not measure one cell")
		})
	}
}

// TestASCIIGlyphsArePrintableASCII is the same invariant for the fallback set,
// which is what a terminal without a Nerd Font draws.
func TestASCIIGlyphsArePrintableASCII(t *testing.T) {
	glyphs := map[string]string{"mark": asciiMark}

	for _, s := range allStatuses() {
		glyphs[s.String()] = asciiIcons[s]
	}

	for name, glyph := range glyphs {
		t.Run(name, func(t *testing.T) {
			require.Len(t, glyph, 1, "not a single byte")

			assert.Truef(t, glyph[0] < utf8.RuneSelf && unicode.IsPrint(rune(glyph[0])), "%q is not printable ASCII", glyph)
			assert.Equal(t, 1, lipgloss.Width(glyph), "does not measure one cell")
		})
	}
}

// TestUnmarkedGlyphIsBlank pins the mark column's shape: both spellings draw a
// space when a row is unmarked, so marking a row cannot shift the columns right
// of it.
func TestUnmarkedGlyphIsBlank(t *testing.T) {
	for _, nerdFont := range []bool{false, true} {
		icons := newIcons(nerdFont)

		assert.Equal(t, " ", icons.mark(false))
		assert.Equal(t, 1, lipgloss.Width(icons.mark(true)))
	}
}
