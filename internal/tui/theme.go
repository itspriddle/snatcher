package tui

import (
	"image/color"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// Theme is the named color slots every style is built from. Lip Gloss v2 has no
// global renderer and no root adaptive color, so a theme is resolved once — for
// one theme name and one light/dark answer — and the resulting Styles are passed
// explicitly into every view function.
//
// T1 ships the slots and the plumbing; T2 fills in the default, dracula, and
// nord palettes behind themeFor.
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

	// Mark is the color of a marked row's glyph.
	Mark color.Color

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
// All three names currently resolve to the same placeholder palette. That is
// deliberate: T1's job is the substrate — every style is built from these slots
// and nothing hardcodes a color — and T2 fills in the three palettes without
// touching a single call site. An unknown name resolves to the default rather
// than failing, because the name was already validated where it was read
// (commands.ResolveClientSettings) and the TUI must not fail to draw over it.
func themeFor(name string, isDark bool) Theme {
	_ = name

	pick := lipgloss.LightDark(isDark)

	return Theme{
		Text:       pick(lipgloss.Color("#1c1c1c"), lipgloss.Color("#e4e4e4")),
		Muted:      pick(lipgloss.Color("#6c6c6c"), lipgloss.Color("#8a8a8a")),
		Accent:     pick(lipgloss.Color("#005faf"), lipgloss.Color("#5fafff")),
		Loud:       pick(lipgloss.Color("#af0000"), lipgloss.Color("#ff5f5f")),
		Cursor:     pick(lipgloss.Color("#d7d7ff"), lipgloss.Color("#303060")),
		CursorText: pick(lipgloss.Color("#1c1c1c"), lipgloss.Color("#ffffff")),
		Mark:       pick(lipgloss.Color("#875f00"), lipgloss.Color("#ffd75f")),
		Border:     pick(lipgloss.Color("#b2b2b2"), lipgloss.Color("#4e4e4e")),
		StatusBar:  pick(lipgloss.Color("#d0d0d0"), lipgloss.Color("#303030")),
		StatusText: pick(lipgloss.Color("#1c1c1c"), lipgloss.Color("#e4e4e4")),
		Warn:       pick(lipgloss.Color("#af5f00"), lipgloss.Color("#ffaf5f")),
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

	// Mark is a marked row's glyph.
	Mark lipgloss.Style

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
}

// newStyles builds every style from a theme. It is the only place a style is
// constructed, so "which color is this" is answerable by reading one function.
func newStyles(t Theme) Styles {
	pane := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.Border)

	tbl := table.DefaultStyles()
	tbl.Header = lipgloss.NewStyle().Bold(true).Foreground(t.Muted).PaddingRight(cellPadding)
	tbl.Cell = lipgloss.NewStyle().Foreground(t.Text).PaddingRight(cellPadding)
	tbl.Selected = lipgloss.NewStyle().Foreground(t.CursorText).Background(t.Cursor)

	return Styles{
		Pane:        pane,
		PaneFocused: pane.BorderForeground(t.Accent),
		PaneLoud:    pane.BorderForeground(t.Loud).Border(lipgloss.DoubleBorder()),
		Title:       lipgloss.NewStyle().Bold(true).Foreground(t.Accent),
		Tab:         lipgloss.NewStyle().Foreground(t.Muted).Padding(0, 1),
		TabActive:   lipgloss.NewStyle().Bold(true).Foreground(t.Accent).Padding(0, 1),
		Cursor:      lipgloss.NewStyle().Foreground(t.CursorText).Background(t.Cursor),
		Mark:        lipgloss.NewStyle().Bold(true).Foreground(t.Mark),
		Muted:       lipgloss.NewStyle().Foreground(t.Muted),
		StatusBar:   lipgloss.NewStyle().Foreground(t.StatusText).Background(t.StatusBar),
		Toast:       lipgloss.NewStyle().Bold(true).Foreground(t.Warn),
		Overlay:     lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.Accent).Padding(0, 1),
		Help:        help.DefaultStyles(true),
		Table:       tbl,
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
