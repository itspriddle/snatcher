package tui

import (
	tea "charm.land/bubbletea/v2"
)

// menuOverlayKind names the action menu, at every level.
const menuOverlayKind = "menu"

// menuMaxWidth caps the action menu, as confirmMaxWidth caps the confirm.
const menuMaxWidth = 60

// menuItem is one row of the action menu: a leaf that runs an action, a submenu,
// or a row drawn greyed because the milestone that implements it has not landed.
type menuItem struct {
	label string

	// action is what a leaf runs: the same handler its key runs, so a verb
	// behaves identically — confirms and all — from the menu and from the list.
	action Action

	// sub is a submenu's rows.
	sub []menuItem

	// disabled rows are drawn muted and never hold the cursor. note follows the
	// label: why a disabled row is disabled, or a caveat on an enabled one.
	disabled bool
	note     string
}

// menuOverlay is m: every torrent action vision.md lists, including the ones
// with no key. It acts on the same selection the keys do.
//
// It is modal. Its stack is ctxMenu alone, so q and the torrent verbs do nothing
// while it is open, and ctrl+c still quits.
type menuOverlay struct {
	title  string
	items  []menuItem
	cursor int
}

// torrentMenu is the action menu over n torrents, in vision.md's order.
func torrentMenu(n int) menuOverlay {
	return menuOverlay{
		title: "Actions (" + pluralTorrents(n) + ")",
		items: []menuItem{
			{label: "force start", action: actionTorrentForceStart},
			{label: "start", action: actionTorrentStart},
			{label: "pause", action: actionTorrentStop},
			{label: "verify", action: actionTorrentVerify},
			{label: "reannounce", action: actionTorrentReann},
			{label: "rename torrent", action: actionTorrentRename},
			{label: "queue", sub: []menuItem{
				{label: "up", action: actionQueueUp},
				{label: "down", action: actionQueueDown},
				{label: "top", action: actionQueueTop},
				{label: "bottom", action: actionQueueBottom},
			}},
			{label: "move / set location", action: actionTorrentMove},
			{label: "set labels", action: actionTorrentLabels},
			{label: "yank", sub: []menuItem{
				{label: "magnet link", action: actionYankMagnet},
				{label: "info hash", action: actionYankHash},
				{label: "name", action: actionYankName},
				{label: "download path", action: actionYankPath},
			}},
			{label: "delete", action: actionTorrentRemove},
			{label: "trackers", sub: []menuItem{
				{label: "add", action: actionTrackerAdd},
				{label: "replace", action: actionTrackerReplace},
				{label: "clear", action: actionTrackerClear},
			}},
			{label: "other", sub: otherMenu()},
		},
	}
}

// kind implements overlay.
func (menuOverlay) kind() string { return menuOverlayKind }

// contexts implements overlay.
func (menuOverlay) contexts() []Context { return []Context{ctxMenu} }

// move steps the cursor over the enabled rows, stopping at the ends.
func (o menuOverlay) move(delta int) menuOverlay {
	for next := o.cursor + delta; next >= 0 && next < len(o.items); next += delta {
		if !o.items[next].disabled {
			o.cursor = next

			return o
		}
	}

	return o
}

// view implements overlay. A submenu row ends in "▸"; a disabled row is muted
// and names the milestone it waits on, so it reads without color too.
func (o menuOverlay) view(m Model, w, h int) string {
	width := min(overlayContentWidth(w), menuMaxWidth)

	rows := make([]string, 0, len(o.items))

	for i, it := range o.items {
		label := it.label
		if len(it.sub) > 0 {
			label += " ▸"
		}

		if it.note != "" {
			label += "  " + it.note
		}

		if it.disabled {
			rows = append(rows, m.styles.Muted.Render(padLine("  "+label, width)))

			continue
		}

		rows = append(rows, cursorRow(m.styles, i == o.cursor, label, width))
	}

	return m.modalBox(modal{title: o.title, rows: rows, focus: o.cursor, ctx: ctxMenu, width: width}, h)
}

// openMenu is m: open the menu over the selection, or close every menu level
// when one is on top. It refuses for the same reasons a verb would, so a menu
// never opens over nothing.
func (m Model) openMenu() Model {
	if top, ok := m.topOverlay(); ok && top.kind() == menuOverlayKind {
		return m.popMenus()
	}

	sel := m.listSelector()
	if why, refused := m.refuseMutation(sel); refused {
		m.toast = why

		return m
	}

	return m.push(torrentMenu(len(sel.IDs)))
}

// popMenus closes every menu level on top of the stack.
func (m Model) popMenus() Model {
	for {
		top, ok := m.topOverlay()
		if !ok || top.kind() != menuOverlayKind {
			return m
		}

		m = m.pop()
	}
}

// menuSelect is enter in the menu: open a submenu, or close every menu level
// and run a leaf. Closing first is what puts a confirm the leaf raises over the
// list rather than over the menu, so answering it returns to the list.
func (m Model) menuSelect(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	o, ok := topAs[menuOverlay](m)
	if !ok || o.cursor >= len(o.items) {
		return m, nil
	}

	item := o.items[o.cursor]

	switch {
	case item.disabled:
		return m, nil
	case len(item.sub) > 0:
		return m.push(menuOverlay{title: o.title + " / " + item.label, items: item.sub}), nil
	}

	return m.popMenus().dispatch(item.action, msg)
}

// menuHandlers are m and the menu's own keys.
func menuHandlers() map[Action]handler {
	return map[Action]handler{
		actionMenuOpen:   plain(Model.openMenu),
		actionMenuUp:     stepTop(func(o menuOverlay) menuOverlay { return o.move(-1) }),
		actionMenuDown:   stepTop(func(o menuOverlay) menuOverlay { return o.move(1) }),
		actionMenuSelect: func(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) { return m.menuSelect(msg) },
	}
}
