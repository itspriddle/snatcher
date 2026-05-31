package commands

import (
	"github.com/spf13/cobra"
)

// NewServerCmd builds the "server" command group, the namespace for every
// server-scoped action: settings, stats, and port. It owns no behavior of its
// own — a bare "snatcher server" prints subcommand help — and re-parents the
// existing per-command constructors so their full paths become "snatcher
// server <cmd>". It mirrors the torrent group's structure (parent command plus
// AddCommand children).
func NewServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Manage server settings, stats, and port",
		Long: "Manage the server itself.\n\n" +
			"Group of commands for session settings, statistics, and the " +
			"BitTorrent listening port. Run a subcommand (settings, stats, " +
			"port) to act on the server.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(NewSettingsCmd())
	cmd.AddCommand(NewStatsCmd())
	cmd.AddCommand(NewPortCmd())

	return cmd
}
