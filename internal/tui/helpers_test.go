package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// testDeps is the dependency set every test builds a model from: no dialer, the
// mock torrents as static rows (which also keeps the refresh spine quiet), and a
// fixed version string so a golden does not change with the build.
func testDeps() Deps {
	return Deps{Theme: "default", NerdFont: false, Version: "v1.2.3", Torrents: mockTorrents()}
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
		"ctrl+p":    'p',
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
		"ctrl+p":    tea.ModCtrl,
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
