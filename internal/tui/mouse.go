package tui

import (
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

// The mouse is a convenience layer over the keyboard. Every click and every
// notch of the wheel does what an existing key does, by calling the transition
// that key's handler calls; it never adds a capability the keyboard lacks, and
// it never adds a registry action.

// wheelLines is how far one notch of the wheel scrolls a viewport.
const wheelLines = 3

// handleMouse is the mouse's router, the counterpart of handleKey. Only clicks
// and the wheel act; a release or a drag is dropped. Each step returns early, so
// the order below is the whole precedence story:
//
//  1. clear any toast, as a key press does
//  2. with client.mouse off, or below the size floor, do nothing
//  3. an open overlay owns the pointer: a click on a modal row runs the row, a
//     click outside the topmost box closes it (esc's "close overlay" rung), and
//     the wheel moves the overlay's cursor. Nothing beneath is reachable
//  4. a click blurs a focused text input, the way esc does, unless it landed
//     on that input — then the click is the input's
//  5. a click cancels a pending chord
//  6. what hitTest says is under the pointer decides
func (m Model) handleMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		return m.mouseClick(msg.Mouse())
	case tea.MouseWheelMsg:
		return m.mouseWheel(msg.Mouse())
	}

	return m, nil
}

// mouseActive clears the toast and reports whether the mouse may act at all.
func (m Model) mouseActive() (Model, bool) {
	m.toast = ""

	return m, m.mouse && !m.layout().tooSmall
}

// mouseClick routes a button press. The middle button and the rest do nothing.
func (m Model) mouseClick(ev tea.Mouse) (Model, tea.Cmd) {
	if ev.Button != tea.MouseLeft && ev.Button != tea.MouseRight {
		return m, nil
	}

	m, ok := m.mouseActive()
	if !ok {
		return m, nil
	}

	right := ev.Button == tea.MouseRight

	if len(m.overlays) > 0 {
		return m.clickOverlay(ev.X, ev.Y, right)
	}

	h := m.hitTest(ev.X, ev.Y)

	if m.inputFocused {
		if m.clickKeepsInput(h) {
			return m, nil
		}

		m = m.blurInput()
	}

	m.chord = chordNone

	return m.clickTarget(h, right)
}

// clickKeepsInput reports whether a click landed on the focused input itself,
// which keeps the keyboard rather than blurring it.
func (m Model) clickKeepsInput(h hit) bool {
	switch m.owner {
	case ownerSearch:
		return h.kind == hitFooter
	case ownerSettings:
		return h.kind == hitSettingsRow && h.index == m.settings.tabs[m.settings.tab].cursor
	case ownerNone, ownerPrompt, ownerForm, ownerPalette:
	}

	return false
}

// clickTarget runs a click on what hitTest found. A right click opens the
// action menu on a row that has one, and otherwise only focuses.
func (m Model) clickTarget(h hit, right bool) (Model, tea.Cmd) {
	switch h.kind {
	case hitNone, hitOverlayRow, hitOverlayBox, hitOverlayOutside:
		return m, nil
	case hitPane, hitFooter:
		return m.setFocus(h.pane), nil
	case hitListRow:
		return m.clickListRow(h.index, right)
	case hitListHeader:
		m = m.setFocus(paneList)
		if right {
			return m, nil
		}

		return m.sortByColumn(h.index), nil
	case hitFilterItem:
		m = m.setFocus(paneFilter)
		if right {
			return m, nil
		}

		m.torrents.filter.cursor = h.index

		return m.applyFilterItem(m.torrents.filter.current()), nil
	case hitDetailTab:
		m = m.setFocus(paneDetail)

		return m.detailTab(m.torrents.detail.gotoTab(h.index)), nil
	case hitDetailRow:
		m = m.setFocus(paneDetail)
		m.torrents.detail = m.torrents.detail.pointAt(h.index)

		if right && m.torrents.detail.tab == tabFiles {
			return m.dispatch(actionFileMenu, tea.KeyPressMsg{})
		}

		return m, nil
	}

	if right {
		return m, nil
	}

	return m.clickSettings(h)
}

