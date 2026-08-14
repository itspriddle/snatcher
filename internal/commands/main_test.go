package commands

import (
	"fmt"
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

// envServer names the environment variable that selects which configured server
// a command dials. resolveServer reads it inline; it is named here so TestMain
// can neutralize it alongside the two secret-store variables.
const envServer = "SNATCHER_SERVER"

// TestMain isolates this package from the environment the suite happens to run
// in, before any test observes it.
//
// Three variables redirect what these commands do with passwords:
// SNATCHER_SECRET_STORE picks a different store, SNATCHER_PASSWORD supplies a
// password without one, and SNATCHER_SERVER selects a different alias. A
// developer with SNATCHER_SECRET_STORE=file exported would otherwise have the
// "server add" tests write a plaintext password into their real
// ~/.local/state/snatcher/secrets.json, and every result here would depend on
// whose shell the suite ran in.
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
	for _, key := range []string{envSecretStore, envPassword, envServer} {
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

	keyring.MockInit()

	code := m.Run()

	_ = os.RemoveAll(stateHome)

	os.Exit(code)
}
