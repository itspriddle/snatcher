package commands

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// fileRow is one rendered row of the files table: a torrent file plus its
// position in TorrentDetail.Files. The position is not carried on
// transmission.File itself, but it is the index space transmission.FileChanges
// addresses, so it has to travel alongside the file for the index column to
// render it — a user reading an index off this table must be able to paste it
// straight into "torrent files update".
type fileRow struct {
	// Index is the file's 0-based position in TorrentDetail.Files.
	Index int

	transmission.File
}

// fileFields is the files command's field registry: it maps a selectable
// --fields key to the column that renders it, for both the TTY aligned table and
// the piped tab-separated rows. Only name is truncatable, per the one-truncatable-
// column-per-registry invariant nameColumnLimit relies on.
var fileFields = map[string]column[fileRow]{
	"index": {header: "Index", rightAlign: true, value: func(f fileRow) string {
		return fmt.Sprintf("%d", f.Index)
	}},
	"name": {header: "Name", truncate: true, sanitize: true, value: func(f fileRow) string {
		return f.Name
	}},
	"size": {header: "Size", rightAlign: true, value: func(f fileRow) string {
		return output.Bytes(f.Length)
	}},
	"done": {header: "Done", rightAlign: true, value: func(f fileRow) string {
		return output.Bytes(f.BytesCompleted)
	}},
	"percent": {header: "%", rightAlign: true, value: func(f fileRow) string {
		return output.Percent(fileFraction(f.File))
	}},
	"priority": {header: "Priority", value: func(f fileRow) string {
		return filePriority(f.Priority)
	}},
	"wanted": {header: "Wanted", value: func(f fileRow) string {
		return yesNo(f.Wanted)
	}},
}

// fileFieldKeys is the files command's default column set, in order. Every key in
// the registry is in it, so it doubles as the canonical ordering for the --fields
// help text.
var fileFieldKeys = []string{
	"index", "name", "size", "done", "percent", "priority", "wanted",
}

// NewFilesCmd builds the "files" command, which resolves a single torrent by
// numeric id or 40-character info-hash and lists the files it contains. It is the
// CLI equivalent of the TUI detail pane's files tab, and the way to read off the
// 0-based indices "torrent files update" takes. Output honors the same precedence
// as list: --jq, then --json, then plaintext (an aligned table with headers on a
// TTY, header-less tab-separated rows when piped or when NO_COLOR is set).
func NewFilesCmd() *cobra.Command {
	var (
		configPath string
		server     string
		asJSON     bool
		jqExpr     string
		fieldsFlag string
	)

	cmd := &cobra.Command{
		Use:           "files <id|hash>",
		Short:         "List the files inside a torrent",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := transmission.ParseSelector(args, false)
			if err != nil {
				return err
			}

			columns, err := parseFields(fileFields, fileFieldKeys, fieldsFlag)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(cmd, configPath, server)
			if err != nil {
				return err
			}

			match, err := resolveTorrent(cmd, backend, serverURL, sel, args[0])
			if err != nil {
				return err
			}

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), jsonFiles(match.Files), jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), jsonFiles(match.Files))
			}

			writeListPlaintext(cmd, fileRows(match.Files), columns, "No files.")

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().StringVar(&fieldsFlag, "fields", "", fmt.Sprintf("comma-separated columns to show, in order (%s)", strings.Join(fileFieldKeys, ", ")))

	cmd.AddCommand(newFilesUpdateCmd())

	return cmd
}

