package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// colSpec is one table column's shape: its heading, its width, and its
// alignment. The torrent table and the detail pane's tables share it, so they
// size and drop columns by one rule.
type colSpec struct {
	// title is the column heading.
	title string

	// width is the column's width in cells, excluding the table's own cell
	// padding. Zero means flex: the column takes whatever the others leave.
	width int

	// right right-aligns the cell. bubbles/v2/table has no alignment, so the
	// cell is left-padded to width when the row is built — which is safe only
	// because every non-flex width is fixed before rows are built.
	right bool
}

// align left-pads s to the column's width when the column is right-aligned. A
// value wider than the column is left for the table to truncate.
func (c colSpec) align(s string) string {
	if !c.right {
		return s
	}

	if gap := c.width - lipgloss.Width(s); gap > 0 {
		return strings.Repeat(" ", gap) + s
	}

	return s
}

// minNameWidth is the narrowest the flex column is allowed to get before a
// column is dropped to feed it.
const minNameWidth = 16

// cellPadding is the trailing padding every table cell carries (Styles.Cell in
// newStyles). It is one rather than the component default's two because nine
// columns of padding on both sides costs eighteen cells, which is most of a
// torrent name.
const cellPadding = 1

// specColumns converts column shapes into the component's own type. A
// right-aligned heading is padded like its cells, so it sits over them.
func specColumns(specs []colSpec) []table.Column {
	out := make([]table.Column, 0, len(specs))

	for _, c := range specs {
		out = append(out, table.Column{Title: c.align(c.title), Width: c.width})
	}

	return out
}

// fitSpecs sizes columns for a pane of the given inner width: the flex column
// takes whatever the others leave, and when that would squeeze it below
// minNameWidth — or, with no flex column, when the columns overflow — the
// rightmost droppable column is dropped and the sum recomputed. The first keep
// columns and the flex column are never dropped.
//
// A dropped column is given a width of zero rather than removed. The component
// skips a zero-width column when rendering both headers and rows, so rows stay
// one cell per column however narrow the pane gets, and nothing has to rebuild
// them on a resize.
func fitSpecs(specs []colSpec, keep, width int) []table.Column {
	out := specColumns(specs)
	flex := -1

	for i, c := range specs {
		if c.width == 0 {
			flex = i
		}
	}

	for next := len(out) - 1; ; next-- {
		spent := 0

		for i, c := range out {
			if i != flex && c.Width > 0 {
				spent += c.Width + cellPadding
			}
		}

		remaining := width - spent - cellPadding
		fits := remaining >= minNameWidth
		if flex < 0 {
			fits = spent <= width
		}

		for next >= keep && next == flex {
			next--
		}

		if fits || next < keep {
			if flex >= 0 {
				out[flex].Width = max(1, remaining)
			}

			return out
		}

		out[next].Width = 0
	}
}

// The detail pane's table columns mirror the CLI's registries — fileFieldKeys
// (internal/commands/files.go), peerFieldKeys (internal/commands/peers.go), and
// trackerFieldKeys (internal/commands/trackers.go) — with their headings and
// renderers. The files table leads with a mark cell, as the torrent table does.
// internal/tui does not import internal/commands, so they are copies pinned by
// TestDetailColumnsMirrorTheCLI.
//
// One heading differs on purpose: the trackers table's "Next" is a countdown
// where the CLI's "Next Announce" prints a date. A pane that refreshes every
// tick would otherwise show the same string all day.
var (
	fileSpecs = []colSpec{
		{title: " ", width: 1},
		{title: "Index", width: 5, right: true},
		{title: "Name"},
		{title: "Size", width: 9, right: true},
		{title: "Done", width: 9, right: true},
		{title: "%", width: 4, right: true},
		{title: "Priority", width: 8},
		{title: "Wanted", width: 6},
	}

	peerSpecs = []colSpec{
		{title: "Address", width: 21},
		{title: "Port", width: 5, right: true},
		{title: "Client"},
		{title: "Progress", width: 8, right: true},
		{title: "Down", width: 11, right: true},
		{title: "Up", width: 11, right: true},
		{title: "Encrypted", width: 9},
		{title: "Status", width: 8},
	}

	trackerSpecs = []colSpec{
		{title: "Announce"},
		{title: "Tier", width: 4, right: true},
		{title: "State", width: 8},
		{title: "Next", width: 8, right: true},
		{title: "Seeders", width: 7, right: true},
		{title: "Leechers", width: 8, right: true},
		{title: "Downloads", width: 9, right: true},
	}
)

