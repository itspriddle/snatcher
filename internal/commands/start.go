package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/torrent"
)

// NewStartCmd builds the "start" command, which resumes the torrents selected by
// positional id/hash arguments (or --all) and confirms by naming the affected
// torrents. It mirrors stop, carrying the same --config, --json, --jq, and --all
// flags plus a --now flag that bypasses the queue (torrent-start-now). Render
// precedence mirrors list: --jq → --json → plaintext.
func NewStartCmd() *cobra.Command {
	var (
		configPath string
		asJSON     bool
		jqExpr     string
		all        bool
		now        bool
	)

	cmd := &cobra.Command{
		Use:   "start [id|hash]...",
		Short: "Start or resume torrents",
		Long: "Start or resume the selected torrents.\n\n" +
			"Select torrents by numeric id, 40-character SHA1 hash, or --all. " +
			"At least one selector is required; a bare 'snatcher torrent start' is an error, " +
			"not a no-op. Use --now to bypass the queue and start immediately.",
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

			if err := backend.StartTorrents(cmd.Context(), sel, now); err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			torrents, err := backend.ListTorrents(cmd.Context(), nil)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			result, err := resolveMutation("start", sel, torrents)
			if err != nil {
				return err
			}

			result.Now = &now

			return renderMutation(cmd, result, jqExpr, asJSON)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVar(&all, "all", false, "start every torrent")
	cmd.Flags().BoolVar(&now, "now", false, "bypass the queue and start immediately")

	return cmd
}
