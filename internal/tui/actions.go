package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/itspriddle/snatcher/internal/output"
)

// handler runs one action. It takes the key press as well as the model because
// the navigation handler forwards the original message into whichever bubbles
// component has focus — which is what makes the component-keymap test
// non-vacuous: the component matches the same key strings the registry holds.
type handler func(Model, tea.KeyPressMsg) (Model, tea.Cmd)

// notYet returns a handler that reports an action as unimplemented.
//
// Every binding vision.md specifies is registered from T1, so the collision test
// sees the whole keyboard at once rather than discovering a clash three
// milestones later. The handlers behind the ones no milestone has built yet say
// so in the status bar, which is also better for a user than a key that appears
// to do nothing.
func notYet(a Action) handler {
	return func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) {
		m.toast = fmt.Sprintf("%s: not implemented yet", a)

		return m, nil
	}
}

// plain adapts a transition that needs neither the key press nor a command,
// which is most of them.
func plain(f func(Model) Model) handler {
	return func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) {
		return f(m), nil
	}
}

// handlers maps every registered action to the code that runs it.
//
// It is a map rather than a switch for two reasons: the invariant "every action
// has a handler and every handler an action" is then a test over two sets rather
// than a reading of a switch, and only this one function type returns a tea.Cmd,
// so the rest of the package can return plain Models without unparam objecting
// to a parameter that is always nil.
var handlers = map[Action]handler{}

// init fills the handler table. It is an init rather than a composite literal
// because several handlers refer to helpers that close over the model, and Go
// does not allow a package-level map literal to reference itself.
func init() {
	register(globalHandlers())
	register(navHandlers())
	register(torrentsHandlers())
	register(detailHandlers())
	register(settingsHandlers())
	register(helpHandlers())
	register(confirmHandlers())
	register(chordHandlers())
	register(spineHandlers())
	register(listHandlers())
	register(mutationHandlers())
	register(promptHandlers())
	register(addHandlers())
	register(menuHandlers())
	register(fileHandlers())
	register(trackerHandlers())
	register(paletteHandlers())
	register(notYetHandlers())
}

// register adds a batch of handlers, refusing a duplicate. A duplicate would
// mean two definitions of one action with the later silently winning.
func register(batch map[Action]handler) {
	for action, h := range batch {
		if _, dup := handlers[action]; dup {
			panic("tui: duplicate handler for action " + action)
		}

		handlers[action] = h
	}
}

// globalHandlers are the actions that mean the same thing in every window.
func globalHandlers() map[Action]handler {
	return map[Action]handler{
		actionQuit:    quitHandler,
		actionRefresh: refreshHandler,
		actionEscape: func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) {
			m, _ = m.escape()

			return m, nil
		},
		actionHelp:           plain(Model.toggleHelp),
		actionWindowTorrents: plain(func(m Model) Model { return m.switchWindow(windowTorrents) }),
		actionWindowSettings: func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.openSettings() },
	}
}

// navHandlers are the navigation keys. They all run one handler, which forwards
// the original key press into the focused component.
func navHandlers() map[Action]handler {
	nav := map[Action]handler{}

	for _, a := range []Action{
		actionNavUp, actionNavDown, actionNavTop, actionNavBottom,
		actionNavPageUp, actionNavPageDown, actionNavHalfPageUp, actionNavHalfPageDown,
	} {
		nav[a] = forwardToFocusedPane
	}

	nav[actionFocusNext] = plain(func(m Model) Model { return m.cycleFocus(1) })
	nav[actionFocusPrev] = plain(func(m Model) Model { return m.cycleFocus(-1) })
	nav[actionFocusChord] = plain(func(m Model) Model { m.chord = chordWindow; return m })
	nav[actionToggleDetail] = plain(Model.toggleDetailPane)

	return nav
}

// torrentsHandlers are the torrents window's implemented actions.
func torrentsHandlers() map[Action]handler {
	return map[Action]handler{
		actionToggleFilter: plain(Model.toggleFilterPane),
		actionToggleMark:   plain(Model.toggleMark),
		actionDetailOpen:   plain(Model.openDetail),
		actionFilterApply:  plain(func(m Model) Model { return m.applyFilterItem(m.torrents.filter.current()) }),
		actionYankChord:    plain(func(m Model) Model { m.chord = chordYank; return m }),
	}
}

// detailHandlers are the detail pane's tab keys, including the 1–5 shortcuts
// that open the pane straight onto a tab.
func detailHandlers() map[Action]handler {
	tabs := map[Action]int{
		actionTabOverview: tabOverview,
		actionTabFiles:    tabFiles,
		actionTabTrackers: tabTrackers,
		actionTabPeers:    tabPeers,
		actionTabPieces:   tabPieces,
	}

	out := map[Action]handler{
		actionDetailPrevTab: plain(func(m Model) Model { return m.detailTab(m.torrents.detail.prevTab()) }),
		actionDetailNextTab: plain(func(m Model) Model { return m.detailTab(m.torrents.detail.nextTab()) }),
	}

	for action, tab := range tabs {
		out[action] = plain(func(m Model) Model { return m.openDetailOnTab(tab) })
	}

	return out
}

