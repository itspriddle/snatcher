package theme

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseColor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in       string
		ansiOnly bool
		want     color.Color
	}{
		{in: "black", want: lipgloss.Black},
		{in: "white", want: lipgloss.White},
		{in: "bright-black", want: lipgloss.BrightBlack},
		{in: "bright-white", want: lipgloss.BrightWhite},
		{in: "Bright-Cyan", want: lipgloss.BrightCyan},
		{in: "  RED  ", want: lipgloss.Red},
		{in: "0", want: lipgloss.Black},
		{in: "4", want: lipgloss.Blue},
		{in: "15", want: lipgloss.BrightWhite},
		{in: "default", want: lipgloss.NoColor{}},
		{in: "DEFAULT", ansiOnly: true, want: lipgloss.NoColor{}},
		{in: "blue", ansiOnly: true, want: lipgloss.Blue},
		{in: "#268bd2", want: lipgloss.Color("#268bd2")},
		{in: "#268BD2", want: lipgloss.Color("#268bd2")},
		{in: "#fff", want: lipgloss.Color("#ffffff")},
	}

	for _, tt := range tests {
		got, err := ParseColor(tt.in, tt.ansiOnly)
		require.NoErrorf(t, err, "%q", tt.in)
		assert.Equalf(t, tt.want, got, "%q", tt.in)
	}
}

func TestParseColorRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in       string
		ansiOnly bool
		want     string
	}{
		{in: "#268bd2", ansiOnly: true, want: "ansi section does not accept"},
		{in: "#12345", want: "not a hex color"},
		{in: "#ggg", want: "not a hex color"},
		{in: "16", want: "out of range"},
		{in: "255", want: "out of range"},
		{in: "99999999999999999999", want: "out of range"},
		{in: "-1", want: "not a color"},
		{in: "brightblack", want: "not a color"},
		{in: "", want: "not a color"},
		{in: "purple", want: "not a color"},
	}

	for _, tt := range tests {
		_, err := ParseColor(tt.in, tt.ansiOnly)
		assert.ErrorContainsf(t, err, tt.want, "%q", tt.in)
	}
}