// newFilesUpdateCmd builds the "files update" subcommand, which changes the
// download priority and the wanted/unwanted selection of individual files inside
// a single torrent — the CLI equivalent of the TUI files tab's action menu.
//
// It is a child of "torrent files", which itself takes a positional selector.
// Cobra resolves args[0] against child command names before treating it as an
// argument, so "torrent files update 5 0" reaches this command and
// "torrent files 5" reaches the parent. That is unambiguous here because a
// selector is either all digits or 40 hex characters and can never be the
// literal word "update".
//
// The update is single-torrent by design: file indices are torrent-relative, so
// index 3 of one torrent names an unrelated file in another and applying one
// index set across several torrents would be incoherent.
func newFilesUpdateCmd() *cobra.Command {
	var (
		configPath string
		server     string
		asJSON     bool
		jqExpr     string
		fieldsFlag string
		changes    fileChangeFlags
	)

	cmd := &cobra.Command{
		Use:   "update <id|hash> <index>...",
		Short: "Change file priority and wanted state",
		Long: "Change the download priority and wanted/unwanted selection of " +
			"individual files inside a single torrent.\n\n" +
			"Select the torrent by numeric id or 40-character SHA1 hash, then " +
			"give one or more file indices. Indices are 0-based and " +
			"torrent-relative — the same numbers the 'index' column of " +
			"'snatcher torrent files' prints. Each index argument may be a " +
			"single index (3), a comma-separated list (0,2,5), an inclusive " +
			"range (2-7), or the literal 'all'; 'all' must be given on its own.\n\n" +
			"At least one of --priority, --wanted, or --unwanted is required; " +
			"an invocation with none of them is an error, not a no-op. " +
			"Every index is validated against the torrent's file count before " +
			"anything is written. The affected files are printed after the " +
			"change, in the same columns as 'snatcher torrent files'.",
		Args:          cobra.MinimumNArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := transmission.ParseSelector(args[:1], false)
			if err != nil {
				return err
			}

			spec, err := parseFileIndexes(args[1:])
			if err != nil {
				return err
			}

			apply, err := changes.resolve(cmd)
			if err != nil {
				return err
			}

			columns, err := parseFields(fileFields, fileFieldKeys, fieldsFlag)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(cmd, configPath, server)
			if err != nil {
				return err
			}

			match, err := resolveTorrent(cmd, backend, serverURL, sel, args[0])
			if err != nil {
				return err
			}

			indexes, err := spec.resolve(len(match.Files))
			if err != nil {
				return err
			}

			target := transmission.Selector{IDs: []int64{match.ID}}
			if err := backend.SetFiles(cmd.Context(), target, apply(indexes)); err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			// Re-read the torrent so the rendered rows show the server's view of
			// the change rather than an optimistic local echo.
			updated, err := backend.TorrentDetail(cmd.Context(), match.ID)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			rows := selectFileRows(updated.Files, indexes)

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), jsonFiles(rowFiles(rows)), jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), jsonFiles(rowFiles(rows)))
			}

			writeListPlaintext(cmd, rows, columns, "No files.")

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().StringVar(&fieldsFlag, "fields", "", fmt.Sprintf("comma-separated columns to show, in order (%s)", strings.Join(fileFieldKeys, ", ")))
	cmd.Flags().StringVar(&changes.priority, "priority", "", "download priority for the selected files: low, normal, or high")
	cmd.Flags().BoolVar(&changes.wanted, "wanted", false, "download the selected files")
	cmd.Flags().BoolVar(&changes.unwanted, "unwanted", false, "do not download the selected files")

	return cmd
}

// fileChangeFlags holds the per-file field flags of "files update", bound by
// newFilesUpdateCmd. It maps 1:1 onto transmission.FileChanges: --wanted fills
// Wanted, --unwanted fills Unwanted, and --priority fills one of PriorityHigh /
// PriorityNormal / PriorityLow.
type fileChangeFlags struct {
	priority string
	wanted   bool
	unwanted bool
}

