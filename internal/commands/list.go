package commands

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/torrent"
)

// listColumn describes a single rendered column: its header, how a torrent's
// value is derived, its alignment, whether the Name-style truncation applies on
// a TTY, and whether the rendered cell carries untrusted, server-derived text
// that must be neutralized of terminal control sequences before display.
type listColumn struct {
	header     string
	value      func(torrent.Torrent) string
	rightAlign bool
	truncate   bool
	// sanitize marks columns whose value comes from the remote server or
	// swarm-supplied .torrent metadata (name, labels, error, download dir). Such
	// cells are passed through output.SanitizeTerminal in the human/plaintext
	// render paths (table and piped) so a crafted torrent name cannot inject ANSI
	// escapes. JSON/jq output is a structured contract and is never sanitized.
	sanitize bool
}

// listFields is the field registry: it maps a selectable --fields key to the
// column that renders it. It is the single source of truth for headers,
// accessors, and alignment, shared by both the TTY aligned output and the piped
// tab-separated output. New columns are added here.
var listFields = map[string]listColumn{
	"id": {header: "ID", rightAlign: true, value: func(t torrent.Torrent) string {
		return fmt.Sprintf("%d", t.ID)
	}},
	"hash": {header: "Hash", value: func(t torrent.Torrent) string {
		return t.Hash
	}},
	"name": {header: "Name", truncate: true, sanitize: true, value: func(t torrent.Torrent) string {
		return t.Name
	}},
	"status": {header: "Status", value: func(t torrent.Torrent) string {
		return t.Status.String()
	}},
	"done": {header: "Done", rightAlign: true, value: func(t torrent.Torrent) string {
		return output.Percent(t.PercentDone)
	}},
	"size": {header: "Size", rightAlign: true, value: func(t torrent.Torrent) string {
		return output.Bytes(t.TotalSize)
	}},
	"down": {header: "Down", rightAlign: true, value: func(t torrent.Torrent) string {
		return output.Speed(t.RateDownload)
	}},
	"up": {header: "Up", rightAlign: true, value: func(t torrent.Torrent) string {
		return output.Speed(t.RateUpload)
	}},
	"ratio": {header: "Ratio", rightAlign: true, value: func(t torrent.Torrent) string {
		return output.Ratio(t.UploadRatio)
	}},
	"eta": {header: "ETA", rightAlign: true, value: func(t torrent.Torrent) string {
		return formatETA(t.ETA)
	}},
	"peers": {header: "Peers", rightAlign: true, value: func(t torrent.Torrent) string {
		return fmt.Sprintf("%d", t.PeersConnected)
	}},
	"added": {header: "Added", value: func(t torrent.Torrent) string {
		return output.Date(t.AddedDate)
	}},
	"download_dir": {header: "Download Dir", sanitize: true, value: func(t torrent.Torrent) string {
		return t.DownloadDir
	}},
	"error": {header: "Error", sanitize: true, value: func(t torrent.Torrent) string {
		return t.Error
	}},
	"labels": {header: "Labels", sanitize: true, value: func(t torrent.Torrent) string {
		return strings.Join(t.Labels, ",")
	}},
}

// defaultFieldKeys is the default column set, in order, shown when --fields is
// not given (the US-003 default: ID, Name, Status, Done, Size, Down, Up, Ratio,
// ETA).
var defaultFieldKeys = []string{
	"id", "name", "status", "done", "size", "down", "up", "ratio", "eta",
}

// allFieldKeys is every selectable field key in registry order. It is the
// canonical ordering of listFields, shared by the --fields help text, the man
// page, and the info command's default (which shows the complete set rather
// than list's smaller defaultFieldKeys).
var allFieldKeys = []string{
	"id", "hash", "name", "status", "done", "size", "down", "up", "ratio",
	"eta", "peers", "added", "download_dir", "error", "labels",
}

// validFieldKeys returns every selectable --fields key in sorted order, for the
// error message listing the valid keys.
func validFieldKeys() string {
	keys := make([]string, 0, len(listFields))
	for k := range listFields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return strings.Join(keys, ", ")
}

