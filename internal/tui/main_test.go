package tui

import (
	"fmt"
	"os"
	"testing"
)

// TestMain isolates the TUI suite from the environment the run happens in.
//
// Rendering is what these tests assert, and rendering reads the environment:
// SNATCHER_THEME, SNATCHER_NO_NERD_FONT, and SNATCHER_COLOR_DEPTH would change which glyphs and colors
// a golden is compared against, and NO_COLOR would strip styling from a test
// whose whole point is that the focused pane is styled differently. TERM and
// COLORTERM are pinned rather than unset so lipgloss resolves one color profile
// no matter whose terminal the suite runs in.
func TestMain(m *testing.M) {
	for _, key := range []string{"SNATCHER_THEME", "SNATCHER_NO_NERD_FONT", "SNATCHER_COLOR_DEPTH", "NO_COLOR"} {
		if err := os.Unsetenv(key); err != nil {
			fmt.Fprintf(os.Stderr, "tui: unsetting %s: %v\n", key, err)
			os.Exit(1)
		}
	}

	for key, value := range map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor"} {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintf(os.Stderr, "tui: setting %s: %v\n", key, err)
			os.Exit(1)
		}
	}

	os.Exit(m.Run())
}
