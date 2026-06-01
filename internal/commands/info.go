package commands

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// NewInfoCmd builds the "info" command, which dials the backend, resolves a
// single torrent by numeric id or 40-character info-hash, and renders its full
// set of fields. The output honors the same precedence as list: --jq filters
// the single torrent object, --json emits it as one JSON object (not an array),
// and the default plaintext is a vertical aligned "key: value" block on a TTY or
// header-less "key<TAB>value" lines when piped. --fields selects and orders the
// plaintext lines (defaulting to the full field set); it has no effect on
// --json (always the full object) or --jq (which selects fields itself). It
// carries its own local --config flag, like list and stats.
func NewInfoCmd() *cobra.Command {
	var (
		configPath string
		server     string
		asJSON     bool
		jqExpr     string
		fieldsFlag string
	)

	cmd := &cobra.Command{
		Use:           "info <id|hash>",
		Short:         "Show details for a single torrent",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := transmission.ParseSelector(args, false)
			if err != nil {
				return err
			}

			columns, err := parseFields(fieldsFlag, allFieldKeys)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(configPath, server)
			if err != nil {
				return err
			}

			match, err := resolveTorrent(cmd, backend, serverURL, sel, args[0])
			if err != nil {
				return err
			}

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), match, jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), match)
			}

			writeInfoPlaintext(cmd, match.Torrent, columns)

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().StringVar(&fieldsFlag, "fields", "", "comma-separated fields to show, in order (default: all; id, hash, name, status, done, size, down, up, ratio, eta, peers, added, download_dir, error, labels)")

	return cmd
}

// resolveTorrent fetches the full detail view of the single torrent named by
// sel. An id selector goes straight to TorrentDetail in one round-trip. A hash
// selector first resolves the hash to its numeric id with a cheap, server-side-
// filtered list (PollFields, one round-trip), then fetches the detail by that id
// — TorrentDetail is keyed by id, so a hash costs one extra lookup. It returns an
// error with a non-zero exit when no torrent matches, naming the original
// argument (arg).
func resolveTorrent(cmd *cobra.Command, backend *transmission.Client, serverURL string, sel transmission.Selector, arg string) (transmission.TorrentDetail, error) {
	id, err := resolveTorrentID(cmd, backend, serverURL, sel, arg)
	if err != nil {
		return transmission.TorrentDetail{}, err
	}

	detail, err := backend.TorrentDetail(cmd.Context(), id)
	if err != nil {
		return transmission.TorrentDetail{}, fmt.Errorf("connecting to %s: %w", serverURL, err)
	}

	return detail, nil
}

// resolveTorrentID returns the numeric id for the single torrent named by sel.
// An id selector returns its id directly. A hash selector is filtered server-side
// in one round-trip (PollFields, cheap) and matched in memory so the lookup is
// correct even if a backend over-returns. It errors, naming arg, when no torrent
// matches.
func resolveTorrentID(cmd *cobra.Command, backend *transmission.Client, serverURL string, sel transmission.Selector, arg string) (int64, error) {
	if len(sel.IDs) == 1 {
		return sel.IDs[0], nil
	}

	wantHash := sel.Hashes[0]

	torrents, err := backend.ListTorrents(cmd.Context(), sel, transmission.PollFields...)
	if err != nil {
		return 0, fmt.Errorf("connecting to %s: %w", serverURL, err)
	}

	for _, t := range torrents {
		if strings.EqualFold(t.Hash, wantHash) {
			return t.ID, nil
		}
	}

	return 0, fmt.Errorf("no torrent matched: %s", arg)
}

// writeInfoPlaintext renders one torrent's fields as a vertical "key: value"
// block. On a TTY the labels are column-aligned via tabwriter; when piped or
// when NO_COLOR is set, each line is "key<TAB>value" (header-less, scripting-
// stable). Sanitizable fields pass through cellValue's SanitizeTerminal in both
// paths; the JSON/jq paths never reach here.
func writeInfoPlaintext(cmd *cobra.Command, t transmission.Torrent, columns []listColumn) {
	out := cmd.OutOrStdout()

	if isTTY(out) {
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, col := range columns {
			_, _ = fmt.Fprintf(tw, "%s:\t%s\n", col.header, cellValue(col, t))
		}
		_ = tw.Flush()

		return
	}

	for _, col := range columns {
		_, _ = fmt.Fprintf(out, "%s\t%s\n", col.header, cellValue(col, t))
	}
}