// clickSettings runs a left click on a target in the settings window, where a
// right click does nothing.
func (m Model) clickSettings(h hit) (Model, tea.Cmd) {
	switch h.kind {
	case hitSettingsTab:
		return runSettings(m, func(m Model) (Model, tea.Cmd) {
			m.settings = m.settings.gotoTab(h.index)

			return m, nil
		})
	case hitSettingsRow:
		return runSettings(m, func(m Model) (Model, tea.Cmd) { return m.clickSettingsRow(h.index) })
	case hitSettingsOption:
		return runSettings(m, func(m Model) (Model, tea.Cmd) {
			m.settings.selectIdx = h.index
			m.settings = m.settings.commitDropdown()

			return m, nil
		})
	case hitSettingsButton:
		key := buttonSave
		if h.index == buttonIndexRevert {
			key = buttonRevert
		}

		return runSettings(m, func(m Model) (Model, tea.Cmd) { return m.pressButton(key) })
	}

	return m, nil
}

// runSettings runs a settings transition behind the guard every settings key
// runs behind: a save in flight blocks it, and the cursor row stays in view.
func runSettings(m Model, f func(Model) (Model, tea.Cmd)) (Model, tea.Cmd) {
	return settingsHandler(f)(m, tea.KeyPressMsg{})
}

// clickListRow is a click on a torrent row: focus the list and put the cursor
// there. A click on the cursor row of an already focused list opens the detail
// pane, as enter does; a right click opens the action menu over the selection,
// as m does.
func (m Model) clickListRow(i int, right bool) (Model, tea.Cmd) {
	again := m.focusedPane() == paneList && i == m.torrents.list.cursor()

	m = m.setFocus(paneList)
	m.torrents.list.table = pointTable(m.torrents.list.table, i)
	m.torrents.sel = m.torrents.sel.setCursor(m.torrents.list.cursor())
	m = m.syncTorrents()

	switch {
	case right:
		return m.dispatch(actionMenuOpen, tea.KeyPressMsg{})
	case again:
		return m.openDetail(), nil
	}

	return m, nil
}

// clickSettingsRow is a click on a field row. A click on another row moves the
// cursor there; a click on the cursor row is enter. An open dropdown is closed
// first, keeping its row's value, whichever row was clicked.
func (m Model) clickSettingsRow(i int) (Model, tea.Cmd) {
	w := m.settings
	cursor := w.tabs[w.tab].cursor

	if w.mode == modeSelect {
		m.settings = w.closeDropdown().pointAt(i)

		return m, nil
	}

	if i != cursor {
		m.settings = w.pointAt(i)

		return m, nil
	}

	return m.settingsEnter()
}

// clickOverlay routes a click while an overlay is open.
func (m Model) clickOverlay(x, y int, right bool) (Model, tea.Cmd) {
	h := m.hitOverlay(x, y)

	switch {
	case h.kind == hitOverlayOutside:
		return m.pop(), nil
	case h.kind != hitOverlayRow || right:
		return m, nil
	}

	top, ok := m.topOverlay()
	if !ok {
		return m, nil
	}

	// A form row being edited keeps a click on itself; a click on another row
	// discards its draft first, as esc would.
	if f, isForm := top.(formOverlay); isForm && f.editing {
		if h.index == f.cursor {
			return m, nil
		}

		m = m.blurInput()
		top, _ = m.topOverlay()
	}

	mo, ok := top.(modalOverlay)
	if !ok {
		return m, nil
	}

	o, ok := mo.pointAt(h.index)
	if !ok {
		return m, nil
	}

	return m.replaceTopOverlay(o).dispatch(mo.clickAction(), tea.KeyPressMsg{})
}