// resolve validates the flag combination and returns a builder that turns a
// resolved index set into the FileChanges to write. It runs before the command
// dials, so a bad combination costs no round-trip and writes nothing: --wanted
// and --unwanted are mutually exclusive, and an invocation with no field flag at
// all is an error rather than a silent no-op.
func (f fileChangeFlags) resolve(cmd *cobra.Command) (func([]int) transmission.FileChanges, error) {
	if f.wanted && f.unwanted {
		return nil, errors.New("--wanted and --unwanted cannot be used together")
	}

	setPriority := cmd.Flags().Changed("priority")

	if !setPriority && !f.wanted && !f.unwanted {
		return nil, errors.New("nothing to change: provide --priority, --wanted, or --unwanted")
	}

	priority := 0
	if setPriority {
		var err error
		if priority, err = parsePriority(f.priority); err != nil {
			return nil, err
		}
	}

	return func(indexes []int) transmission.FileChanges {
		changes := transmission.FileChanges{}

		switch {
		case f.wanted:
			changes.Wanted = indexes
		case f.unwanted:
			changes.Unwanted = indexes
		}

		if setPriority {
			switch priority {
			case -1:
				changes.PriorityLow = indexes
			case 1:
				changes.PriorityHigh = indexes
			default:
				changes.PriorityNormal = indexes
			}
		}

		return changes
	}, nil
}

// fileIndexRange is one inclusive, 0-based span of file indices parsed from an
// <index> argument. A bare index parses as a range whose start equals its end,
// so the resolver has a single shape to expand.
type fileIndexRange struct {
	start int
	end   int
}

// fileIndexSpec is the parsed <index>... argument list. Ranges are kept
// unexpanded until the torrent's file count is known, so a typo like "0-999999"
// is rejected as out of range instead of first materializing a million indices.
type fileIndexSpec struct {
	// all reports that the literal "all" was given, selecting every file.
	all bool

	// ranges are the explicitly named spans, in argument order.
	ranges []fileIndexRange
}

// parseFileIndexes parses the <index>... arguments of "files update". Each
// argument may hold several comma-separated tokens, and each token is a bare
// index (3), an inclusive range (2-7), or the literal "all" (case-insensitive).
// Tokens may be mixed freely across arguments, except that "all" must be the
// only token given — combining it with another index is contradictory rather
// than redundant, so it is rejected instead of silently ignored.
func parseFileIndexes(args []string) (fileIndexSpec, error) {
	var (
		spec   fileIndexSpec
		tokens int
	)

	for _, arg := range args {
		for _, raw := range strings.Split(arg, ",") {
			tokens++

			token := strings.TrimSpace(raw)
			if token == "" {
				return fileIndexSpec{}, fileIndexErr(arg, token, "must not be empty")
			}

			if strings.EqualFold(token, "all") {
				spec.all = true
				continue
			}

			span, err := parseFileIndexRange(arg, token)
			if err != nil {
				return fileIndexSpec{}, err
			}

			spec.ranges = append(spec.ranges, span)
		}
	}

	if spec.all && tokens > 1 {
		return fileIndexSpec{}, errors.New(`"all" must be the only file index given`)
	}

	return spec, nil
}

// parseFileIndexRange parses a single non-"all" token into an inclusive span.
// A leading "-" is not a range separator (there is no valid empty range start),
// so "-1" falls through to the plain-integer path and is rejected as negative.
func parseFileIndexRange(arg, token string) (fileIndexRange, error) {
	if lo, hi, ok := strings.Cut(token, "-"); ok && lo != "" {
		start, err := parseFileIndex(arg, token, lo)
		if err != nil {
			return fileIndexRange{}, err
		}

		end, err := parseFileIndex(arg, token, hi)
		if err != nil {
			return fileIndexRange{}, err
		}

		if start > end {
			return fileIndexRange{}, fileIndexErr(arg, token, "range start must not be greater than range end")
		}

		return fileIndexRange{start: start, end: end}, nil
	}

	index, err := parseFileIndex(arg, token, token)
	if err != nil {
		return fileIndexRange{}, err
	}

	return fileIndexRange{start: index, end: index}, nil
}

// parseFileIndex parses one integer part of a token (the token itself, or one
// side of a range). Indices are 0-based positions in TorrentDetail.Files, so a
// negative value is never meaningful.
func parseFileIndex(arg, token, part string) (int, error) {
	index, err := strconv.Atoi(part)
	if err != nil || index < 0 {
		return 0, fileIndexErr(arg, token, "must be a non-negative integer")
	}

	return index, nil
}

