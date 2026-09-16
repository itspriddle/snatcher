package theme

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustParse parses src as a theme named x.
func mustParse(t *testing.T, src string) Spec {
	t.Helper()

	spec, err := Parse("x", "x.theme", []byte(src))
	require.NoError(t, err)

	return spec
}

func TestResolveLightFallsBackToDarkPerSlot(t *testing.T) {
	t.Parallel()

	spec := mustParse(t, "[dark]\ntext = #111111\naccent = #222222\n[light]\ntext = #eeeeee\n")

	light := spec.Resolve(colorprofile.TrueColor, false)
	assert.Equal(t, lipgloss.Color("#eeeeee"), light.Text)
	assert.Equal(t, lipgloss.Color("#222222"), light.Accent, "a slot [light] leaves out comes from [dark]")

	dark := spec.Resolve(colorprofile.TrueColor, true)
	assert.Equal(t, lipgloss.Color("#111111"), dark.Text, "[light] is never read on a dark background")
}

func TestResolveMissingSlotFallsBackToTheDefault(t *testing.T) {
	t.Parallel()

	spec := mustParse(t, "[dark]\ntext = #111111\n")
	base := Default().Resolve(colorprofile.TrueColor, true)

	got := spec.Resolve(colorprofile.TrueColor, true)
	assert.Equal(t, lipgloss.Color("#111111"), got.Text)
	assert.Equal(t, base.Accent, got.Accent)
	assert.Equal(t, base.Warn, got.Warn)
}

func TestResolveTruecolorFallsBackToTheFilesANSISection(t *testing.T) {
	t.Parallel()

	spec := mustParse(t, "[dark]\ntext = #111111\n[ansi]\naccent = magenta\n[ansi.light]\naccent = green\n")

	assert.Equal(t, lipgloss.Magenta, spec.Resolve(colorprofile.TrueColor, true).Accent)
	assert.Equal(t, lipgloss.Green, spec.Resolve(colorprofile.TrueColor, false).Accent)
}

func TestResolveANSIPicksTheANSISectionAndIgnoresHex(t *testing.T) {
	t.Parallel()

	spec := mustParse(t, "[dark]\ntext = #111111\naccent = #222222\n[ansi]\naccent = magenta\n")
	base := Default().Resolve(colorprofile.ANSI, true)

	got := spec.Resolve(colorprofile.ANSI, true)
	assert.Equal(t, lipgloss.Magenta, got.Accent)
	assert.Equal(t, base.Text, got.Text, "[dark] is never consulted under sixteen colors")
}

func TestResolveANSIWithoutANSISectionIsTheDefault(t *testing.T) {
	t.Parallel()

	spec := mustParse(t, "[dark]\ntext = #111111\n[light]\ntext = #eeeeee\n")

	for _, isDark := range []bool{false, true} {
		assert.Equal(t, Default().Resolve(colorprofile.ANSI, isDark), spec.Resolve(colorprofile.ANSI, isDark))
	}
}

func TestResolvePartialANSILight(t *testing.T) {
	t.Parallel()

	spec, err := Builtins().Lookup("solarized")
	require.NoError(t, err)

	light := spec.Resolve(colorprofile.ANSI, false)
	assert.Equal(t, lipgloss.BrightGreen, light.Text, "[ansi.light] sets text")
	assert.Equal(t, lipgloss.Blue, light.Accent, "[ansi.light] leaves accent to [ansi]")

	dark := spec.Resolve(colorprofile.ANSI, true)
	assert.Equal(t, lipgloss.BrightCyan, dark.Text, "[ansi.light] is never read on a dark background")
}

func TestResolveANSI256KeepsTheHexValues(t *testing.T) {
	t.Parallel()

	spec, err := Builtins().Lookup("nord")
	require.NoError(t, err)

	for _, isDark := range []bool{false, true} {
		assert.Equal(t, spec.Resolve(colorprofile.TrueColor, isDark), spec.Resolve(colorprofile.ANSI256, isDark),
			"the renderer downsamples; Resolve does not")
	}
}

// TestResolveBaseIsTheEmbeddedDefault checks a user default.theme shadowing the
// builtin is a theme like any other, not the base other themes fall back to.
func TestResolveBaseIsTheEmbeddedDefault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTheme(t, dir, "default.theme", "[ansi]\naccent = magenta\n")

	reg, err := Load(dir)
	require.NoError(t, err)

	user, err := reg.Lookup("default")
	require.NoError(t, err)
	assert.Equal(t, lipgloss.Magenta, user.Resolve(colorprofile.ANSI, true).Accent)
	assert.Equal(t, lipgloss.BrightBlack, user.Resolve(colorprofile.ANSI, true).Muted, "the embedded default fills the rest")

	other := mustParse(t, "[dark]\ntext = #111111\n")
	assert.Equal(t, lipgloss.Blue, other.Resolve(colorprofile.TrueColor, true).Accent)
}

// TestResolveNeverLeavesASlotNil checks every slot answers even for a theme
// that sets nothing.
func TestResolveNeverLeavesASlotNil(t *testing.T) {
	t.Parallel()

	got := mustParse(t, "[dark]\n").Resolve(colorprofile.TrueColor, true)

	assert.Equal(t, Default().Resolve(colorprofile.TrueColor, true), got)

	empty := fromSlots(nil)
	assert.Equal(t, lipgloss.NoColor{}, empty.Text)
	assert.Equal(t, lipgloss.NoColor{}, empty.Warn)
}
