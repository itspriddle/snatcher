package tui

import (
	"errors"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProwlarrDialsLazilyOnFirstOpen checks ctrl+p dials the default alias as a
// command, the status bar names the server and its version once it lands, and
// the first open hands the query field the keyboard.
func TestProwlarrDialsLazilyOnFirstOpen(t *testing.T) {
	stub := newProwlarrStub(t)

	var dialed []string

	m := withProwlarr(newTestModel(120, 40), stub, []ServerEntry{
		{Name: "alpha", URL: "http://a:9696"},
		{Name: "indexer", URL: "http://prowlarr:9696", Default: true},
	}, &dialed)
	m.ctx = t.Context()

	require.Empty(t, dialed, "nothing dials before the window opens")

	m, cmd := pressCmd(m, "ctrl+p")

	assert.Equal(t, windowProwlarr, m.window)
	assert.True(t, m.prowlarr.dialing)
	assert.True(t, m.inputFocused)
	assert.Equal(t, ownerQuery, m.owner)
	assert.Equal(t, paneSearch, m.focusedPane())
	assert.Contains(t, ansi.Strip(m.render()), "connecting to indexer…")
	require.NotNil(t, cmd)

	for _, msg := range cmdMsgs(cmd) {
		if dial, ok := msg.(prowlarrDialedMsg); ok {
			m, _ = m.update(dial)
		}
	}

	assert.Equal(t, []string{"indexer"}, dialed, "the default alias is dialed, once")
	require.NotNil(t, m.prowlarr.client)
	assert.Contains(t, m.statusBarView(120), "prowlarr: indexer (v1.37.0.5076)")
	assert.Contains(t, ansi.Strip(m.render()), "press / to search")

	m, cmd = pressCmd(press(m, "esc", "esc"), "ctrl+p")

	assert.Nil(t, cmd, "a second open does not dial again")
	assert.False(t, m.inputFocused, "only the first open focuses the query")
}

// TestDefaultAliasFallsBackToTheFirst checks the alias choice with no default.
func TestDefaultAliasFallsBackToTheFirst(t *testing.T) {
	assert.Equal(t, "b", defaultAlias([]ServerEntry{{Name: "a"}, {Name: "b", Default: true}}))
	assert.Equal(t, "a", defaultAlias([]ServerEntry{{Name: "a"}, {Name: "b"}}))
	assert.Empty(t, defaultAlias(nil))
}

// TestProwlarrUnconfigured checks a snatcher with no Prowlarr alias gets a hint,
// no command, and no crash.
func TestProwlarrUnconfigured(t *testing.T) {
	for name, m := range map[string]Model{
		"no seam":  newTestModel(120, 40),
		"no alias": withProwlarr(newTestModel(120, 40), newProwlarrStub(t), nil, nil),
	} {
		t.Run(name, func(t *testing.T) {
			m, cmd := pressCmd(m, "ctrl+p")

			assert.Nil(t, cmd)
			assert.True(t, m.prowlarr.unconfigured)
			assert.False(t, m.inputFocused)

			frame := ansi.Strip(m.render())
			assert.Contains(t, frame, "no Prowlarr server configured")
			assert.Contains(t, frame, "snatcher server add --type prowlarr")

			for _, k := range []string{"/", "enter", "+", "a", "w", "O", "P", "f", "R", "S"} {
				m = press(m, k)
				m = press(m, "esc")
			}
		})
	}
}

// TestProwlarrDialRefusedWhileSwitching checks the Prowlarr dial never overlaps
// a Transmission switch: the two dialers share one error-writer swap.
func TestProwlarrDialRefusedWhileSwitching(t *testing.T) {
	m := withProwlarr(newTestModel(120, 40), newProwlarrStub(t), prowlarrServers(), nil)
	m.live.switching = true
	m.prowlarr.opened = true

	m, cmd := pressCmd(m, "ctrl+p")

	assert.False(t, m.prowlarr.dialing)
	assert.Equal(t, "switching servers…", m.toast)
	assert.Nil(t, cmd)
}

// TestProwlarrDialFailureShowsInThePane checks a failed first dial is drawn in
// the results pane with its hint, and that R retries it.
func TestProwlarrDialFailureShowsInThePane(t *testing.T) {
	stub := newProwlarrStub(t)
	stub.status.Store(500)

	m := withProwlarr(newTestModel(120, 40), stub, prowlarrServers(), nil)
	m.ctx = t.Context()
	m.prowlarr.opened = true

	m = runKey(t, m, "ctrl+p")

	assert.Nil(t, m.prowlarr.client)
	require.Error(t, m.prowlarr.dialErr)
	assert.Contains(t, ansi.Strip(m.render()), "Prowlarr error")
	assert.Contains(t, m.statusBarView(120), "prowlarr: ! server error")

	stub.status.Store(0)

	m = runKey(t, m, "R")

	assert.NotNil(t, m.prowlarr.client)
	assert.NoError(t, m.prowlarr.dialErr)
}

// TestSubmitSearchFillsTheResults checks enter runs the search with the CLI's
// defaults, blurs the query, focuses the results, and shows the releases in the
// server's order.
func TestSubmitSearchFillsTheResults(t *testing.T) {
	stub, m := prowlarrModel(t, 120, 40)

	m = typeText(press(m, "/"), "ubuntu")
	m, cmd := pressCmd(m, "enter")

	assert.False(t, m.inputFocused)
	assert.Equal(t, paneResults, m.focusedPane())
	assert.True(t, m.prowlarr.inFlight)
	assert.Contains(t, ansi.Strip(m.render()), "searching…")

	m = settle(t, m, cmd)

	q := stub.lastQuery(t)
	assert.Equal(t, "ubuntu", q.Get("query"))
	assert.Equal(t, "search", q.Get("type"))
	assert.Equal(t, "100", q.Get("limit"))
	assert.Empty(t, q.Get("offset"))

	require.Len(t, m.prowlarr.rows, 3)
	assert.Equal(t, "g-ubuntu", m.prowlarr.rows[0].GUID)
	assert.Equal(t, "3 results", m.toast)
	assert.Contains(t, m.statusBarView(120), "results: 3")
	assert.NotContains(t, m.statusBarView(120), "results: 3+", "a short page is the last")
}

// TestEmptyQueryIsRefused checks enter with nothing typed does not search, as the
// CLI refuses a search with no query.
func TestEmptyQueryIsRefused(t *testing.T) {
	stub, m := prowlarrModel(t, 120, 40)

	m, cmd := pressCmd(press(m, "/"), "enter")

	assert.Nil(t, cmd)
	assert.Equal(t, "type a query first", m.toast)
	assert.True(t, m.inputFocused)
	assert.Zero(t, stub.searches())
}

// TestStaleSearchRepliesAreDropped checks a reply for a superseded search, or
// from before a server switch, changes nothing.
func TestStaleSearchRepliesAreDropped(t *testing.T) {
	_, m := searchModel(t, 120, 40)

	before := m.prowlarr.rows

	m, _ = m.update(searchMsg{gen: m.prowlarr.gen, seq: m.prowlarr.seq - 1})
	m, _ = m.update(searchMsg{gen: m.prowlarr.gen - 1, seq: m.prowlarr.seq})

	assert.Equal(t, before, m.prowlarr.rows)
}

// TestFetchMoreAppends checks + asks for the next offset and appends, and is
// refused once a page comes back short.
func TestFetchMoreAppends(t *testing.T) {
	stub, m := prowlarrModel(t, 120, 40)
	m.prowlarr.params.Limit = 2

	m = runSearchFor(t, m, "ubuntu")

	require.Len(t, m.prowlarr.results, 2)
	assert.Contains(t, m.statusBarView(120), "results: 2+")

	m = press(m, "j")
	m = runKey(t, m, "+")

	assert.Equal(t, "2", stub.lastQuery(t).Get("offset"))
	require.Len(t, m.prowlarr.results, 3)
	assert.Equal(t, "g-debian", m.prowlarr.cursor.guid, "the cursor stays on its release")
	assert.True(t, m.prowlarr.exhausted)

	m, cmd := pressCmd(m, "+")

	assert.Nil(t, cmd)
	assert.Equal(t, "no more results", m.toast)
}

// TestFetchMoreDropsAMismatchedOffset checks a page whose offset is not the one +
// asked for is not appended.
func TestFetchMoreDropsAMismatchedOffset(t *testing.T) {
	_, m := prowlarrModel(t, 120, 40)
	m.prowlarr.params.Limit = 2

	m = runSearchFor(t, m, "ubuntu")
	m, _ = pressCmd(m, "+")

	m, _ = m.update(searchMsg{gen: m.prowlarr.gen, seq: m.prowlarr.seq, offset: 7, releases: m.prowlarr.results})

	assert.Len(t, m.prowlarr.results, 2)
}

// TestFetchMoreRefusals checks + before a search and while one is in flight.
func TestFetchMoreRefusals(t *testing.T) {
	_, m := prowlarrModel(t, 120, 40)

	assert.Equal(t, "no search yet: press / to search", press(m, "+").toast)

	m = typeText(press(m, "/"), "ubuntu")
	m, _ = pressCmd(m, "enter")

	assert.Equal(t, "search in flight", press(m, "+").toast)
}

// TestRefreshResubmitsTheSearch checks R before a search says so, and after one
// reruns the submitted query from the first page, not the field's draft.
func TestRefreshResubmitsTheSearch(t *testing.T) {
	stub, m := prowlarrModel(t, 120, 40)

	m, cmd := pressCmd(m, "R")

	assert.Nil(t, cmd)
	assert.Equal(t, "no search to resubmit", m.toast)

	m = runSearchFor(t, m, "ubuntu")
	m = typeText(press(m, "/", "ctrl+u"), "draft")
	m = press(m, "esc", "down")

	m = runKey(t, m, "R")

	assert.Equal(t, 2, stub.searches())
	assert.Equal(t, "ubuntu", stub.lastQuery(t).Get("query"))
	assert.Len(t, m.prowlarr.rows, 3)
}

// TestReleaseSortKeepsTheCursor checks o sorts the rows, keeps the cursor on its
// release, sorts unreported counts last in both directions, and leaves the
// results in the server's order.
func TestReleaseSortKeepsTheCursor(t *testing.T) {
	_, m := searchModel(t, 120, 40)

	m = press(m, "j")
	require.Equal(t, "g-debian", m.prowlarr.cursor.guid)

	// seeders is the seventh field.
	m = press(m, "o", "j", "j", "j", "j", "j", "j", "enter")

	assert.Equal(t, releaseSort{field: releaseSortSeeders}, m.prowlarr.sort)
	assert.Equal(t, []string{"g-debian", "g-ubuntu", "g-hostile"}, guids(m))
	assert.Equal(t, "g-debian", m.prowlarr.cursor.guid)
	assert.Equal(t, 0, m.prowlarr.table.Cursor())

	m = press(m, "o", "r", "enter")

	assert.Equal(t, []string{"g-ubuntu", "g-debian", "g-hostile"}, guids(m))
	assert.Equal(t, 1, m.prowlarr.table.Cursor())
	assert.Equal(t, "g-ubuntu", m.prowlarr.results[0].GUID, "sorting never reorders the results")
	assert.Equal(t, "sort: seeders, reversed", m.toast)
}

// guids is the window's rows by guid, in order.
func guids(m Model) []string {
	out := make([]string, 0, len(m.prowlarr.rows))
	for _, r := range m.prowlarr.rows {
		out = append(out, r.GUID)
	}

	return out
}

// TestReleaseColumnsOverlay checks c hides a column for the session, refuses an
// empty set, and never touches the torrent columns.
func TestReleaseColumnsOverlay(t *testing.T) {
	_, m := searchModel(t, 120, 40)
	torrentColumns := m.torrents.listView.columns

	m = press(m, "c")

	_, ok := topAs[releaseColumnsOverlay](m)
	require.True(t, ok)

	// age is the first row.
	m = press(m, "space", "enter")

	assert.Equal(t, []string{"title", "indexer", "size", "grabs", "seeders", "leechers"}, m.prowlarr.columns)
	assert.NotContains(t, ansi.Strip(m.render()), " Age ")
	assert.Equal(t, torrentColumns, m.torrents.listView.columns)

	m = press(m, "c")
	for range m.prowlarr.columns {
		m = press(m, "space", "j")
	}

	m = press(m, "enter")

	assert.Equal(t, "pick at least one column", m.toast)
	assert.True(t, m.hasOverlay(releaseColumnsOverlayKind))
}

// TestReleaseDetailOpensAndRedacts checks enter opens and focuses the detail
// pane, } toggles it, esc returns to the results, and no frame carries the key
// or the hostile title's escape sequence.
func TestReleaseDetailOpensAndRedacts(t *testing.T) {
	_, m := searchModel(t, 120, 40)

	m = press(m, "enter")

	assert.True(t, m.prowlarr.detailOpen)
	assert.Equal(t, paneResultDetail, m.focusedPane())

	frame := m.render()
	assert.Contains(t, ansi.Strip(frame), "Ubuntu 24.04 Desktop")
	assert.Contains(t, ansi.Strip(frame), "apikey=REDACTED")
	assert.NotContains(t, frame, prowlarrTestKey)
	assert.Contains(t, m.prowlarr.detail.lines(), overviewLine("Download URL", m.prowlarr.redact(m.prowlarr.rows[0]).DownloadURL))
	assert.NotContains(t, m.prowlarr.redact(m.prowlarr.rows[0]).DownloadURL, prowlarrTestKey)

	m = press(m, "esc")
	assert.Equal(t, paneResults, m.focusedPane())

	m = press(m, "j", "j")
	frame = m.render()
	assert.Contains(t, ansi.Strip(frame), "hostile")
	assert.NotContains(t, frame, "\x1b[31m")
	assert.NotContains(t, frame, prowlarrTestKey)

	assert.Equal(t, prowlarrTestKey, apiKeyOf(t, m.prowlarr.rows[2].DownloadURL), "the stored release stays byte-exact")

	m = press(m, "}")
	assert.False(t, m.prowlarr.detailOpen)
}

// TestReleaseEscLadder checks esc from a focused query blurs it, from the
// blurred search pane leaves the window, and from the results leaves too.
func TestReleaseEscLadder(t *testing.T) {
	_, m := prowlarrModel(t, 120, 40)

	m = press(m, "/")

	m, rung := m.escape()
	assert.Equal(t, "blur input", rung)
	assert.Equal(t, paneSearch, m.focusedPane())

	left, rung := m.escape()
	assert.Equal(t, "return to torrents", rung)
	assert.Equal(t, windowTorrents, left.window)

	m = press(m, "tab")
	require.Equal(t, paneResults, m.focusedPane())

	_, rung = m.escape()
	assert.Equal(t, "return to torrents", rung)
}

// TestProwlarrFocusMovesVertically checks tab and ctrl+w walk the stacked panes.
func TestProwlarrFocusMovesVertically(t *testing.T) {
	_, m := searchModel(t, 120, 40)

	m = press(m, "}")
	assert.Equal(t, []Pane{paneSearch, paneResults, paneResultDetail}, m.openPanes())

	assert.Equal(t, paneResultDetail, press(m, "ctrl+w", "j").focusedPane())
	assert.Equal(t, paneSearch, press(m, "ctrl+w", "k").focusedPane())
	assert.Equal(t, paneResults, press(m, "ctrl+w", "h").focusedPane())
	assert.Equal(t, paneResultDetail, press(m, "tab").focusedPane())

	m = press(m, "tab", "}")
	assert.Equal(t, paneResults, m.focusedPane(), "closing the focused pane returns to the results")
}

// TestProwlarrKeepsTheTorrentsWindow checks the torrents window is untouched by a
// Prowlarr session: its client, rows, and focus.
func TestProwlarrKeepsTheTorrentsWindow(t *testing.T) {
	_, m := searchModel(t, 120, 40)

	m = press(m, "ctrl+t")

	assert.Equal(t, windowTorrents, m.window)
	assert.Equal(t, paneList, m.focusedPane())
	assert.Len(t, m.torrents.rows, len(mockTorrents()))
	assert.NotContains(t, m.statusBarView(120), "prowlarr:")
}

// TestProwlarrSearchErrorIsShown checks a failed search is drawn in the pane and
// the status bar.
func TestProwlarrSearchErrorIsShown(t *testing.T) {
	stub, m := prowlarrModel(t, 120, 40)
	stub.status.Store(500)

	m = runSearchFor(t, m, "ubuntu")

	require.Error(t, m.prowlarr.err)
	assert.Contains(t, ansi.Strip(m.render()), "Prowlarr error")
	assert.Contains(t, m.toast, "search failed")
	assert.Contains(t, m.statusBarView(120), "prowlarr: ! server error")
}

// TestProwlarrPickerListsProwlarrAliases checks S lists Prowlarr aliases in the
// Prowlarr window and Transmission ones elsewhere, and that a Prowlarr switch
// never touches the Transmission client.
func TestProwlarrPickerListsProwlarrAliases(t *testing.T) {
	_, srv := newRPCStub(t)
	stub := newProwlarrStub(t)

	var dialed []string

	m := withServers(pollOnce(t, liveModel(t, srv, 120, 40)), pickerServers())
	m = withProwlarr(m, stub, []ServerEntry{
		{Name: "indexer", URL: "http://prowlarr:9696", Default: true},
		{Name: "public", URL: "http://p2:9696"},
	}, &dialed)
	m.prowlarr.opened = true
	m = runKey(t, m, "ctrl+p")
	m = runSearchFor(t, m, "ubuntu")
	m.prowlarr.columns = []string{"title"}

	transmissionClient := m.client

	m = press(m, "S")

	p, ok := topAs[pickerOverlay](m)
	require.True(t, ok)
	assert.True(t, p.prowlarr)
	assert.Equal(t, "indexer", p.current)
	assert.Len(t, p.servers, 2)
	assert.Contains(t, ansi.Strip(m.render()), "Switch Prowlarr server")

	m = press(m, "j")
	m, cmd := pressCmd(m, "enter")

	assert.True(t, m.prowlarr.dialing)
	assert.Equal(t, "switching servers…", press(m, "S").toast, "S is refused mid-dial")

	m = settle(t, m, cmd)

	assert.Equal(t, []string{"indexer", "public"}, dialed)
	assert.Equal(t, "public", m.prowlarr.serverName)
	assert.Same(t, transmissionClient, m.client)
	assert.Equal(t, "home", m.serverName)
	assert.Empty(t, m.prowlarr.results, "the old server's results are gone")
	assert.Equal(t, "ubuntu", m.prowlarr.queryInput.Value(), "the query text stays")
	assert.Equal(t, []string{"title"}, m.prowlarr.columns, "the view stays")
	assert.Equal(t, "switched to public", m.toast)

	t2, ok := topAs[pickerOverlay](press(press(m, "ctrl+t"), "S"))
	require.True(t, ok)
	assert.False(t, t2.prowlarr)
	assert.Equal(t, "home", t2.current)
}

// TestProwlarrSwitchFailureKeepsTheServer checks a failed switch keeps the old
// client and its results.
func TestProwlarrSwitchFailureKeepsTheServer(t *testing.T) {
	_, m := searchModel(t, 120, 40)
	client := m.prowlarr.client

	m.deps.DialProwlarr = func(string) (ProwlarrDialResult, error) { return ProwlarrDialResult{}, errors.New("boom") }

	m, cmd := m.switchProwlarr("public")
	m = settle(t, m, cmd)

	assert.Same(t, client, m.prowlarr.client)
	assert.Len(t, m.prowlarr.rows, 3)
	assert.Equal(t, "switch failed: boom", m.toast)
}

// TestTransmissionPickerRefusedWhileProwlarrDials checks the other direction of
// the shared error-writer rule.
func TestTransmissionPickerRefusedWhileProwlarrDials(t *testing.T) {
	m := withServers(newTestModel(120, 40), pickerServers())
	m.prowlarr.dialing = true

	m = press(m, "S")

	assert.False(t, m.hasOverlay(pickerOverlayKind))
	assert.Equal(t, "switching servers…", m.toast)
}