// settingsHandlers are the settings window's keys and the text input's two
// escapes. An open dropdown is closed by any key that moves away from its row.
func settingsHandlers() map[Action]handler {
	return map[Action]handler{
		actionSettingsPrevTab: windowStep(func(w settingsWindow) settingsWindow { return w.closeDropdown().switchTab(-1) }),
		actionSettingsNextTab: windowStep(func(w settingsWindow) settingsWindow { return w.closeDropdown().switchTab(1) }),
		actionSettingsNextField: windowStep(func(w settingsWindow) settingsWindow {
			return w.closeDropdown().moveField(1)
		}),
		actionSettingsPrevField: windowStep(func(w settingsWindow) settingsWindow {
			return w.closeDropdown().moveField(-1)
		}),
		actionSettingsFieldUp:   windowStep(func(w settingsWindow) settingsWindow { return w.fieldStep(-1) }),
		actionSettingsFieldDown: windowStep(func(w settingsWindow) settingsWindow { return w.fieldStep(1) }),
		actionSettingsEdit:      settingsHandler(Model.settingsEnter),

		actionInputBlur:   plain(Model.blurInput),
		actionInputSubmit: func(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) { return m.submitFocusedInput(msg) },
	}
}

// submitFocusedInput is enter in whichever input owns the keyboard. A prompt's
// submit acts, so it may return a command; the other inputs' submits are plain
// transitions.
func (m Model) submitFocusedInput(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch m.owner {
	case ownerPrompt:
		return m.promptSubmit()
	case ownerForm:
		return m.formSubmitInput(), nil
	case ownerPalette:
		return m.paletteSubmit(msg)
	case ownerNone, ownerSettings, ownerSearch:
	}

	return m.submitInput().syncSettings(), nil
}

// helpHandlers are the help overlay's scroll keys.
func helpHandlers() map[Action]handler {
	return map[Action]handler{
		actionHelpScrollUp:   scrollHelp,
		actionHelpScrollDown: scrollHelp,
		actionHelpPageUp:     scrollHelp,
		actionHelpPageDown:   scrollHelp,
	}
}

// chordHandlers are the second keys of the two chords.
func chordHandlers() map[Action]handler {
	yanks := map[Action]struct {
		what  string
		value func(Model) (string, bool)
	}{
		actionYankHash: {"info hash", func(m Model) (string, bool) { return m.cursorField(fieldHash) }},
		actionYankName: {"name", func(m Model) (string, bool) { return m.cursorField(fieldName) }},
		actionYankPath: {"download path", func(m Model) (string, bool) { return m.cursorField(fieldPath) }},
	}

	out := map[Action]handler{
		actionYankMagnet: func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.yankMagnet() },
		actionFocusLeft:  plain(func(m Model) Model { return m.focusDirection(actionFocusLeft) }),
		actionFocusRight: plain(func(m Model) Model { return m.focusDirection(actionFocusRight) }),
		actionFocusUp:    plain(func(m Model) Model { return m.focusDirection(actionFocusUp) }),
		actionFocusDown:  plain(func(m Model) Model { return m.focusDirection(actionFocusDown) }),
	}

	for action, yank := range yanks {
		out[action] = yankHandler(yank.what, yank.value)
	}

	return out
}

// spineHandlers are the actions that ride the refresh spine: the server picker
// and the alt-speed switch.
func spineHandlers() map[Action]handler {
	out := pickerHandlers()
	out[actionServerAltSpeed] = func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.toggleAltSpeed() }

	return out
}

// notYetHandlers covers every registered action no milestone has implemented.
// It is derived from the registry rather than listed, so a binding added without
// a handler is a missing handler at run time in exactly one place, and the
// handler-coverage test in actions_test.go stays honest.
func notYetHandlers() map[Action]handler {
	out := map[Action]handler{}

	for _, b := range registry {
		if _, done := handlers[b.Action]; done {
			continue
		}

		if _, dup := out[b.Action]; dup {
			continue
		}

		out[b.Action] = notYet(b.Action)
	}

	return out
}

// torrentField names the yankable fields of the torrent under the cursor.
type torrentField int

const (
	fieldHash torrentField = iota
	fieldName
	fieldPath
)

// cursorField reads one field of the torrent under the cursor.
//
// The value is the raw server string, not the sanitized one: this is going to
// the clipboard, where the caller wants the actual name, and sanitization is a
// property of what the terminal draws, not of what the data is. Only the toast
// announcing the yank is sanitized.
func (m Model) cursorField(f torrentField) (string, bool) {
	row, ok := m.torrents.sel.row(m.torrents.rows)
	if !ok {
		return "", false
	}

	switch f {
	case fieldHash:
		return row.Hash, true
	case fieldName:
		return row.Name, true
	case fieldPath:
		return row.DownloadDir, true
	}

	return "", false
}

