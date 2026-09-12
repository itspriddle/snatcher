package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// The settings form is hand-rolled over bubbles/v2/textinput rather than built
// on huh/v2. huh.Input cannot strip the banned ctrl+h from its embedded
// textinput, its field accessors write on every keystroke, NewForm reads TERM,
// and a submitted form's View goes blank — and the window needs only three
// widget kinds. See docs/tui-todo.md, T3 decisions.

// fieldKind is the widget a settings row is drawn and edited as.
type fieldKind int

const (
	// fieldToggle is a bool, flipped in place by enter, up, or down.
	fieldToggle fieldKind = iota

	// fieldText is a free-form value edited in the text input and parsed on
	// submit.
	fieldText

	// fieldSelect is one of a fixed set of options, chosen from an inline
	// dropdown or cycled in place with up and down.
	fieldSelect

	// fieldButton is one of the Save / Revert row at the bottom of every tab.
	fieldButton
)

// fieldSource is where a settings row reads from and writes to.
type fieldSource int

const (
	// sourceSession is a Transmission session setting, written with SetSession.
	sourceSession fieldSource = iota

	// sourceClient is one of snatcher's own client settings, written through
	// Deps.Config.
	sourceClient
)

// The two button keys. They are not setting keys and never reach a backend.
const (
	buttonSave   = "save"
	buttonRevert = "revert"
)

// settingsField is one row of the settings form.
type settingsField struct {
	// key is the setting key the row writes: "peer-port", "client.theme", or a
	// button key.
	key string

	// label is what the row is called on screen: the key as the CLI spells it,
	// so the window, "server show", and "config list" all name a setting the
	// same way.
	label string

	// desc is the one-line hint shown while the cursor is on the row.
	desc string

	// unit is the muted hint drawn after a raw numeric value ("KB/s").
	unit string

	// kind is the widget.
	kind fieldKind

	// source is the backend the row belongs to.
	source fieldSource

	// def is the registry entry behind a session row. It is the zero value for
	// client rows and buttons.
	def transmission.SettingDef

	// options are a select row's choices.
	options []string

	// loaded is the value as of the last load or successful save, and value is
	// the value now. A row is dirty when they differ.
	loaded any
	value  any

	// readOnly rows are drawn muted, skipped by tab, and never hold the cursor.
	readOnly bool

	// note explains why a row is read-only.
	note string

	// err is the last commit or save error for this row.
	err string

	// sanitize marks a server-derived value, which must be neutralized before
	// it is drawn.
	sanitize bool
}

// dirty reports whether the row holds an unsaved change.
func (f settingsField) dirty() bool {
	return f.kind != fieldButton && !f.readOnly && f.value != f.loaded
}

// text is the row's value as plain text, sanitized when it came from the
// server. An unloaded value is empty.
func (f settingsField) text() string {
	if f.value == nil {
		return ""
	}

	s := transmission.FormatSettingValue(f.value)
	if f.sanitize {
		s = output.SanitizeTerminal(s)
	}

	return s
}

// display is the row's value as drawn in navigation: a checkbox, an option
// between arrows, or the plain text.
func (f settingsField) display() string {
	switch f.kind {
	case fieldToggle:
		if on, _ := f.value.(bool); on {
			return "[x]"
		}

		return "[ ]"
	case fieldSelect:
		return "< " + f.text() + " >"
	case fieldText, fieldButton:
	}

	return f.text()
}

// optionIndex is the index of the row's current value among its options, or 0
// when the value is not one of them — a server can report an encryption mode
// the registry does not list.
func (f settingsField) optionIndex() int {
	current, _ := f.value.(string)

	if i := slices.Index(f.options, current); i >= 0 {
		return i
	}

	return 0
}

// sessionField builds the row for one registry entry.
func sessionField(def transmission.SettingDef) settingsField {
	f := settingsField{
		key:      def.Key,
		label:    def.Key,
		desc:     def.Desc,
		unit:     def.Unit,
		kind:     fieldText,
		source:   sourceSession,
		def:      def,
		sanitize: def.ServerDerived,
	}

	switch def.Type {
	case transmission.SettingBool:
		f.kind = fieldToggle
	case transmission.SettingEnum:
		f.kind = fieldSelect
		f.options = def.Allowed
	case transmission.SettingInt, transmission.SettingInt64, transmission.SettingFloat, transmission.SettingString:
	}

	if def.ReadOnly {
		f.readOnly = true
		f.note = "(read-only)"
		f.desc = "Reported by the server; it cannot be changed here"
	}

	return f
}

// buttonFields are the Save and Revert rows every tab ends with.
func buttonFields() []settingsField {
	return []settingsField{
		{key: buttonSave, label: "Save", kind: fieldButton, desc: "Write every unsaved change, on every tab"},
		{key: buttonRevert, label: "Revert", kind: fieldButton, desc: "Discard every unsaved change, on every tab"},
	}
}

// parseFieldInput turns the text input's contents into the value a row
// commits. A session row parses through the registry, exactly as a "server
// update" flag would; a client row is validated by the same store "config set"
// uses, so the window cannot accept a value the CLI rejects.
func parseFieldInput(f settingsField, raw string, cfg ClientConfig) (any, error) {
	if f.source == sourceSession {
		return transmission.ParseSettingValue(f.def, raw)
	}

	value := strings.TrimSpace(raw)

	if cfg != nil {
		if err := cfg.Validate(f.key, value); err != nil {
			return nil, err
		}
	}

	return value, nil
}

// textInputKeyMap is the settings input's keymap, built explicitly.
//
// It is not "the defaults minus ctrl+h": textinput.DefaultKeyMap also binds
// seven alt chords, and the registry bans the alt prefix outright. What is left
// is readline — the chords vision.md says a focused input keeps — with
// backspace alone deleting backward.
//
// Paste stays unbound. textinput's paste command reads the system clipboard,
// which vision.md rules out (the clipboard is write-only); the terminal's own
// bracketed paste still arrives, as a tea.PasteMsg the model forwards. The
// suggestion bindings stay unbound because the input offers no suggestions, and
// their defaults (tab, up, down, ctrl+n, ctrl+p) would swallow keys for nothing.
func textInputKeyMap() textinput.KeyMap {
	keys := func(k ...string) key.Binding { return key.NewBinding(key.WithKeys(k...)) }

	return textinput.KeyMap{
		CharacterForward:        keys("right", "ctrl+f"),
		CharacterBackward:       keys("left", "ctrl+b"),
		WordForward:             keys("ctrl+right"),
		WordBackward:            keys("ctrl+left"),
		DeleteWordBackward:      keys("ctrl+w", "ctrl+backspace"),
		DeleteWordForward:       keys("ctrl+delete"),
		DeleteAfterCursor:       keys("ctrl+k"),
		DeleteBeforeCursor:      keys("ctrl+u"),
		DeleteCharacterBackward: keys("backspace"),
		DeleteCharacterForward:  keys("delete", "ctrl+d"),
		LineStart:               keys("home", "ctrl+a"),
		LineEnd:                 keys("end", "ctrl+e"),
	}
}