// parseFields resolves a --fields value into the ordered columns to render. An
// empty value selects defaults (list passes defaultFieldKeys, info passes the
// full allFieldKeys). An unknown key returns an error listing the valid keys.
// Keys are matched case-insensitively after trimming whitespace.
func parseFields(spec string, defaults []string) ([]listColumn, error) {
	keys := defaults
	if strings.TrimSpace(spec) != "" {
		keys = nil
		for _, raw := range strings.Split(spec, ",") {
			key := strings.ToLower(strings.TrimSpace(raw))
			if key == "" {
				continue
			}
			if _, ok := listFields[key]; !ok {
				return nil, fmt.Errorf("invalid field %q: valid fields are %s", strings.TrimSpace(raw), validFieldKeys())
			}
			keys = append(keys, key)
		}
		if len(keys) == 0 {
			return nil, fmt.Errorf("no fields selected: valid fields are %s", validFieldKeys())
		}
	}

	cols := make([]listColumn, len(keys))
	for i, key := range keys {
		cols[i] = listFields[key]
	}

	return cols, nil
}

// NewListCmd builds the "list" command, which dials the backend, fetches all
// torrents, and renders them as an aligned plaintext table on a TTY, as plain
// tab-separated full-value rows when piped, as JSON (--json), or filtered through
// jq (--jq). It carries its own local --config flag, like stats and the auth
// subcommands.
func NewListCmd() *cobra.Command {
	var (
		configPath string
		asJSON     bool
		jqExpr     string
		filterFlag string
		sortFlag   string
		searchFlag string
		reverse    bool
		fieldsFlag string
	)

	cmd := &cobra.Command{
		Use:           "list",
		Short:         "List torrents",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter, err := torrent.ParseFilter(filterFlag)
			if err != nil {
				return err
			}

			sortField, err := torrent.ParseSortField(sortFlag)
			if err != nil {
				return err
			}

			columns, err := parseFields(fieldsFlag, defaultFieldKeys)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(configPath)
			if err != nil {
				return err
			}

			torrents, err := backend.ListTorrents(cmd.Context(), nil)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			// Apply the in-memory transforms in a fixed order: status filter,
			// then name search, then sort. This order is shared by every output
			// mode (table, piped, JSON/jq).
			torrents = torrent.FilterByStatus(torrents, filter)
			torrents = torrent.SearchByName(torrents, searchFlag)
			torrents = torrent.SortTorrents(torrents, sortField, reverse)

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), jsonTorrents(torrents), jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), jsonTorrents(torrents))
			}

			writeListPlaintext(cmd, torrents, columns)

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().StringVar(&filterFlag, "filter", "all", "filter by status (all, active, downloading, seeding, stopped, verifying, queued-to-verify, queued-to-download, queued-to-seed, error)")
	cmd.Flags().StringVar(&sortFlag, "sort", "queue", "sort by field (queue, name, added, size, progress, ratio, speed)")
	cmd.Flags().StringVar(&searchFlag, "search", "", "filter to torrents whose name contains this text (case-insensitive)")
	cmd.Flags().BoolVar(&reverse, "reverse", false, "reverse the sort order")
	cmd.Flags().StringVar(&fieldsFlag, "fields", "", "comma-separated columns to show (id, hash, name, status, done, size, down, up, ratio, eta, peers, added, download_dir, error, labels)")

	return cmd
}

// jsonTorrents normalizes a torrent slice for structured output so an empty
// result serializes as "[]" rather than "null".
func jsonTorrents(torrents []torrent.Torrent) []torrent.Torrent {
	if torrents == nil {
		return []torrent.Torrent{}
	}

	return torrents
}

// writeListPlaintext renders torrents in the appropriate plaintext mode for the
// command's output: an aligned, header-and-truncation table on a TTY, or plain
// tab-separated, full-value, header-less rows when piped or when NO_COLOR is set.
func writeListPlaintext(cmd *cobra.Command, torrents []torrent.Torrent, columns []listColumn) {
	out := cmd.OutOrStdout()

	if isTTY(out) {
		writeListTable(out, torrents, columns, terminalWidth(out))
		return
	}

	writeListPiped(out, torrents, columns)
}

// cellValue renders a column's value for a torrent, neutralizing terminal
// control sequences for columns whose text is untrusted, server-derived
// (sanitize). It is the single rendering seam shared by the table and piped
// paths so both apply the same protection; the JSON/jq paths never call it.
func cellValue(col listColumn, t torrent.Torrent) string {
	v := col.value(t)
	if col.sanitize {
		v = output.SanitizeTerminal(v)
	}

	return v
}

