package tui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestYankChordShowsItsPrefix checks the status bar tells a user a chord is
// live. Without it, a mistyped y turns the next key into a silent no-op with
// nothing on screen to explain why.
func TestYankChordShowsItsPrefix(t *testing.T) {
	m := press(newTestModel(120, 40), "y")

	require.Equal(t, chordYank, m.chord)
	assert.Contains(t, m.statusBarView(120), "pending: y")
}

// TestYankChordCopiesToTheClipboard checks the three implemented yanks: each
// produces a clipboard command and says what it sent.
func TestYankChordCopiesToTheClipboard(t *testing.T) {
	tests := []struct {
		key  string
		what string
	}{
		{"h", "info hash"},
		{"n", "name"},
		{"p", "download path"},
	}

	for _, tc := range tests {
		t.Run(tc.what, func(t *testing.T) {
			m, cmd := pressCmd(press(newTestModel(120, 40), "y"), tc.key)

			assert.Equal(t, chordNone, m.chord)
			assert.Contains(t, m.toast, tc.what)
			require.NotNil(t, cmd, "a yank must produce a clipboard command")
			assert.NotEmpty(t, clipboardContent(t, cmd))
		})
	}
}

// TestYankSanitizesWhatItSends checks a hostile torrent name cannot ride the
// clipboard into the terminal that pastes it.
func TestYankSanitizesWhatItSends(t *testing.T) {
	m := newTestModel(120, 40)
	m.torrents.sel = m.torrents.sel.setCursor(len(m.torrents.rows) - 1)

	_, cmd := pressCmd(press(m, "y"), "n")
	require.NotNil(t, cmd)

	content := clipboardContent(t, cmd)

	assert.Contains(t, content, "hostile")
	assert.NotContains(t, content, "\x1b")
}

// clipboardContent runs a clipboard command and returns what it would send.
//
// bubbletea's clipboard message type is unexported, so the payload is read back
// through its string form rather than by a type assertion. That is enough for
// what these tests assert — that something was sent, and that it was sanitized.
func clipboardContent(t *testing.T, cmd tea.Cmd) string {
	t.Helper()

	return fmt.Sprint(cmd())
}

// TestUnboundChordKeyCancelsSilently checks a key the chord does not bind
// cancels it rather than falling through — so a mistyped y followed by q does
// not quit.
func TestUnboundChordKeyCancelsSilently(t *testing.T) {
	m, cmd := pressCmd(press(newTestModel(120, 40), "y"), "q")

	assert.Equal(t, chordNone, m.chord)
	assert.Nil(t, cmd, "the chord must swallow the key, not quit")
	assert.Empty(t, m.toast)
}

// TestEscDuringAChordRunsOnlyTheFirstRung checks rung 0 of the esc ladder: esc
// cancels the chord and does nothing else, leaving marks and focus alone.
func TestEscDuringAChordRunsOnlyTheFirstRung(t *testing.T) {
	m := press(newTestModel(120, 40), "space", "y")
	require.Equal(t, chordYank, m.chord)

	m = press(m, "esc")

	assert.Equal(t, chordNone, m.chord)
	assert.Equal(t, 1, m.torrents.sel.markCount(), "the marks rung must not have run")
}

// TestCtrlCQuitsThroughAChordAndAnInput checks the one key that escapes every
// modal state. A user must never be trapped by a widget that has swallowed the
// keyboard.
func TestCtrlCQuitsThroughAChordAndAnInput(t *testing.T) {
	m := press(newTestModel(120, 40), "y")
	m.inputFocused = true

	_, cmd := pressCmd(m, "ctrl+c")

	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}

// TestChordLabelsAreDistinct checks every chord announces itself, since the
// status bar prefix is the only thing that distinguishes them on screen.
func TestChordLabelsAreDistinct(t *testing.T) {
	assert.Equal(t, "y", chordYank.label())
	assert.Equal(t, "ctrl+w", chordWindow.label())
	assert.Empty(t, chordNone.label())
	assert.False(t, chordNone.pending())
	assert.True(t, chordWindow.pending())
}
