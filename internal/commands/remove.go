package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/torrent"
)

// NewRemoveCmd builds the "remove" command, which removes the torrents selected
// by positional id/hash arguments (or --all) and confirms by naming the affected
// torrents. It is the first destructive mutation command, so it adds a
// confirmation gate: a removal requires an explicit --yes/-y. With --delete-data
// the downloaded local files are deleted too. It carries the same --config,
// --json, --jq, and --all flags as the other mutation commands. Render precedence
// mirrors list: --jq → --json → plaintext.
//
// Unlike stop, remove lists the torrents *before* removing them so their names
// are available for the confirmation and output (the torrents are gone after).
func NewRemoveCmd() *cobra.Command {
	var (
		configPath string
		asJSON     bool
		jqExpr     string
		all        bool
		deleteData bool
		yes        bool
	)

	cmd := &cobra.Command{
		Use:   "remove [id|hash]...",
		Short: "Remove torrents",
		Long: "Remove the selected torrents from the session.\n\n" +
			"Select torrents by numeric id, 40-character SHA1 hash, or --all. " +
			"At least one selector is required; a bare 'snatcher remove' is an error, " +
			"not a no-op. Use --delete-data to also delete the downloaded local files. " +
			"Removal is destructive and requires confirmation: pass --yes to proceed.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := torrent.ParseSelector(args, all)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(configPath)
			if err != nil {
				return err
			}

			// List before removing so torrent names are available for the
			// confirmation and output; afterward the torrents are gone.
			torrents, err := backend.ListTorrents(cmd.Context(), nil)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			result, err := resolveMutation("remove", sel, torrents)
			if err != nil {
				return err
			}

			result.DeleteData = &deleteData

			if !yes {
				return fmt.Errorf(
					"refusing to remove %s without confirmation; pass --yes to proceed",
					pluralizeTorrents(result.Count),
				)
			}

			if err := backend.RemoveTorrents(cmd.Context(), sel, deleteData); err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			return renderMutation(cmd, result, jqExpr, asJSON)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVar(&all, "all", false, "remove every torrent")
	cmd.Flags().BoolVar(&deleteData, "delete-data", false, "also delete the downloaded local files")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	return cmd
}
