package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// listRow is the screen point of the list pane's nth row, in a table that has
// not scrolled.
func listRow(m Model, n int) (x, y int) {
	inner := m.layout().list.inner()

	return inner.x + 10, inner.y + 1 + n
}

// TestViewTurnsOnMouseReporting checks the view asks for cell motion only when
// client.mouse is on, so toggling it applies on the next frame.
func TestViewTurnsOnMouseReporting(t *testing.T) {
	m := newTestModel(120, 40)
	assert.Equal(t, tea.MouseModeCellMotion, m.View().MouseMode)

	m.mouse = false
	assert.Equal(t, tea.MouseModeNone, m.View().MouseMode)
}

// TestMouseLadder checks the router's precedence: the toast clears, nothing
// happens with the mouse off or below the size floor, a focused input is
// blurred before the click lands, a chord is canceled, and a release or a
// middle click does nothing.
func TestMouseLadder(t *testing.T) {
	base := newTestModel(120, 40)
	x, y := listRow(base, 3)

	t.Run("clears the toast", func(t *testing.T) {
		m := base
		m.toast = "hello"

		assert.Empty(t, click(m, x, y).toast)
		assert.Empty(t, wheel(m, x, y, 1).toast)
	})

	t.Run("off", func(t *testing.T) {
		m := base
		m.mouse = false

		assert.Zero(t, click(m, x, y).torrents.list.cursor())
		assert.Zero(t, wheel(m, x, y, 2).torrents.list.cursor())
	})

	t.Run("too small", func(t *testing.T) {
		m := base.setFocus(paneFilter).resize(40, 10)
		m = click(m, 30, 4)

		assert.Equal(t, paneFilter, m.focusedPane())
		assert.Zero(t, m.torrents.list.cursor())
	})

	t.Run("blurs the input, then lands", func(t *testing.T) {
		m := typeText(press(base, "/"), "i")
		require.True(t, m.inputFocused)

		_, row := listRow(m, 0)
		m = click(m, x, row)

		assert.False(t, m.inputFocused)
		assert.Equal(t, "i", m.torrents.listView.search, "blurring the search keeps its text")
		assert.Equal(t, row-listRowTop(m), m.torrents.list.cursor())
	})

	t.Run("a click on the search keeps it", func(t *testing.T) {
		m := typeText(press(base, "/"), "i")
		fx, fy := locate(t, m, "/ i")

		m = click(m, fx, fy)

		assert.True(t, m.inputFocused)
		assert.Equal(t, ownerSearch, m.owner)
	})

	t.Run("cancels a chord", func(t *testing.T) {
		m := press(base, "y")
		require.True(t, m.chord.pending())

		m = click(m, x, y)

		assert.False(t, m.chord.pending())
		assert.Equal(t, 3, m.torrents.list.cursor())
	})

	t.Run("release, motion, and middle do nothing", func(t *testing.T) {
		for _, msg := range []tea.Msg{
			tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft},
			tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft},
			tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseMiddle},
		} {
			m, cmd := base.update(msg)

			assert.Zero(t, m.torrents.list.cursor(), "%T", msg)
			assert.Nil(t, cmd)
		}
	})
}

// listRowTop is the screen row of the list's first row.
func listRowTop(m Model) int {
	return m.layout().list.inner().y + 1
}

// TestClickFocusesAPane checks a click anywhere in a pane, border included,
// focuses it, and a right click does the same.
func TestClickFocusesAPane(t *testing.T) {
	m := press(newTestModel(120, 40), "}")
	l := m.layout()

	m = click(m, l.filter.x, l.filter.y)
	assert.Equal(t, paneFilter, m.focusedPane())

	m = rightClick(m, l.detail.x+3, l.detail.y+l.detail.h-1)
	assert.Equal(t, paneDetail, m.focusedPane())

	m = click(m, l.list.x+l.list.w-1, l.list.y+l.list.h-1)
	assert.Equal(t, paneList, m.focusedPane())
}

