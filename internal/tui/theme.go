package tui

import (
	"image/color"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// Theme is the named color slots every style is built from. Lip Gloss v2 has no
// global renderer and no root adaptive color, so a theme is resolved once — for
// one theme name and one light/dark answer — and the resulting Styles are passed
// explicitly into every view function.
//
// Three palettes fill these slots, behind themeFor: default, dracula, and nord.
type Theme struct {
	// Text is the default foreground.
	Text color.Color

	// Muted is for secondary text: headers, inactive tabs, help.
	Muted color.Color

	// Accent is the focused-pane border and the active tab.
	Accent color.Color

	// Loud is the filter pane's focused border. Its job is blast radius: the
	// filter pane's group actions hit every torrent in a group, and vision.md
	// requires focus alone never be the only warning.
	Loud color.Color

	// Cursor is the background of the cursor row.
	Cursor color.Color

	// CursorText is the foreground on the cursor row.
	CursorText color.Color

	// Border is an unfocused pane's border.
	Border color.Color

	// StatusBar is the status bar's background.
	StatusBar color.Color

	// StatusText is the status bar's foreground.
	StatusText color.Color

	// Warn is a toast or a not-implemented-yet notice.
	Warn color.Color
}

// themeFor resolves a theme name and a light/dark answer into a palette.
//
// Every name ships both a light and a dark arrangement and goes through the
// same lipgloss.LightDark pick, so no theme can render invisible text on a
// terminal whose background it did not expect. An unknown name resolves to the
// default rather than failing, because the name was already validated where it
// was read (commands.ResolveClientSettings) and the TUI must not fail to draw
// over it.
func themeFor(name string, isDark bool) Theme {
	switch name {
	case themeDracula:
		return draculaTheme(isDark)
	case themeNord:
		return nordTheme(isDark)
	default:
		// "default", and any name the validator somehow let through.
		return defaultTheme()
	}
}

// The two named palettes, matching config's clientThemes. They are spelled here
// rather than imported because internal/tui imports neither internal/config nor
// internal/commands; the name arrives through Deps already validated. "default"
// needs no constant — it is themeFor's fallthrough.
const (
	themeDracula = "dracula"
	themeNord    = "nord"
)

// defaultTheme is the terminal's own ANSI-16 palette, identical for light and
// dark.
//
// Text is NoColor — the terminal's configured foreground — and every other slot
// is one of the sixteen colors the terminal already resolves to whatever the
// user's color scheme says. That is why it needs no light/dark pick: the
// terminal has already made both decisions, consistently with every other
// program the user runs. dracula and nord are the opt-in fixed looks, for a
// user who wants snatcher to look like snatcher.
func defaultTheme() Theme {
	return Theme{
		Text:       lipgloss.NoColor{},
		Muted:      lipgloss.BrightBlack,
		Accent:     lipgloss.Blue,
		Loud:       lipgloss.Red,
		Cursor:     lipgloss.Blue,
		CursorText: lipgloss.BrightWhite,
		Border:     lipgloss.BrightBlack,
		StatusBar:  lipgloss.BrightBlack,
		StatusText: lipgloss.BrightWhite,
		Warn:       lipgloss.Yellow,
	}
}

// draculaTheme is the Dracula palette, with Alucard — Dracula's own official
// light arrangement — as its light variant.
func draculaTheme(isDark bool) Theme {
	pick := lipgloss.LightDark(isDark)

	return Theme{
		Text:       pick(lipgloss.Color("#1f1f1f"), lipgloss.Color("#f8f8f2")),
		Muted:      pick(lipgloss.Color("#6c664b"), lipgloss.Color("#6272a4")),
		Accent:     pick(lipgloss.Color("#644ac9"), lipgloss.Color("#bd93f9")),
		Loud:       pick(lipgloss.Color("#cb3a2a"), lipgloss.Color("#ff5555")),
		Cursor:     pick(lipgloss.Color("#cfcfde"), lipgloss.Color("#44475a")),
		CursorText: pick(lipgloss.Color("#1f1f1f"), lipgloss.Color("#f8f8f2")),
		Border:     pick(lipgloss.Color("#6c664b"), lipgloss.Color("#6272a4")),
		StatusBar:  pick(lipgloss.Color("#cfcfde"), lipgloss.Color("#44475a")),
		StatusText: pick(lipgloss.Color("#1f1f1f"), lipgloss.Color("#f8f8f2")),
		Warn:       pick(lipgloss.Color("#a34d14"), lipgloss.Color("#ffb86c")),
	}
}

// nordTheme is the Nord palette. Its light variant follows Nord's own guidance
// for bright interfaces: Polar Night for text, Snow Storm for surfaces.
func nordTheme(isDark bool) Theme {
	pick := lipgloss.LightDark(isDark)

	return Theme{
		Text:       pick(lipgloss.Color("#2e3440"), lipgloss.Color("#d8dee9")),
		Muted:      lipgloss.Color("#4c566a"),
		Accent:     pick(lipgloss.Color("#5e81ac"), lipgloss.Color("#88c0d0")),
		Loud:       lipgloss.Color("#bf616a"),
		Cursor:     pick(lipgloss.Color("#d8dee9"), lipgloss.Color("#434c5e")),
		CursorText: pick(lipgloss.Color("#2e3440"), lipgloss.Color("#eceff4")),
		Border:     lipgloss.Color("#4c566a"),
		StatusBar:  pick(lipgloss.Color("#e5e9f0"), lipgloss.Color("#3b4252")),
		StatusText: pick(lipgloss.Color("#2e3440"), lipgloss.Color("#d8dee9")),
		Warn:       pick(lipgloss.Color("#d08770"), lipgloss.Color("#ebcb8b")),
	}
}

// Styles is every style the TUI draws with, built once from a Theme and held on
// the root model. Panes hold no styles of their own, so re-theming — a theme
// change, or the background-color reply arriving after the first frame — is a
// rebuild here plus one table.SetStyles, never a walk of the pane tree.
type Styles struct {
	// Pane is an unfocused pane's border box.
	Pane lipgloss.Style

	// PaneFocused is a focused pane's border box.
	PaneFocused lipgloss.Style

	// PaneLoud is a focused filter pane's border box: the group actions it
	// carries act on every torrent in a group, so its focus is styled louder
	// than any other pane's.
	PaneLoud lipgloss.Style

	// Title is a pane heading.
	Title lipgloss.Style

	// Tab and TabActive are the tab strip shared by the detail pane and the
	// settings window.
	Tab       lipgloss.Style
	TabActive lipgloss.Style

	// Cursor is the filter pane's selected row. The table draws its own.
	Cursor lipgloss.Style

	// Muted is secondary text: counts, placeholders, field labels.
	Muted lipgloss.Style

	// StatusBar is the bottom line.
	StatusBar lipgloss.Style

	// Toast is a transient message inside the status bar.
	Toast lipgloss.Style

	// Overlay is the box an overlay is drawn in.
	Overlay lipgloss.Style

	// Help styles the short-help line and the help overlay's columns.
	Help help.Styles

	// Table styles the torrent list.
	Table table.Styles

	// FieldCursor is the settings row under the cursor; FieldDirty a row with
	// an unsaved change; FieldReadOnly a row that cannot be edited; FieldError
	// the hint line when it carries an error. The cursor and dirty states are
	// also spelled in text ("> " and "*"), so they survive monochrome.
	FieldCursor   lipgloss.Style
	FieldDirty    lipgloss.Style
	FieldReadOnly lipgloss.Style
	FieldError    lipgloss.Style

	// Button and ButtonFocused are the settings window's Save / Revert row.
	Button        lipgloss.Style
	ButtonFocused lipgloss.Style

	// Input styles the settings window's text input.
	Input textinput.Styles
}

// monochrome reports whether a color profile can carry color at all.
//
// This is the one place the question is asked, and it is asked of the profile
// rather than of the environment: NO_COLOR, TERM=dumb, a pipe, and an explicit
// tea.WithColorProfile all arrive here as one value. ultraviolet already strips
// color from every style under these two profiles — Ascii keeps bold, reverse,
// and underline, NoTTY keeps nothing — so what is left for the TUI to decide is
// only what to say with the attributes that survive.
func monochrome(p colorprofile.Profile) bool {
	return p == colorprofile.Ascii || p == colorprofile.NoTTY
}

// newStyles builds every style from a theme. It is the only place a style is
// constructed, so "which color is this" is answerable by reading one function.
//
// mono is the monochrome switch the model derives from the terminal's color
// profile. Under it no style sets a color at all, and the three places color
// alone carried meaning — the cursor row, the table's selected row, and the
// status bar — switch to reverse video. Pane focus needs no fallback: it is a
// border shape either way.
func newStyles(t Theme, mono bool) Styles {
	// paint is where the monochrome branch lives, so every style below is
	// written once rather than twice. A nil color is skipped, which is how a
	// foreground-only style is spelled.
	paint := func(s lipgloss.Style, fg, bg color.Color) lipgloss.Style {
		if mono {
			return s
		}

		if fg != nil {
			s = s.Foreground(fg)
		}

		if bg != nil {
			s = s.Background(bg)
		}

		return s
	}

	pane := borderStyle(lipgloss.NewStyle(), lipgloss.RoundedBorder(), t.Border, mono)
	cursor := paint(lipgloss.NewStyle(), t.CursorText, t.Cursor).Reverse(mono)

	tbl := table.DefaultStyles()
	tbl.Header = paint(lipgloss.NewStyle().Bold(true), t.Muted, nil).PaddingRight(cellPadding)
	tbl.Cell = paint(lipgloss.NewStyle(), t.Text, nil).PaddingRight(cellPadding)
	tbl.Selected = cursor

	return Styles{
		Pane:        pane,
		PaneFocused: borderStyle(lipgloss.NewStyle(), lipgloss.ThickBorder(), t.Accent, mono),
		PaneLoud:    borderStyle(lipgloss.NewStyle(), lipgloss.DoubleBorder(), t.Loud, mono),
		Title:       paint(lipgloss.NewStyle().Bold(true), t.Accent, nil),
		Tab:         paint(lipgloss.NewStyle().Padding(0, 1), t.Muted, nil),
		TabActive:   paint(lipgloss.NewStyle().Bold(true).Padding(0, 1), t.Accent, nil).Underline(mono),
		Cursor:      cursor,
		Muted:       paint(lipgloss.NewStyle(), t.Muted, nil),
		StatusBar:   paint(lipgloss.NewStyle(), t.StatusText, t.StatusBar).Reverse(mono),
		Toast:       paint(lipgloss.NewStyle().Bold(true), t.Warn, nil),
		Overlay:     borderStyle(lipgloss.NewStyle().Padding(0, 1), lipgloss.RoundedBorder(), t.Accent, mono),
		Help:        helpStyles(t, mono),
		Table:       tbl,

		FieldCursor:   paint(lipgloss.NewStyle().Bold(true), t.Accent, nil),
		FieldDirty:    paint(lipgloss.NewStyle().Bold(true), t.Warn, nil),
		FieldReadOnly: paint(lipgloss.NewStyle(), t.Muted, nil),
		FieldError:    paint(lipgloss.NewStyle().Bold(true), t.Warn, nil),
		Button:        paint(lipgloss.NewStyle(), t.Text, nil),
		ButtonFocused: cursor.Bold(true),
		Input:         inputStyles(t, mono),
	}
}

// inputStyles builds the text input's styles from the theme, replacing
// textinput.DefaultStyles, whose hardcoded 256-color greys answer to neither the
// palette nor a monochrome profile.
//
// The virtual cursor is drawn in reverse video whatever its color, so under a
// monochrome profile it keeps no color at all and is still visible.
func inputStyles(t Theme, mono bool) textinput.Styles {
	text := lipgloss.NewStyle()
	placeholder := lipgloss.NewStyle()

	var cursorColor color.Color = lipgloss.NoColor{}

	if !mono {
		text = text.Foreground(t.Text)
		placeholder = placeholder.Foreground(t.Muted)
		cursorColor = t.Accent
	}

	state := textinput.StyleState{Text: text, Placeholder: placeholder, Suggestion: placeholder, Prompt: lipgloss.NewStyle()}

	return textinput.Styles{
		Focused: state,
		Blurred: state,
		Cursor:  textinput.CursorStyle{Color: cursorColor, Shape: tea.CursorBlock, Blink: true},
	}
}

// borderStyle gives s a border shape and, unless monochrome, a border color.
//
// The shape is the point. vision.md requires the filter pane's focus be louder
// than every other pane's, and T2 extends that to focus generally: an unfocused
// pane is rounded, a focused pane is thick, a focused filter pane is double.
// That reads without color, on a sixteen-color terminal and to a colorblind
// user — and it is the only part of focus the ANSI-stripped goldens can pin.
func borderStyle(s lipgloss.Style, b lipgloss.Border, c color.Color, mono bool) lipgloss.Style {
	s = s.Border(b)
	if mono {
		return s
	}

	return s.BorderForeground(c)
}

// helpStyles builds the help bubble's styles from the theme, replacing
// help.DefaultStyles, whose hardcoded greys answer to neither the palette nor
// the light/dark reply.
func helpStyles(t Theme, mono bool) help.Styles {
	key := lipgloss.NewStyle()
	desc := lipgloss.NewStyle()

	if mono {
		// Without color the key is what a reader's eye lands on, so it carries
		// the only emphasis left.
		key = key.Bold(true)
	} else {
		key = key.Foreground(t.Text)
		desc = desc.Foreground(t.Muted)
	}

	return help.Styles{
		ShortKey:       key,
		ShortDesc:      desc,
		ShortSeparator: desc,
		Ellipsis:       desc,
		FullKey:        key,
		FullDesc:       desc,
		FullSeparator:  desc,
	}
}

// Icons is the glyph set the UI draws status and marks with. It has two
// spellings — Nerd Font and ASCII — chosen by --no-nerd-font / client.nerd_font.
//
// Every glyph occupies exactly one terminal cell in both spellings, and status
// glyphs live in their own fixed-width column, so switching spellings cannot
// move a column. theme_test.go asserts that on the glyphs themselves, because a
// golden of a private-use glyph cannot tell a missing glyph from a present one.
type Icons struct {
	// Marked and Unmarked are the first column of the torrent list.
	Marked   string
	Unmarked string

	// statuses maps a torrent's derived status to its glyph.
	statuses map[transmission.Status]string
}

// nerdFontIcons and asciiIcons are the two glyph sets. The ASCII set is the one
// every golden but one is rendered with, so a golden diff is readable.
//
// The Nerd Font glyphs are written as \u escapes, never as literal characters.
// They live in the Unicode private use area, where a glyph is invisible in a
// diff, unreadable in review, and — as T1 proved by shipping this map as eight
// empty strings — silently droppable by any tool in the chain between an editor
// and a commit. An escape survives every one of them.
//
// They are drawn from Nerd Fonts' Font Awesome range (nf-fa-*, U+F000–U+F2E0),
// which is in the basic multilingual plane and measures one cell. The nf-md-*
// range is supplementary-plane and renders two cells in some terminals, which
// would move the status column.
var (
	nerdFontIcons = map[transmission.Status]string{
		transmission.StatusStopped:          "\uf04c", // nf-fa-pause
		transmission.StatusQueuedToVerify:   "\uf017", // nf-fa-clock_o
		transmission.StatusVerifying:        "\uf021", // nf-fa-refresh
		transmission.StatusQueuedToDownload: "\uf017", // nf-fa-clock_o
		transmission.StatusDownloading:      "\uf019", // nf-fa-download
		transmission.StatusQueuedToSeed:     "\uf017", // nf-fa-clock_o
		transmission.StatusSeeding:          "\uf093", // nf-fa-upload
		transmission.StatusError:            "\uf071", // nf-fa-warning
	}

	asciiIcons = map[transmission.Status]string{
		transmission.StatusStopped:          "x",
		transmission.StatusQueuedToVerify:   "~",
		transmission.StatusVerifying:        "%",
		transmission.StatusQueuedToDownload: "~",
		transmission.StatusDownloading:      "v",
		transmission.StatusQueuedToSeed:     "~",
		transmission.StatusSeeding:          "^",
		transmission.StatusError:            "!",
	}
)

// nerdFontMark and asciiMark are the marked-row glyphs.
//
// The Nerd Font mark is a private-use glyph for the same reason the status
// glyphs are: it is one cell wide by construction. The obvious Unicode choice,
// "◉" (U+25C9), is East-Asian-ambiguous width — one cell here and two in a CJK
// locale, which would break the frame-fills-the-terminal invariant on somebody
// else's terminal and nowhere in this suite.
const (
	nerdFontMark = "\uf058" // nf-fa-check_circle
	asciiMark    = "*"
)

// newIcons returns the glyph set for this invocation.
func newIcons(nerdFont bool) Icons {
	if nerdFont {
		return Icons{Marked: nerdFontMark, Unmarked: " ", statuses: nerdFontIcons}
	}

	return Icons{Marked: asciiMark, Unmarked: " ", statuses: asciiIcons}
}

// status returns the glyph for a torrent status, or "?" for a value the enum
// does not cover — a server reporting something unknown must still render a
// row rather than a blank cell that silently misaligns the column.
func (i Icons) status(s transmission.Status) string {
	if glyph, ok := i.statuses[s]; ok {
		return glyph
	}

	return "?"
}

// mark returns the glyph for a row's mark state.
func (i Icons) mark(marked bool) string {
	if marked {
		return i.Marked
	}

	return i.Unmarked
}
