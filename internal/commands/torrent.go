package commands

import (
	"github.com/spf13/cobra"
)

// NewTorrentCmd builds the "torrent" command group, the namespace for every
// torrent-scoped action: list, start, stop, verify, reannounce, remove, and
// upload. It owns no behavior of its own — a bare "snatcher torrent" prints
// subcommand help — and re-parents the existing per-command constructors so
// their full paths become "snatcher torrent <cmd>". It mirrors the settings
// group's structure (parent command plus AddCommand children), differing only
// in that bare invocation shows help rather than running a default action.
func NewTorrentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "torrent",
		Short: "Manage torrents",
		Long: "Manage torrents.\n\n" +
			"Group of commands for listing, adding, controlling, and removing " +
			"torrents on the server. Run a subcommand (list, start, stop, " +
			"verify, reannounce, move, remove, upload) to act on torrents.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(NewListCmd())
	cmd.AddCommand(NewMoveCmd())
	cmd.AddCommand(NewQueueCmd())
	cmd.AddCommand(NewReannounceCmd())
	cmd.AddCommand(NewRemoveCmd())
	cmd.AddCommand(NewStartCmd())
	cmd.AddCommand(NewStopCmd())
	cmd.AddCommand(NewUploadCmd())
	cmd.AddCommand(NewVerifyCmd())

	return cmd
}
