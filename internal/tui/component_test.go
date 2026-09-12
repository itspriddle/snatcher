package tui

import (
	"reflect"
	"testing"

	"charm.land/bubbles/v2/key"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// componentKeyMap describes one bubbles component's live keymap: the fields the
// registry fills, and the context they are filled from. Every other field of
// that component's KeyMap must be empty.
//
// This is the check docs/TUI-PLAN.md asks for and the one the registry alone
// cannot make. Every component ships a default keymap that fights vision.md —
// table binds f, b, space, u, d, g, G; viewport binds space, f, b, u, d, h, l —
// so a component constructed without an explicit keymap responds to keys the
// registry has never seen, and a collision test that only reads the registry
// would pass anyway.
type componentKeyMap struct {
	name    string
	context Context
	keyMap  any
	wired   map[string]Action
}

// componentKeyMaps is every component keymap the TUI installs.
func componentKeyMaps() []componentKeyMap {
	return []componentKeyMap{
		{
			name:    "torrent table",
			context: ctxNav,
			keyMap:  tableKeyMap(),
			wired: map[string]Action{
				"LineUp":       actionNavUp,
				"LineDown":     actionNavDown,
				"PageUp":       actionNavPageUp,
				"PageDown":     actionNavPageDown,
				"HalfPageUp":   actionNavHalfPageUp,
				"HalfPageDown": actionNavHalfPageDown,
				"GotoTop":      actionNavTop,
				"GotoBottom":   actionNavBottom,
			},
		},
		{
			name:    "detail viewport",
			context: ctxNav,
			keyMap:  viewportKeyMap(),
			wired: map[string]Action{
				"Up":           actionNavUp,
				"Down":         actionNavDown,
				"PageUp":       actionNavPageUp,
				"PageDown":     actionNavPageDown,
				"HalfPageUp":   actionNavHalfPageUp,
				"HalfPageDown": actionNavHalfPageDown,
			},
		},
		{
			name:    "help viewport",
			context: ctxHelp,
			keyMap:  helpViewportKeyMap(),
			wired: map[string]Action{
				"Up":       actionHelpScrollUp,
				"Down":     actionHelpScrollDown,
				"PageUp":   actionHelpPageUp,
				"PageDown": actionHelpPageDown,
			},
		},
	}
}

// TestComponentKeyMapsComeFromTheRegistry reflects over each component's live
// keymap: a wired field must hold exactly the registry's keys for its action, and
// every unwired field must be empty rather than carrying a component default.
func TestComponentKeyMapsComeFromTheRegistry(t *testing.T) {
	for _, c := range componentKeyMaps() {
		t.Run(c.name, func(t *testing.T) {
			v := reflect.ValueOf(c.keyMap)

			require.Equal(t, reflect.Struct, v.Kind())

			for i := range v.NumField() {
				field := v.Type().Field(i).Name

				binding, ok := v.Field(i).Interface().(key.Binding)
				require.Truef(t, ok, "%s is not a key.Binding", field)

				action, wired := c.wired[field]
				if !wired {
					assert.Emptyf(t, binding.Keys(), "%s kept its component default: %v", field, binding.Keys())

					continue
				}

				want := bindingFor(c.context, action)

				require.NotEmptyf(t, want.Keys(), "%s is registered to %s, which binds nothing", field, action)
				assert.Equalf(t, want.Keys(), binding.Keys(), "%s does not match the registry", field)
			}
		})
	}
}

// TestEveryWiredFieldNamesARealField guards the table above against a typo: a
// field name that no longer exists would otherwise silently assert nothing.
func TestEveryWiredFieldNamesARealField(t *testing.T) {
	for _, c := range componentKeyMaps() {
		v := reflect.ValueOf(c.keyMap)

		for field := range c.wired {
			assert.Truef(t, v.FieldByName(field).IsValid(), "%s has no field %s", c.name, field)
		}
	}
}

// TestForwardedNavKeysMoveTheFocusedComponent is the other half of the same
// proof: the registry's keys are forwarded as the original message, and the
// component moves because its own keymap matched the same string.
func TestForwardedNavKeysMoveTheFocusedComponent(t *testing.T) {
	m := newTestModel(120, 40)

	m = press(m, "j", "j")
	assert.Equal(t, 2, m.torrents.list.cursor())
	assert.Equal(t, 2, m.torrents.sel.cursor)

	m = press(m, "k")
	assert.Equal(t, 1, m.torrents.list.cursor())

	m = press(m, "G")
	assert.Equal(t, len(m.torrents.rows)-1, m.torrents.list.cursor())

	m = press(m, "g")
	assert.Equal(t, 0, m.torrents.list.cursor())
}

// TestTheTableIgnoresItsOwnDefaultKeys checks the defaults are genuinely gone:
// f, b, u, and d page a default table, and every one of them means something
// else in vision.md.
func TestTheTableIgnoresItsOwnDefaultKeys(t *testing.T) {
	m := press(newTestModel(120, 40), "j", "j", "j")
	require.Equal(t, 3, m.torrents.list.cursor())

	for _, k := range []string{"f", "b", "u", "d"} {
		assert.Equalf(t, 3, press(m, k).torrents.list.cursor(), "%q moved the table", k)
	}
}

// TestTheFilterPaneMovesOnTheSameKeys checks the hand-rolled sidebar is driven
// from the registry too, rather than from its own string comparisons.
func TestTheFilterPaneMovesOnTheSameKeys(t *testing.T) {
	m := newTestModel(120, 40).setFocus(paneFilter)

	before := m.torrents.filter.cursor

	assert.Greater(t, press(m, "j").torrents.filter.cursor, before)
	assert.Equal(t, before, press(m, "j", "k").torrents.filter.cursor)
	assert.Equal(t, press(m, "G").torrents.filter.cursor, press(m, "end").torrents.filter.cursor)
}
