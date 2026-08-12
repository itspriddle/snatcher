package commands

import (
	"github.com/spf13/cobra"
)

// NewServerCmd builds the "server" command group, the namespace for every
// server-scoped action. It owns two kinds of subcommands: a registry of named
// servers (add/list/default/ping/remove), modeled after "git remote", and the
// live operations against the selected server (show/update/reset, stats, port,
// free-space). It owns no behavior of its own — a bare "snatcher server" prints
// subcommand help. It mirrors the torrent group's structure (parent command plus
// AddCommand children), including the show/update verb pair.
func NewServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Manage servers, settings, stats, port, and free space",
		Long: "Manage configured servers and the server itself.\n\n" +
			"Maintain the inventory of named servers (add, list, default, ping, " +
			"remove) and operate on the selected server: read its connection and " +
			"session settings (show), change them (update, reset), and inspect its " +
			"statistics, BitTorrent listening port, and download-path free space " +
			"(stats, port, free-space).",
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
	cmd.AddCommand(newServerPingCmd())
	cmd.AddCommand(newServerRemoveCmd())

	// Live operations against the selected server.
	cmd.AddCommand(newServerShowCmd())
	cmd.AddCommand(newServerUpdateCmd())
	cmd.AddCommand(newServerResetCmd())
	cmd.AddCommand(NewStatsCmd())
	cmd.AddCommand(NewPortCmd())
	cmd.AddCommand(NewFreeSpaceCmd())

	return cmd
}
