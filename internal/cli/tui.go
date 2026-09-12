package cli

import (
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/itspriddle/snatcher/internal/commands"
	"github.com/itspriddle/snatcher/internal/tui"
	"github.com/itspriddle/snatcher/internal/version"
)

// isTerminal reports whether v is an interactive terminal.
//
// It deliberately ignores NO_COLOR, unlike the isTTY helper in
// internal/commands: there, NO_COLOR folds into "not a terminal" because the
// question being asked is whether to draw a colored, aligned table. Here the
// question is whether to launch a full-screen application at all, and
// "monochrome" is not an answer to it. NO_COLOR belongs in T2's palette work.
func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}

	return term.IsTerminal(int(f.Fd()))
}

// launchTUI resolves everything the TUI needs from this invocation and runs it.
//
// Both resolutions happen here rather than inside internal/tui, which imports
// neither internal/commands nor internal/config: the TUI is handed values and a
// dialer. An invalid stored setting therefore fails on the terminal, naming the
// config key, before the alt screen is entered.
func launchTUI(cmd *cobra.Command) error {
	settings, err := commands.ResolveClientSettings(cmd)
	if err != nil {
		return err
	}

	return tui.Run(cmd.Context(), tui.Deps{
		Dial:         commands.Dialer(cmd),
		Theme:        settings.Theme,
		NerdFont:     settings.NerdFont,
		PollInterval: settings.PollInterval,
		Version:      version.FullVersion(),
	})
}
