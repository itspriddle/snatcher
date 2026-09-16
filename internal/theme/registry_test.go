package theme

import (
	"os"
	"path/filepath"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTheme writes one file into dir.
func writeTheme(t *testing.T, dir, name, src string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600))
}

func TestLoadMissingDirIsTheBuiltins(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{"", filepath.Join(t.TempDir(), "absent")} {
		reg, err := Load(dir)
		require.NoError(t, err)

		assert.Equal(t, Builtins().Names(), reg.Names())
		assert.Empty(t, reg.Warnings())
	}
}

func TestLoadUnreadableDirIsAnError(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "themes")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	_, err := Load(file)
	assert.ErrorContains(t, err, file)
}

func TestLoadAddsAndShadows(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTheme(t, dir, "mine.theme", "[dark]\naccent = #010203\n")
	writeTheme(t, dir, "nord.theme", "[dark]\naccent = #040506\n")

	reg, err := Load(dir)
	require.NoError(t, err)

	assert.Equal(t, []string{"default", "dracula", "mine", "nord", "solarized"}, reg.Names())

	mine, err := reg.Lookup("mine")
	require.NoError(t, err)
	assert.Equal(t, "mine", mine.Name)
	assert.Equal(t, lipgloss.Color("#010203"), mine.Resolve(colorprofile.TrueColor, true).Accent)

	nord, err := reg.Lookup("nord")
	require.NoError(t, err)
	assert.Equal(t, lipgloss.Color("#040506"), nord.Resolve(colorprofile.TrueColor, true).Accent, "a user file shadows the builtin")

	builtin, err := Builtins().Lookup("nord")
	require.NoError(t, err)
	assert.Equal(t, lipgloss.Color("#88c0d0"), builtin.Resolve(colorprofile.TrueColor, true).Accent, "Load must not touch the shared builtins")
}

func TestLoadSkipsBrokenFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTheme(t, dir, "broken.theme", "[dark]\ntext = nope\n")
	writeTheme(t, dir, "dracula.theme", "[night]\n")

	reg, err := Load(dir)
	require.NoError(t, err)

	assert.Equal(t, []string{"default", "nord", "solarized"}, reg.Names(), "a broken file is not offered, and removes the builtin it shadows")
	require.Len(t, reg.Warnings(), 2)
	assert.Contains(t, reg.Warnings()[0], filepath.Join(dir, "broken.theme")+":2:")

	_, err = reg.Lookup("broken")
	require.ErrorContains(t, err, filepath.Join(dir, "broken.theme")+":2:")
	require.NotErrorIs(t, err, ErrUnknown)

	_, err = reg.Lookup("dracula")
	require.ErrorContains(t, err, filepath.Join(dir, "dracula.theme")+":1:")
}

func TestLoadIgnoresNonThemeEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTheme(t, dir, "notes.txt", "garbage")
	writeTheme(t, dir, ".hidden.theme", "garbage")
	writeTheme(t, dir, "mine.theme.bak", "garbage")
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub.theme"), 0o700))
	writeTheme(t, dir, "real.theme", "[ansi]\n")
	require.NoError(t, os.Symlink(filepath.Join(dir, "real.theme"), filepath.Join(dir, "linked.theme")))

	reg, err := Load(dir)
	require.NoError(t, err)

	assert.Equal(t, []string{"default", "dracula", "linked", "nord", "real", "solarized"}, reg.Names())
	assert.Empty(t, reg.Warnings())
}

func TestLookupUnknown(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "DRACULA", "monokai"} {
		_, err := Builtins().Lookup(name)
		assert.ErrorIs(t, err, ErrUnknown, "names match exactly: %q", name)
	}
}
