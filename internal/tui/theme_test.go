package tui

import (
	"errors"
	"image/color"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/theme"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// themeNames is every builtin theme name.
var themeNames = theme.Builtins().Names()

// themeFor is a builtin theme resolved for truecolor, the way the model
// resolves one before bubbletea has reported a profile.
func themeFor(t *testing.T, name string, isDark bool) theme.Theme {
	t.Helper()

	spec, err := theme.Builtins().Lookup(name)
	require.NoError(t, err)

	return spec.Resolve(colorprofile.TrueColor, isDark)
}

// TestANSIProfileSelectsTheANSIVariant checks a sixteen-color profile draws the
// theme's curated [ansi] section rather than a downsample of its hex palette.
func TestANSIProfileSelectsTheANSIVariant(t *testing.T) {
	deps := testDeps()
	deps.Theme = "solarized"

	m := New(deps)
	_, isHex := m.styles.Title.GetForeground().(color.RGBA)
	require.True(t, isHex, "truecolor draws the hex palette")

	m, _ = m.update(tea.ColorProfileMsg{Profile: colorprofile.ANSI})

	assert.Equal(t, lipgloss.Blue, m.styles.Title.GetForeground())
	_, isBasic := m.styles.Title.GetForeground().(ansi.BasicColor)
	assert.True(t, isBasic)
}

// TestUnknownThemeNameDrawsTheDefault checks a name the registry cannot answer
// draws rather than panicking or rendering an empty palette. The TUI must not
// fail to draw over a config value.
func TestUnknownThemeNameDrawsTheDefault(t *testing.T) {
	want := theme.Default().Resolve(colorprofile.TrueColor, true)

	for _, name := range []string{"", "DRACULA", "monokai"} {
		deps := testDeps()
		deps.Theme = name

		assert.Equal(t, want, New(deps).palette(true))
	}

	deps := testDeps()
	deps.Theme = "mine"
	deps.Themes = func(string) (theme.Spec, error) { return theme.Spec{}, errors.New("mine.theme:3: broken") }

	assert.Equal(t, want, New(deps).palette(true), "a theme file broken since launch draws the default")
}

// TestThemesSeamIsConsulted checks the model resolves names through
// Deps.Themes, which is how an operator's theme file reaches the TUI.
func TestThemesSeamIsConsulted(t *testing.T) {
	spec, err := theme.Parse("mine", "mine.theme", []byte("[dark]\naccent = #010203\n"))
	require.NoError(t, err)

	deps := testDeps()
	deps.Theme = "mine"
	deps.Themes = func(name string) (theme.Spec, error) {
		if name == "mine" {
			return spec, nil
		}

		return theme.Spec{}, theme.ErrUnknown
	}

	assert.Equal(t, lipgloss.Color("#010203"), New(deps).styles.Title.GetForeground())
}

// TestHelpStylesFollowTheTheme checks the help bubble is styled from the
// palette. T1 hardcoded help.DefaultStyles(true), which answered to neither the
// theme name nor the light/dark reply.
func TestHelpStylesFollowTheTheme(t *testing.T) {
	dracula := helpStyles(themeFor(t, "dracula", true), false)

	assert.Equal(t, themeFor(t, "dracula", true).Muted, dracula.ShortDesc.GetForeground())
	assert.NotEqual(t, helpStyles(themeFor(t, "nord", true), false).ShortDesc.GetForeground(), dracula.ShortDesc.GetForeground())
	assert.NotEqual(t, helpStyles(themeFor(t, "dracula", false), false).ShortKey.GetForeground(), dracula.ShortKey.GetForeground())
}

// TestFocusIsEncodedInTheBorderShape pins decision 3: focus is legible without
// color, so each of the three pane states gets its own border shape.
func TestFocusIsEncodedInTheBorderShape(t *testing.T) {
	styles := newStyles(themeFor(t, "default", true), false)

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

// TestEveryStatusHasAnIcon checks no status the enum names falls through to the
// "?" placeholder, in either spelling.
func TestEveryStatusHasAnIcon(t *testing.T) {
	for _, s := range allStatuses() {
		_, ok := iconForStatus(s)

		assert.Truef(t, ok, "no icon for status %d (%s)", s, s)
	}

	for _, nerdFont := range []bool{false, true} {
		assert.Equal(t, "?", newIcons(nerdFont).status(transmission.Status(255)))
		assert.Equal(t, "?", newIcons(nerdFont).statusCell(transmission.Status(255)))
	}
}

// TestStatusIconsMatchTheSidebar pins which sidebar icon a torrent row borrows.
func TestStatusIconsMatchTheSidebar(t *testing.T) {
	want := map[transmission.Status]statusIcon{
		transmission.StatusStopped:          iconStopped,
		transmission.StatusQueuedToVerify:   iconWaiting,
		transmission.StatusVerifying:        iconWaiting,
		transmission.StatusQueuedToDownload: iconWaiting,
		transmission.StatusDownloading:      iconDownloading,
		transmission.StatusQueuedToSeed:     iconWaiting,
		transmission.StatusSeeding:          iconCompleted,
		transmission.StatusError:            iconError,
	}

	for s, icon := range want {
		got, ok := iconForStatus(s)

		require.Truef(t, ok, "%s", s)
		assert.Equalf(t, icon, got, "%s", s)
	}
}

// TestEverySidebarStatusHasItsOwnIcon checks each Status row of the filter pane
// draws a distinct icon, and that every icon is used by one.
func TestEverySidebarStatusHasItsOwnIcon(t *testing.T) {
	seen := map[statusIcon]transmission.Filter{}

	for _, f := range statusFilters {
		icon, ok := iconForFilter(f)
		require.Truef(t, ok, "no icon for filter %s", f)

		prev, dup := seen[icon]
		assert.Falsef(t, dup, "%s and %s share an icon", prev, f)

		seen[icon] = f
	}

	assert.Len(t, seen, int(iconCount))

	_, ok := iconForFilter(transmission.FilterVerifying)
	assert.False(t, ok)
}

// TestPaintedIconsKeepTheRowBackground pins how an icon is colored: a
// foreground-only sequence closed by SGR 39, never a full reset, so the cursor
// row's background carries on past the glyph.
func TestPaintedIconsKeepTheRowBackground(t *testing.T) {
	icons := newIcons(false).painted(theme.Theme{IconError: lipgloss.Color("#dc322f")}, false)
	cell := icons.statusCell(transmission.StatusError)

	assert.Equal(t, "\x1b[38;2;220;50;47m!\x1b[39m", cell)
	assert.NotContains(t, cell, "\x1b[m")
	assert.NotContains(t, cell, "\x1b[0m")

	// A slot with no color, and a monochrome profile, draw the bare glyph.
	assert.Equal(t, "x", icons.statusCell(transmission.StatusStopped))

	mono := newIcons(false).painted(theme.Theme{IconError: lipgloss.Color("#dc322f")}, true)
	assert.Equal(t, "!", mono.statusCell(transmission.StatusError))
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

	for icon, glyph := range nerdFontIcons {
		glyphs[strconv.Itoa(icon)] = glyph
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

	for icon, glyph := range asciiIcons {
		glyphs[strconv.Itoa(icon)] = glyph
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

// TestMonochromeIsAProfileQuestion pins which profiles mean "no color". The two
// that do are the two ultraviolet strips color for; everything else, including
// the sixteen-color terminal the default theme is written for, renders in color.
func TestMonochromeIsAProfileQuestion(t *testing.T) {
	for _, p := range []colorprofile.Profile{colorprofile.Ascii, colorprofile.NoTTY} {
		assert.Truef(t, monochrome(p), "%s must render without color", p)
	}

	for _, p := range []colorprofile.Profile{colorprofile.ANSI, colorprofile.ANSI256, colorprofile.TrueColor} {
		assert.Falsef(t, monochrome(p), "%s can carry color", p)
	}
}

// sgrSeq matches one SGR escape sequence and captures its parameters.
var sgrSeq = regexp.MustCompile(`\x1b\[([\d;]*)m`)

// hasReverse reports whether s sets the reverse-video attribute, which is what
// carries the cursor row, the selected table row, and the status bar once color
// cannot. It walks the SGR parameters rather than matching a bare 7, because a
// truecolor argument such as 48;2;7;54;66 carries a 7 that is a color channel.
func hasReverse(s string) bool {
	for _, m := range sgrSeq.FindAllStringSubmatch(s, -1) {
		params := strings.Split(m[1], ";")

		for i := 0; i < len(params); i++ {
			switch params[i] {
			case "7":
				return true
			case "38", "48", "58":
				// Skip the color's own arguments: 5;n or 2;r;g;b.
				if i+1 < len(params) && params[i+1] == "5" {
					i += 2
				} else if i+1 < len(params) && params[i+1] == "2" {
					i += 4
				}
			}
		}
	}

	return false
}

// TestMonochromeStylesTradeColorForReverseVideo checks the three styles whose
// whole job was a background color still say something when they cannot set
// one, and that nothing in the monochrome set paints.
func TestMonochromeStylesTradeColorForReverseVideo(t *testing.T) {
	for _, name := range themeNames {
		palette := themeFor(t, name, true)
		mono := newStyles(palette, true)
		colored := newStyles(palette, false)

		for label, pair := range map[string][2]lipgloss.Style{
			"Cursor":         {mono.Cursor, colored.Cursor},
			"Table.Selected": {mono.Table.Selected, colored.Table.Selected},
			"StatusBar":      {mono.StatusBar, colored.StatusBar},
		} {
			t.Run(name+"/"+label, func(t *testing.T) {
				assert.Truef(t, hasReverse(pair[0].Render("x")), "monochrome must fall back to reverse video: %q", pair[0].Render("x"))
				assert.Falsef(t, hasReverse(pair[1].Render("x")), "%q: reverse video is the monochrome fallback, not a color style", pair[1].Render("x"))
			})
		}

		for label, style := range map[string]lipgloss.Style{
			"Pane": mono.Pane, "PaneFocused": mono.PaneFocused, "PaneLoud": mono.PaneLoud,
			"Title": mono.Title, "Tab": mono.Tab, "TabActive": mono.TabActive,
			"Cursor": mono.Cursor, "Muted": mono.Muted, "StatusBar": mono.StatusBar,
			"Toast": mono.Toast, "Overlay": mono.Overlay,
			"Table.Header": mono.Table.Header, "Table.Cell": mono.Table.Cell,
			"Help.ShortKey": mono.Help.ShortKey, "Help.ShortDesc": mono.Help.ShortDesc,
			"FieldCursor": mono.FieldCursor, "FieldDirty": mono.FieldDirty,
			"FieldReadOnly": mono.FieldReadOnly, "FieldError": mono.FieldError,
			"Button": mono.Button, "ButtonFocused": mono.ButtonFocused,
			"Input.Text": mono.Input.Focused.Text,
		} {
			t.Run(name+"/"+label+"/paints nothing", func(t *testing.T) {
				assert.NotRegexp(t, colorPattern, style.Render("x"))
			})
		}
	}
}

// TestMonochromeSettingsFramesPaintNothing extends the monochrome rule to the
// settings form, including the text input's cursor, whose component default is a
// hardcoded 256-color grey.
func TestMonochromeSettingsFramesPaintNothing(t *testing.T) {
	m := networkTab(settingsModel(120, 40))
	m, _ = m.update(tea.ColorProfileMsg{Profile: colorprofile.Ascii})

	frames := map[string]Model{
		"loaded":  m,
		"dirty":   press(m, "tab", "enter"),
		"editing": press(m, "enter"),
		"select":  press(m, "tab", "tab", "tab", "enter"),
		"confirm": press(m, "tab", "enter", "esc"),
	}

	for name, frame := range frames {
		t.Run(name, func(t *testing.T) {
			assert.NotRegexp(t, colorPattern, frame.render())
		})
	}
}

// TestMonochromeKeepsTheFocusBorderShapes is the reason focus needed no
// monochrome fallback of its own: the shape is the signal, and it is the same
// shape with and without color.
func TestMonochromeKeepsTheFocusBorderShapes(t *testing.T) {
	mono := newStyles(themeFor(t, "default", true), true)

	assert.Equal(t, lipgloss.RoundedBorder(), mono.Pane.GetBorderStyle())
	assert.Equal(t, lipgloss.ThickBorder(), mono.PaneFocused.GetBorderStyle())
	assert.Equal(t, lipgloss.DoubleBorder(), mono.PaneLoud.GetBorderStyle())
}
