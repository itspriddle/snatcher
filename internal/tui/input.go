package tui

import (
	tea "charm.land/bubbletea/v2"
)

// handleKey is the router. Each step returns early, so the order below is the
// whole precedence story:
//
//  1. clear any toast, because the next key press is the acknowledgement
//  2. ctrl+c quits — the one key that escapes even a focused text input, so a
//     user is never trapped by a widget that has swallowed the keyboard
//  3. esc runs the ladder, which must precede the text-input check because two
//     of its rungs (cancel a chord, close an overlay) sit above "blur the input"
//  4. a focused text input owns everything else
//  5. a pending chord resolves against its own context only; hit or miss, the
//     chord is cleared
//  6. an open overlay resolves against its own contexts only and swallows a miss
//  7. otherwise the focused pane's stack decides
//
// Below the size floor only quit is honored: there is nothing drawn to act on.
func (m Model) handleKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	m.toast = ""

	if msg.String() == quitKey {
		return m, tea.Quit
	}

	if m.layout().tooSmall {
		return m, nil
	}

	if msg.String() == escapeKey {
		m, _ = m.escape()

		return m, nil
	}

	if m.inputFocused {
		return m.handleInputKey(msg)
	}

	if m.chord.pending() {
		return m.handleChordKey(msg)
	}

	action, ok := lookup(m.contextStack(), msg)
	if !ok {
		return m, nil
	}

	return m.dispatch(action, msg)
}

// quitKey and escapeKey are the two keys the router intercepts before the
// registry is consulted. They are still registered — help must list them, and
// the collision test must see them — but their dispatch cannot be
// context-dependent, which is exactly what makes them the two exceptions.
const (
	quitKey   = "ctrl+c"
	escapeKey = "esc"
)

// handleInputKey resolves a key while a text input owns the keyboard. Only the
// keys the input's stack binds resolve — ctxTextInput's two escapes, and
// whatever an overlay holding the input binds above them; everything else is
// forwarded to the input itself, never swallowed, which is the "a focused text
// input owns the keyboard" rule.
func (m Model) handleInputKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	action, ok := lookup(m.contextStack(), msg)
	if !ok {
		return m.forwardToInput(msg)
	}

	return m.dispatch(action, msg)
}

// handleChordKey resolves the second key of a pending chord. The chord is
// cleared either way: a key it does not bind cancels it silently rather than
// falling through, so a mistyped y followed by q does not quit.
func (m Model) handleChordKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	pending := m.chord
	m.chord = chordNone

	action, ok := lookup([]Context{pending.context()}, msg)
	if !ok {
		return m, nil
	}

	return m.dispatch(action, msg)
}

// dispatch runs an action's handler. An action with no handler is a programming
// error the handler-coverage test catches; at run time it is reported in the
// status bar rather than panicking in a user's terminal.
func (m Model) dispatch(action Action, msg tea.KeyPressMsg) (Model, tea.Cmd) {
	h, ok := handlers[action]
	if !ok {
		m.toast = string(action) + ": no handler"

		return m, nil
	}

	return h(m, msg)
}
