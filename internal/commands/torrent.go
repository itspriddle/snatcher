package commands

import (
	"github.com/spf13/cobra"
)

// NewTorrentCmd builds the "torrent" command group, the namespace for every
// torrent-scoped action. It owns no behavior of its own — a bare "snatcher
// torrent" prints subcommand help — and re-parents the existing per-command
// constructors so their full paths become "snatcher torrent <cmd>". It mirrors
// the settings group's structure (parent command plus AddCommand children),
// differing only in that bare invocation shows help rather than running a
// default action.
//
// The subcommands are enumerated once, in Long, in the same order as the man
// page's .SS "torrent" summary line. That list is pinned to the registered set
// by TestTorrentGroupLongNamesEverySubcommand — neither check-surface nor
// check-man guards help prose, and this sentence has drifted before.
func NewTorrentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "torrent",
		Short: "Manage torrents",
		Long: "Manage torrents.\n\n" +
			"Group of commands for listing, inspecting, adding, controlling, and " +
			"removing torrents on the server. Run a subcommand (add, show, files, " +
			"peers, trackers, list, start, stop, verify, reannounce, move, queue, " +
			"remove, update) to act on torrents.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(NewAddCmd())
	cmd.AddCommand(NewFilesCmd())
	cmd.AddCommand(NewListCmd())
	cmd.AddCommand(NewMoveCmd())
	cmd.AddCommand(NewPeersCmd())
	cmd.AddCommand(NewQueueCmd())
	cmd.AddCommand(NewReannounceCmd())
	cmd.AddCommand(NewRemoveCmd())
	cmd.AddCommand(NewShowCmd())
	cmd.AddCommand(NewStartCmd())
	cmd.AddCommand(NewStopCmd())
	cmd.AddCommand(NewTrackersCmd())
	cmd.AddCommand(NewUpdateCmd())
	cmd.AddCommand(NewVerifyCmd())

	return cmd
}
