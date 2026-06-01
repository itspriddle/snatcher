package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/torrent"
)

// NewVerifyCmd builds the "verify" command, which queues local-data
// verification (a re-hash of downloaded files against torrent metadata) for the
// torrents selected by positional id/hash arguments (or --all) and confirms by
// naming the affected torrents. It mirrors stop's mutation pattern and carries
// the same --config, --json, and --jq flags, plus an --all flag. Render
// precedence mirrors list: --jq → --json → plaintext.
func NewVerifyCmd() *cobra.Command {
	var (
		configPath string
		server     string
		asJSON     bool
		jqExpr     string
		all        bool
	)

	cmd := &cobra.Command{
		Use:   "verify [id|hash]...",
		Short: "Verify local torrent data",
		Long: "Verify local torrent data by queueing a re-hash of downloaded files.\n\n" +
			"Select torrents by numeric id, 40-character SHA1 hash, or --all. " +
			"At least one selector is required; a bare 'snatcher torrent verify' is an error, " +
			"not a no-op.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := torrent.ParseSelector(args, all)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(configPath, server)
			if err != nil {
				return err
			}

			if err := backend.VerifyTorrents(cmd.Context(), sel); err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			torrents, err := backend.ListTorrents(cmd.Context(), torrent.Selector{All: true})
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			result, err := resolveMutation("verify", sel, torrents)
			if err != nil {
				return err
			}

			return renderMutation(cmd, result, jqExpr, asJSON)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVar(&all, "all", false, "verify every torrent")

	return cmd
}
