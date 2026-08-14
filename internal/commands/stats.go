package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// NewStatsCmd builds the "stats" command, which dials the backend, fetches
// session statistics, and renders them as an aligned plaintext summary (the
// default), as JSON (--json), or filtered through jq (--jq). It carries its own
// local --config flag, like the auth subcommands.
func NewStatsCmd() *cobra.Command {
	var (
		configPath string
		server     string
		asJSON     bool
		jqExpr     string
	)

	cmd := &cobra.Command{
		Use:           "stats",
		Short:         "Show session statistics",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			backend, serverURL, err := dial(cmd, configPath, server)
			if err != nil {
				return err
			}

			stats, err := backend.Stats(cmd.Context())
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), stats, jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), stats)
			}

			writeStatsPlaintext(cmd, stats)

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// writeStatsPlaintext renders stats as an aligned, human-readable summary using
// the output package's byte/speed formatters.
func writeStatsPlaintext(cmd *cobra.Command, stats transmission.Stats) {
	out := cmd.OutOrStdout()

	_, _ = fmt.Fprintf(out, "Torrents:        %d total, %d active, %d paused\n",
		stats.TorrentCount, stats.ActiveTorrentCount, stats.PausedTorrentCount)
	_, _ = fmt.Fprintf(out, "Download speed:  %s\n", output.Speed(stats.DownloadSpeed))
	_, _ = fmt.Fprintf(out, "Upload speed:    %s\n", output.Speed(stats.UploadSpeed))

	_, _ = fmt.Fprintln(out, "Cumulative:")
	writeSnapshot(cmd, stats.CumulativeStats)

	_, _ = fmt.Fprintln(out, "Current session:")
	writeSnapshot(cmd, stats.CurrentStats)
}

// writeSnapshot renders a single stats snapshot's uploaded/downloaded totals
// indented beneath its heading.
func writeSnapshot(cmd *cobra.Command, snap transmission.StatsSnapshot) {
	out := cmd.OutOrStdout()

	_, _ = fmt.Fprintf(out, "  Uploaded:      %s\n", output.Bytes(snap.UploadedBytes))
	_, _ = fmt.Fprintf(out, "  Downloaded:    %s\n", output.Bytes(snap.DownloadedBytes))
}
