package tui

import (
	"testing"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

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