// TestClickSelectsATorrentAndClickAgainOpensDetail checks a click moves the
// cursor and the selection, and a second click on the cursor row of the focused
// list opens the detail pane, as enter does.
func TestClickSelectsATorrentAndClickAgainOpensDetail(t *testing.T) {
	m := newTestModel(120, 40).setFocus(paneFilter)
	x, y := locate(t, m, "archlinux")

	m = click(m, x, y)

	assert.Equal(t, paneList, m.focusedPane())
	assert.Equal(t, 2, m.torrents.list.cursor())
	assert.Equal(t, 2, m.torrents.sel.cursor)
	require.NotNil(t, m.torrents.detail.torrent)
	assert.Equal(t, int64(3), m.torrents.detail.torrent.ID, "the detail pane follows the cursor")
	assert.False(t, m.torrents.detailOpen)

	m = click(m, x, y)

	assert.True(t, m.torrents.detailOpen)
	assert.Equal(t, paneDetail, m.focusedPane())
}

// TestFirstClickOnAnUnfocusedCursorRowOnlyFocuses checks a click on the cursor
// row of a list that was not focused does not open the detail pane.
func TestFirstClickOnAnUnfocusedCursorRowOnlyFocuses(t *testing.T) {
	m := newTestModel(120, 40).setFocus(paneFilter)
	x, y := listRow(m, 0)

	m = click(m, x, y)

	assert.Equal(t, paneList, m.focusedPane())
	assert.False(t, m.torrents.detailOpen)
}

// TestClickKeepsMarks checks a click moves the cursor without touching marks.
func TestClickKeepsMarks(t *testing.T) {
	m := press(newTestModel(120, 40), "space")
	x, y := listRow(m, 4)

	m = click(m, x, y)

	assert.Equal(t, 1, m.torrents.sel.markCount())
	assert.Equal(t, 4, m.torrents.list.cursor())
}

// TestRightClickOpensTheActionMenu checks a right click moves the cursor and
// opens the menu over it, as m does.
func TestRightClickOpensTheActionMenu(t *testing.T) {
	_, m := polledModel(t)
	x, y := listRow(m, 1)

	m = rightClick(m, x, y)

	assert.Equal(t, 1, m.torrents.list.cursor())
	require.True(t, m.hasOverlay(menuOverlayKind))

	o, _ := topAs[menuOverlay](m)
	assert.Equal(t, "Actions (1 torrent)", o.title)
}

// TestWheelScrollsTheListWithoutFocusing checks each notch moves the cursor
// one row, from any pane, and leaves focus where it was.
func TestWheelScrollsTheListWithoutFocusing(t *testing.T) {
	m := newTestModel(60, 16).setFocus(paneFilter)
	x, y := listRow(m, 0)

	m = wheel(m, x, y, 11)

	assert.Equal(t, paneFilter, m.focusedPane())
	assert.Equal(t, 11, m.torrents.list.cursor())
	assert.Equal(t, 11, m.torrents.sel.cursor)

	m = wheel(m, x, y, -3)
	assert.Equal(t, 8, m.torrents.list.cursor())

	m = wheel(m, x, y, -20)
	assert.Zero(t, m.torrents.list.cursor(), "the wheel stops at the top")
}

// TestClickAfterScrollSelectsWhatIsDrawn checks a click on a scrolled table
// selects the row drawn under the pointer, which is the case the row markers
// exist for.
func TestClickAfterScrollSelectsWhatIsDrawn(t *testing.T) {
	m := wheel(newTestModel(60, 16), 30, 3, 11)
	x, y := locate(t, m, "freebsd")

	m = click(m, x, y)

	require.NotNil(t, m.torrents.detail.torrent)
	assert.Equal(t, int64(7), m.torrents.detail.torrent.ID)
}

