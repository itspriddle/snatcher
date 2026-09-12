package tui

import (
	"strconv"
)

// settingsTabs are the settings window's tabs, in display order. General and
// Polling write snatcher's own config; the rest write Transmission session
// settings, driven by the same category table that powers "server show
// --category".
var settingsTabs = []string{"General", "Polling", "Download", "Network", "Bandwidth", "Queue"}

// staticField is one read-only settings row. T3 replaces these with huh fields;
// until then they exist so the window has a real shape to lay out and focus, and
// so the tab strip is exercised by something other than the detail pane.
type staticField struct {
	// label is the setting's name.
	label string

	// value is what it currently resolves to.
	value string
}

// settingsWindow is the settings window: a tab strip over a list of fields.
//
// It is a mock in T1 by design. The window that matters is the one that
// round-trips GetSession and SetSession through huh, and huh is not a drop-in
// tea.Model; solving that is T3's whole job. What T1 needs from this window is
// that it exists, that it can be focused, and that its keymap — which owns tab
// and up/down itself, unlike every other pane — is in the collision test.
type settingsWindow struct {
	// tab is the selected tab index.
	tab int

	// fields are the rows shown per tab, keyed by tab name.
	fields map[string][]staticField
}

// newSettingsWindow builds the mock window. The General and Polling tabs show
// the settings this process actually resolved, so the window is not lying about
// the two tabs T3 wires first.
func newSettingsWindow(theme string, nerdFont bool, poll string) settingsWindow {
	return settingsWindow{
		fields: map[string][]staticField{
			"General": {
				{label: "client.theme", value: theme},
				{label: "client.nerd_font", value: strconv.FormatBool(nerdFont)},
			},
			"Polling": {
				{label: "client.poll_interval", value: poll},
			},
			"Download": {
				{label: "download-dir", value: "(not loaded)"},
				{label: "incomplete-dir-enabled", value: "(not loaded)"},
				{label: "start-added-torrents", value: "(not loaded)"},
			},
			"Network": {
				{label: "peer-port", value: "(not loaded)"},
				{label: "port-forwarding-enabled", value: "(not loaded)"},
				{label: "encryption", value: "(not loaded)"},
			},
			"Bandwidth": {
				{label: "speed-limit-down", value: "(not loaded)"},
				{label: "speed-limit-up", value: "(not loaded)"},
				{label: "alt-speed-enabled", value: "(not loaded)"},
			},
			"Queue": {
				{label: "download-queue-size", value: "(not loaded)"},
				{label: "seed-queue-size", value: "(not loaded)"},
				{label: "queue-stalled-minutes", value: "(not loaded)"},
			},
		},
	}
}

// nextTab and prevTab cycle the tabs, wrapping in both directions.
func (w settingsWindow) nextTab() settingsWindow {
	w.tab = (w.tab + 1) % len(settingsTabs)

	return w
}

func (w settingsWindow) prevTab() settingsWindow {
	w.tab = (w.tab + len(settingsTabs) - 1) % len(settingsTabs)

	return w
}

// view renders the window into the whole body region.
func (w settingsWindow) view(st Styles, l layout) string {
	inner := l.body.inner()

	lines := []string{renderTabs(st, settingsTabs, w.tab, inner.w), tabRule(st, inner.w)}

	for _, f := range w.fields[settingsTabs[w.tab]] {
		lines = append(lines, pad(st.Muted.Render(f.label), f.value, inner.w))
	}

	lines = append(lines, "", st.Muted.Render("editing lands in T3"))

	return paneBox(st, paneSettings, true).
		Width(l.body.w).
		Height(l.body.h).
		Render(paneContent(lines, l.body))
}