// writeListPiped writes plain, tab-separated, full-value rows with no header —
// the stable scripting contract. An empty list writes nothing.
func writeListPiped(w io.Writer, torrents []torrent.Torrent, columns []listColumn) {
	for _, t := range torrents {
		cells := make([]string, len(columns))
		for i, col := range columns {
			cells[i] = cellValue(col, t)
		}

		_, _ = fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
}

// writeListTable writes a whitespace-aligned table with a header row, numeric
// columns right-aligned and text columns left-aligned, truncating truncatable
// columns (Name) with an ellipsis so rows fit width. An empty list writes a
// friendly "No torrents." line. A width of 0 disables truncation.
func writeListTable(w io.Writer, torrents []torrent.Torrent, columns []listColumn, width int) {
	if len(torrents) == 0 {
		_, _ = fmt.Fprintln(w, "No torrents.")
		return
	}

	nameLimit := nameColumnLimit(torrents, columns, width)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	headers := make([]string, len(columns))
	for i, col := range columns {
		headers[i] = col.header
	}
	_, _ = fmt.Fprintln(tw, strings.Join(headers, "\t"))

	for _, t := range torrents {
		cells := make([]string, len(columns))
		for i, col := range columns {
			cell := cellValue(col, t)
			if col.truncate && nameLimit > 0 {
				cell = truncate(cell, nameLimit)
			}
			if col.rightAlign {
				cell = "\t" + cell
			}
			cells[i] = cell
		}

		// Right-aligned cells are prefixed with a tab above; join the rest with
		// tabs and strip the doubled tab that the prefix introduces.
		row := strings.ReplaceAll(strings.Join(cells, "\t"), "\t\t", "\t")
		_, _ = fmt.Fprintln(tw, row)
	}

	_ = tw.Flush()
}

// nameColumnLimit computes how many runes the truncatable (Name) column may
// occupy so the widest row fits within width. It returns 0 (no truncation) when
// the width is unknown or already roomy enough. Only the first truncatable
// column is considered.
func nameColumnLimit(torrents []torrent.Torrent, columns []listColumn, width int) int {
	if width <= 0 {
		return 0
	}

	// Sum the widest rendered width of every non-truncatable column, plus the
	// 2-space gutter tabwriter inserts between columns.
	const gutter = 2

	fixed := 0
	nameIndex := -1
	for i, col := range columns {
		if col.truncate && nameIndex == -1 {
			nameIndex = i
			continue
		}

		colWidth := len(col.header)
		for _, t := range torrents {
			if cellWidth := len([]rune(cellValue(col, t))); cellWidth > colWidth {
				colWidth = cellWidth
			}
		}
		fixed += colWidth
	}

	if nameIndex == -1 {
		return 0
	}

	gutters := (len(columns) - 1) * gutter
	available := width - fixed - gutters

	// Keep a small floor so a very narrow terminal still shows a sliver of the
	// name rather than nothing.
	const minName = 8
	if available < minName {
		return minName
	}

	// Don't truncate when the longest name already fits.
	longest := len(columns[nameIndex].header)
	for _, t := range torrents {
		if w := len([]rune(cellValue(columns[nameIndex], t))); w > longest {
			longest = w
		}
	}
	if longest <= available {
		return 0
	}

	return available
}

// truncate shortens s to at most limit runes, replacing the tail with a single
// "…" when it would otherwise overflow. Truncation is rune-count based; wide
// (CJK) display width is not yet accounted for.
func truncate(s string, limit int) string {
	if limit <= 0 {
		return s
	}

	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}

	if limit == 1 {
		return "…"
	}

	return string(runes[:limit-1]) + "…"
}

// formatETA renders a torrent's ETA. A negative value is Transmission's
// "unknown/not applicable" sentinel and renders as a dash.
func formatETA(eta int64) string {
	if eta < 0 {
		return "-"
	}

	return output.Duration(eta)
}

// isTTY reports whether w is a terminal whose colored/aligned table output is
// appropriate. A non-empty NO_COLOR forces the plain piped contract regardless
// of whether w is a terminal.
func isTTY(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}

	f, ok := w.(*os.File)
	if !ok {
		return false
	}

	return term.IsTerminal(int(f.Fd()))
}

// terminalWidth returns the column count of w's terminal, or 0 when it cannot be
// determined (in which case truncation is disabled).
func terminalWidth(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}

	cols, _, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0
	}

	return cols
}