// TestHeaderClickSortsAndFlips checks a heading sorts by its column, the same
// heading again flips the direction, and a column with no sort says so.
func TestHeaderClickSortsAndFlips(t *testing.T) {
	m := newTestModel(120, 40)
	x, y := locate(t, m, "Ratio")

	m = click(m, x, y)
	assert.Equal(t, transmission.SortRatio, m.torrents.listView.sort)
	assert.False(t, m.torrents.listView.reverse)
	assert.Equal(t, "sort: ratio", m.toast)

	m = click(m, x, y)
	assert.True(t, m.torrents.listView.reverse)
	assert.Equal(t, "sort: ratio, reversed", m.toast)

	m = click(m, x, y)
	assert.False(t, m.torrents.listView.reverse)

	sx, sy := locateIn(t, m, m.layout().list, "Status")
	m = click(m, sx, sy)
	assert.Equal(t, transmission.SortRatio, m.torrents.listView.sort, "a column with no sort changes nothing")
	assert.Equal(t, "no sort for status", m.toast)

	dx, dy := locate(t, m, "Down")
	m = click(m, dx, dy)
	assert.Equal(t, transmission.SortSpeed, m.torrents.listView.sort)
}

// TestHeaderClickPersistsTheSort checks a heading click writes the sort keys
// through the config store, as the o modal does.
func TestHeaderClickPersistsTheSort(t *testing.T) {
	deps := testDeps()
	cfg := newFakeConfig()
	deps.Config = cfg

	m := New(deps).applyTheme(true).resize(120, 40)
	x, y := locate(t, m, "Size")

	click(click(m, x, y), x, y)

	assert.Equal(t, string(transmission.SortSize), cfg.value(clientKeyListSort))
	assert.Equal(t, true, cfg.value(clientKeyListReverse))
}

// TestClickAppliesASidebarFilter checks a click on a sidebar row focuses the
// sidebar, moves its cursor, and applies the filter, clearing marks.
func TestClickAppliesASidebarFilter(t *testing.T) {
	m := press(newTestModel(120, 40), "space")
	x, y := locateIn(t, m, m.layout().filter, "Stopped")

	m = click(m, x, y)

	assert.Equal(t, paneFilter, m.focusedPane())
	assert.Equal(t, "Stopped", m.torrents.filter.current().label)
	assert.Equal(t, m.torrents.filter.cursor, m.torrents.filter.applied)
	assert.Equal(t, transmission.FilterStopped, m.torrents.listView.filter.status)
	assert.Len(t, m.torrents.rows, 2)
	assert.Zero(t, m.torrents.sel.markCount(), "a filter clears marks")

	hx, hy := locateIn(t, m, m.layout().filter, "Labels")
	before := m.torrents.filter
	m = click(m, hx, hy)
	assert.Equal(t, before, m.torrents.filter, "a heading applies nothing")
}

// TestWheelMovesTheSidebarWithoutApplying checks the wheel steps the sidebar
// cursor over selectable rows and applies nothing.
func TestWheelMovesTheSidebarWithoutApplying(t *testing.T) {
	m := newTestModel(120, 40)
	l := m.layout()

	m = wheel(m, l.filter.x+2, l.filter.y+2, 8)

	assert.Equal(t, paneList, m.focusedPane())
	assert.Equal(t, "linux", m.torrents.filter.current().label, "the Labels heading is skipped")
	assert.False(t, m.torrents.listView.filter.set())
}

// TestClickSwitchesDetailTabs checks a tab click selects it and focuses the
// pane.
func TestClickSwitchesDetailTabs(t *testing.T) {
	m := press(newTestModel(120, 40), "}")
	x, y := locateIn(t, m, m.layout().detail, "Trackers")

	m = click(m, x, y)

	assert.Equal(t, tabTrackers, m.torrents.detail.tab)
	assert.Equal(t, paneDetail, m.focusedPane())
	assert.True(t, m.torrents.detail.trackers.table.Focused(), "the tab's table takes the focus")
}

