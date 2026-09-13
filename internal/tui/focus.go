package tui

// focusedPane returns the pane that owns the keyboard.
func (m Model) focusedPane() Pane {
	if m.window == windowSettings {
		return paneSettings
	}

	return m.torrents.focus
}

// openPanes lists the current window's open panes in cycle order.
func (m Model) openPanes() []Pane {
	if m.window == windowSettings {
		return []Pane{paneSettings}
	}

	return m.torrents.openPanes()
}

// mainPane is the window's primary pane: where the action verbs live, and where
// focus returns to when a focused pane closes or esc unwinds a pane focus.
func (m Model) mainPane() Pane {
	if m.window == windowSettings {
		return paneSettings
	}

	return paneList
}

// setFocus moves focus to a pane, driving the components' own focus flags.
//
// table.Update is a no-op on an unfocused table, so a nav key forwarded to a
// table nobody told about the focus change would silently do nothing. That is
// why focus is pushed into the component here rather than read at view time.
func (m Model) setFocus(p Pane) Model {
	if m.window == windowSettings {
		return m
	}

	m.torrents.focus = p
	m.torrents.list = m.torrents.list.focus(p == paneList)
	m.torrents.detail = m.torrents.detail.focus(p == paneDetail)

	return m
}

// cycleFocus moves focus by step through the window's open panes, wrapping. It
// is tab and shift+tab, which cycle panes only: tabs inside a pane are [ and ].
func (m Model) cycleFocus(step int) Model {
	panes := m.openPanes()
	if len(panes) < 2 {
		return m
	}

	current := 0

	for i, p := range panes {
		if p == m.focusedPane() {
			current = i

			break
		}
	}

	return m.setFocus(panes[(current+step+len(panes))%len(panes)])
}

// focusDirection answers the ctrl+w chord: focus the pane in a direction.
func (m Model) focusDirection(dir Action) Model {
	if m.window == windowSettings {
		return m
	}

	return m.setFocus(m.torrents.neighbor(dir))
}

// ensureFocusIsOpen returns focus to the main pane when the focused pane has
// been closed. Closing the pane you are standing in must not leave the keyboard
// pointed at something that is not drawn.
func (m Model) ensureFocusIsOpen() Model {
	for _, p := range m.openPanes() {
		if p == m.focusedPane() {
			return m
		}
	}

	return m.setFocus(m.mainPane())
}

// switchWindow brings a window to the front. It pops every overlay and cancels a
// pending chord: both belong to the window that was in front, and carrying a
// half-pressed y into another window would make the next key mean something the
// user never asked for.
func (m Model) switchWindow(w window) Model {
	m.overlays = nil
	m.chord = chordNone
	m.window = w
	m.settings = m.settings.closeDropdown()

	if w == windowTorrents {
		return m.ensureFocusIsOpen()
	}

	return m
}

// contextStack is the stack a key press is resolved against right now, most
// specific first.
//
// The three modal states each replace the stack rather than extending it. That
// is the "a focused text input owns the keyboard" rule from vision.md, and the
// same shape gives an overlay and a pending chord their swallow-everything
// behavior for free.
func (m Model) contextStack() []Context {
	if m.inputFocused {
		return []Context{ctxTextInput}
	}

	if m.chord.pending() {
		return []Context{m.chord.context()}
	}

	if o, ok := m.topOverlay(); ok {
		return o.contexts()
	}

	return paneContexts(m.focusedPane())
}
