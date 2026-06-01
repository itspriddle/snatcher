package commands

import (
	"github.com/spf13/cobra"
)

// NewServerCmd builds the "server" command group, the namespace for every
// server-scoped action. It owns two kinds of subcommands: a registry of named
// servers (add/list/default/show/ping/remove), modeled after "git remote", and
// the live operations against the selected server (settings, stats, port). It
// owns no behavior of its own — a bare "snatcher server" prints subcommand help.
// It mirrors the torrent group's structure (parent command plus AddCommand
// children).
func NewServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Manage servers, settings, stats, port, and free space",
		Long: "Manage configured servers and the server itself.\n\n" +
			"Maintain the inventory of named servers (add, list, default, " +
			"show, ping, remove) and operate on the selected server's session " +
			"settings, statistics, BitTorrent listening port, and download-path " +
			"free space (settings, stats, port, free-space).",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	// Named-server registry (local config + keyring).
	cmd.AddCommand(newServerAddCmd())
	cmd.AddCommand(newServerListCmd())
	cmd.AddCommand(newServerDefaultCmd())
	cmd.AddCommand(newServerShowCmd())
	cmd.AddCommand(newServerPingCmd())
	cmd.AddCommand(newServerRemoveCmd())

	// Live operations against the selected server.
	cmd.AddCommand(NewSettingsCmd())
	cmd.AddCommand(NewStatsCmd())
	cmd.AddCommand(NewPortCmd())
	cmd.AddCommand(NewFreeSpaceCmd())

	return cmd
}
