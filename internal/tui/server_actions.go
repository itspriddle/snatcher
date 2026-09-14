package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// The server's long tail: `server port test` and `server reset <key>`, which
// have no key and are reached by name from the palette in any window.
const (
	actionServerPortTest     Action = "server.port-test"
	actionServerResetSetting Action = "server.reset-setting"
)

// resetChooserTitle titles the palette that picks a setting to reset.
const resetChooserTitle = "Reset setting"

// portTestMsg is the answer to a port test.
type portTestMsg struct {
	gen    int
	result transmission.PortTest
	err    error
}

// settingResetMsg is the answer to a setting reset.
type settingResetMsg struct {
	gen int
	def transmission.SettingDef
	err error
}

// refuseServerAction says why a server action cannot run, or reports that it
// can.
func (m Model) refuseServerAction() (string, bool) {
	switch {
	case m.client == nil:
		return "no server", true
	case m.live.switching:
		return "switching servers…", true
	}

	return "", false
}

// portTest asks the server whether its peer port is reachable from outside.
func (m Model) portTest() (Model, tea.Cmd) {
	if why, refused := m.refuseServerAction(); refused {
		m.toast = why

		return m, nil
	}

	ctx, client, gen := m.ctx, m.client, m.live.gen
	m.toast = "testing port…"

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
		defer cancel()

		result, err := client.TestPort(ctx)

		return portTestMsg{gen: gen, result: result, err: err}
	}
}

// portTested reports a port test. A server that names the IP protocol it tested
// over (4.1+) has it shown; the bespoke scheme never does.
func (m Model) portTested(msg portTestMsg) (Model, tea.Cmd) {
	if msg.gen != m.live.gen {
		return m, nil
	}

	switch {
	case msg.err != nil:
		m.toast = "port test: " + errorLine(msg.err)
	case !msg.result.Open:
		m.toast = "port is closed"
	case msg.result.IPProtocol != "":
		m.toast = "port is open (" + output.SanitizeTerminal(msg.result.IPProtocol) + ")"
	default:
		m.toast = "port is open"
	}

	return m, nil
}

// openResetSetting opens the reset chooser: the palette again, over every
// setting that can be written, matched by key or by description.
func (m Model) openResetSetting() (Model, tea.Cmd) {
	if why, refused := m.refuseServerAction(); refused {
		m.toast = why

		return m, nil
	}

	if m.hasOverlay(paletteOverlayKind) {
		return m, nil
	}

	return m.pushPalette(newPalette(m.styles, resetChooserTitle, resetSettingEntries()))
}

// resetSettingEntries are the reset chooser's rows, in the settings registry's
// order. The read-only setting has no default to write, so it is left out.
func resetSettingEntries() []paletteEntry {
	var out []paletteEntry

	for _, def := range transmission.SettingDefs() {
		if def.ReadOnly {
			continue
		}

		out = append(out, paletteEntry{
			label: def.Key,
			note:  def.Desc,
			run: func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) {
				question := "Reset " + def.Key + " to " + defaultWord(def) + "?"

				return m.pop().push(newConfirm(question, func(m Model) (Model, tea.Cmd) { return m.resetSetting(def) }, nil)), nil
			},
		})
	}

	return out
}

// defaultWord is a setting's default as a confirm or a toast words it: as
// `server show` formats it, with an empty string quoted so it reads as a value.
func defaultWord(def transmission.SettingDef) string {
	if v := transmission.FormatSettingValue(def.Default); v != "" {
		return v
	}

	return `""`
}

// resetSetting writes a setting's default, as `server reset <key>` does.
func (m Model) resetSetting(def transmission.SettingDef) (Model, tea.Cmd) {
	if why, refused := m.refuseServerAction(); refused {
		m.toast = why

		return m, nil
	}

	ctx, client, gen := m.ctx, m.client, m.live.gen
	m.toast = "resetting " + def.Key + "…"

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
		defer cancel()

		return settingResetMsg{gen: gen, def: def, err: client.SetSession(ctx, map[string]any{def.Key: def.Default})}
	}
}

// settingReset reports a reset and re-reads what it changed: the session for
// the status bar, and the settings window when it is loaded and clean.
//
// alt-speed leaves the settings window alone because the status bar shows what
// it changed. A reset is picked from the list the settings window draws, so a
// stale row there is a lie the next `,` shows at once. A dirty window is left
// alone: its draft wins, and R's discard prompt is the way to refresh it.
func (m Model) settingReset(msg settingResetMsg) (Model, tea.Cmd) {
	if msg.gen != m.live.gen {
		return m, nil
	}

	if msg.err != nil {
		m.toast = "reset " + msg.def.Key + ": " + errorLine(msg.err)

		return m, nil
	}

	poll := pollSession(m.ctx, m.client, m.live.gen)
	toast := "Reset " + msg.def.Key + " to " + defaultWord(msg.def)

	if m.settings.state != stateLoaded || m.settings.dirtyCount() > 0 {
		m.toast = toast

		return m, poll
	}

	m, reload := m.reloadSettings()
	m.toast = toast

	return m, tea.Batch(poll, reload)
}

// serverActionHandlers are the palette's server actions.
func serverActionHandlers() map[Action]handler {
	return map[Action]handler{
		actionServerPortTest:     func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.portTest() },
		actionServerResetSetting: func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.openResetSetting() },
	}
}
