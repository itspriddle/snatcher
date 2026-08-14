package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/commands"
	"github.com/itspriddle/snatcher/internal/version"
)

// NewRootCmd creates and returns the root cobra command.
func NewRootCmd() *cobra.Command {
	var showVersion bool

	cmd := &cobra.Command{
		Use:   "snatcher",
		Short: "Transmission BitTorrent CLI client",
		Long:  "Transmission BitTorrent CLI client\n\nA modern CLI/TUI for managing BitTorrent clients, Transmission first.",
		// Checked ahead of every command, at every depth: a bad --theme or
		// SNATCHER_NO_NERD_FONT is an error naming what is accepted, rather than a
		// value silently ignored by the commands that do not read it. No
		// subcommand declares its own persistent pre-run, so this one always runs.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return commands.ValidateGlobalSettings(cmd)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), version.FullVersion())
				return nil
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
	cmd.AddCommand(commands.NewServerCmd())
	cmd.AddCommand(commands.NewTorrentCmd())

	return cmd
}
