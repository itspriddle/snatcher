package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// specTitles is the headings of a column set, in order.
func specTitles(specs []colSpec) []string {
	out := make([]string, 0, len(specs))
	for _, c := range specs {
		out = append(out, c.title)
	}

	return out
}

// TestDetailColumnsMirrorTheCLI pins the detail tables to the CLI's registries:
// fileFieldKeys' headers (internal/commands/files.go) after a mark cell,
// peerFieldKeys' (internal/commands/peers.go), and trackerFieldKeys'
// (internal/commands/trackers.go), in order. "Next" stands in for "Next
// Announce" because the pane draws a countdown rather than a date.
func TestDetailColumnsMirrorTheCLI(t *testing.T) {
	assert.Equal(t, []string{" ", "Index", "Name", "Size", "Done", "%", "Priority", "Wanted"}, specTitles(fileSpecs))
	assert.Equal(t, []string{"Address", "Port", "Client", "Progress", "Down", "Up", "Encrypted", "Status"}, specTitles(peerSpecs))
	assert.Equal(t, []string{"Announce", "Tier", "State", "Next", "Seeders", "Leechers", "Downloads"}, specTitles(trackerSpecs))
}

// TestDetailTablesDropColumnsFromTheRight checks a narrow pane gives up columns
// right to left, never the flex column, and a wide one keeps them all.
func TestDetailTablesDropColumnsFromTheRight(t *testing.T) {
	for _, specs := range [][]colSpec{peerSpecs, trackerSpecs} {
		wide := fitSpecs(specs, 0, 200)
		for i, c := range wide {
			assert.Positivef(t, c.Width, "%s dropped at 200 columns", specs[i].title)
		}

		narrow := fitSpecs(specs, 0, 56)

		dropped := false

		for i, c := range narrow {
			if specs[i].width == 0 {
				assert.GreaterOrEqualf(t, c.Width, minNameWidth, "the flex column %s was squeezed", specs[i].title)

				continue
			}

			if c.Width == 0 {
				dropped = true
			} else {
				assert.Falsef(t, dropped, "%s kept after a column to its left was dropped", specs[i].title)
			}
		}

		assert.Truef(t, dropped, "%s dropped nothing at 56 columns", specTitles(specs))
	}

	files := fitSpecs(fileSpecs, 1, 30)
	assert.Equal(t, 1, files[0].Width, "the mark cell is never dropped")
	assert.Zero(t, files[len(files)-1].Width)
}

// TestFilesRowsRenderTheCLIsWords checks the files table draws priority, wanted,
// and completion as `torrent files` does, and sanitizes a file's name.
func TestFilesRowsRenderTheCLIsWords(t *testing.T) {
	rows, keys := fileRows(mockDetail().Files, newFileSelection(), newIcons(false))

	require.Len(t, rows, 3)
	assert.Equal(t, []string{"0", "1", "2"}, keys)

	for i, want := range []string{"high", "normal", "low"} {
		assert.Equal(t, want, rows[i][6])
	}

	assert.Equal(t, "yes", rows[0][7])
	assert.Equal(t, "no", rows[2][7])
	assert.Equal(t, "42%", strings.TrimSpace(rows[0][5]))
	assert.Equal(t, "100%", rows[2][5], "a zero-length file is complete")
	assert.NotContains(t, rows[2][2], "\x1b")
}

// TestTrackerCountsRenderDashForUnknown checks a -1 scrape count draws as the
// CLI's dash.
func TestTrackerCountsRenderDashForUnknown(t *testing.T) {
	rows, keys := trackerRows(mockDetail().Trackers, 0)

	require.Len(t, rows, 2)
	assert.Equal(t, []string{"https://tracker.example.org/announce", "udp://backup.example.net:6969/announce"}, keys)

	assert.Equal(t, "120", strings.TrimSpace(rows[0][4]))

	for _, cell := range rows[1][4:] {
		assert.Equal(t, "-", strings.TrimSpace(cell))
	}
}

// TestNextAnnounceIsACountdown checks the Next column counts down from now in
// its two largest units.
func TestNextAnnounceIsACountdown(t *testing.T) {
	const now = 1_700_000_000

	for want, at := range map[string]int64{
		"-":     0,
		"now":   now - 5,
		"45s":   now + 45,
		"12m":   now + 12*60 + 30,
		"1h 3m": now + 3600 + 3*60 + 9,
		"2d 4h": now + 2*86400 + 4*3600 + 59,
	} {
		assert.Equalf(t, want, countdown(at, now), "at now%+d", at-now)
	}
}

// TestPeerStringsAreSanitized checks a swarm-chosen client or flag string cannot
// reach the terminal raw.
func TestPeerStringsAreSanitized(t *testing.T) {
	rows, _ := peerRows(mockDetail().Peers)

	var text []string
	for _, row := range rows {
		text = append(text, row...)
	}

	joined := strings.Join(text, " ")

	assert.Contains(t, joined, "evil")
	assert.NotContains(t, joined, "\x1b")
	assert.NotContains(t, joined, "\x07")

	trackers, _ := trackerRows([]transmission.Tracker{{Announce: "http://x\x1b[2J/announce"}}, 0)
	assert.NotContains(t, strings.Join(trackers[0], " "), "\x1b")
}

