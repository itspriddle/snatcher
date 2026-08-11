package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// NewUpdateCmd builds the "update" command, which writes mutable per-torrent
// fields (priority, queue position, peer limit, the session-limit/sequential
// toggles, the speed and seeding limits, and the labels, tracker list, and
// bandwidth group) on the torrents selected by positional id/hash arguments (or
// --all). It maps to the Transmission RPC torrent-set method.
//
// Only the fields whose flags were explicitly set are written (detected via
// cmd.Flags().Changed), so unspecified fields are never clobbered. With no field
// flag the command is an error rather than a no-op. It carries the same --config,
// --json, --jq, and --all flags as the other mutation commands. Render precedence
// mirrors list: --jq → --json → plaintext.
func NewUpdateCmd() *cobra.Command {
	var (
		configPath string
		server     string
		asJSON     bool
		jqExpr     string
		all        bool
		flags      updateFlags
	)

	cmd := &cobra.Command{
		Use:   "update [id|hash]...",
		Short: "Change mutable per-torrent fields",
		Long: "Change mutable per-torrent fields on the selected torrents.\n\n" +
			"Select torrents by numeric id, 40-character SHA1 hash, or --all. " +
			"At least one selector is required, and at least one field flag must " +
			"be given; a bare 'snatcher torrent update' is an error, not a no-op. " +
			"Only the fields whose flags are explicitly set are written, so " +
			"unspecified fields are left unchanged.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := transmission.ParseSelector(args, all)
			if err != nil {
				return err
			}

			changes, err := flags.collectChanges(cmd)
			if err != nil {
				return err
			}

			if len(changes) == 0 {
				return fmt.Errorf("nothing to update: provide at least one field flag")
			}

			backend, serverURL, err := dial(configPath, server)
			if err != nil {
				return err
			}

			if err := backend.SetTorrents(cmd.Context(), sel, changes); err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			torrents, err := backend.ListTorrents(cmd.Context(), transmission.Selector{All: true})
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			result, err := resolveMutation("update", sel, torrents)
			if err != nil {
				return err
			}

			result.Fields = changes

			return renderMutation(cmd, result, jqExpr, asJSON)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVar(&all, "all", false, "update the fields on every torrent")
	cmd.Flags().StringVar(&flags.priority, "priority", "", "bandwidth priority: low, normal, or high")
	cmd.Flags().IntVar(&flags.queuePosition, "queue-position", 0, "position in the download/seed queue (0-based)")
	cmd.Flags().IntVar(&flags.peerLimit, "peer-limit", 0, "maximum number of peers for the torrent")
	cmd.Flags().BoolVar(&flags.honorsSessionLimits, "honors-session-limits", false, "honor the global session speed limits")
	cmd.Flags().BoolVar(&flags.sequentialDownload, "sequential-download", false, "download pieces in sequential order (Transmission 4.1+)")
	cmd.Flags().StringVar(&flags.downloadLimit, "download-limit", "", "download speed limit in kB/s, or 'off' to disable")
	cmd.Flags().StringVar(&flags.uploadLimit, "upload-limit", "", "upload speed limit in kB/s, or 'off' to disable")
	cmd.Flags().StringVar(&flags.seedRatio, "seed-ratio", "", "seed ratio limit, or 'global'/'unlimited'")
	cmd.Flags().StringVar(&flags.seedIdle, "seed-idle", "", "seed idle limit in minutes or as a duration (30m, 1h), or 'global'/'unlimited'")
	cmd.Flags().StringArrayVar(&flags.labels, "label", nil, "label to apply, replacing the torrent's labels (repeatable)")
	cmd.Flags().BoolVar(&flags.clearLabels, "clear-labels", false, "remove all labels from the torrent")
	cmd.Flags().StringArrayVar(&flags.trackers, "tracker", nil, "announce URL, replacing the torrent's tracker list (repeatable)")
	cmd.Flags().BoolVar(&flags.clearTrackers, "clear-trackers", false, "remove all trackers from the torrent")
	cmd.Flags().StringVar(&flags.group, "group", "", "bandwidth group name")

	return cmd
}

// updateFlags holds the per-torrent field flag values for the update command,
// bound by NewUpdateCmd. collectChanges reads the values that were explicitly set
// into a neutral kebab-case changes map.
type updateFlags struct {
	priority            string
	queuePosition       int
	peerLimit           int
	honorsSessionLimits bool
	sequentialDownload  bool
	downloadLimit       string
	uploadLimit         string
	seedRatio           string
	seedIdle            string
	labels              []string
	clearLabels         bool
	trackers            []string
	clearTrackers       bool
	group               string
}

// collectChanges builds the neutral kebab-case changes map from the flags that
// were explicitly set (detected via cmd.Flags().Changed), validating each value
// and returning a clear error (and no map entry) on bad input. Unset flags are
// never written, so unspecified fields are left untouched on the server.
func (f *updateFlags) collectChanges(cmd *cobra.Command) (map[string]any, error) {
	changes := make(map[string]any)

	if cmd.Flags().Changed("priority") {
		v, err := parsePriority(f.priority)
		if err != nil {
			return nil, err
		}
		changes["bandwidth-priority"] = v
	}

	if cmd.Flags().Changed("queue-position") {
		if f.queuePosition < 0 {
			return nil, fmt.Errorf("invalid value for --queue-position: must be 0 or greater")
		}
		changes["queue-position"] = f.queuePosition
	}

	if cmd.Flags().Changed("peer-limit") {
		if f.peerLimit < 1 {
			return nil, fmt.Errorf("invalid value for --peer-limit: must be 1 or greater")
		}
		changes["peer-limit"] = f.peerLimit
	}

	if cmd.Flags().Changed("honors-session-limits") {
		changes["honors-session-limits"] = f.honorsSessionLimits
	}

	if cmd.Flags().Changed("sequential-download") {
		changes["sequential-download"] = f.sequentialDownload
	}

	if cmd.Flags().Changed("download-limit") {
		if err := applyRateLimit(changes, "download", f.downloadLimit); err != nil {
			return nil, err
		}
	}

	if cmd.Flags().Changed("upload-limit") {
		if err := applyRateLimit(changes, "upload", f.uploadLimit); err != nil {
			return nil, err
		}
	}

	if cmd.Flags().Changed("seed-ratio") {
		if err := applySeedRatio(changes, f.seedRatio); err != nil {
			return nil, err
		}
	}

	if cmd.Flags().Changed("seed-idle") {
		if err := applySeedIdle(changes, f.seedIdle); err != nil {
			return nil, err
		}
	}

	if err := applyLabels(cmd, changes, f.labels, f.clearLabels); err != nil {
		return nil, err
	}

	if err := applyTrackers(cmd, changes, f.trackers, f.clearTrackers); err != nil {
		return nil, err
	}

	if cmd.Flags().Changed("group") {
		changes["group"] = f.group
	}

	return changes, nil
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

// applyRateLimit writes the paired <kind>-limit / <kind>-limited keys for a
// --download-limit / --upload-limit value. "off" (case-insensitive) disables the
// limit by setting <kind>-limited=false and leaves the limit value untouched; a
// non-negative integer sets the limit (in kB/s) and enables it via
// <kind>-limited=true. Any other value is rejected.
func applyRateLimit(changes map[string]any, kind, raw string) error {
	value := strings.TrimSpace(raw)

	if strings.EqualFold(value, "off") {
		changes[kind+"-limited"] = false
		return nil
	}

	kbps, err := strconv.Atoi(value)
	if err != nil || kbps < 0 {
		return fmt.Errorf("invalid value for --%s-limit: must be a non-negative integer (kB/s) or 'off'", kind)
	}

	changes[kind+"-limit"] = kbps
	changes[kind+"-limited"] = true

	return nil
}

// applySeedRatio writes the paired seed-ratio-limit / seed-ratio-mode keys for a
// --seed-ratio value. A numeric ratio sets the limit and seed-ratio-mode=1
// (single); "global" sets mode=0; "unlimited" sets mode=2. Any other value is
// rejected.
func applySeedRatio(changes map[string]any, raw string) error {
	value := strings.TrimSpace(raw)

	switch strings.ToLower(value) {
	case "global":
		changes["seed-ratio-mode"] = 0
		return nil
	case "unlimited":
		changes["seed-ratio-mode"] = 2
		return nil
	}

	ratio, err := strconv.ParseFloat(value, 64)
	if err != nil || ratio < 0 {
		return fmt.Errorf("invalid value for --seed-ratio: must be a non-negative number, 'global', or 'unlimited'")
	}

	changes["seed-ratio-limit"] = ratio
	changes["seed-ratio-mode"] = 1

	return nil
}

// applySeedIdle writes the paired seed-idle-limit / seed-idle-mode keys for a
// --seed-idle value. A bare integer (minutes) or a Go-style duration string
// (30m, 1h, 1h30m) sets the limit in whole minutes and seed-idle-mode=1
// (single); "global" sets mode=0; "unlimited" sets mode=2. Durations with
// sub-minute precision (e.g. 90s) and any other value are rejected.
func applySeedIdle(changes map[string]any, raw string) error {
	value := strings.TrimSpace(raw)

	switch strings.ToLower(value) {
	case "global":
		changes["seed-idle-mode"] = 0
		return nil
	case "unlimited":
		changes["seed-idle-mode"] = 2
		return nil
	}

	if minutes, err := strconv.Atoi(value); err == nil {
		if minutes < 0 {
			return invalidSeedIdleErr()
		}
		changes["seed-idle-limit"] = minutes
		changes["seed-idle-mode"] = 1
		return nil
	}

	d, err := time.ParseDuration(value)
	if err != nil || d < 0 || d%time.Minute != 0 {
		return invalidSeedIdleErr()
	}

	changes["seed-idle-limit"] = int(d / time.Minute)
	changes["seed-idle-mode"] = 1

	return nil
}

func invalidSeedIdleErr() error {
	return fmt.Errorf("invalid value for --seed-idle: must be whole minutes (integer or duration like 30m/1h), 'global', or 'unlimited'")
}

// applyLabels writes the labels key from --label / --clear-labels. --label
// (repeatable) replaces the torrent's full label set; --clear-labels sets it to
// an empty list. The two flags are mutually exclusive.
func applyLabels(cmd *cobra.Command, changes map[string]any, labels []string, clearAll bool) error {
	hasLabels := cmd.Flags().Changed("label")

	if hasLabels && clearAll {
		return fmt.Errorf("--label and --clear-labels cannot be used together")
	}

	switch {
	case clearAll:
		changes["labels"] = []string{}
	case hasLabels:
		changes["labels"] = labels
	}

	return nil
}

// applyTrackers writes the tracker-list key from --tracker / --clear-trackers.
// --tracker (repeatable) replaces the torrent's announce list, newline-joined as
// Transmission's tracker_list expects; --clear-trackers sets it to the empty
// string. The two flags are mutually exclusive.
func applyTrackers(cmd *cobra.Command, changes map[string]any, trackers []string, clearAll bool) error {
	hasTrackers := cmd.Flags().Changed("tracker")

	if hasTrackers && clearAll {
		return fmt.Errorf("--tracker and --clear-trackers cannot be used together")
	}

	switch {
	case clearAll:
		changes["tracker-list"] = ""
	case hasTrackers:
		changes["tracker-list"] = strings.Join(trackers, "\n")
	}

	return nil
}
