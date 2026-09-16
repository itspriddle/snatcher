package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runRoot executes the real command tree with args, returning stdout and the
// error Execute reported.
func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()

	root := NewRootCmd()

	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	err := root.Execute()

	return out.String(), err
}

// emptyConfig writes a config file with no servers and no client section, for
// commands that must load one without being told anything by it.
func emptyConfig(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"servers": {}}`), 0o600))

	return path
}

// TestRootChecksAnAppearanceSettingOnlyWhereItIsRead pins the blast radius of
// an invalid override: it fails the commands that read the setting and nothing
// else. A stale SNATCHER_THEME in a shell profile is a plausible accident, and
// until the TUI exists nothing renders a theme at all, so letting it fail the
// whole CLI — the "config" commands that repair it most of all — would trade a
// cosmetic typo for an unusable tool.
func TestRootChecksAnAppearanceSettingOnlyWhereItIsRead(t *testing.T) {
	cfgPath := emptyConfig(t)

	t.Setenv(envTheme, "monokai")

	t.Run("a command that never reads it", func(t *testing.T) {
		_, err := runRoot(t, "server", "list", "--config", cfgPath)
		assert.NoError(t, err)
	})

	t.Run("torrent list reports its own failure, not the theme's", func(t *testing.T) {
		// No server is configured, so this cannot succeed; what matters is that
		// it gets as far as saying so instead of being turned away by a setting
		// it never reads.
		_, err := runRoot(t, "torrent", "list", "--config", cfgPath)
		require.Error(t, err)

		assert.Contains(t, err.Error(), "not configured")
		assert.NotContains(t, err.Error(), envTheme)
	})

	t.Run("the flag rung is equally narrow", func(t *testing.T) {
		_, err := runRoot(t, "server", "list", "--config", cfgPath, "--theme", "monokai")
		assert.NoError(t, err)
	})

	t.Run("config set repairs the file", func(t *testing.T) {
		_, err := runRoot(t, "config", "set", "client.theme", "nord", "--config", cfgPath)
		require.NoError(t, err)

		stored, readErr := os.ReadFile(cfgPath)
		require.NoError(t, readErr)
		assert.Contains(t, string(stored), "nord")
	})

	t.Run("config unset repairs the file", func(t *testing.T) {
		_, err := runRoot(t, "config", "unset", "client.theme", "--config", cfgPath)
		require.NoError(t, err)

		stored, readErr := os.ReadFile(cfgPath)
		require.NoError(t, readErr)
		assert.NotContains(t, string(stored), "nord")
	})

	t.Run("a command that does read it still rejects the value", func(t *testing.T) {
		_, err := runRoot(t, "config", "get", "client.theme", "--config", cfgPath)
		require.Error(t, err)

		assert.Contains(t, err.Error(), envTheme)
		assert.Contains(t, err.Error(), "not a known theme")
	})

	t.Run("and names the flag when the flag supplied it", func(t *testing.T) {
		_, err := runRoot(t, "config", "list", "--config", cfgPath, "--theme", "chartreuse")
		require.Error(t, err)

		assert.Contains(t, err.Error(), "--theme")
		assert.Contains(t, err.Error(), "not a known theme")
	})
}

// TestRootChecksColorDepthOnlyWhereItIsRead is the same blast-radius pin for
// SNATCHER_COLOR_DEPTH and --color-depth.
func TestRootChecksColorDepthOnlyWhereItIsRead(t *testing.T) {
	cfgPath := emptyConfig(t)

	t.Setenv(envColorDepth, "lots")

	t.Run("a command that never reads it", func(t *testing.T) {
		_, err := runRoot(t, "server", "list", "--config", cfgPath)
		assert.NoError(t, err)
	})

	t.Run("the flag rung is equally narrow", func(t *testing.T) {
		_, err := runRoot(t, "server", "list", "--config", cfgPath, "--color-depth", "lots")
		assert.NoError(t, err)
	})

	t.Run("config set repairs the file", func(t *testing.T) {
		_, err := runRoot(t, "config", "set", "client.color_depth", "256", "--config", cfgPath)
		require.NoError(t, err)

		stored, readErr := os.ReadFile(cfgPath)
		require.NoError(t, readErr)
		assert.Contains(t, string(stored), `"color_depth": "256"`)
	})

	t.Run("a command that does read it still rejects the value", func(t *testing.T) {
		_, err := runRoot(t, "config", "get", "client.color_depth", "--config", cfgPath)
		require.Error(t, err)

		assert.Contains(t, err.Error(), envColorDepth)
		assert.Contains(t, err.Error(), "not a color depth")
	})

	t.Run("and names the flag when the flag supplied it", func(t *testing.T) {
		_, err := runRoot(t, "config", "list", "--config", cfgPath, "--color-depth", "8")
		require.Error(t, err)

		assert.Contains(t, err.Error(), "--color-depth")
		assert.Contains(t, err.Error(), "not a color depth")
	})
}

// TestRootAcceptsValidGlobalSettings checks the appearance flags are
// accepted at every depth even though nothing renders them yet, so passing one
// is never an error on a command that ignores it. Both environment companions
// are already out of the way — TestMain unsets them for the whole package.
func TestRootAcceptsValidGlobalSettings(t *testing.T) {
	out, err := runRoot(t, "--theme", "dracula", "--no-nerd-font", "--color-depth", "16", "--version")
	require.NoError(t, err)

	assert.NotEmpty(t, out)
}

// TestRootDeclaresNoPersistentPreRun pins the absence deliberately: a check
// hung off the root's persistent pre-run runs ahead of every command in the
// tree, which is precisely the blast radius the appearance settings must not
// have. Validation belongs with the command that reads the value.
func TestRootDeclaresNoPersistentPreRun(t *testing.T) {
	var walk func(cmd *cobra.Command)

	walk = func(cmd *cobra.Command) {
		assert.Nil(t, cmd.PersistentPreRunE, "%s declares a persistent pre-run, which runs ahead of every command below it", cmd.CommandPath())
		assert.Nil(t, cmd.PersistentPreRun, "%s declares a persistent pre-run, which runs ahead of every command below it", cmd.CommandPath())

		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}

	walk(NewRootCmd())
}
