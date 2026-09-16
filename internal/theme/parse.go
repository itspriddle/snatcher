package theme

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image/color"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

const (
	// MaxFileSize is the largest theme file Parse accepts. A theme is ten
	// slots in four sections; anything near this size is not one.
	MaxFileSize = 64 << 10

	// MaxLineLen is the longest line Parse accepts, in bytes, excluding the
	// line ending.
	MaxLineLen = 512
)

// knownSections is every section a theme file may carry.
var knownSections = []string{SectionDark, SectionLight, SectionANSI, SectionANSILight}

// hexColor matches #rgb and #rrggbb, lowercased. It is checked before
// lipgloss.Color sees a value, because lipgloss.Color answers bad input with
// NoColor rather than an error.
var hexColor = regexp.MustCompile(`^#(?:[0-9a-f]{3}|[0-9a-f]{6})$`)

// decimal matches an unsigned decimal index.
var decimal = regexp.MustCompile(`^[0-9]+$`)

// colorNames are the sixteen terminal colors by name.
var colorNames = map[string]color.Color{
	"black":          lipgloss.Black,
	"red":            lipgloss.Red,
	"green":          lipgloss.Green,
	"yellow":         lipgloss.Yellow,
	"blue":           lipgloss.Blue,
	"magenta":        lipgloss.Magenta,
	"cyan":           lipgloss.Cyan,
	"white":          lipgloss.White,
	"bright-black":   lipgloss.BrightBlack,
	"bright-red":     lipgloss.BrightRed,
	"bright-green":   lipgloss.BrightGreen,
	"bright-yellow":  lipgloss.BrightYellow,
	"bright-blue":    lipgloss.BrightBlue,
	"bright-magenta": lipgloss.BrightMagenta,
	"bright-cyan":    lipgloss.BrightCyan,
	"bright-white":   lipgloss.BrightWhite,
}

// utf8BOM is a byte order mark some editors write at the start of a file.
var utf8BOM = []byte("\xef\xbb\xbf")

// Parse reads one theme file.
//
// The grammar is line-based: a blank line, a whole-line # comment, a [section]
// header, or a key = value pair. Comments are whole lines only, because a hex
// value starts with #. A value may be quoted; the quotes are stripped. CRLF line
// endings and a leading byte order mark are tolerated.
//
// name is the theme's name, and origin is how errors name the file: its path,
// or builtin:<file> for an embedded one. Every line-level error is
// "<origin>:<line>: message".
func Parse(name, origin string, src []byte) (Spec, error) {
	if len(src) > MaxFileSize {
		return Spec{}, fmt.Errorf("%s: theme file is larger than %d bytes", origin, MaxFileSize)
	}

	p := parser{origin: origin, spec: Spec{Name: name, sections: map[string]map[string]color.Color{}}}

	scanner := bufio.NewScanner(bytes.NewReader(bytes.TrimPrefix(src, utf8BOM)))
	// Room for a line of MaxLineLen plus its CRLF, and one byte over so an
	// overlong line reaches the explicit length check below.
	scanner.Buffer(make([]byte, 0, MaxLineLen+3), MaxLineLen+3)

	for scanner.Scan() {
		p.line++

		if err := p.parseLine(scanner.Bytes()); err != nil {
			return Spec{}, err
		}
	}

	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			// The scanner gave up on the line after the last one it returned.
			p.line++

			return Spec{}, p.errorf("line is longer than %d bytes", MaxLineLen)
		}

		return Spec{}, fmt.Errorf("%s: %w", origin, err)
	}

	if !p.spec.Has(SectionDark) && !p.spec.Has(SectionANSI) {
		return Spec{}, fmt.Errorf("%s: theme has no [%s] or [%s] section", origin, SectionDark, SectionANSI)
	}

	return p.spec, nil
}

// parser is Parse's state between lines.
type parser struct {
	origin  string
	line    int
	section string
	spec    Spec
}

// errorf is an error naming the current line.
func (p *parser) errorf(format string, args ...any) error {
	return fmt.Errorf("%s:%d: %s", p.origin, p.line, fmt.Sprintf(format, args...))
}

// parseLine handles one line, without its line ending.
func (p *parser) parseLine(raw []byte) error {
	if len(raw) > MaxLineLen {
		return p.errorf("line is longer than %d bytes", MaxLineLen)
	}

	line := strings.TrimSpace(string(raw))

	switch {
	case line == "", strings.HasPrefix(line, "#"):
		return nil
	case strings.HasPrefix(line, "["):
		return p.parseSection(line)
	default:
		return p.parsePair(line)
	}
}

// parseSection handles a [section] header.
func (p *parser) parseSection(line string) error {
	if !strings.HasSuffix(line, "]") {
		return p.errorf("section header %q is missing its closing ]", line)
	}

	name := strings.TrimSpace(line[1 : len(line)-1])

	if !slices.Contains(knownSections, name) {
		return p.errorf("unknown section [%s]: valid sections are %s", name, strings.Join(knownSections, ", "))
	}

	if p.spec.Has(name) {
		return p.errorf("section [%s] appears more than once", name)
	}

	p.section = name
	p.spec.sections[name] = map[string]color.Color{}

	return nil
}

// parsePair handles a key = value line.
func (p *parser) parsePair(line string) error {
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return p.errorf("expected key = value, a [section], or a # comment")
	}

	key = strings.TrimSpace(key)
	value = unquote(strings.TrimSpace(value))

	if p.section == "" {
		return p.errorf("key %q appears before any [section]", key)
	}

	if !slices.Contains(Slots, key) {
		return p.errorf("unknown key %q: valid keys are %s", key, strings.Join(Slots, ", "))
	}

	slots := p.spec.sections[p.section]

	if _, dup := slots[key]; dup {
		return p.errorf("key %q appears more than once in [%s]", key, p.section)
	}

	c, err := ParseColor(value, strings.HasPrefix(p.section, SectionANSI))
	if err != nil {
		return p.errorf("%s: %v", key, err)
	}

	slots[key] = c

	return nil
}

// unquote strips one pair of matching double or single quotes.
func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return strings.TrimSpace(s[1 : len(s)-1])
	}

	return s
}

// ParseColor reads one color value, case-insensitively:
//
//   - #rrggbb or #rgb, unless ansiOnly;
//   - one of the sixteen names, black through white and bright-black through
//     bright-white;
//   - an index from 0 to 15;
//   - default, the terminal's own foreground (or no background painted).
//
// ansiOnly is set for the ansi sections, where a hex value is exactly what the
// section exists to avoid.
func ParseColor(s string, ansiOnly bool) (color.Color, error) {
	v := strings.ToLower(strings.TrimSpace(s))

	switch {
	case v == "default":
		return lipgloss.NoColor{}, nil
	case strings.HasPrefix(v, "#"):
		if !hexColor.MatchString(v) {
			return nil, fmt.Errorf("%q is not a hex color: use #rrggbb or #rgb", s)
		}

		if ansiOnly {
			return nil, fmt.Errorf("%q is a hex color, which an ansi section does not accept: use a color name or an index from 0 to 15", s)
		}

		return lipgloss.Color(v), nil
	case decimal.MatchString(v):
		n, err := strconv.Atoi(v)
		if err != nil || n > 15 {
			return nil, fmt.Errorf("%q is out of range: a color index must be from 0 to 15", s)
		}

		return lipgloss.Color(v), nil
	}

	if c, ok := colorNames[v]; ok {
		return c, nil
	}

	return nil, fmt.Errorf("%q is not a color: use #rrggbb, a name from black to bright-white, an index from 0 to 15, or default", s)
}
