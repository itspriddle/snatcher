package tui

import (
	"net/url"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/itspriddle/snatcher/internal/output"
)

// pickerOverlayKind names the server picker overlay.
const pickerOverlayKind = "picker"

// pickerMaxWidth caps the picker box, as confirmMaxWidth caps the confirm.
const pickerMaxWidth = 60

// pickerOverlay is S: a list of the configured servers of the type the window
// in front uses, to switch to. The switch is for this session only, like
// --server; it never writes a default.
//
// It is modal, like the confirm overlay. Its stack is ctxPicker alone, so q and
// the window keys do nothing while it is open, and ctrl+c still quits.
type pickerOverlay struct {
	// prowlarr says the list is Prowlarr servers, which switch the Prowlarr
	// window, rather than Transmission ones.
	prowlarr bool

	// servers are the configured servers, sorted by alias.
	servers []ServerEntry

	// current is the alias the TUI is on.
	current string

	// cursor indexes servers.
	cursor int
}

// newPicker builds the picker with its cursor on the current server.
func newPicker(servers []ServerEntry, current string, prowlarr bool) pickerOverlay {
	p := pickerOverlay{servers: servers, current: current, prowlarr: prowlarr}

	for i, s := range servers {
		if s.Name == current {
			p.cursor = i
		}
	}

	return p
}

// kind implements overlay.
func (pickerOverlay) kind() string { return pickerOverlayKind }

// contexts implements overlay.
func (pickerOverlay) contexts() []Context { return []Context{ctxPicker} }

// move steps the cursor, stopping at the ends rather than wrapping.
func (p pickerOverlay) move(delta int) pickerOverlay {
	p.cursor = clamp(p.cursor+delta, 0, max(0, len(p.servers)-1))

	return p
}

// view implements overlay. The cursor row carries a ">" marker as well as the
// cursor style, so it survives NO_COLOR and a stripped golden.
func (p pickerOverlay) view(m Model, w, _ int) string {
	width := min(overlayContentWidth(w), pickerMaxWidth)

	names := make([]string, len(p.servers))
	hosts := make([]string, len(p.servers))
	nameW, hostW := 0, 0

	for i, s := range p.servers {
		names[i], hosts[i] = output.SanitizeTerminal(s.Name), hostOf(s.URL)
		nameW = max(nameW, len([]rune(names[i])))
		hostW = max(hostW, len([]rune(hosts[i])))
	}

	title := "Switch server"
	if p.prowlarr {
		title = "Switch Prowlarr server"
	}

	lines := []string{m.styles.Title.Render(truncate(title, width)), ""}

	for i, s := range p.servers {
		marker := "  "
		if i == p.cursor {
			marker = "> "
		}

		line := padLine(marker+padLine(names[i], nameW)+"  "+padLine(hosts[i], hostW)+"  "+p.note(s), width)

		if i == p.cursor {
			line = m.styles.Cursor.Render(line)
		}

		lines = append(lines, line)
	}

	lines = append(lines, "", m.help.ShortHelpView(contextBindings(ctxPicker)))

	return m.styles.Overlay.Render(strings.Join(lines, "\n"))
}

// note says which server is the current one and which is the configured
// default.
func (p pickerOverlay) note(s ServerEntry) string {
	switch {
	case s.Name == p.current && s.Default:
		return "(current, default)"
	case s.Name == p.current:
		return "(current)"
	case s.Default:
		return "(default)"
	}

	return ""
}

// hostOf is a server URL as a host, sanitized: the RPC path is the same on
// every Transmission server and spends width saying nothing, and a host carries
// no credential.
func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return output.SanitizeTerminal(u.Host)
	}

	return output.SanitizeTerminal(raw)
}

// togglePicker is S: open the picker, or close it when it is the overlay on top.
// It lists the servers of the type the window in front uses. Either window's
// dial in flight refuses it: the two dialers share one error-writer swap.
func (m Model) togglePicker() Model {
	if top, ok := m.topOverlay(); ok && top.kind() == pickerOverlayKind {
		return m.pop()
	}

	isProwlarr := m.window == windowProwlarr
	list, current := m.deps.Servers, m.serverName

	if isProwlarr {
		list, current = m.deps.ProwlarrServers, m.prowlarr.serverName
	}

	switch {
	case m.live.switching || m.prowlarr.dialing:
		m.toast = "switching servers…"

		return m
	case list == nil:
		m.toast = "no server list"

		return m
	}

	servers, err := list()

	switch {
	case err != nil:
		m.toast = "server list: " + err.Error()
	case len(servers) == 0 && isProwlarr:
		m.toast = "no Prowlarr server configured"
	case len(servers) <= 1:
		m.toast = "only one server configured"
	default:
		return m.push(newPicker(servers, current, isProwlarr))
	}

	return m
}

// topPicker returns the picker when it is the overlay on top.
func (m Model) topPicker() (pickerOverlay, bool) {
	top, ok := m.topOverlay()
	if !ok {
		return pickerOverlay{}, false
	}

	p, ok := top.(pickerOverlay)

	return p, ok
}

// movePicker moves the picker's cursor.
func (m Model) movePicker(delta int) Model {
	p, ok := m.topPicker()
	if !ok {
		return m
	}

	return m.replaceTopOverlay(p.move(delta))
}

// pickServer is enter in the picker. A switch replaces the settings window, so
// unsaved settings are confirmed away first, exactly as a reload would.
func (m Model) pickServer() (Model, tea.Cmd) {
	p, ok := m.topPicker()
	if !ok || len(p.servers) == 0 {
		return m, nil
	}

	m = m.pop()
	name := p.servers[p.cursor].Name

	current := m.serverName
	if p.prowlarr {
		current = m.prowlarr.serverName
	}

	if name == current {
		m.toast = "already on " + name

		return m, nil
	}

	if p.prowlarr {
		return m.switchProwlarr(name)
	}

	if n := m.settings.dirtyCount(); n > 0 {
		question := countQuestion("Discard", n, "unsaved change", "and switch to "+output.SanitizeTerminal(name)+"?")

		return m.push(newConfirm(question, func(m Model) (Model, tea.Cmd) {
			return m.switchServer(name)
		}, nil)), nil
	}

	return m.switchServer(name)
}

// pickerHandlers are S and the picker's own keys.
func pickerHandlers() map[Action]handler {
	return map[Action]handler{
		actionServerPicker: plain(Model.togglePicker),
		actionPickerUp:     plain(func(m Model) Model { return m.movePicker(-1) }),
		actionPickerDown:   plain(func(m Model) Model { return m.movePicker(1) }),
		actionPickerSelect: func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.pickServer() },
	}
}
