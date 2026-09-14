package commands

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// trackerFields is the trackers command's field registry: it maps a selectable
// --fields key to the column that renders it, for both the TTY aligned table and
// the piped tab-separated rows. Only announce is truncatable, per the
// one-truncatable-column-per-registry invariant nameColumnLimit relies on.
//
// Every string column is sanitized. A tracker's announce URL, host, and site name
// come from the torrent's own metadata (which the operator did not author), and
// the last announce result is text the tracker itself chose.
//
// internal/tui/detail_table.go mirrors its default columns' headers and
// renderers for the TUI detail pane's trackers tab, under a pinning test; a
// change here fails that test until the mirror follows.
var trackerFields = map[string]column[transmission.Tracker]{
	"announce": {header: "Announce", truncate: true, sanitize: true, value: func(t transmission.Tracker) string {
		return t.Announce
	}},
	"tier": {header: "Tier", rightAlign: true, value: func(t transmission.Tracker) string {
		return strconv.Itoa(t.Tier)
	}},
	"state": {header: "State", value: func(t transmission.Tracker) string {
		return transmission.AnnounceState(t.AnnounceState)
	}},
	"next_announce": {header: "Next Announce", value: func(t transmission.Tracker) string {
		return output.Date(t.NextAnnounceTime)
	}},
	"seeders": {header: "Seeders", rightAlign: true, value: func(t transmission.Tracker) string {
		return trackerCount(t.SeederCount)
	}},
	"leechers": {header: "Leechers", rightAlign: true, value: func(t transmission.Tracker) string {
		return trackerCount(t.LeecherCount)
	}},
	"downloads": {header: "Downloads", rightAlign: true, value: func(t transmission.Tracker) string {
		return trackerCount(t.DownloadCount)
	}},
	"host": {header: "Host", sanitize: true, value: func(t transmission.Tracker) string {
		return t.Host
	}},
	"sitename": {header: "Site", sanitize: true, value: func(t transmission.Tracker) string {
		return t.Sitename
	}},
	"last_result": {header: "Last Result", sanitize: true, value: func(t transmission.Tracker) string {
		return t.LastAnnounceResult
	}},
}

// trackerFieldKeys is the trackers command's default column set, in order. The
// registry also carries host, sitename, and last_result, which are selectable via
// --fields but left out of the default table: the announce URL already names the
// tracker, and a last announce result is a free-text sentence that would dominate
// the row.
var trackerFieldKeys = []string{
	"announce", "tier", "state", "next_announce", "seeders", "leechers", "downloads",
}

// allTrackerFieldKeys is every key the trackers command's --fields accepts: the
// default set plus the opt-in columns. It exists for the --fields help text,
// which must name every selectable key, and is derived so the help can never
// drift from the registry.
var allTrackerFieldKeys = slices.Concat(trackerFieldKeys, []string{"host", "sitename", "last_result"})

// NewTrackersCmd builds the "trackers" command, which resolves a single torrent
// by numeric id or 40-character info-hash and lists its trackers and their
// per-tracker announce stats. It is the CLI equivalent of the TUI detail pane's
// trackers tab, and the way to diagnose a torrent that is not announcing. Output
// honors the same precedence as list: --jq, then --json, then plaintext (an
// aligned table with headers on a TTY, header-less tab-separated rows when piped
// or when NO_COLOR is set).
func NewTrackersCmd() *cobra.Command {
	var (
		server     string
		asJSON     bool
		jqExpr     string
		fieldsFlag string
	)

	cmd := &cobra.Command{
		Use:           "trackers <id|hash>",
		Short:         "List a torrent's trackers and announce stats",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := transmission.ParseSelector(args, false)
			if err != nil {
				return err
			}

			columns, err := parseFields(trackerFields, trackerFieldKeys, fieldsFlag)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(cmd, server)
			if err != nil {
				return err
			}

			match, err := resolveTorrent(cmd, backend, serverURL, sel, args[0])
			if err != nil {
				return err
			}

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), jsonSlice(match.Trackers), jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), jsonSlice(match.Trackers))
			}

			writeListPlaintext(cmd, match.Trackers, columns, "No trackers.")

			return nil
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().StringVar(&fieldsFlag, "fields", "", fmt.Sprintf("comma-separated columns to show, in order (default: %s; also available: %s)", strings.Join(trackerFieldKeys, ", "), strings.Join(allTrackerFieldKeys[len(trackerFieldKeys):], ", ")))

	return cmd
}

// trackerCount renders one of a tracker's scrape counts (seeders, leechers,
// completed downloads). Transmission reports -1 for a count it has not scraped,
// so a negative value renders as "-" rather than as a nonsensical number. The
// wire value is untouched in --json, where -1 stays -1.
func trackerCount(n int) string {
	if n < 0 {
		return "-"
	}

	return strconv.Itoa(n)
}
