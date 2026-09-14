package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEscLadderRungs is the ladder's specification as a test: one case per rung,
// each constructed so that rung and *every rung below it* also applies.
//
// That construction is the point. Any rung will pass a test where it is the only
// applicable one; what the ladder promises is an order, so each case starts from
// the state the rung above it left behind and asserts both that its own rung ran
// and that the rung below it did not.
func TestEscLadderRungs(t *testing.T) {
	tests := []struct {
		// name is the subtest's name and the rung the case expects to run.
		name string

		// from is the rung to unwind the loaded model through before the case
		// starts, or the empty string to start from the loaded model itself.
		from string

		// chord and window are the two pieces of state the ladder cares about
		// that unwinding cannot produce.
		chord  chord
		window window

		// setup applies any remaining state the case needs, after the window
		// switch, or is nil.
		setup func(Model) Model

		// check asserts the rung's effect, and that the next rung down has not
		// also run.
		check func(*testing.T, Model)
	}{
		{
			name:  "cancel chord",
			chord: chordYank,
			check: func(t *testing.T, m Model) {
				t.Helper()

				assert.Equal(t, chordNone, m.chord)
				assert.Len(t, m.overlays, 1, "the overlay below must survive")
			},
		},
		{
			name: "close overlay",
			check: func(t *testing.T, m Model) {
				t.Helper()

				assert.Empty(t, m.overlays)
				assert.True(t, m.inputFocused, "the input below must stay focused")
			},
		},
		{
			name:   "close dropdown",
			from:   "close overlay",
			window: windowSettings,
			setup: func(m Model) Model {
				m.settings.mode = modeSelect

				return m
			},
			check: func(t *testing.T, m Model) {
				t.Helper()

				assert.Equal(t, modeNav, m.settings.mode)
				assert.True(t, m.inputFocused, "the input below must stay focused")
			},
		},
		{
			name: "blur input",
			from: "close overlay",
			check: func(t *testing.T, m Model) {
				t.Helper()

				assert.False(t, m.inputFocused)
				assert.Equal(t, paneFilter, m.focusedPane(), "focus below must not move")
			},
		},
		{
			name: "clear file marks",
			from: "blur input",
			setup: func(m Model) Model {
				m = withMockDetail(press(m.setFocus(paneDetail), "2"))

				return press(m, "space")
			},
			check: func(t *testing.T, m Model) {
				t.Helper()

				assert.Zero(t, m.torrents.detail.fileSel.markCount())
				assert.Equal(t, paneDetail, m.focusedPane(), "focus below must not move")
			},
		},
		{
			name: "focus main pane",
			from: "blur input",
			check: func(t *testing.T, m Model) {
				t.Helper()

				assert.Equal(t, paneList, m.focusedPane())
				assert.Equal(t, "kernel", m.torrents.listView.search, "the search below must survive")
			},
		},
		{
			name: "clear search",
			from: "focus main pane",
			check: func(t *testing.T, m Model) {
				t.Helper()

				assert.Empty(t, m.torrents.listView.search)
				assert.Equal(t, 1, m.torrents.sel.markCount(), "the mark below must survive")
			},
		},
		{
			name: "clear marks",
			from: "clear search",
			check: func(t *testing.T, m Model) {
				t.Helper()

				assert.Zero(t, m.torrents.sel.markCount())
				assert.True(t, m.torrents.sel.active, "the cursor below must stay selected")
			},
		},
		{
			name: "deselect cursor",
			from: "clear marks",
			check: func(t *testing.T, m Model) {
				t.Helper()

				assert.False(t, m.torrents.sel.active)
				assert.Equal(t, windowTorrents, m.window)
			},
		},
		{
			name:   "prompt to save",
			from:   "deselect cursor",
			window: windowSettings,
			setup:  withDirtySetting,
			check: func(t *testing.T, m Model) {
				t.Helper()

				require.True(t, m.hasOverlay(confirmOverlayKind))
				assert.Equal(t, windowSettings, m.window, "the window below must stay open")
				assert.Equal(t, 1, m.settings.dirtyCount(), "prompting must not discard the change")
			},
		},
		{
			name:   "return to torrents",
			from:   "deselect cursor",
			window: windowSettings,
			check: func(t *testing.T, m Model) {
				t.Helper()

				assert.Equal(t, windowTorrents, m.window)
			},
		},
		{
			name: "at rest",
			from: "deselect cursor",
			check: func(t *testing.T, m Model) {
				t.Helper()

				assert.Equal(t, windowTorrents, m.window, "esc never quits")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := loadedModel(t)

			if tc.from != "" {
				m = unwound(t, m, tc.from)
			}

			m.chord = tc.chord

			if tc.window != windowTorrents {
				m = m.switchWindow(tc.window)
			}

			if tc.setup != nil {
				m = tc.setup(m)
			}

			got, rung := m.escape()

			require.Equal(t, tc.name, rung)
			tc.check(t, got)
		})
	}
}

// loadedModel is a model with every rung of the ladder below the chord
// applicable at once: an open overlay, a focused input, focus off the main pane,
// an active search, a mark, and an active cursor.
func loadedModel(t *testing.T) Model {
	t.Helper()

	m := press(newTestModel(120, 40), "space")

	require.Equal(t, 1, m.torrents.sel.markCount())

	m.torrents.listView.search = "kernel"
	m = m.setFocus(paneFilter)
	m.inputFocused = true

	return m.push(newHelpOverlay())
}

// unwound runs the ladder until the named rung has run, and returns the result.
// It is how each case is built from the one above it: the state a rung leaves
// behind is exactly the state the next rung is specified against.
func unwound(t *testing.T, m Model, through string) Model {
	t.Helper()

	for range len(ladder) {
		next, rung := m.escape()
		m = next

		if rung == through {
			return m
		}
	}

	t.Fatalf("the ladder never reached %q", through)

	return m
}

// TestEscapeAtRestChangesNothing checks the last rung is genuinely inert: a model
// at rest comes back unchanged, with no command.
func TestEscapeAtRestChangesNothing(t *testing.T) {
	m := unwound(t, loadedModel(t), "deselect cursor")

	got, cmd := m.handleKey(keyMsg("esc"))

	assert.Nil(t, cmd)
	assert.Equal(t, m.window, got.window)
	assert.Equal(t, m.focusedPane(), got.focusedPane())
	assert.Equal(t, m.torrents.sel, got.torrents.sel)
	assert.Empty(t, got.overlays)
}

// TestEscapePrecedesTheFocusedInput checks the router's one subtle ordering: esc
// is dispatched before the "a focused input owns the keyboard" check, because
// two rungs of the ladder — cancel a chord, close an overlay — sit above blurring
// the input.
func TestEscapePrecedesTheFocusedInput(t *testing.T) {
	got := press(loadedModel(t), "esc")

	assert.Empty(t, got.overlays)
	assert.True(t, got.inputFocused, "the overlay closes before the input blurs")
}

// TestLadderRungNamesAreUnique guards the test above, which identifies a rung by
// its name: two rungs sharing one would make a case assert nothing.
func TestLadderRungNamesAreUnique(t *testing.T) {
	names := map[string]bool{}

	for _, r := range ladder {
		assert.Falsef(t, names[r.name], "two rungs are both named %q", r.name)

		names[r.name] = true
	}

	assert.Len(t, ladder, 12)
}