// mouseWheel routes a notch of the wheel. It scrolls what is under the pointer
// and never moves focus, applies a filter, or changes a value.
func (m Model) mouseWheel(ev tea.Mouse) (Model, tea.Cmd) {
	var delta int

	switch ev.Button {
	case tea.MouseWheelUp:
		delta = -1
	case tea.MouseWheelDown:
		delta = 1
	default:
		return m, nil
	}

	m, ok := m.mouseActive()
	if !ok {
		return m, nil
	}

	if len(m.overlays) > 0 {
		return m.wheelOverlay(delta)
	}

	h := m.hitTest(ev.X, ev.Y)
	if h.kind == hitNone {
		return m, nil
	}

	switch h.pane {
	case paneList:
		m.torrents.list.table = wheelTable(m.torrents.list.table, delta)
		m.torrents.sel = m.torrents.sel.setCursor(m.torrents.list.cursor())

		return m.syncTorrents(), nil
	case paneFilter:
		m.torrents.filter = m.torrents.filter.move(delta)
	case paneDetail:
		m.torrents.detail = m.torrents.detail.wheel(delta)
	case paneSettings:
		return runSettings(m, func(m Model) (Model, tea.Cmd) {
			m.settings = m.settings.wheel(delta)

			return m, nil
		})
	}

	return m, nil
}

// wheel is a notch over the settings window: the open dropdown's cursor, or
// the row cursor, stopping at the ends. It never flips a toggle or cycles a
// select, as up and down on a row would — a scroll must not change a value.
// A row being edited keeps the cursor where it is.
func (w settingsWindow) wheel(delta int) settingsWindow {
	switch w.mode {
	case modeSelect:
		return w.fieldStep(delta)
	case modeEdit:
		return w
	case modeNav:
	}

	before := w.tabs[w.tab].cursor
	next := w.moveField(delta)
	after := next.tabs[next.tab].cursor

	if (delta > 0 && after < before) || (delta < 0 && after > before) {
		return w
	}

	return next
}

// wheelOverlay is a notch while an overlay is open: the help view scrolls, and
// a modal moves its cursor as its own up and down keys do.
func (m Model) wheelOverlay(delta int) (Model, tea.Cmd) {
	top, ok := m.topOverlay()
	if !ok {
		return m, nil
	}

	switch o := top.(type) {
	case helpOverlay:
		o = o.fit(m, m.width, m.height)
		o.vp = wheelViewport(o.vp, delta)

		return m.replaceTopOverlay(o), nil
	case modalOverlay:
		up, down := o.wheelActions()
		if delta < 0 {
			return m.dispatch(up, tea.KeyPressMsg{})
		}

		return m.dispatch(down, tea.KeyPressMsg{})
	}

	return m, nil
}

// wheelTable moves a table's cursor delta rows. The table's own Update ignores
// every message while it is blurred, so the movement is called directly: the
// wheel scrolls a pane without focusing it.
func wheelTable(t table.Model, delta int) table.Model {
	if delta < 0 {
		t.MoveUp(-delta)
	} else {
		t.MoveDown(delta)
	}

	return t
}

// pointTable moves a table's cursor to row i, a row on screen, without
// scrolling. SetCursor would re-anchor the table's rendered rows on the new
// cursor while keeping its old scroll offset, which jumps the view on a long
// list; stepping a row at a time is what j and k do, and it leaves a visible
// target where it is drawn.
func pointTable(t table.Model, i int) table.Model {
	i = clamp(i, 0, max(0, len(t.Rows())-1))

	for t.Cursor() < i {
		t.MoveDown(1)
	}

	for t.Cursor() > i {
		t.MoveUp(1)
	}

	return t
}

// wheelViewport scrolls a viewport delta notches.
func wheelViewport(vp viewport.Model, delta int) viewport.Model {
	if delta < 0 {
		vp.ScrollUp(-delta * wheelLines)
	} else {
		vp.ScrollDown(delta * wheelLines)
	}

	return vp
}