// yankHandler copies a field of the cursor torrent to the clipboard.
//
// The clipboard is write-only over OSC 52 (vision.md, "Terminal constraints"),
// and not every terminal implements even that, so the toast reports what was
// sent rather than claiming it arrived.
func yankHandler(what string, value func(Model) (string, bool)) handler {
	return func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) {
		v, ok := value(m)
		if !ok {
			m.toast = "nothing selected"

			return m, nil
		}

		m.toast = "yanked " + what

		return m, tea.SetClipboard(output.SanitizeTerminal(v))
	}
}

// forwardToFocusedPane hands the original key press to the focused component,
// then pulls the component's resulting cursor back into the selection.
//
// Forwarding the message rather than calling a movement method is what ties the
// registry to the components: the component moves because its own KeyMap — built
// from the same registry rows — matched the same key string.
func forwardToFocusedPane(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch m.focusedPane() {
	case paneList:
		table, cmd := m.torrents.list.table.Update(msg)
		m.torrents.list.table = table
		m.torrents.sel = m.torrents.sel.setCursor(m.torrents.list.cursor())

		return m.syncTorrents(), cmd
	case paneDetail:
		var cmd tea.Cmd

		m.torrents.detail, cmd = m.torrents.detail.forward(msg)

		return m, cmd
	case paneFilter:
		return m.moveFilterCursor(msg), nil
	case paneSettings:
	}

	return m, nil
}

// moveFilterCursor applies a navigation key to the hand-rolled sidebar. It
// resolves the key back to its action rather than comparing strings, so the
// sidebar moves on exactly the keys the registry binds.
func (m Model) moveFilterCursor(msg tea.KeyPressMsg) Model {
	action, ok := lookup([]Context{ctxNav}, msg)
	if !ok {
		return m
	}

	switch action {
	case actionNavUp:
		m.torrents.filter = m.torrents.filter.move(-1)
	case actionNavDown:
		m.torrents.filter = m.torrents.filter.move(1)
	case actionNavTop:
		m.torrents.filter = m.torrents.filter.top()
	case actionNavBottom:
		m.torrents.filter = m.torrents.filter.bottom()
	case actionNavPageUp, actionNavHalfPageUp:
		m.torrents.filter = m.torrents.filter.move(-filterPageSize)
	case actionNavPageDown, actionNavHalfPageDown:
		m.torrents.filter = m.torrents.filter.move(filterPageSize)
	}

	return m
}

// filterPageSize is how far a page key moves the sidebar cursor. The sidebar is
// short and never scrolls, so a page is a fixed jump rather than a function of
// the pane's height.
const filterPageSize = 5

// toggleHelp opens or closes the help overlay.
func (m Model) toggleHelp() Model {
	if m.hasOverlay(helpOverlayKind) {
		return m.pop()
	}

	return m.push(newHelpOverlay())
}

// toggleFilterPane opens or closes the filter sidebar, returning focus to the
// main pane when the pane being closed is the focused one.
func (m Model) toggleFilterPane() Model {
	m.torrents.filterOpen = !m.torrents.filterOpen

	return m.ensureFocusIsOpen().resize(m.width, m.height)
}

// toggleDetailPane opens or closes the detail pane, with the same focus rule.
func (m Model) toggleDetailPane() Model {
	m.torrents.detailOpen = !m.torrents.detailOpen

	return m.ensureFocusIsOpen().resize(m.width, m.height)
}

// openDetail opens the detail pane and focuses it, which is what enter does on a
// list row.
func (m Model) openDetail() Model {
	m.torrents.detailOpen = true

	return m.resize(m.width, m.height).setFocus(paneDetail)
}

// openDetailOnTab opens the detail pane straight onto a tab. It does not move
// focus: 1–5 are bound in the torrents window, so they work from the list pane
// without stealing the keyboard from it.
func (m Model) openDetailOnTab(tab int) Model {
	m.torrents.detailOpen = true
	m.torrents.detail = m.torrents.detail.gotoTab(tab)

	return m.resize(m.width, m.height)
}

// detailTab installs a detail pane whose tab has already been changed.
func (m Model) detailTab(p detailPane) Model {
	m.torrents.detail = p

	return m
}

// toggleMark toggles the mark on the cursor row.
func (m Model) toggleMark() Model {
	m.torrents.sel = m.torrents.sel.toggleMark(m.torrents.rows)

	return m.syncTorrents()
}

// applyFilterItem applies a sidebar row as the list's filter: the sidebar's
// enter and the f modal's enter both land here.
//
// It clears marks, per vision.md's selection rule: a mark hidden by a filter
// would be acted on invisibly, and with exactly one hidden mark an action would
// skip the cursor row with nothing to say so. It resizes because the filter
// decides whether the list pane has a footer.
func (m Model) applyFilterItem(item filterItem) Model {
	if !item.selectable() {
		return m
	}

	v := m.torrents.listView
	v.filter = item.view()

	m.torrents.sel = m.torrents.sel.clearMarks()
	m.torrents = m.torrents.setView(v, m.icons)
	m.torrents.filter = m.torrents.filter.applyItem(item)

	m.toast = "filter cleared"
	if v.filter.set() {
		m.toast = "filter: " + v.filter.text()
	}

	return m.resize(m.width, m.height)
}
