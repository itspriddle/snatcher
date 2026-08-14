package commands

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// detailFields is the show command's field registry. The 15 list columns are
// promoted rather than restated, so they keep exactly one definition of their
// header, accessor, alignment, and sanitization; the detail-only scalars that a
// list Torrent does not carry are added on top. Its keys are the TorrentDetail
// json tags, which are the stable scripting surface — a --fields key and a
// --json key name the same thing.
var detailFields = newDetailFields()

// newDetailFields builds the show registry: promote(listFields) plus the
// detail-only scalars. It is a function rather than a composite literal so the
// promoted columns and the detail-only ones live in one map without the 15
// shared accessors being written out a second time by hand.
func newDetailFields() map[string]column[transmission.TorrentDetail] {
	registry := promote(listFields)

	maps.Copy(registry, map[string]column[transmission.TorrentDetail]{
		"magnet_link": {header: "Magnet Link", sanitize: true, value: func(d transmission.TorrentDetail) string {
			return d.MagnetLink
		}},
		"comment": {header: "Comment", sanitize: true, value: func(d transmission.TorrentDetail) string {
			return d.Comment
		}},
		"creator": {header: "Creator", sanitize: true, value: func(d transmission.TorrentDetail) string {
			return d.Creator
		}},
		"date_created": {header: "Created", value: func(d transmission.TorrentDetail) string {
			return output.Date(d.DateCreated)
		}},
		"is_private": {header: "Private", value: func(d transmission.TorrentDetail) string {
			return yesNo(d.IsPrivate)
		}},
		"piece_count": {header: "Pieces", rightAlign: true, value: func(d transmission.TorrentDetail) string {
			return strconv.FormatInt(d.PieceCount, 10)
		}},
		"piece_size": {header: "Piece Size", rightAlign: true, value: func(d transmission.TorrentDetail) string {
			return output.Bytes(d.PieceSize)
		}},
		"downloaded_ever": {header: "Downloaded Ever", rightAlign: true, value: func(d transmission.TorrentDetail) string {
			return output.Bytes(d.DownloadedEver)
		}},
		"uploaded_ever": {header: "Uploaded Ever", rightAlign: true, value: func(d transmission.TorrentDetail) string {
			return output.Bytes(d.UploadedEver)
		}},
		"corrupt_ever": {header: "Corrupt Ever", rightAlign: true, value: func(d transmission.TorrentDetail) string {
			return output.Bytes(d.CorruptEver)
		}},
		"have_valid": {header: "Have Valid", rightAlign: true, value: func(d transmission.TorrentDetail) string {
			return output.Bytes(d.HaveValid)
		}},
		"seconds_downloading": {header: "Downloading Time", value: func(d transmission.TorrentDetail) string {
			return output.Duration(d.SecondsDownloading)
		}},
		"seconds_seeding": {header: "Seeding Time", value: func(d transmission.TorrentDetail) string {
			return output.Duration(d.SecondsSeeding)
		}},
		"recheck_progress": {header: "Recheck", rightAlign: true, value: func(d transmission.TorrentDetail) string {
			return output.Percent(d.RecheckProgress)
		}},
		"metadata_percent_complete": {header: "Metadata", rightAlign: true, value: func(d transmission.TorrentDetail) string {
			return output.Percent(d.MetadataPercentComplete)
		}},
	})

	return registry
}

// detailFieldKeys is show's default field set, in order: every list field
// followed by the detail-only scalars.
//
// magnet_link is deliberately absent. It is selectable with --fields — that is
// the CLI equivalent of the TUI's "y m" yank-magnet key — but a magnet URI is a
// single wall-of-text line that would dominate the default block.
var detailFieldKeys = slices.Concat(allFieldKeys, []string{
	"comment", "creator", "date_created", "is_private", "piece_count",
	"piece_size", "downloaded_ever", "uploaded_ever", "corrupt_ever",
	"have_valid", "seconds_downloading", "seconds_seeding",
	"recheck_progress", "metadata_percent_complete",
})

// allDetailFieldKeys is every key show's --fields accepts: the default set plus
// the opt-in magnet_link. It exists for the --fields help text, which must name
// every selectable key, and is derived so the help can never drift from the
// registry.
var allDetailFieldKeys = slices.Concat(detailFieldKeys, []string{"magnet_link"})

