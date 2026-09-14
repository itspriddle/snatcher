package commands

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// column describes a single rendered column of a row of type T: its header, how
// a row's value is derived, its alignment, whether the Name-style truncation
// applies on a TTY, and whether the rendered cell carries untrusted,
// server-derived text that must be neutralized of terminal control sequences
// before display.
//
// The type parameter is what lets one renderer serve every row shape the CLI
// prints — torrents, torrent details, files, peers, trackers — instead of each
// command growing its own copy of the TTY/piped/truncation logic.
type column[T any] struct {
	header     string
	value      func(T) string
	rightAlign bool
	truncate   bool
	// sanitize marks columns whose value comes from the remote server or
	// swarm-supplied .torrent metadata (name, labels, error, download dir). Such
	// cells are passed through output.SanitizeTerminal in the human/plaintext
	// render paths (table and piped) so a crafted torrent name cannot inject ANSI
	// escapes. JSON/jq output is a structured contract and is never sanitized.
	sanitize bool
}

// listFields is the list command's field registry: it maps a selectable
// --fields key to the column that renders it. It is the single source of truth
// for headers, accessors, and alignment, shared by both the TTY aligned output
// and the piped tab-separated output. New list columns are added here; other
// commands own their own registries with their own key names.
var listFields = map[string]column[transmission.Torrent]{
	"id": {header: "ID", rightAlign: true, value: func(t transmission.Torrent) string {
		return strconv.FormatInt(t.ID, 10)
	}},
	"hash": {header: "Hash", value: func(t transmission.Torrent) string {
		return t.Hash
	}},
	"name": {header: "Name", truncate: true, sanitize: true, value: func(t transmission.Torrent) string {
		return t.Name
	}},
	"status": {header: "Status", value: func(t transmission.Torrent) string {
		return t.Status.String()
	}},
	"done": {header: "Done", rightAlign: true, value: func(t transmission.Torrent) string {
		return output.Percent(t.PercentDone)
	}},
	"size": {header: "Size", rightAlign: true, value: func(t transmission.Torrent) string {
		return output.Bytes(t.TotalSize)
	}},
	"down": {header: "Down", rightAlign: true, value: func(t transmission.Torrent) string {
		return output.Speed(t.RateDownload)
	}},
	"up": {header: "Up", rightAlign: true, value: func(t transmission.Torrent) string {
		return output.Speed(t.RateUpload)
	}},
	"ratio": {header: "Ratio", rightAlign: true, value: func(t transmission.Torrent) string {
		return output.Ratio(t.UploadRatio)
	}},
	"eta": {header: "ETA", rightAlign: true, value: func(t transmission.Torrent) string {
		return output.ETA(t.ETA)
	}},
	"peers": {header: "Peers", rightAlign: true, value: func(t transmission.Torrent) string {
		return strconv.FormatInt(t.PeersConnected, 10)
	}},
	"added": {header: "Added", value: func(t transmission.Torrent) string {
		return output.Date(t.AddedDate)
	}},
	"download_dir": {header: "Download Dir", sanitize: true, value: func(t transmission.Torrent) string {
		return t.DownloadDir
	}},
	"error": {header: "Error", sanitize: true, value: func(t transmission.Torrent) string {
		return t.Error
	}},
	"labels": {header: "Labels", sanitize: true, value: func(t transmission.Torrent) string {
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
// page, and the show command's default (which shows the complete set rather
// than list's smaller defaultFieldKeys). TestListFieldKeysCoverRegistry pins it
// to listFields, since the help string is built from this slice while
// parseFields validates against the registry map. internal/tui/columns.go
// mirrors it and defaultFieldKeys, pinned by TestColumnKeysMirrorTheCLI.
var allFieldKeys = []string{
	"id", "hash", "name", "status", "done", "size", "down", "up", "ratio",
	"eta", "peers", "added", "download_dir", "error", "labels",
}

// validFieldKeys returns every selectable --fields key of the given registry in
// sorted order, for the error message listing the valid keys. Each command
// passes its own registry, so the error names only that command's keys.
func validFieldKeys[T any](registry map[string]column[T]) string {
	keys := make([]string, 0, len(registry))
	for k := range registry {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return strings.Join(keys, ", ")
}

// parseFields resolves a --fields value into the ordered columns to render,
// looked up in the calling command's own registry. An empty spec selects the
// given defaults (list passes defaultFieldKeys, show passes the full
// detailFieldKeys). An unknown key returns an error listing the registry's valid
// keys. Keys are matched case-insensitively after trimming whitespace.
//
// The caller's defaults are validated too, not just the user's spec. A key in a
// defaults slice that is absent from the registry would otherwise yield a zero
// column[T] whose nil value accessor panics in cellValue — a typo becoming a
// crash at render time rather than an error at parse time. The two failures
// carry different messages because they have different audiences: an invalid
// field is user input, an unknown default is a programming mistake.
func parseFields[T any](registry map[string]column[T], defaults []string, spec string) ([]column[T], error) {
	keys := defaults
	if strings.TrimSpace(spec) != "" {
		keys = nil
		for _, raw := range strings.Split(spec, ",") {
			key := strings.ToLower(strings.TrimSpace(raw))
			if key == "" {
				continue
			}
			if _, ok := registry[key]; !ok {
				return nil, fmt.Errorf("invalid field %q: valid fields are %s", strings.TrimSpace(raw), validFieldKeys(registry))
			}
			keys = append(keys, key)
		}
		if len(keys) == 0 {
			return nil, fmt.Errorf("no fields selected: valid fields are %s", validFieldKeys(registry))
		}
	}

	cols := make([]column[T], len(keys))
	for i, key := range keys {
		// A miss here can only come from the defaults: the spec branch above
		// validated every key it appended, and reports the raw token verbatim,
		// which this loop no longer has.
		col, ok := registry[key]
		if !ok {
			return nil, fmt.Errorf("default field %q is not in the registry: valid fields are %s", key, validFieldKeys(registry))
		}
		cols[i] = col
	}

	return cols, nil
}

// listDefaults are the three answers to "how should list render" that an
// operator can store in the client config section: which columns, which sort
// field, and whether to reverse. Each applies only when the corresponding flag
// was not passed.
type listDefaults struct {
	columns []string
	sort    string
	reverse bool
}

// resolveListDefaults reads client.list_columns, client.list_sort, and
// client.list_reverse for this invocation, falling back to the registry defaults
// — which are exactly the flag defaults — when nothing is stored. An operator
// with no client section, or no config file at all, therefore gets the list they
// got before the section existed.
//
// It runs before dialing so a nonsense value hand-edited into the file is
// reported, naming the config key rather than a flag, without a network round
// trip first. dial reads the same file again: both reads are of a small local
// file, and the alternative — threading a loaded config out through the dial
// seam — would spread config plumbing across every command to serve this one.
func resolveListDefaults(cmd *cobra.Command) (listDefaults, error) {
	cfg, _, err := loadConfig(configFlag(cmd))
	if err != nil {
		return listDefaults{}, err
	}

	columns, err := clientSetting[[]string](cmd, cfg, keyListColumns)
	if err != nil {
		return listDefaults{}, err
	}

	sortField, err := clientSetting[string](cmd, cfg, keyListSort)
	if err != nil {
		return listDefaults{}, err
	}

	reverse, err := clientSetting[bool](cmd, cfg, keyListReverse)
	if err != nil {
		return listDefaults{}, err
	}

	return listDefaults{columns: normalizeFieldKeys(columns), sort: sortField, reverse: reverse}, nil
}

// normalizeFieldKeys lowercases and trims stored column keys to the spelling
// listFields uses. A stored list is validated case-insensitively, exactly as
// --fields is, so a hand-edited "Name" is a legitimate setting; parseFields
// looks its defaults up verbatim and would otherwise report that legitimate
// setting as a registry mistake.
func normalizeFieldKeys(keys []string) []string {
	normalized := make([]string, len(keys))
	for i, key := range keys {
		normalized[i] = strings.ToLower(strings.TrimSpace(key))
	}

	return normalized
}

// flagOr returns the flag's value when it was explicitly passed, and the
// configured fallback otherwise. Presence is the flag's Changed state, never a
// comparison against the flag's zero value, so an explicit "--reverse=false"
// overrides a stored true instead of reading as an absent flag.
func flagOr[T any](cmd *cobra.Command, name string, flagValue, configured T) T {
	if cmd.Flags().Changed(name) {
		return flagValue
	}

	return configured
}

// NewListCmd builds the "list" command, which dials the backend, fetches all
// torrents, and renders them as an aligned plaintext table on a TTY, as plain
// tab-separated full-value rows when piped, as JSON (--json), or filtered through
// jq (--jq).
//
// The column set, sort field, and sort direction fall back to the stored
// client.list_columns, client.list_sort, and client.list_reverse settings when
// their flags are not passed, so a preference is expressed once with "config
// set" rather than repeated on every invocation.
func NewListCmd() *cobra.Command {
	var (
		server     string
		asJSON     bool
		jqExpr     string
		filterFlag string
		sortFlag   string
		searchFlag string
		reverse    bool
		fieldsFlag string
		topFlag    int
	)

	cmd := &cobra.Command{
		Use:           "list",
		Aliases:       []string{"ls"},
		Short:         "List torrents",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter, err := transmission.ParseFilter(filterFlag)
			if err != nil {
				return err
			}

			// Presence is the flag's Changed state, never a comparison against
			// 0: 0 is the flag's own default and means unlimited, so only a
			// value the caller actually typed can be out of range. Rejected
			// here, before dialing, so a typo costs no round trip.
			if cmd.Flags().Changed("top") && topFlag < 1 {
				return fmt.Errorf("invalid --top %d: must be at least 1", topFlag)
			}

			defaults, err := resolveListDefaults(cmd)
			if err != nil {
				return err
			}

			sortField, err := transmission.ParseSortField(flagOr(cmd, "sort", sortFlag, defaults.sort))
			if err != nil {
				return err
			}

			// An empty --fields selects the configured columns, which are
			// themselves defaultFieldKeys when nothing is stored.
			columns, err := parseFields(listFields, defaults.columns, fieldsFlag)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(cmd, server)
			if err != nil {
				return err
			}

			torrents, err := backend.ListTorrents(cmd.Context(), transmission.Selector{All: true})
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			// Apply the in-memory transforms in a fixed order: status filter,
			// then name search, then sort, then truncation. This order is
			// shared by every output mode (table, piped, JSON/jq). Truncation
			// is last on purpose, so --top means "the first N rows of the
			// rendered order" and composes with the three transforms above
			// rather than racing them.
			torrents = transmission.FilterByStatus(torrents, filter)
			torrents = transmission.SearchByName(torrents, searchFlag)
			torrents = transmission.SortTorrents(torrents, sortField, flagOr(cmd, "reverse", reverse, defaults.reverse))
			torrents = transmission.Top(torrents, topFlag)

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), jsonSlice(torrents), jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), jsonSlice(torrents))
			}

			writeListPlaintext(cmd, torrents, columns, "No torrents.")

			return nil
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().StringVar(&filterFlag, "filter", "all", "filter by status (all, active, downloading, seeding, stopped, verifying, queued-to-verify, queued-to-download, queued-to-seed, error)")
	cmd.Flags().StringVar(&sortFlag, "sort", "queue", "sort by field (queue, name, added, size, progress, ratio, speed)")
	cmd.Flags().StringVar(&searchFlag, "search", "", "filter to torrents whose name contains this text (case-insensitive)")
	cmd.Flags().BoolVar(&reverse, "reverse", false, "reverse the sort order")
	cmd.Flags().IntVar(&topFlag, "top", 0, "show only the first N rows of the rendered order")
	cmd.Flags().StringVar(&fieldsFlag, "fields", "", fmt.Sprintf("comma-separated columns to show, in order (%s)", strings.Join(allFieldKeys, ", ")))

	return cmd
}

// jsonSlice normalizes a row slice for structured output so an empty collection
// serializes as "[]" rather than "null" — no torrents, no files, no connected
// peers, no trackers. Rows are emitted as-is, in the server's order — which is
// what makes a file's array position its index column — and are never
// sanitized: --json/--jq is a structured contract its consumers handle
// themselves.
func jsonSlice[T any](rows []T) []T {
	if rows == nil {
		return []T{}
	}

	return rows
}

// writeListPlaintext renders rows in the appropriate plaintext mode for the
// command's output: an aligned, header-and-truncation table on a TTY, or plain
// tab-separated, full-value, header-less rows when piped or when NO_COLOR is set.
// empty is the friendly line the TTY path prints for an empty collection (e.g.
// "No torrents.", "No files."); the piped path prints nothing either way.
func writeListPlaintext[T any](cmd *cobra.Command, rows []T, columns []column[T], empty string) {
	out := cmd.OutOrStdout()

	if isTTY(out) {
		writeListTable(out, rows, columns, terminalWidth(out), empty)
		return
	}

	writeListPiped(out, rows, columns)
}

// cellValue renders a column's value for a row, neutralizing terminal control
// sequences for columns whose text is untrusted, server-derived (sanitize). It
// is the single rendering seam shared by the table, piped, and vertical-block
// paths so all of them apply the same protection; the JSON/jq paths never call
// it.
func cellValue[T any](col column[T], row T) string {
	v := col.value(row)
	if col.sanitize {
		v = output.SanitizeTerminal(v)
	}

	return v
}

// writeListPiped writes plain, tab-separated, full-value rows with no header —
// the stable scripting contract. An empty list writes nothing.
func writeListPiped[T any](w io.Writer, rows []T, columns []column[T]) {
	for _, row := range rows {
		cells := make([]string, len(columns))
		for i, col := range columns {
			cells[i] = cellValue(col, row)
		}

		_, _ = fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
}

// gutter is the number of spaces between two adjacent table columns. The
// renderer and nameColumnLimit share it so the width the truncation math
// budgets for is the width the rows actually occupy.
const gutter = 2

// writeListTable writes a whitespace-aligned table with a header row, numeric
// columns right-aligned and text columns left-aligned, truncating truncatable
// columns (Name) with an ellipsis so rows fit width. An empty list writes the
// caller's friendly empty line ("No torrents.", "No files.", …), which each
// command names for its own row type. A width of 0 disables truncation.
//
// Padding is computed here rather than delegated to text/tabwriter: alignment is
// per column (numbers right, text left) and tabwriter's AlignRight is a
// writer-wide flag, so a mixed grid is not something it can express.
func writeListTable[T any](w io.Writer, rows []T, columns []column[T], width int, empty string) {
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(w, empty)
		return
	}

	nameLimit := nameColumnLimit(rows, columns, width)

	// The header row is laid out with the data rows so a column is exactly as
	// wide as the widest of its header and its cells.
	lines := make([][]string, 0, len(rows)+1)

	headers := make([]string, len(columns))
	for i, col := range columns {
		headers[i] = col.header
	}
	lines = append(lines, headers)

	for _, row := range rows {
		cells := make([]string, len(columns))
		for i, col := range columns {
			cell := cellValue(col, row)
			if col.truncate && nameLimit > 0 {
				cell = truncate(cell, nameLimit)
			}
			cells[i] = cell
		}
		lines = append(lines, cells)
	}

	widths := make([]int, len(columns))
	for _, cells := range lines {
		for i, cell := range cells {
			if n := cellDisplayWidth(cell); n > widths[i] {
				widths[i] = n
			}
		}
	}

	var b strings.Builder
	for _, cells := range lines {
		b.Reset()
		for i, cell := range cells {
			if i > 0 {
				b.WriteString(strings.Repeat(" ", gutter))
			}

			pad := strings.Repeat(" ", widths[i]-cellDisplayWidth(cell))
			if columns[i].rightAlign {
				b.WriteString(pad)
				b.WriteString(cell)
				continue
			}

			b.WriteString(cell)
			b.WriteString(pad)
		}

		// Padding the final column would leave every line with a ragged tail of
		// invisible spaces.
		_, _ = fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}

// cellDisplayWidth reports the width a rendered cell occupies in the table.
// Rune-count based, with the same caveat truncate documents: wide (CJK) display
// width is not accounted for.
func cellDisplayWidth(s string) int {
	return len([]rune(s))
}

// nameColumnLimit computes how many runes the truncatable (Name) column may
// occupy so the widest row fits within width. It returns 0 (no truncation) when
// the width is unknown or already roomy enough. Only the first truncatable
// column is considered.
func nameColumnLimit[T any](rows []T, columns []column[T], width int) int {
	if width <= 0 {
		return 0
	}

	// Sum the widest rendered width of every non-truncatable column, plus the
	// gutter the renderer inserts between columns.
	fixed := 0
	nameIndex := -1
	for i, col := range columns {
		if col.truncate && nameIndex == -1 {
			nameIndex = i
			continue
		}

		colWidth := cellDisplayWidth(col.header)
		for _, row := range rows {
			if cellWidth := cellDisplayWidth(cellValue(col, row)); cellWidth > colWidth {
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
	longest := cellDisplayWidth(columns[nameIndex].header)
	for _, row := range rows {
		if w := cellDisplayWidth(cellValue(columns[nameIndex], row)); w > longest {
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
//
// NOTE: this is intentionally CLI-only and must NOT be reused for TUI layout.
// The TUI needs display-width-aware truncation (e.g. via a wide-character /
// grapheme-cluster width measure) so a row of CJK or emoji names does not
// overflow the pane; a rune count over-counts every wide glyph as one column.
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

// envNoColor is the conventional variable from no-color.org. Unlike snatcher's
// own SNATCHER_-prefixed variables it is not paired with a flag: there is no
// --no-color, because the convention is an environment one.
const envNoColor = "NO_COLOR"

// noColor reports whether NO_COLOR is set in the environment.
//
// The predicate is "non-empty", per no-color.org, and it is deliberately not
// colorprofile's: that package parses the value with strconv.ParseBool, so
// NO_COLOR=yes would disable color in snatcher's plaintext output and not in its
// TUI. One helper, read in one place, is what keeps the two in agreement — see
// ResolveClientSettings, which hands the answer to the TUI.
func noColor() bool {
	return os.Getenv(envNoColor) != ""
}

// isTTY reports whether w is a terminal whose colored/aligned table output is
// appropriate. A non-empty NO_COLOR forces the plain piped contract regardless
// of whether w is a terminal.
func isTTY(w io.Writer) bool {
	if noColor() {
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