// alignRow pads each cell as its column aligns.
func alignRow(specs []colSpec, cells ...string) table.Row {
	row := make(table.Row, len(cells))

	for i, cell := range cells {
		row[i] = specs[i].align(cell)
	}

	return row
}

// fileRows renders a torrent's files as table rows, keyed by index, with each
// file's mark from the files tab's selection.
func fileRows(files []transmission.File, sel fileSelection, icons Icons) ([]table.Row, []string) {
	rows := make([]table.Row, 0, len(files))
	keys := make([]string, 0, len(files))

	for i, f := range files {
		rows = append(rows, alignRow(fileSpecs,
			icons.mark(sel.isMarked(i)),
			strconv.Itoa(i),
			output.SanitizeTerminal(f.Name),
			output.Bytes(f.Length),
			output.Bytes(f.BytesCompleted),
			output.Percent(fileFraction(f)),
			filePriority(f.Priority),
			yesNo(f.Wanted),
		))
		keys = append(keys, strconv.Itoa(i))
	}

	return rows, keys
}

// fileFraction is a file's completion, the CLI's fileFraction: a zero-length
// file has nothing to fetch, so it is complete.
func fileFraction(f transmission.File) float64 {
	if f.Length <= 0 {
		return 1
	}

	return float64(f.BytesCompleted) / float64(f.Length)
}

// filePriority names Transmission's per-file priority, the CLI's filePriority.
func filePriority(priority int) string {
	switch priority {
	case -1:
		return "low"
	case 0:
		return "normal"
	case 1:
		return "high"
	}

	return strconv.Itoa(priority)
}

// peerRows renders peers as table rows, keyed by address and port. Every string
// a peer sends is sanitized: a client name and a flag string are chosen by an
// arbitrary member of the swarm.
func peerRows(peers []transmission.Peer) ([]table.Row, []string) {
	rows := make([]table.Row, 0, len(peers))
	keys := make([]string, 0, len(peers))

	for _, p := range peers {
		rows = append(rows, alignRow(peerSpecs,
			output.SanitizeTerminal(p.Address),
			strconv.Itoa(p.Port),
			output.SanitizeTerminal(p.Client),
			output.Percent(p.Progress),
			output.Speed(p.RateToClient),
			output.Speed(p.RateToPeer),
			yesNo(p.IsEncrypted),
			output.SanitizeTerminal(p.FlagStr),
		))
		keys = append(keys, fmt.Sprintf("%s:%d", p.Address, p.Port))
	}

	return rows, keys
}

// trackerRows renders trackers as table rows, keyed by announce URL. now is the
// Unix time the Next countdown counts from.
func trackerRows(trackers []transmission.Tracker, now int64) ([]table.Row, []string) {
	rows := make([]table.Row, 0, len(trackers))
	keys := make([]string, 0, len(trackers))

	for _, t := range trackers {
		rows = append(rows, alignRow(trackerSpecs,
			output.SanitizeTerminal(t.Announce),
			strconv.Itoa(t.Tier),
			transmission.AnnounceState(t.AnnounceState),
			countdown(t.NextAnnounceTime, now),
			trackerCount(t.SeederCount),
			trackerCount(t.LeecherCount),
			trackerCount(t.DownloadCount),
		))
		keys = append(keys, t.Announce)
	}

	return rows, keys
}