// promote lifts a Torrent column registry into a TorrentDetail one by wrapping
// each accessor to read the embedded Torrent. TorrentDetail is a superset of
// Torrent, so every column that renders a torrent renders a detail unchanged;
// promoting keeps the shared columns defined once instead of duplicating
// fifteen accessors that would then have to be kept in step by hand.
func promote(registry map[string]column[transmission.Torrent]) map[string]column[transmission.TorrentDetail] {
	promoted := make(map[string]column[transmission.TorrentDetail], len(registry))
	for key, col := range registry {
		promoted[key] = column[transmission.TorrentDetail]{
			header:     col.header,
			rightAlign: col.rightAlign,
			truncate:   col.truncate,
			sanitize:   col.sanitize,
			value: func(d transmission.TorrentDetail) string {
				return col.value(d.Torrent)
			},
		}
	}

	return promoted
}

// NewShowCmd builds the "show" command, which dials the backend, resolves a
// single torrent by numeric id or 40-character info-hash, and renders its full
// set of fields. The output honors the same precedence as list: --jq filters
// the single torrent object, --json emits it as one JSON object (not an array),
// and the default plaintext is a vertical aligned "key: value" block on a TTY or
// header-less "key<TAB>value" lines when piped — or, when --fields selects
// exactly one field, the bare value alone. --fields selects and orders the
// plaintext lines (defaulting to detailFieldKeys — every field but the opt-in
// magnet_link); it has no effect on
// --json (always the full object) or --jq (which selects fields itself). The
// "info" alias is provided for muscle memory; it is a pure cobra alias and
// changes no output.
func NewShowCmd() *cobra.Command {
	var (
		server     string
		asJSON     bool
		jqExpr     string
		fieldsFlag string
	)

	cmd := &cobra.Command{
		Use:           "show <id|hash>",
		Aliases:       []string{"info"},
		Short:         "Show details for a single torrent",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := transmission.ParseSelector(args, false)
			if err != nil {
				return err
			}

			columns, err := parseFields(detailFields, detailFieldKeys, fieldsFlag)
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
				return output.JQ(cmd.OutOrStdout(), match, jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), match)
			}

			writeShowPlaintext(cmd, match, columns)

			return nil
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().StringVar(&fieldsFlag, "fields", "", fmt.Sprintf("comma-separated fields to show, in order (default: every field except magnet_link; %s)", strings.Join(allDetailFieldKeys, ", ")))

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

// writeShowPlaintext renders one row's fields as a vertical "key: value" block.
// On a TTY the labels are column-aligned via tabwriter; when piped or when
// NO_COLOR is set, each line is "key<TAB>value" (header-less, scripting-
// stable), except that a single selected field is written as its bare value —
// see writeShowPiped. Sanitizable fields pass through cellValue's
// SanitizeTerminal in both paths; the JSON/jq paths never reach here. A
// column's rightAlign and truncate
// are inert on this path — they exist so one column[T] serves both the vertical
// block and the table renderer.
// It mirrors writeListPlaintext's split into a TTY writer and a piped writer so
// each branch can be exercised directly by a test, which a terminal check alone
// makes impossible.
func writeShowPlaintext[T any](cmd *cobra.Command, row T, columns []column[T]) {
	out := cmd.OutOrStdout()

	if isTTY(out) {
		writeShowBlock(out, row, columns)
		return
	}

	writeShowPiped(out, row, columns)
}

// writeShowBlock writes the TTY form of the vertical block: one "Header: value"
// line per column, with the values column-aligned by tabwriter.
func writeShowBlock[T any](w io.Writer, row T, columns []column[T]) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, col := range columns {
		_, _ = fmt.Fprintf(tw, "%s:\t%s\n", col.header, cellValue(col, row))
	}
	_ = tw.Flush()
}

// writeShowPiped writes the header-less "key<TAB>value" form of the vertical
// block — the stable scripting contract, one line per column.
//
// Selecting exactly one field drops the key column too, writing the bare value:
// with nothing to disambiguate, the label is noise a script has to cut off, and
// "show --fields magnet_link <id> | pbcopy" has to yank the URI alone to be the
// equivalent of the TUI's "y m" key. Two or more fields keep the key column,
// which is what makes a multi-field block parseable.
func writeShowPiped[T any](w io.Writer, row T, columns []column[T]) {
	if len(columns) == 1 {
		_, _ = fmt.Fprintln(w, cellValue(columns[0], row))
		return
	}

	for _, col := range columns {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", col.header, cellValue(col, row))
	}
}
