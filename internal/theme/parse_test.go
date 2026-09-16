package theme

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAcceptsTheGrammar(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"comments and blanks": "# a theme\n\n  # indented comment\n[dark]\naccent = #268bd2\n",
		"double quotes":       "[dark]\naccent = \"#268bd2\"\n",
		"single quotes":       "[dark]\naccent='#268bd2'\n",
		"crlf":                "[dark]\r\naccent = #268bd2\r\n",
		"bom":                 "\xef\xbb\xbf[dark]\naccent = #268bd2\n",
		"no final newline":    "[dark]\naccent = #268bd2",
		"padded header":       "[ dark ]\n\taccent\t=\t#268bd2\n",
	}

	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			spec, err := Parse("x", "x.theme", []byte(src))
			require.NoError(t, err)

			assert.Equal(t, "x", spec.Name)

			c, ok := spec.Color(SectionDark, SlotAccent)
			require.True(t, ok)
			assert.Equal(t, lipgloss.Color("#268bd2"), c)
		})
	}
}

func TestParseSections(t *testing.T) {
	t.Parallel()

	spec, err := Parse("x", "x.theme", []byte("[ansi]\n[ansi.light]\ntext = black\n"))
	require.NoError(t, err)

	assert.True(t, spec.Has(SectionANSI), "an empty section still counts as present")
	assert.True(t, spec.Has(SectionANSILight))
	assert.False(t, spec.Has(SectionDark))
	assert.False(t, spec.Has(SectionLight))

	_, ok := spec.Color(SectionANSI, SlotText)
	assert.False(t, ok)
}

func TestParseRejects(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		src  string
		want string
	}{
		"unknown section":     {src: "[dark]\n[night]\n", want: "x.theme:2: unknown section [night]"},
		"unclosed section":    {src: "[dark\n", want: "x.theme:1: section header"},
		"duplicate section":   {src: "[dark]\ntext = red\n[dark]\n", want: "x.theme:3: section [dark] appears more than once"},
		"unknown key":         {src: "[dark]\nhue = red\n", want: `x.theme:2: unknown key "hue"`},
		"uppercase key":       {src: "[dark]\nTEXT = red\n", want: `x.theme:2: unknown key "TEXT"`},
		"duplicate key":       {src: "[dark]\ntext = red\ntext = blue\n", want: `x.theme:3: key "text" appears more than once`},
		"bad value":           {src: "[dark]\n\ntext = purple\n", want: `x.theme:3: text: "purple" is not a color`},
		"hex in ansi":         {src: "[ansi]\ntext = #fff\n", want: "x.theme:2: text:"},
		"hex in ansi.light":   {src: "[ansi]\n[ansi.light]\ntext = #fff\n", want: "x.theme:3: text:"},
		"indexed 256":         {src: "[dark]\ntext = 208\n", want: "x.theme:2: text: \"208\" is out of range"},
		"key before section":  {src: "# hi\ntext = red\n[dark]\n", want: `x.theme:2: key "text" appears before any [section]`},
		"not a pair":          {src: "[dark]\ntext red\n", want: "x.theme:2: expected key = value"},
		"trailing comment":    {src: "[dark]\ntext = red # comment\n", want: "x.theme:2: text:"},
		"no base section":     {src: "[light]\ntext = #fff\n", want: "x.theme: theme has no [dark] or [ansi] section"},
		"empty file":          {src: "", want: "x.theme: theme has no [dark] or [ansi] section"},
		"long line":           {src: "[dark]\n#" + strings.Repeat("x", MaxLineLen) + "\n", want: "x.theme:2: line is longer than 512 bytes"},
		"very long line":      {src: "[dark]\n#" + strings.Repeat("x", 4*MaxLineLen) + "\n", want: "x.theme:2: line is longer than 512 bytes"},
		"large file":          {src: "[dark]\n" + strings.Repeat("#\n", MaxFileSize), want: "x.theme: theme file is larger than"},
		"section after error": {src: "[ansi]\n[dark]\ntext = nope\n", want: "x.theme:3:"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := Parse("x", "x.theme", []byte(tt.src))
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

// TestParseLineLimitIsInclusive checks a line of exactly MaxLineLen bytes, with
// a CRLF after it, still parses.
func TestParseLineLimitIsInclusive(t *testing.T) {
	t.Parallel()

	src := "[dark]\r\n#" + strings.Repeat("x", MaxLineLen-1) + "\r\ntext = red\r\n"

	_, err := Parse("x", "x.theme", []byte(src))
	assert.NoError(t, err)
}
