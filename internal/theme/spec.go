package theme

import (
	"image/color"

	"github.com/charmbracelet/colorprofile"
)

// Spec is one parsed theme file. Each section is partial: a slot a section does
// not set is answered by the next section in Resolve's fallback chain.
type Spec struct {
	// Name is the theme's name, the file's stem.
	Name string

	// sections maps a section name to the slots it sets. A section whose header
	// appears in the file is present even when it sets nothing.
	sections map[string]map[string]color.Color
}

// Has reports whether the file carried the section's header.
func (s Spec) Has(section string) bool {
	_, ok := s.sections[section]

	return ok
}

// Color returns the color a section sets for a slot, and whether it sets one.
func (s Spec) Color(section, slot string) (color.Color, bool) {
	c, ok := s.sections[section][slot]

	return c, ok
}

// Resolve picks one color per slot for a color profile and a background.
//
// Each slot takes the first section in its chain that sets it:
//
//   - Under a sixteen-color profile: [ansi.light] (light backgrounds only),
//     then [ansi], then the same two sections of the embedded default theme.
//     [dark] and [light] are never consulted, so a hex palette is never guessed
//     down to the terminal's colors; a theme with no ansi sections renders as
//     the terminal's own palette.
//   - Under every other profile: [light] (light only), [dark], [ansi.light]
//     (light only), [ansi], then the embedded default's ansi sections. A
//     256-color terminal gets the hex values, which the renderer downsamples.
//     Ascii and NoTTY resolve the same way; the TUI strips their color.
//
// The base is always the embedded default, never a user file shadowing it, so
// a partial user default cannot leave a slot unanswered.
func (s Spec) Resolve(p colorprofile.Profile, isDark bool) Theme {
	chain := make([]string, 0, 4)

	if p != colorprofile.ANSI {
		if !isDark {
			chain = append(chain, SectionLight)
		}

		chain = append(chain, SectionDark)
	}

	if !isDark {
		chain = append(chain, SectionANSILight)
	}

	chain = append(chain, SectionANSI)

	base := Default()
	slots := make(map[string]color.Color, len(Slots))

	for _, slot := range Slots {
		if c, ok := firstColor(s, chain, slot); ok {
			slots[slot] = c
		} else if c, ok := firstColor(base, ansiChain(isDark), slot); ok {
			slots[slot] = c
		}
	}

	return fromSlots(slots)
}

// ansiChain is the sixteen-color part of a chain, for a background.
func ansiChain(isDark bool) []string {
	if isDark {
		return []string{SectionANSI}
	}

	return []string{SectionANSILight, SectionANSI}
}

// firstColor is the first section in chain that sets slot.
func firstColor(s Spec, chain []string, slot string) (color.Color, bool) {
	for _, section := range chain {
		if c, ok := s.Color(section, slot); ok {
			return c, true
		}
	}

	return nil, false
}
