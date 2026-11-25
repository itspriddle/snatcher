package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// testDeps is the dependency set every test builds a model from: no dialer, the
// mock torrents as static rows (which also keeps the refresh spine quiet), and a
// fixed version string so a golden does not change with the build.
func testDeps() Deps {
	return Deps{Theme: "default", NerdFont: false, Mouse: true, Torrents: mockTorrents()}
}

// newTestModel builds a model sized to a terminal, with the dark palette pinned
// rather than left to whatever a tea.BackgroundColorMsg would have said.
func newTestModel(w, h int) Model {
	return New(testDeps()).applyTheme(true).resize(w, h)
}

// keyMsg builds the KeyPressMsg a terminal sends for a key spelling, for the
// spellings the tests use.
//
// The text field is what tea's String() returns for a printable key, and the
// code field is what it falls back to for a named one; setting both the way a
// real terminal would is what keeps these tests honest about the strings the
// registry matches on.
func keyMsg(k string) tea.KeyPressMsg {
	if code, ok := namedKeys[k]; ok {
		return tea.KeyPressMsg{Code: code, Mod: namedMods[k]}
	}

	runes := []rune(k)

	return tea.KeyPressMsg{Code: runes[0], Text: k}
}

// namedKeys and namedMods spell the non-printable keys the tests press.
var (
	namedKeys = map[string]rune{
		"esc":       tea.KeyEscape,
		"enter":     tea.KeyEnter,
		"tab":       tea.KeyTab,
		"shift+tab": tea.KeyTab,
		"space":     tea.KeySpace,
		"up":        tea.KeyUp,
		"down":      tea.KeyDown,
		"left":      tea.KeyLeft,
		"right":     tea.KeyRight,
		"pgup":      tea.KeyPgUp,
		"pgdown":    tea.KeyPgDown,
		"home":      tea.KeyHome,
		"end":       tea.KeyEnd,
		"ctrl+c":    'c',
		"ctrl+d":    'd',
		"ctrl+u":    'u',
		"ctrl+w":    'w',
		"ctrl+t":    't',
		"backspace": tea.KeyBackspace,
		"delete":    tea.KeyDelete,
		"ctrl+h":    'h',
		"ctrl+a":    'a',
		"ctrl+e":    'e',
		"ctrl+k":    'k',
	}

	namedMods = map[string]tea.KeyMod{
		"shift+tab": tea.ModShift,
		"ctrl+c":    tea.ModCtrl,
		"ctrl+d":    tea.ModCtrl,
		"ctrl+u":    tea.ModCtrl,
		"ctrl+w":    tea.ModCtrl,
		"ctrl+t":    tea.ModCtrl,
		"ctrl+h":    tea.ModCtrl,
		"ctrl+a":    tea.ModCtrl,
		"ctrl+e":    tea.ModCtrl,
		"ctrl+k":    tea.ModCtrl,
	}
)

// sizeMsg is the resize message a terminal sends.
func sizeMsg(w, h int) tea.WindowSizeMsg {
	return tea.WindowSizeMsg{Width: w, Height: h}
}

// lightBackgroundMsg is the reply a light terminal gives to
// tea.RequestBackgroundColor.
func lightBackgroundMsg() tea.BackgroundColorMsg {
	return tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")}
}

// press sends one key and returns the resulting model, discarding the command.
func press(m Model, keys ...string) Model {
	for _, k := range keys {
		m, _ = m.handleKey(keyMsg(k))
	}

	return m
}

// pressCmd sends one key and returns both the model and the command, for the
// handlers whose whole effect is the command they return.
func pressCmd(m Model, k string) (Model, tea.Cmd) {
	return m.handleKey(keyMsg(k))
}

// clickMsg, rightClickMsg, and wheelMsg are the mouse messages a terminal sends
// in cell-motion mode.
func clickMsg(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func rightClickMsg(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight}
}

func wheelMsg(x, y int, up bool) tea.MouseWheelMsg {
	button := tea.MouseWheelDown
	if up {
		button = tea.MouseWheelUp
	}

	return tea.MouseWheelMsg{X: x, Y: y, Button: button}
}

// click, rightClick, and wheel send mouse messages through update, as press
// sends keys, discarding the command.
func click(m Model, x, y int) Model {
	m, _ = m.update(clickMsg(x, y))

	return m
}

func rightClick(m Model, x, y int) Model {
	m, _ = m.update(rightClickMsg(x, y))

	return m
}

func wheel(m Model, x, y, notches int) Model {
	for range abs(notches) {
		m, _ = m.update(wheelMsg(x, y, notches < 0))
	}

	return m
}

// locate finds text in the rendered frame and returns the cell it starts at.
// Tests click what they can see, so a hit-test that drifts from the drawing
// fails here rather than in a terminal.
func locate(t *testing.T, m Model, text string) (x, y int) {
	t.Helper()

	return locateIn(t, m, rect{w: m.width, h: m.height}, text)
}

// locateIn is locate restricted to a region, for text drawn in more than one
// pane.
func locateIn(t *testing.T, m Model, r rect, text string) (x, y int) {
	t.Helper()

	lines := strings.Split(ansi.Strip(m.render()), "\n")

	for y := r.y; y < r.y+r.h && y < len(lines); y++ {
		line := ansi.Cut(lines[y], r.x, r.x+r.w)
		if i := strings.Index(line, text); i >= 0 {
			return r.x + ansi.StringWidth(line[:i]), y
		}
	}

	t.Fatalf("%q is not drawn in %+v:\n%s", text, r, strings.Join(lines, "\n"))

	return 0, 0
}
