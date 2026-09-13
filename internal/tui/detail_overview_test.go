package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOverviewMirrorsShow pins the overview to `torrent show`'s default field
// list: its headers, in its order. The first fifteen are listFields
// (internal/commands/list.go, via columnDefs); the rest are detailFieldKeys'
// detail-only scalars (internal/commands/show.go:90). A field added to show
// without the overview following fails here.
func TestOverviewMirrorsShow(t *testing.T) {
	want := []string{
		"ID", "Hash", "Name", "Status", "Done", "Size", "Down", "Up", "Ratio", "ETA",
		"Peers", "Added", "Download Dir", "Error", "Labels",
		"Comment", "Creator", "Created", "Private", "Pieces", "Piece Size",
		"Downloaded Ever", "Uploaded Ever", "Corrupt Ever", "Have Valid",
		"Downloading Time", "Seeding Time", "Recheck", "Metadata",
	}

	rows := mockTorrents()
	lines := newDetailPane(Styles{}, true).setTorrent(&rows[0]).setDetail(mockDetail()).overviewLines(Styles{})

	require.Len(t, lines, len(want))

	for i, header := range want {
		assert.Truef(t, strings.HasPrefix(lines[i], header+":"), "line %d is %q, want the %s header", i, lines[i], header)
	}

	assert.Contains(t, lines[19], "10", "Pieces renders the count")
	assert.Contains(t, lines[20], "4.0 MiB", "Piece Size renders bytes")
	assert.Contains(t, lines[18], "yes", "Private renders yes/no")
	assert.Contains(t, lines[25], "1h 23m 45s", "times render as durations")
	assert.Equal(t, overviewLabelWidth, strings.Index(lines[25], "1h"), "the widest label keeps show's padding")
}

// TestOverviewDrawsListFieldsBeforeTheDetailLands checks the overview never
// waits: the polled fields draw at once, and the detail-only ones say they are
// loading — or are left out when no detail is coming.
func TestOverviewDrawsListFieldsBeforeTheDetailLands(t *testing.T) {
	rows := mockTorrents()

	loading := newDetailPane(Styles{}, true).setTorrent(&rows[0]).overviewLines(Styles{})

	require.Len(t, loading, len(allColumnKeys)+len(overviewDetailRows))
	assert.Contains(t, loading[2], "ubuntu-24.04.1-desktop-amd64.iso")
	assert.Contains(t, loading[len(allColumnKeys)], "loading…")

	static := newDetailPane(Styles{}, false).setTorrent(&rows[0]).overviewLines(Styles{})
	assert.Len(t, static, len(allColumnKeys))
}

// TestOverviewSanitizesServerStrings checks the torrent's name, directory, and
// the detail's comment and creator cannot reach the terminal raw.
func TestOverviewSanitizesServerStrings(t *testing.T) {
	rows := mockTorrents()
	hostile := rows[11]

	detail := mockDetail()
	detail.Torrent = hostile
	detail.Comment = "evil\x1b]0;title\x07comment"
	detail.Creator = "creator\x1b[2J"

	text := strings.Join(newDetailPane(Styles{}, true).setTorrent(&hostile).setDetail(detail).overviewLines(Styles{}), "\n")

	assert.Contains(t, text, "hostile")
	assert.NotContains(t, text, "\x1b")
	assert.NotContains(t, text, "\x07")
}

// TestDetailOverviewLoadedGolden pins the overview with its detail installed.
func TestDetailOverviewLoadedGolden(t *testing.T) {
	assertGolden(t, "detail_overview_loaded", withMockDetail(press(newTestModel(120, 40), "enter")).render())
}

// withMockDetail installs mockDetail in a static model's detail pane, as a fetch
// would.
func withMockDetail(m Model) Model {
	m.torrents.detail = m.torrents.detail.setDetail(mockDetail())

	return m
}