// TestClickSelectsAFileAndRightClickOpensTheFileMenu checks the files tab's
// rows follow a click, and a right click opens the file menu over the row.
func TestClickSelectsAFileAndRightClickOpensTheFileMenu(t *testing.T) {
	m := staticFiles().setFocus(paneList)
	x, y := locateIn(t, m, m.layout().detail, "SHA256SUMS")

	m = click(m, x, y)

	assert.Equal(t, paneDetail, m.focusedPane())
	assert.Equal(t, 1, m.torrents.detail.files.cursor())
	assert.Equal(t, 1, m.torrents.detail.fileSel.cursor)

	_, live := liveFilesModel(t, 120, 40)
	lx, ly := locateIn(t, live, live.layout().detail, "Index")

	live = rightClick(live, lx, ly+2)

	assert.Equal(t, 1, live.torrents.detail.files.cursor())
	assert.True(t, live.hasOverlay(menuOverlayKind))
}

// TestWheelScrollsTheDetailPane checks the wheel moves a table tab's cursor and
// scrolls the overview, without focusing the pane.
func TestWheelScrollsTheDetailPane(t *testing.T) {
	m := staticFiles().setFocus(paneList)
	l := m.layout()

	m = wheel(m, l.detail.x+5, l.detail.y+5, 2)

	assert.Equal(t, paneList, m.focusedPane())
	assert.Equal(t, 2, m.torrents.detail.files.cursor())
	assert.Equal(t, 2, m.torrents.detail.fileSel.cursor)

	m = press(m.setFocus(paneList), "1").resize(120, 20)
	l = m.layout()
	require.Positive(t, m.torrents.detail.vp.TotalLineCount()-m.torrents.detail.vp.Height(), "the overview must overflow")

	m = wheel(m, l.detail.x+5, l.detail.y+3, 1)
	assert.Equal(t, wheelLines, m.torrents.detail.vp.YOffset())
}

// TestWheelOverChromeDoesNothing checks the help line and status bar are not a
// wheel target.
func TestWheelOverChromeDoesNothing(t *testing.T) {
	m := newTestModel(120, 40)
	l := m.layout()

	assert.Zero(t, wheel(m, 5, l.statusBar.y, 3).torrents.list.cursor())
	assert.Zero(t, wheel(m, 5, l.helpLine.y, 3).torrents.list.cursor())
}

// TestClickOpensTheProwlarrQuery checks a click on the search pane focuses its
// field, and a click elsewhere blurs it keeping the draft.
func TestClickOpensTheProwlarrQuery(t *testing.T) {
	_, m := prowlarrModel(t, 120, 40)
	l := m.layout()

	m = click(m, l.search.x+10, l.search.y+1)
	assert.True(t, m.inputFocused)
	assert.Equal(t, ownerQuery, m.owner)

	m = typeText(m, "abc")
	m = click(m, l.search.x+20, l.search.y+1)
	assert.True(t, m.inputFocused, "a click on the field keeps it")

	m = click(m, l.list.x+5, l.list.y+5)
	assert.False(t, m.inputFocused)
	assert.Equal(t, paneResults, m.focusedPane())
	assert.Equal(t, "abc", m.prowlarr.queryInput.Value())
}

// TestClickSelectsAReleaseAndOpensItsDetail checks the results table follows a
// click, a second click opens the detail pane, and a right click opens the
// release menu.
func TestClickSelectsAReleaseAndOpensItsDetail(t *testing.T) {
	_, m := searchModel(t, 120, 40)
	m = m.setFocus(paneSearch)
	inner := m.layout().list.inner()

	m = click(m, inner.x+10, inner.y+2)

	assert.Equal(t, paneResults, m.focusedPane())
	assert.Equal(t, 1, m.prowlarr.table.Cursor())
	assert.Equal(t, keyOf(m.prowlarr.rows[1]), m.prowlarr.cursor)
	assert.False(t, m.prowlarr.detailOpen)

	m = click(m, inner.x+10, inner.y+2)
	assert.True(t, m.prowlarr.detailOpen)
	assert.Equal(t, paneResultDetail, m.focusedPane())

	m = rightClick(m, inner.x+10, inner.y+3)
	assert.Equal(t, 2, m.prowlarr.table.Cursor())
	assert.True(t, m.hasOverlay(menuOverlayKind))
}

