package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/torrent"
)

// NewSetCmd builds the "set" command, which writes mutable per-torrent fields
// (priority, queue position, peer limit, and the session-limit/sequential
// toggles) on the torrents selected by positional id/hash arguments (or --all).
// It maps to the Transmission RPC torrent-set method.
//
// Only the fields whose flags were explicitly set are written (detected via
// cmd.Flags().Changed), so unspecified fields are never clobbered. With no field
// flag the command is an error rather than a no-op. It carries the same --config,
// --json, --jq, and --all flags as the other mutation commands. Render precedence
// mirrors list: --jq → --json → plaintext.
func NewSetCmd() *cobra.Command {
	var (
		configPath          string
		asJSON              bool
		jqExpr              string
		all                 bool
		priority            string
		queuePosition       int
		peerLimit           int
		honorsSessionLimits bool
		sequentialDownload  bool
	)

	cmd := &cobra.Command{
		Use:   "set [id|hash]...",
		Short: "Change mutable per-torrent fields",
		Long: "Change mutable per-torrent fields on the selected torrents.\n\n" +
			"Select torrents by numeric id, 40-character SHA1 hash, or --all. " +
			"At least one selector is required, and at least one field flag must " +
			"be given; a bare 'snatcher torrent set' is an error, not a no-op. " +
			"Only the fields whose flags are explicitly set are written, so " +
			"unspecified fields are left unchanged.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := torrent.ParseSelector(args, all)
			if err != nil {
				return err
			}

			changes := make(map[string]any)

			if cmd.Flags().Changed("priority") {
				v, err := parsePriority(priority)
				if err != nil {
					return err
				}
				changes["bandwidth-priority"] = v
			}

			if cmd.Flags().Changed("queue-position") {
				if queuePosition < 0 {
					return fmt.Errorf("invalid value for --queue-position: must be 0 or greater")
				}
				changes["queue-position"] = queuePosition
			}

			if cmd.Flags().Changed("peer-limit") {
				if peerLimit < 1 {
					return fmt.Errorf("invalid value for --peer-limit: must be 1 or greater")
				}
				changes["peer-limit"] = peerLimit
			}

			if cmd.Flags().Changed("honors-session-limits") {
				changes["honors-session-limits"] = honorsSessionLimits
			}

			if cmd.Flags().Changed("sequential-download") {
				changes["sequential-download"] = sequentialDownload
			}

			if len(changes) == 0 {
				return fmt.Errorf("nothing to set: provide at least one field flag")
			}

			backend, serverURL, err := dial(configPath)
			if err != nil {
				return err
			}

			if err := backend.SetTorrents(cmd.Context(), sel, changes); err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			torrents, err := backend.ListTorrents(cmd.Context(), nil)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			result, err := resolveMutation("set", sel, torrents)
			if err != nil {
				return err
			}

			result.Fields = changes

			return renderMutation(cmd, result, jqExpr, asJSON)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVar(&all, "all", false, "set the fields on every torrent")
	cmd.Flags().StringVar(&priority, "priority", "", "bandwidth priority: low, normal, or high")
	cmd.Flags().IntVar(&queuePosition, "queue-position", 0, "position in the download/seed queue (0-based)")
	cmd.Flags().IntVar(&peerLimit, "peer-limit", 0, "maximum number of peers for the torrent")
	cmd.Flags().BoolVar(&honorsSessionLimits, "honors-session-limits", false, "honor the global session speed limits")
	cmd.Flags().BoolVar(&sequentialDownload, "sequential-download", false, "download pieces in sequential order (Transmission 4.1+)")

	return cmd
}

// parsePriority maps a --priority value (low, normal, or high, matched
// case-insensitively) to the Transmission bandwidth-priority integer
// (-1, 0, 1). Any other value returns a clear error and the caller performs no
// write.
func parsePriority(raw string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "low":
		return -1, nil
	case "normal":
		return 0, nil
	case "high":
		return 1, nil
	default:
		return 0, fmt.Errorf("invalid value for --priority: must be low, normal, or high")
	}
}