// trackerCount renders a tracker's scrape count as the CLI does
// (internal/commands' trackerCount): -1 is "not scraped", drawn as a dash.
func trackerCount(n int) string {
	if n < 0 {
		return "-"
	}

	return strconv.Itoa(n)
}

// countdown is the time from now until a Unix timestamp, in its two largest
// units: "12m", "1h 3m", "2d 4h". No timestamp is "-", and a time already
// passed is "now".
func countdown(at, now int64) string {
	const (
		minute = 60
		hour   = 60 * minute
		day    = 24 * hour
	)

	if at <= 0 {
		return "-"
	}

	secs := at - now

	switch {
	case secs <= 0:
		return "now"
	case secs >= day:
		return fmt.Sprintf("%dd %dh", secs/day, secs%day/hour)
	case secs >= hour:
		return fmt.Sprintf("%dh %dm", secs/hour, secs%hour/minute)
	case secs >= minute:
		return fmt.Sprintf("%dm", secs/minute)
	}

	return fmt.Sprintf("%ds", secs)
}

// detailTable is one of the detail pane's tables: a bubbles table built from the
// registry's navigation keys, the shapes it fits, and the key of each row, which
// the cursor follows across a refresh.
type detailTable struct {
	table table.Model
	specs []colSpec

	// keep is how many leading columns are never dropped.
	keep int

	// keys identify the rows, index for index.
	keys []string

	// empty is what the table says under its header when it has no rows.
	empty string
}

// newDetailTable builds a blurred table. It is focused only while the detail
// pane is focused and its tab is the current one: an unfocused table ignores the
// nav keys, and a focused one on a hidden tab would eat them silently.
func newDetailTable(st Styles, specs []colSpec, keep int, empty string) detailTable {
	t := table.New(
		table.WithColumns(specColumns(specs)),
		table.WithKeyMap(tableKeyMap()),
		table.WithStyles(st.Table),
	)
	t.Blur()

	return detailTable{table: t, specs: specs, keep: keep, empty: empty}
}

// setRows installs rows, keeping the cursor on the row it was on by key, or at
// its index, clamped, when that row is gone.
func (t detailTable) setRows(rows []table.Row, keys []string) detailTable {
	cursor := t.table.Cursor()

	if cursor >= 0 && cursor < len(t.keys) {
		was := t.keys[cursor]

		for i, k := range keys {
			if k == was {
				cursor = i

				break
			}
		}
	}

	t.keys = keys
	t.table.SetRows(rows)
	t.table.SetCursor(max(0, cursor))

	return t
}

// clear empties the table and puts its cursor back at the top, for a different
// torrent.
func (t detailTable) clear() detailTable {
	t.keys = nil
	t.table.SetRows(nil)
	t.table.SetCursor(0)

	return t
}

// resize fits the table to the pane's inner region, less the tab strip and its
// rule. The component takes its own header out of the height it is given.
func (t detailTable) resize(inner rect) detailTable {
	t.table.SetColumns(fitSpecs(t.specs, t.keep, inner.w))
	t.table.SetWidth(inner.w)
	t.table.SetHeight(max(1, inner.h-2))

	return t
}

// focus drives the component's focus flag.
func (t detailTable) focus(on bool) detailTable {
	if on {
		t.table.Focus()
	} else {
		t.table.Blur()
	}

	return t
}

// setStyles re-themes the table.
func (t detailTable) setStyles(st Styles) detailTable {
	t.table.SetStyles(st.Table)

	return t
}

// update forwards a key to the table.
func (t detailTable) update(msg tea.Msg) (detailTable, tea.Cmd) {
	tbl, cmd := t.table.Update(msg)
	t.table = tbl

	return t, cmd
}

// cursor is the table's current row index.
func (t detailTable) cursor() int {
	return t.table.Cursor()
}

// lines renders the table, or its header and what it says when it has no rows.
func (t detailTable) lines(st Styles) []string {
	lines := strings.Split(t.table.View(), "\n")

	if len(t.table.Rows()) == 0 {
		return []string{lines[0], st.Muted.Render(t.empty)}
	}

	return lines
}