// TestResultHeaderClickSorts checks a results heading sorts by its field and
// flips on a second click.
func TestResultHeaderClickSorts(t *testing.T) {
	_, m := searchModel(t, 120, 40)
	x, y := locateIn(t, m, m.layout().list, "Seeders")

	m = click(m, x, y)
	assert.Equal(t, releaseSort{field: releaseSortSeeders}, m.prowlarr.sort)

	m = click(m, x, y)
	assert.Equal(t, releaseSort{field: releaseSortSeeders, reverse: true}, m.prowlarr.sort)
}

// TestWheelScrollsTheResults checks the wheel moves the results cursor and the
// release detail follows it.
func TestWheelScrollsTheResults(t *testing.T) {
	_, m := searchModel(t, 120, 40)
	inner := m.layout().list.inner()

	m = wheel(m, inner.x, inner.y+4, 2)

	assert.Equal(t, 2, m.prowlarr.table.Cursor())
	assert.Equal(t, keyOf(m.prowlarr.rows[2]), m.prowlarr.cursor)
}

// TestClickSwitchesSettingsTabs checks a tab click selects it, closing an open
// dropdown.
func TestClickSwitchesSettingsTabs(t *testing.T) {
	m := settingsModel(120, 40)
	m.settings = m.settings.openDropdown()
	x, y := locate(t, m, "Bandwidth")

	m = click(m, x, y)

	assert.Equal(t, tabBandwidth, m.settings.tab)
	assert.Equal(t, modeNav, m.settings.mode)
}

// TestClickOnASettingsRowMovesThenEdits checks the first click on a row moves
// the cursor and the second is enter: a toggle flips, a text row edits.
func TestClickOnASettingsRowMovesThenEdits(t *testing.T) {
	m := settingsModel(120, 40)
	x, y := locate(t, m, clientKeyNerdFont)

	m = click(m, x, y)
	assert.Equal(t, clientKeyNerdFont, cursorKey(m))
	assert.Zero(t, m.settings.dirtyCount(), "the first click only moves")

	m = click(m, x, y)
	assert.Equal(t, 1, m.settings.dirtyCount(), "the second click flips the toggle")

	m = networkTab(m)
	px, py := locate(t, m, "peer-port ")

	m = click(m, px, py)
	require.True(t, m.inputFocused)
	assert.Equal(t, ownerSettings, m.owner)

	m = click(m, px+30, py)
	assert.True(t, m.inputFocused, "a click on the row being edited keeps the input")

	m = typeText(m, "9")
	ox, oy := locate(t, m, "peer-port-random-on-start")
	m = click(m, ox, oy)

	assert.False(t, m.inputFocused, "a click on another row discards the draft")
	assert.Equal(t, "peer-port-random-on-start", cursorKey(m))
	assert.Equal(t, 1, m.settings.dirtyCount(), "the draft was not committed")
}

// TestClickPicksADropdownOption checks a click on an open dropdown's option
// commits it, and a click on its row closes it.
func TestClickPicksADropdownOption(t *testing.T) {
	m := settingsModel(120, 40)
	x, y := locate(t, m, clientKeyTheme)

	m = click(m, x, y)
	require.Equal(t, modeSelect, m.settings.mode)

	nx, ny := locate(t, m, "nord")
	m = click(m, nx, ny)

	assert.Equal(t, modeNav, m.settings.mode)
	assert.Equal(t, "nord", m.settings.current().value)

	m = click(m, x, y)
	require.Equal(t, modeSelect, m.settings.mode)

	m = click(m, x, y)
	assert.Equal(t, modeNav, m.settings.mode)
	assert.Equal(t, "nord", m.settings.current().value, "closing keeps the value")
}

