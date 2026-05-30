package commands

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/torrent"
)

// NewInfoCmd builds the "info" command, which dials the backend, resolves a
// single torrent by numeric id or 40-character info-hash, and renders its full
// set of fields. The output honors the same precedence as list: --jq filters
// the single torrent object, --json emits it as one JSON object (not an array),
// and the default plaintext is a vertical aligned "key: value" block on a TTY or
// header-less "key<TAB>value" lines when piped. It carries its own local
// --config flag, like list and stats.
func NewInfoCmd() *cobra.Command {
	var (
		configPath string
		asJSON     bool
		jqExpr     string
	)

	cmd := &cobra.Command{
		Use:           "info <id|hash>",
		Short:         "Show details for a single torrent",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := torrent.ParseSelector(args, false)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(configPath)
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

			writeInfoPlaintext(cmd, match, infoColumns())

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// resolveTorrent fetches the single torrent named by sel. A numeric id narrows
// the fetch with the backend's RPC-side id filter; a hash fetches all torrents
// and matches on Hash, consistent with how mutation commands resolve hashes. In
// both cases the result is matched in memory so the command is correct even if a
// backend over-returns. It returns an error with a non-zero exit when no torrent
// matches, naming the original argument (arg).
func resolveTorrent(cmd *cobra.Command, backend torrent.Backend, serverURL string, sel torrent.Selector, arg string) (torrent.Torrent, error) {
	var (
		ids      []int64
		wantID   int64
		byID     bool
		wantHash string
	)

	switch {
	case len(sel.IDs) == 1:
		ids = sel.IDs
		wantID = sel.IDs[0]
		byID = true
	case len(sel.Hashes) == 1:
		wantHash = sel.Hashes[0]
	}

	torrents, err := backend.ListTorrents(cmd.Context(), ids)
	if err != nil {
		return torrent.Torrent{}, fmt.Errorf("connecting to %s: %w", serverURL, err)
	}

	for _, t := range torrents {
		if byID {
			if t.ID == wantID {
				return t, nil
			}
			continue
		}

		if strings.EqualFold(t.Hash, wantHash) {
			return t, nil
		}
	}

	return torrent.Torrent{}, fmt.Errorf("no torrent matched: %s", arg)
}

// infoColumns returns the columns info renders, in registry order — the full
// listFields set. It reuses list's field registry so info and list share
// headers, accessors, alignment, and sanitization.
func infoColumns() []listColumn {
	cols := make([]listColumn, len(allFieldKeys))
	for i, key := range allFieldKeys {
		cols[i] = listFields[key]
	}

	return cols
}

// writeInfoPlaintext renders one torrent's fields as a vertical "key: value"
// block. On a TTY the labels are column-aligned via tabwriter; when piped or
// when NO_COLOR is set, each line is "key<TAB>value" (header-less, scripting-
// stable). Sanitizable fields pass through cellValue's SanitizeTerminal in both
// paths; the JSON/jq paths never reach here.
func writeInfoPlaintext(cmd *cobra.Command, t torrent.Torrent, columns []listColumn) {
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
