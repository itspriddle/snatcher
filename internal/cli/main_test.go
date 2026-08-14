package cli

import (
	"fmt"
	"os"
	"testing"
)

// The environment companions of the two appearance flags. internal/commands
// keeps its own constants beside the flags they override; they are unexported,
// and this package only needs the names to get them out of the way.
const (
	envTheme      = "SNATCHER_THEME"
	envNoNerdFont = "SNATCHER_NO_NERD_FONT"
)

// TestMain isolates this package from the environment the suite happens to run
// in, before any test observes it, the same way internal/commands' TestMain
// does.
//
// The root command validates both appearance settings ahead of every command,
// walking the registry theme-first, so a developer with an invalid
// SNATCHER_THEME exported would see the invalid-environment case below fail on
// the wrong variable: the theme is rejected before SNATCHER_NO_NERD_FONT is ever
// read. Neither variable may steer a result here; a test that wants one sets it
// itself with t.Setenv.
func TestMain(m *testing.M) {
	for _, key := range []string{envTheme, envNoNerdFont} {
		if err := os.Unsetenv(key); err != nil {
			fmt.Fprintf(os.Stderr, "cli: unsetting %s: %v\n", key, err)
			os.Exit(1)
		}
	}

	os.Exit(m.Run())
}
