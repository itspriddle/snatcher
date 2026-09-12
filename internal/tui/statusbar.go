package tui

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"

	"github.com/itspriddle/snatcher/internal/output"
)

// mockHost is what the status bar shows when the TUI was constructed without
// dialing. T4 replaces the whole segment set with live server data.
const mockHost = "mock"

// statusBarView renders the bottom line: what snatcher this is, what it is
// pointed at, and the two transient facts a user needs to understand what the
// next key will do.
//
// The mark count appears only when marks exist, and the chord prefix only while
// one is pending. Both are answers to "why did that key do something other than
// what I expected", and a status bar that always showed "marks: 0" would bury
// the one case that matters.
func (m Model) statusBarView(width int) string {
	segments := []string{"snatcher " + m.deps.Version, m.hostLabel()}

	if n := m.torrents.sel.markCount(); n > 0 && m.window == windowTorrents {
		segments = append(segments, fmt.Sprintf("marks: %d", n))
	}

	// Unsaved settings are shown from every window: leaving the settings window
	// does not save them, and the count is the reminder that they exist.
	if n := m.settings.dirtyCount(); n > 0 {
		segments = append(segments, fmt.Sprintf("unsaved: %d", n))
	}

	if m.chord.pending() {
		segments = append(segments, "pending: "+m.chord.label())
	}

	left := strings.Join(segments, " │ ")

	if m.toast != "" {
		return m.styles.StatusBar.Render(pad(left, m.styles.Toast.Render(output.SanitizeTerminal(m.toast)), width))
	}

	return m.styles.StatusBar.Render(padLine(left, width))
}

// hostLabel is the server the TUI is pointed at, as alias@host rather than a
// full RPC URL: the path is the same on every Transmission server and spends
// status bar width saying nothing, and the alias is what the picker lists.
func (m Model) hostLabel() string {
	if m.serverURL == "" {
		return mockHost
	}

	host := m.serverURL
	if u, err := url.Parse(m.serverURL); err == nil && u.Host != "" {
		host = u.Host
	}

	if m.serverName != "" {
		host = m.serverName + "@" + host
	}

	return output.SanitizeTerminal(host)
}

// helpLineView renders the one-line short help above the status bar, listing the
// keys the current context actually responds to.
func (m Model) helpLineView(width int) string {
	return padLine(m.help.ShortHelpView(m.shortBindings()), width)
}

// shortBindings are the handful of keys the short-help line advertises: the most
// specific context's bindings, then the always-true ones.
//
// It reads the same registry the router does, so the line cannot advertise a key
// that does not work — which is the reason the keymap is data in the first
// place.
func (m Model) shortBindings() []key.Binding {
	stack := m.contextStack()

	var out []key.Binding

	for _, b := range registry {
		if len(stack) > 0 && b.Context == stack[0] {
			out = append(out, b.Key)
		}
	}

	// The global pair is only advertised where it resolves. A modal stack — a
	// focused input, a confirm prompt, a pending chord — swallows ? and q, and a
	// help line offering them there would be advertising dead keys.
	if slices.Contains(stack, ctxGlobal) {
		for _, action := range []Action{actionHelp, actionQuit} {
			out = append(out, bindingFor(ctxGlobal, action))
		}
	}

	return out
}
