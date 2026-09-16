package cli

import (
	"fmt"
	"os"
	"testing"
)

// The environment companions of the three appearance flags. internal/commands
// keeps its own constants beside the flags they override; they are unexported,
// and this package only needs the names to get them out of the way.
const (
	envTheme      = "SNATCHER_THEME"
	envNoNerdFont = "SNATCHER_NO_NERD_FONT"
	envColorDepth = "SNATCHER_COLOR_DEPTH"
)

// TestMain isolates this package from the environment the suite happens to run
// in, before any test observes it, the same way internal/commands' TestMain
// does.
//
// The tests below run whole commands through the real tree, and the commands
// that read an appearance setting report — or reject — whichever rung supplied
// it. A developer with SNATCHER_THEME exported would steer those results, and
// an invalid one would fail them outright. None of the variables may decide
// anything here; a test that wants one sets it itself with t.Setenv.
func TestMain(m *testing.M) {
	for _, key := range []string{envTheme, envNoNerdFont, envColorDepth} {
		if err := os.Unsetenv(key); err != nil {
			fmt.Fprintf(os.Stderr, "cli: unsetting %s: %v\n", key, err)
			os.Exit(1)
		}
	}

	os.Exit(m.Run())
}
