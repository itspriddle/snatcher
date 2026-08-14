package commands

import (
	"fmt"
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

	return cmd
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
