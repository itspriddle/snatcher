package tui

// rung is one step of the esc ladder: a name for tests and help, a predicate,
// and the transition it applies.
type rung struct {
	// name identifies the rung in test failures.
	name string

	// applies reports whether this rung is the one that should run.
	applies func(Model) bool

	// apply runs the rung.
	apply func(Model) Model
}

// ladder is the esc ladder from vision.md, in order. It is one ordered stack
// evaluated top to bottom: the first applicable rung runs, and only that rung.
//
// Writing it as data rather than as a chain of ifs is the point. The order is
// the specification, the test asserts one case per rung with every rung below it
// also applicable, and "at rest, do nothing" is a rung rather than the absence
// of one — esc never quits, and that is a decision worth being able to point at.
var ladder = []rung{
	{
		name:    "cancel chord",
		applies: func(m Model) bool { return m.chord.pending() },
		apply:   func(m Model) Model { m.chord = chordNone; return m },
	},
	{
		name:    "close overlay",
		applies: func(m Model) bool { return len(m.overlays) > 0 },
		apply:   Model.pop,
	},
	{
		// A dropdown is not an overlay — it is drawn inline, under its row — but
		// it is the thing on top, so it closes before anything below it unwinds.
		// The row keeps its value.
		name:    "close dropdown",
		applies: func(m Model) bool { return m.window == windowSettings && m.settings.mode == modeSelect },
		apply: func(m Model) Model {
			m.settings = m.settings.closeDropdown()

			return m
		},
	},
	{
		// vision.md says blurring keeps the input's contents, and for a search
		// field — whose text is its state — it does. A settings row's state is its
		// last committed value, so there the contents are a draft and blurring
		// discards it; see Model.blurInput.
		name:    "blur input",
		applies: func(m Model) bool { return m.inputFocused },
		apply:   Model.blurInput,
	},
	{
		name:    "focus main pane",
		applies: func(m Model) bool { return m.focusedPane() != m.mainPane() },
		apply:   func(m Model) Model { return m.setFocus(m.mainPane()) },
	},
	{
		name:    "clear search",
		applies: func(m Model) bool { return m.window == windowTorrents && m.torrents.listView.search != "" },
		apply:   func(m Model) Model { m.torrents.listView.search = ""; return m.syncTorrents() },
	},
	{
		name:    "clear marks",
		applies: func(m Model) bool { return m.window == windowTorrents && m.torrents.sel.markCount() > 0 },
		apply: func(m Model) Model {
			m.torrents.sel = m.torrents.sel.clearMarks()

			return m.syncTorrents()
		},
	},
	{
		name:    "deselect cursor",
		applies: func(m Model) bool { return m.window == windowTorrents && m.torrents.sel.active },
		apply: func(m Model) Model {
			m.torrents.sel = m.torrents.sel.deselect()

			return m.syncTorrents()
		},
	},
	{
		// Leaving the settings window with unsaved changes asks first. The prompt
		// is an overlay, so the next esc closes it (rung "close overlay") and the
		// window stays open with its changes.
		name:    "prompt to save",
		applies: func(m Model) bool { return m.window == windowSettings && m.settings.dirtyCount() > 0 },
		apply:   Model.promptSaveBeforeLeaving,
	},
	{
		name:    "return to torrents",
		applies: func(m Model) bool { return m.window != windowTorrents },
		apply:   func(m Model) Model { return m.switchWindow(windowTorrents) },
	},
	{
		// esc never quits. At rest it does nothing, and saying so as a rung
		// rather than as a fallthrough is what stops a later milestone from
		// "helpfully" adding a quit here.
		name:    "at rest",
		applies: func(Model) bool { return true },
		apply:   func(m Model) Model { return m },
	},
}

// escape runs the ladder and reports which rung ran. The name is returned for
// the tests, which assert the rung as well as its effect: two rungs with the
// same visible effect at a given moment would otherwise be indistinguishable.
func (m Model) escape() (Model, string) {
	for _, r := range ladder {
		if r.applies(m) {
			return r.apply(m), r.name
		}
	}

	return m, ""
}

// syncTorrents rebuilds the torrents window's derived state after a change to
// its rows, marks, or cursor.
func (m Model) syncTorrents() Model {
	m.torrents = m.torrents.sync(m.icons)

	return m
}
