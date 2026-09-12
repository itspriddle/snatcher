package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/commands"
	"github.com/itspriddle/snatcher/internal/version"
)

// NewRootCmd creates and returns the root cobra command.
//
// The tree deliberately declares no PersistentPreRunE. The two appearance
// settings — --theme / --no-nerd-font and their environment companions — are
// checked where they are read, by the commands that resolve them, rather than
// ahead of every command in the tree. A stale SNATCHER_THEME left exported in a
// shell profile must not fail "snatcher torrent list", which never renders a
// theme, nor "snatcher config unset client.theme", which is the command that
// repairs it. An invalid value at any rung is still an error naming that rung
// wherever the setting is actually consumed.
func NewRootCmd() *cobra.Command {
	var showVersion bool

	cmd := &cobra.Command{
		Use:   "snatcher",
		Short: "Transmission BitTorrent CLI client",
		Long: "Transmission BitTorrent CLI client\n\n" +
			"A modern CLI/TUI for managing BitTorrent clients, Transmission first.\n\n" +
			"Run without arguments on a terminal to start the TUI.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), version.FullVersion())
				return nil
			}

			// A bare invocation on a terminal starts the TUI; anything else —
			// a subcommand, --help, or a pipe on either side — keeps the CLI
			// behavior. Both streams are checked because the TUI reads keys as
			// well as drawing: a piped stdin has no keys to give it, and a
			// piped stdout is someone asking for text.
			if len(args) == 0 && isTerminal(cmd.InOrStdin()) && isTerminal(cmd.OutOrStdout()) {
				return launchTUI(cmd)
			}

			return cmd.Help()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.Flags().BoolVar(&showVersion, "version", false, "Print version information and exit")

	// The tree-wide flags are declared once, here, and inherited by every
	// subcommand at every depth. See commands.AddGlobalFlags.
	commands.AddGlobalFlags(cmd)

	cmd.AddCommand(commands.NewAPICmd())
	cmd.AddCommand(commands.NewConfigCmd())
	cmd.AddCommand(commands.NewSearchCmd())
	cmd.AddCommand(commands.NewServerCmd())
	cmd.AddCommand(commands.NewTorrentCmd())

	return cmd
}