// TestClickPressesSettingsButtons checks Revert discards and Save starts a
// save.
func TestClickPressesSettingsButtons(t *testing.T) {
	m := withDirtySetting(settingsModel(120, 40))
	require.Equal(t, 1, m.settings.dirtyCount())

	rx, ry := locate(t, m, "Revert")
	reverted := click(m, rx, ry)
	assert.Zero(t, reverted.settings.dirtyCount())

	sx, sy := locate(t, m, "Save (1)")
	m, cmd := m.update(clickMsg(sx, sy))

	assert.Equal(t, stateSaving, m.settings.state)
	assert.NotNil(t, cmd)
}

// TestWheelInSettingsMovesTheCursorOnly checks the wheel moves the row cursor,
// stops at the ends, moves an open dropdown's cursor, and never changes a value.
func TestWheelInSettingsMovesTheCursorOnly(t *testing.T) {
	m := settingsModel(120, 40)
	require.Equal(t, clientKeyTheme, cursorKey(m))

	m = wheel(m, 20, 5, 1)
	assert.Equal(t, clientKeyNerdFont, cursorKey(m))
	assert.Zero(t, m.settings.dirtyCount())

	m = wheel(m, 20, 5, -5)
	assert.Equal(t, clientKeyTheme, cursorKey(m), "the wheel stops at the top")

	m = wheel(m, 20, 5, 50)
	assert.Equal(t, buttonRevert, cursorKey(m), "the wheel stops at the bottom")

	m = press(wheel(m, 20, 5, -50), "enter")
	require.Equal(t, modeSelect, m.settings.mode)

	m = wheel(m, 20, 5, 1)
	assert.Equal(t, 1, m.settings.selectIdx)
	assert.Zero(t, m.settings.dirtyCount(), "moving the dropdown commits nothing")
}

// TestClickOnAMenuRowRunsIt checks a click on a menu row runs it as enter does:
// a submenu opens, a leaf runs.
func TestClickOnAMenuRowRunsIt(t *testing.T) {
	_, m := polledModel(t)
	m = press(m, "m")
	r := m.placeBox(m.overlays[0].view(m, m.width, m.height))

	x, y := locateIn(t, m, r, "queue ▸")
	m = click(m, x, y)

	require.Len(t, m.overlays, 2)
	sub, _ := topAs[menuOverlay](m)
	assert.Equal(t, "Actions (1 torrent) / queue", sub.title)

	m = press(m, "esc")
	x, y = locateIn(t, m, r, "rename torrent")
	m = click(m, x, y)

	assert.False(t, m.hasOverlay(menuOverlayKind), "a leaf closes the menu")
	assert.True(t, m.hasOverlay(promptOverlayKind), "and runs: rename opens its prompt")
}

// TestClickOutsideClosesTheTopOverlay checks a click outside the topmost box
// pops it, one level at a time, and a click inside a box that is not a row
// does nothing.
func TestClickOutsideClosesTheTopOverlay(t *testing.T) {
	_, m := polledModel(t)
	m = openQueueMenu(m)
	require.Len(t, m.overlays, 2)

	top := m.placeBox(m.overlays[1].view(m, m.width, m.height))
	m = click(m, top.x+1, top.y+1)
	require.Len(t, m.overlays, 2, "the title is not a row")

	m = click(m, 0, 0)
	assert.Len(t, m.overlays, 1)

	m = rightClick(m, 0, 0)
	assert.Empty(t, m.overlays)
}

// TestOverlayBlocksThePaneBeneath checks a click where a list row lies under an
// open overlay's box reaches neither, and one outside only closes.
func TestOverlayBlocksThePaneBeneath(t *testing.T) {
	m := press(newTestModel(120, 40), "?")
	x, y := listRow(m, 3)

	m = click(m, x, y)

	assert.Zero(t, m.torrents.list.cursor())
	assert.Empty(t, m.overlays)
}