// fileIndexErr builds the error for a malformed index, naming the offending
// token and — when it came from a longer comma-separated argument — the
// argument it appeared in, so the user can find it among several.
func fileIndexErr(arg, token, reason string) error {
	if token == arg {
		return fmt.Errorf("invalid file index %q: %s", arg, reason)
	}

	return fmt.Errorf("invalid file index %q in %q: %s", token, arg, reason)
}

// resolve expands the spec against a torrent's file count into a sorted,
// de-duplicated index set. Every index is checked here, before any write, so an
// out-of-range index leaves the torrent untouched. "all" expands to every file,
// which is an empty set for a torrent with no files.
func (s fileIndexSpec) resolve(count int) ([]int, error) {
	if s.all {
		indexes := make([]int, count)
		for i := range indexes {
			indexes[i] = i
		}

		return indexes, nil
	}

	var indexes []int
	for _, span := range s.ranges {
		// start <= end always holds, so start is the first bound to check; when
		// only end is out of range it is still the value the user typed.
		if span.start >= count {
			return nil, fileIndexRangeErr(span.start, count)
		}
		if span.end >= count {
			return nil, fileIndexRangeErr(span.end, count)
		}

		for i := span.start; i <= span.end; i++ {
			indexes = append(indexes, i)
		}
	}

	slices.Sort(indexes)

	return slices.Compact(indexes), nil
}

// fileIndexRangeErr reports an index the torrent does not have, naming both the
// index and the file count so the valid range is obvious.
func fileIndexRangeErr(index, count int) error {
	return fmt.Errorf("file index %d is out of range: the torrent has %s", index, pluralizeFiles(count))
}

// pluralizeFiles renders a file count with the correctly pluralized noun, e.g.
// "1 file" or "3 files".
func pluralizeFiles(n int) string {
	if n == 1 {
		return "1 file"
	}

	return fmt.Sprintf("%d files", n)
}

// selectFileRows returns the rows for the named indices, in ascending index
// order, each carrying its true 0-based position in files. Indices beyond the
// slice are skipped rather than panicking: the re-fetch after a write is a
// separate round-trip, and the torrent could in principle have changed under it.
func selectFileRows(files []transmission.File, indexes []int) []fileRow {
	rows := make([]fileRow, 0, len(indexes))
	for _, i := range indexes {
		if i < 0 || i >= len(files) {
			continue
		}

		rows = append(rows, fileRow{Index: i, File: files[i]})
	}

	return rows
}

// rowFiles strips the rendering index back off a row set, yielding the
// []transmission.File that --json and --jq emit. Order is preserved, so the
// array is in ascending index order.
func rowFiles(rows []fileRow) []transmission.File {
	files := make([]transmission.File, len(rows))
	for i, row := range rows {
		files[i] = row.File
	}

	return files
}

// jsonFiles normalizes a file slice for structured output so a torrent with no
// files serializes as "[]" rather than "null". The slice is emitted as-is, in
// torrent order, so an element's array position is its index column.
func jsonFiles(files []transmission.File) []transmission.File {
	if files == nil {
		return []transmission.File{}
	}

	return files
}

// fileRows pairs each file with its 0-based position, which is what the plaintext
// index column renders and what transmission.FileChanges addresses.
func fileRows(files []transmission.File) []fileRow {
	rows := make([]fileRow, len(files))
	for i, f := range files {
		rows[i] = fileRow{Index: i, File: f}
	}

	return rows
}

// fileFraction returns a file's completion fraction in the range 0–1. A
// zero-length file is legal in a torrent and is complete by definition — it has
// no bytes to fetch — so it reports 1 rather than dividing by zero.
func fileFraction(f transmission.File) float64 {
	if f.Length <= 0 {
		return 1
	}

	return float64(f.BytesCompleted) / float64(f.Length)
}

// filePriority renders Transmission's per-file priority int (-1 low, 0 normal,
// 1 high). Any other value renders as its decimal integer rather than being
// mislabeled as one of the three known priorities.
func filePriority(priority int) string {
	switch priority {
	case -1:
		return "low"
	case 0:
		return "normal"
	case 1:
		return "high"
	default:
		return fmt.Sprintf("%d", priority)
	}
}
