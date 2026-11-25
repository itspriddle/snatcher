package tui

// chord is a pending multi-key sequence: the first key has been pressed and the
// TUI is waiting for the second.
//
// A live chord replaces the context stack entirely — only its own context is
// consulted — and the status bar shows its prefix, so a user who has pressed y
// by accident can see why the next key did nothing and press esc.
type chord int

const (
	// chordNone means no chord is pending.
	chordNone chord = iota

	// chordYank is the y prefix: yank something to the clipboard.
	chordYank

	// chordWindow is the ctrl+w prefix: focus a pane directly, vim-style.
	chordWindow
)

// context returns the chord's context, which is the whole stack while it is
// pending. A key the chord does not bind cancels it rather than falling through
// to a global action: half of a chord followed by an unrelated q must not quit.
func (c chord) context() Context {
	switch c {
	case chordYank:
		return ctxChordYank
	case chordWindow:
		return ctxChordWindow
	case chordNone:
	}

	return ctxGlobal
}

// label is the prefix shown in the status bar while the chord is pending.
func (c chord) label() string {
	switch c {
	case chordYank:
		return "y"
	case chordWindow:
		return "ctrl+w"
	case chordNone:
	}

	return ""
}

// pending reports whether a chord is waiting for its second key.
func (c chord) pending() bool {
	return c != chordNone
}