// TestClickOutsideASavePromptKeepsTheWindow checks a click outside the save
// prompt is esc: the prompt closes and the window stays open with its changes.
func TestClickOutsideASavePromptKeepsTheWindow(t *testing.T) {
	m := press(withDirtySetting(settingsModel(120, 40)), "esc")
	require.True(t, m.hasOverlay(confirmOverlayKind))

	m = click(m, 0, 0)

	assert.Empty(t, m.overlays)
	assert.Equal(t, windowSettings, m.window)
	assert.Equal(t, 1, m.settings.dirtyCount())
}

// TestClickOnModalRows checks a click on a row of each list modal points the
// cursor there and runs the row's key.
func TestClickOnModalRows(t *testing.T) {
	t.Run("sort applies", func(t *testing.T) {
		m := press(newTestModel(120, 40), "o")
		x, y := locate(t, m, "progress")

		m = click(m, x, y)

		assert.Empty(t, m.overlays)
		assert.Equal(t, transmission.SortProgress, m.torrents.listView.sort)
	})

	t.Run("columns toggles", func(t *testing.T) {
		m := press(newTestModel(120, 40), "c")
		x, y := locate(t, m, "[ ] hash")

		m = click(m, x, y)

		o, ok := topAs[columnsOverlay](m)
		require.True(t, ok, "a click toggles; enter applies")
		assert.Contains(t, o.selected(), "hash")
	})

	t.Run("filter applies", func(t *testing.T) {
		m := press(newTestModel(120, 40), "f")
		r := m.placeBox(m.overlays[0].view(m, m.width, m.height))
		x, y := locateIn(t, m, r, "Error")

		m = click(m, x, y)

		assert.Empty(t, m.overlays)
		assert.Equal(t, transmission.FilterError, m.torrents.listView.filter.status)
	})

	t.Run("filter heading is not a row", func(t *testing.T) {
		m := press(newTestModel(120, 40), "f")
		r := m.placeBox(m.overlays[0].view(m, m.width, m.height))
		x, y := locateIn(t, m, r, "Labels")

		m = click(m, x, y)

		assert.Len(t, m.overlays, 1)
	})

	t.Run("palette runs", func(t *testing.T) {
		m := typeText(press(newTestModel(120, 40), ":"), "help")
		r := m.placeBox(m.overlays[0].view(m, m.width, m.height))
		x, y := locateIn(t, m, r, "> help")

		m = click(m, x, y)

		assert.False(t, m.hasOverlay(paletteOverlayKind))
		assert.True(t, m.hasOverlay(helpOverlayKind))
		assert.False(t, m.inputFocused, "the palette released the keyboard")
	})

	t.Run("form edits a row", func(t *testing.T) {
		_, m := addModel(t, "")
		m = press(m, "A")
		require.True(t, m.hasOverlay(formOverlayKind))

		r := m.placeBox(m.overlays[0].view(m, m.width, m.height))
		x, y := locateIn(t, m, r, "Labels")
		m = click(m, x, y)

		o, _ := topAs[formOverlay](m)
		assert.Equal(t, addRowLabels, o.cursor)
		assert.True(t, o.editing)
		assert.Equal(t, ownerForm, m.owner)

		m = typeText(m, "x")
		sx, sy := locateIn(t, m, r, "Start now")
		m = click(m, sx, sy)

		o, _ = topAs[formOverlay](m)
		assert.False(t, o.editing, "a click on another row discards the draft")
		assert.Empty(t, o.rows[addRowLabels].text)
		assert.True(t, o.rows[addRowStart].on, "and the click lands: the toggle flips")
		assert.False(t, m.inputFocused)
	})
}

// TestWheelInOverlays checks the wheel moves a modal's cursor and scrolls the
// help view, wherever the pointer is.
func TestWheelInOverlays(t *testing.T) {
	m := press(newTestModel(120, 40), "o")
	m = wheel(m, 0, 0, 2)

	o, _ := topAs[sortOverlay](m)
	assert.Equal(t, 2, o.cursor)

	h := press(newTestModel(60, 16), "?")
	h = wheel(h, 30, 8, 1)

	help, _ := topAs[helpOverlay](h)
	assert.Equal(t, wheelLines, help.vp.YOffset())
}
