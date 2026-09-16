// Package theme loads snatcher's color themes: the builtin .theme files
// embedded in the binary, and the operator's own files in the themes directory
// beside config.json.
//
// A theme file is data, not code. It carries up to four sections — a truecolor
// [dark] and [light] arrangement, and a curated sixteen-color [ansi] and
// [ansi.light] arrangement — and Spec.Resolve picks, slot by slot, the one the
// terminal's color profile and background call for. Under a sixteen-color
// profile only the ansi sections are consulted, so a hex palette is never
// guessed down to the terminal's own colors.
//
// The package is a leaf: it imports nothing of snatcher's, so internal/commands
// (which validates a theme name) and internal/tui (which draws one) share it
// without importing each other.
package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Theme is the named color slots every TUI style is built from. Lip Gloss v2 has
// no global renderer and no root adaptive color, so a theme is resolved once —
// for one color profile and one light/dark answer — and the resulting styles are
// passed explicitly into every view function.
//
// It is comparable, so two resolved themes can be told apart with ==.
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

// The slot keys a theme file spells, one per Theme field.
const (
	SlotText     = "text"
	SlotMuted    = "muted"
	SlotAccent   = "accent"
	SlotLoud     = "loud"
	SlotCursorBg = "cursor_bg"
	SlotCursorFg = "cursor_fg"
	SlotBorder   = "border"
	SlotStatusBg = "status_bg"
	SlotStatusFg = "status_fg"
	SlotWarn     = "warn"
)

// Slots is every slot key in Theme's field order. It is the file format's
// schema: the parser's key whitelist, and the list a completeness check walks.
var Slots = []string{
	SlotText, SlotMuted, SlotAccent, SlotLoud, SlotCursorBg,
	SlotCursorFg, SlotBorder, SlotStatusBg, SlotStatusFg, SlotWarn,
}

// The sections a theme file may carry.
const (
	// SectionDark is the truecolor arrangement for a dark background, and the
	// fallback for a light one.
	SectionDark = "dark"

	// SectionLight is the truecolor arrangement for a light background.
	SectionLight = "light"

	// SectionANSI is the sixteen-color arrangement: color names and indices
	// only, resolved by the terminal's own scheme.
	SectionANSI = "ansi"

	// SectionANSILight is the sixteen-color arrangement for a light
	// background, falling back to SectionANSI slot by slot.
	SectionANSILight = "ansi.light"
)

// DefaultName is the builtin theme that is the terminal's own palette, and the
// base every theme falls back to.
const DefaultName = "default"

// fromSlots builds a Theme from one resolved color per slot key. A slot missing
// from the map is lipgloss.NoColor rather than nil, so no style is left without
// an answer.
func fromSlots(slots map[string]color.Color) Theme {
	get := func(key string) color.Color {
		if c, ok := slots[key]; ok {
			return c
		}

		return lipgloss.NoColor{}
	}

	return Theme{
		Text:       get(SlotText),
		Muted:      get(SlotMuted),
		Accent:     get(SlotAccent),
		Loud:       get(SlotLoud),
		Cursor:     get(SlotCursorBg),
		CursorText: get(SlotCursorFg),
		Border:     get(SlotBorder),
		StatusBar:  get(SlotStatusBg),
		StatusText: get(SlotStatusFg),
		Warn:       get(SlotWarn),
	}
}