// TestTableCursorFollowsItsKeyAcrossARefresh checks a refresh that reorders the
// rows keeps the cursor on its row, and one that removes it keeps the index.
func TestTableCursorFollowsItsKeyAcrossARefresh(t *testing.T) {
	trackers := mockDetail().Trackers

	tbl := newDetailTable(Styles{}, trackerSpecs, 0, "no trackers")
	tbl = tbl.setRows(trackerRows(trackers, 0))
	tbl.table.SetCursor(1)

	tbl = tbl.setRows(trackerRows([]transmission.Tracker{trackers[1], trackers[0]}, 0))
	assert.Equal(t, 0, tbl.cursor(), "the cursor follows its tracker")

	tbl = tbl.setRows(trackerRows([]transmission.Tracker{trackers[0]}, 0))
	assert.Equal(t, 0, tbl.cursor(), "a vanished row leaves the cursor clamped")

	cleared := tbl.clear()
	assert.Empty(t, cleared.table.Rows())
	assert.Equal(t, 0, cleared.setRows(trackerRows(trackers, 0)).cursor(), "a cleared table starts at the top")
}

// focusedTables names which of the detail tables are focused.
func focusedTables(m Model) []string {
	var out []string

	if m.torrents.detail.files.table.Focused() {
		out = append(out, "files")
	}

	if m.torrents.detail.peers.table.Focused() {
		out = append(out, "peers")
	}

	if m.torrents.detail.trackers.table.Focused() {
		out = append(out, "trackers")
	}

	return out
}

// TestOnlyTheCurrentTabsTableIsFocused walks every path that changes the tab or
// the pane's focus: a table has focus only while the pane does and its tab is
// the one shown.
func TestOnlyTheCurrentTabsTableIsFocused(t *testing.T) {
	m := withMockDetail(press(newTestModel(120, 40), "enter"))
	assert.Empty(t, focusedTables(m), "overview")

	steps := []struct {
		keys []string
		want []string
	}{
		{[]string{"3"}, []string{"trackers"}},
		{[]string{"]"}, []string{"peers"}},
		{[]string{"l"}, nil},
		{[]string{"h"}, []string{"peers"}},
		{[]string{"["}, []string{"trackers"}},
		{[]string{"left"}, []string{"files"}},
		{[]string{"right"}, []string{"trackers"}},
		{[]string{"tab"}, nil},
		{[]string{"shift+tab"}, []string{"trackers"}},
		{[]string{"ctrl+w", "k"}, nil},
		{[]string{"ctrl+w", "j"}, []string{"trackers"}},
		{[]string{"4"}, []string{"peers"}},
		{[]string{"[", "["}, []string{"files"}},
		{[]string{"}"}, nil},
	}

	for _, step := range steps {
		m = press(m, step.keys...)

		assert.Equalf(t, step.want, focusedTables(m), "after %v", step.keys)
	}
}

// TestNavKeysMoveTheCurrentTabsTable checks j reaches the trackers table on its
// tab, and the viewport nowhere else.
func TestNavKeysMoveTheCurrentTabsTable(t *testing.T) {
	m := withMockDetail(press(newTestModel(120, 40), "enter", "3"))

	m = press(m, "j")

	assert.Equal(t, 1, m.torrents.detail.trackers.cursor())
	assert.Zero(t, m.torrents.detail.peers.cursor())
	assert.Zero(t, m.torrents.detail.vp.YOffset())

	m = press(m, "k", "]", "j")
	assert.Equal(t, 1, m.torrents.detail.peers.cursor())
	assert.Zero(t, m.torrents.detail.trackers.cursor())
}

// TestEmptyTableSaysSo checks a torrent with no peers draws its header and says
// so, rather than an empty box.
func TestEmptyTableSaysSo(t *testing.T) {
	detail := mockDetail()
	detail.Peers = nil

	m := press(newTestModel(120, 40), "enter", "4")
	m.torrents.detail = m.torrents.detail.setDetail(detail)

	frame := m.render()

	assert.Contains(t, frame, "Address")
	assert.Contains(t, frame, "no peers")
	assert.Empty(t, m.torrents.detail.peers.table.Rows())
}

// TestDetailTrackersGolden and TestDetailPeersGolden pin the two read-only tables.
func TestDetailTrackersGolden(t *testing.T) {
	assertGolden(t, "detail_trackers", withMockDetail(press(newTestModel(120, 40), "enter", "3")).render())
}

func TestDetailPeersGolden(t *testing.T) {
	assertGolden(t, "detail_peers", withMockDetail(press(newTestModel(120, 40), "enter", "4")).render())
}
