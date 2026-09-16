package commands

import (
	"fmt"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// dialCmd builds a command carrying the persistent root flags with --config set
// to path, so a test can call dial directly the way an executed command does.
// dial reads --config off the command it is handed rather than taking a path, so
// a standalone command needs the flag registered on it — internal/cli does that
// once on the real root.
func dialCmd(t *testing.T, path string) *cobra.Command {
	t.Helper()

	cmd := NewServerCmd()
	AddGlobalFlags(cmd)
	require.NoError(t, cmd.PersistentFlags().Set(flagConfig, path))

	return cmd
}

// TestMain isolates this package from the environment the suite happens to run
// in, before any test observes it.
//
// Four variables redirect what these commands do with secrets:
// SNATCHER_SECRET_STORE picks a different store, SNATCHER_PASSWORD and
// SNATCHER_API_KEY supply a Transmission password and a Prowlarr API key
// without one, and SNATCHER_SERVER selects a different alias. A
// developer with SNATCHER_SECRET_STORE=file exported would otherwise have the
// "server add" tests write a plaintext password into their real
// ~/.local/state/snatcher/secrets.json, and every result here would depend on
// whose shell the suite ran in.
//
// Three more override client settings for an invocation — SNATCHER_THEME,
// SNATCHER_NO_NERD_FONT, and SNATCHER_COLOR_DEPTH — and would otherwise make "config list" report a source
// of "env" for a key the test staged in a config file. NO_COLOR joins them: it
// is read by isTTY and reported by ResolveClientSettings, so a developer with it
// exported would see a different table contract and a different ClientSettings
// than CI does.
//
// XDG_CONFIG_HOME is pointed at a throwaway directory too, so a test that
// validates a theme without --config cannot see the developer's own
// ~/.config/snatcher/themes. Tests still pass a temp --config themselves.
//
// XDG_STATE_HOME is pointed at a throwaway directory for the same reason, as the
// belt to the unset variable's braces: no test — including one written later
// that forgets stateDir — can resolve the real secrets file. Tests that do use
// the file store still call stateDir, which overrides this for their own
// duration.
//
// The keyring provider is mocked for the whole run so that nothing can read or
// write the developer's actual keychain either. Tests needing a particular
// keyring state still call keyring.MockInit or mockStoreUnavailable themselves;
// this only decides what an unprepared test gets.
func TestMain(m *testing.M) {
	for _, key := range []string{envSecretStore, envPassword, envAPIKey, envServer, envTheme, envNoNerdFont, envColorDepth, envNoColor} {
		if err := os.Unsetenv(key); err != nil {
			fmt.Fprintf(os.Stderr, "commands: unsetting %s: %v\n", key, err)
			os.Exit(1)
		}
	}

	stateHome, err := os.MkdirTemp("", "snatcher-commands-state")
	if err != nil {
		fmt.Fprintf(os.Stderr, "commands: creating a throwaway state directory: %v\n", err)
		os.Exit(1)
	}

	if err := os.Setenv("XDG_STATE_HOME", stateHome); err != nil {
		fmt.Fprintf(os.Stderr, "commands: setting XDG_STATE_HOME: %v\n", err)
		os.Exit(1)
	}

	configHome, err := os.MkdirTemp("", "snatcher-commands-config")
	if err != nil {
		fmt.Fprintf(os.Stderr, "commands: creating a throwaway config directory: %v\n", err)
		os.Exit(1)
	}

	if err := os.Setenv("XDG_CONFIG_HOME", configHome); err != nil {
		fmt.Fprintf(os.Stderr, "commands: setting XDG_CONFIG_HOME: %v\n", err)
		os.Exit(1)
	}

	keyring.MockInit()

	code := m.Run()

	_ = os.RemoveAll(stateHome)
	_ = os.RemoveAll(configHome)

	os.Exit(code)
}
