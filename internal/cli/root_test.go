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

// TestRootRejectsAnInvalidGlobalSettingEverywhere pins that the appearance
// settings are checked ahead of every command, not only the ones that read
// them. Until the TUI exists nothing renders a theme, so without this hook a
// typo would be accepted in silence by all but two commands.
func TestRootRejectsAnInvalidGlobalSettingEverywhere(t *testing.T) {
	cfgPath := emptyConfig(t)

	t.Run("flag on the root command", func(t *testing.T) {
		_, err := runRoot(t, "--theme", "solarized", "--version")
		require.Error(t, err)

		assert.Contains(t, err.Error(), "--theme")
		assert.Contains(t, err.Error(), "not a known theme")
	})

	t.Run("flag on a command that never reads it", func(t *testing.T) {
		_, err := runRoot(t, "server", "list", "--config", cfgPath, "--theme", "solarized")
		require.Error(t, err)

		assert.Contains(t, err.Error(), "--theme")
	})

	t.Run("environment variable on a command that never reads it", func(t *testing.T) {
		t.Setenv(envNoNerdFont, "yes-please")

		_, err := runRoot(t, "server", "list", "--config", cfgPath)
		require.Error(t, err)

		assert.Contains(t, err.Error(), envNoNerdFont)
		assert.Contains(t, err.Error(), "not a boolean")
	})
}

// TestRootAcceptsValidGlobalSettings checks the hook costs an ordinary
// invocation nothing, including one that passes the flags it does not yet
// render. Both environment companions are already out of the way — TestMain
// unsets them for the whole package.
func TestRootAcceptsValidGlobalSettings(t *testing.T) {
	out, err := runRoot(t, "--theme", "dracula", "--no-nerd-font", "--version")
	require.NoError(t, err)

	assert.NotEmpty(t, out)
}

// TestRootDeclaresNoCompetingPersistentPreRun guards the assumption the hook
// rests on: cobra runs only the closest PersistentPreRunE, so a subcommand that
// declared its own would silently skip the root's check.
func TestRootDeclaresNoCompetingPersistentPreRun(t *testing.T) {
	var walk func(cmd *cobra.Command)

	walk = func(cmd *cobra.Command) {
		for _, sub := range cmd.Commands() {
			assert.Nil(t, sub.PersistentPreRunE, "%s declares a persistent pre-run that would shadow the root's", sub.CommandPath())
			assert.Nil(t, sub.PersistentPreRun, "%s declares a persistent pre-run that would shadow the root's", sub.CommandPath())
			walk(sub)
		}
	}

	walk(NewRootCmd())
}
