package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// rpcTimeout bounds one settings round trip, so a server that accepts the
// connection and never answers cannot leave the window loading forever.
const rpcTimeout = 15 * time.Second

// settingsLoadedMsg is the answer to loadSettings.
type settingsLoadedMsg struct {
	session transmission.SessionSettings
	err     error
}

// settingsSavedMsg is the answer to saveSettings. The two backends are written
// independently, so each reports on its own.
type settingsSavedMsg struct {
	// session is the snapshot re-read after SetSession, when both succeeded.
	session transmission.SessionSettings

	// sessionWrote are the session keys SetSession accepted, and sessionErr
	// the SetSession or re-read failure. A failed re-read after a successful
	// write reports both.
	sessionWrote []string
	sessionErr   error

	// clientWrote are the client keys written, and clientErr the first
	// failure, on clientErrKey. Nothing after a failing client key is written.
	clientWrote  []string
	clientErr    error
	clientErrKey string
}

// clientChange is one unsaved client setting.
type clientChange struct {
	key   string
	value any
}

// loadSettings fetches the session snapshot the server tabs draw.
func loadSettings(ctx context.Context, client *transmission.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
		defer cancel()

		session, err := client.GetSession(ctx)

		return settingsLoadedMsg{session: session, err: err}
	}
}

// saveSettings writes every unsaved change: the client keys through the config
// store, then the session keys in one SetSession followed by a GetSession, so the
// window shows what the server actually kept (the "server reset" pattern).
//
// Every client value is validated before any is written, so a bad value cannot
// leave half the client keys saved.
func saveSettings(
	ctx context.Context,
	client *transmission.Client,
	cfg ClientConfig,
	clientChanges []clientChange,
	sessionChanges map[string]any,
) tea.Cmd {
	return func() tea.Msg {
		var msg settingsSavedMsg

		if cfg != nil {
			msg = writeClientChanges(cfg, clientChanges, msg)
		}

		if client == nil || len(sessionChanges) == 0 {
			return msg
		}

		ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
		defer cancel()

		if err := client.SetSession(ctx, sessionChanges); err != nil {
			msg.sessionErr = err

			return msg
		}

		for k := range sessionChanges {
			msg.sessionWrote = append(msg.sessionWrote, k)
		}

		slices.Sort(msg.sessionWrote)

		msg.session, msg.sessionErr = client.GetSession(ctx)

		return msg
	}
}

// writeClientChanges validates, then writes, the client changes in order,
// stopping at the first failure.
func writeClientChanges(cfg ClientConfig, changes []clientChange, msg settingsSavedMsg) settingsSavedMsg {
	for _, c := range changes {
		if err := cfg.Validate(c.key, c.value); err != nil {
			msg.clientErr, msg.clientErrKey = err, c.key

			return msg
		}
	}

	for _, c := range changes {
		if err := cfg.Set(c.key, c.value); err != nil {
			msg.clientErr, msg.clientErrKey = err, c.key

			return msg
		}

		msg.clientWrote = append(msg.clientWrote, c.key)
	}

	return msg
}

// sessionChanges is every unsaved server setting, keyed as SetSession expects.
func (w settingsWindow) sessionChanges() map[string]any {
	changes := map[string]any{}

	for _, t := range w.tabs {
		for _, f := range t.fields {
			if f.source == sourceSession && f.dirty() {
				changes[f.key] = f.value
			}
		}
	}

	return changes
}

// clientChanges is every unsaved client setting, in tab order.
func (w settingsWindow) clientChanges() []clientChange {
	var changes []clientChange

	for _, t := range w.tabs {
		for _, f := range t.fields {
			if f.source == sourceClient && f.dirty() {
				changes = append(changes, clientChange{key: f.key, value: f.value})
			}
		}
	}

	return changes
}

// markWritten makes the value of each written row its loaded value, so it is
// no longer dirty.
func (w settingsWindow) markWritten(keys []string) settingsWindow {
	w = w.cloned()

	for i := range w.tabs {
		for j, f := range w.tabs[i].fields {
			if f.kind != fieldButton && slices.Contains(keys, f.key) {
				w.tabs[i].fields[j].loaded = f.value
				w.tabs[i].fields[j].err = ""
			}
		}
	}

	return w
}

// setFieldErr puts an error on the row with the given key, wherever it is.
func (w settingsWindow) setFieldErr(key string, err error) settingsWindow {
	w = w.cloned()

	for i := range w.tabs {
		for j := range w.tabs[i].fields {
			if w.tabs[i].fields[j].key == key {
				w.tabs[i].fields[j].err = err.Error()
			}
		}
	}

	return w
}

// fieldValue is the current value of the row with the given key.
func (w settingsWindow) fieldValue(key string) (any, bool) {
	for _, t := range w.tabs {
		for _, f := range t.fields {
			if f.key == key {
				return f.value, true
			}
		}
	}

	return nil, false
}

// startLoad fetches the session snapshot. A window with no server has nothing to
// fetch.
func (m Model) startLoad() (Model, tea.Cmd) {
	if m.client == nil {
		return m, nil
	}

	m.settings.state = stateLoading
	m.settings.loadErr = nil

	return m, loadSettings(m.ctx, m.client)
}

// startSave writes every unsaved change, then runs after once everything has
// succeeded. With nothing to save it runs after at once.
func (m Model) startSave(after afterSave) (Model, tea.Cmd) {
	if busy := m.settings.busy(); busy != "" {
		m.toast = busy

		return m, nil
	}

	client, session := m.settings.clientChanges(), m.settings.sessionChanges()

	if len(client) == 0 && len(session) == 0 {
		m.toast = "nothing to save"

		return m.runAfter(after)
	}

	m.settings.state = stateSaving
	m.settings.saveErr = nil
	m.settings.after = after

	return m, saveSettings(m.ctx, m.client, m.deps.Config, client, session)
}

// runAfter is what a save prompt promised to do once the save was done.
func (m Model) runAfter(after afterSave) (Model, tea.Cmd) {
	switch after {
	case afterLeave:
		return m.switchWindow(windowTorrents), nil
	case afterQuit:
		return m, tea.Quit
	case afterNone:
	}

	return m, nil
}

// settingsLoaded installs a fetched snapshot, or the error fetching it. A reply
// the window is no longer waiting for is dropped.
func (m Model) settingsLoaded(msg settingsLoadedMsg) (Model, tea.Cmd) {
	if m.settings.state != stateLoading {
		return m, nil
	}

	if msg.err != nil {
		m.settings.state = stateError
		m.settings.loadErr = msg.err
		m.settings = m.settings.fixCursors()

		return m.syncSettings(), nil
	}

	m.settings = m.settings.loadSession(msg.session)

	return m.syncSettings(), nil
}

// settingsSaved settles a save: written rows stop being dirty, failures land on
// their row or the hint line, a changed appearance is applied live, and the
// prompt's follow-up runs only if nothing failed.
func (m Model) settingsSaved(msg settingsSavedMsg) (Model, tea.Cmd) {
	if m.settings.state != stateSaving {
		return m, nil
	}

	w := m.settings.markWritten(append(slices.Clone(msg.clientWrote), msg.sessionWrote...))

	if msg.sessionErr == nil && len(msg.sessionWrote) > 0 {
		w = w.loadSession(msg.session)
	}

	w.state = stateLoaded
	w.saveErr = msg.sessionErr

	if msg.clientErr != nil {
		w = w.setFieldErr(msg.clientErrKey, msg.clientErr)
	}

	after := w.after
	w.after = afterNone
	m.settings = w.fixCursors()
	m = m.applyClientWrites(msg.clientWrote).syncSettings()

	// A session write changes what the status bar draws — limits, alt-speed, the
	// download directory whose free space it shows — so it is re-read now rather
	// than at the next slow refresh.
	var refresh tea.Cmd
	if len(msg.sessionWrote) > 0 && m.polling() && after != afterQuit {
		refresh = pollSession(m.ctx, m.client, m.live.gen)
	}

	if err := errors.Join(msg.clientErr, msg.sessionErr); err != nil {
		m.toast = "save failed: " + firstErrorLine(msg.clientErr, msg.sessionErr)

		return m, refresh
	}

	n := len(msg.clientWrote) + len(msg.sessionWrote)
	m.toast = fmt.Sprintf("saved %d %s", n, plural(n, "setting"))

	m, cmd := m.runAfter(after)

	return m, tea.Batch(cmd, refresh)
}

// firstErrorLine is the first non-nil error as an errorLine.
func firstErrorLine(errs ...error) string {
	for _, err := range errs {
		if err != nil {
			return errorLine(err)
		}
	}

	return ""
}

// applyClientWrites makes saved client settings take effect in this session: a
// theme or icon change re-styles the UI at once, and a poll interval is handed
// to the refresh spine.
func (m Model) applyClientWrites(keys []string) Model {
	restyle := false

	for _, k := range keys {
		v, ok := m.settings.fieldValue(k)
		if !ok {
			continue
		}

		switch k {
		case clientKeyTheme:
			if name, isString := v.(string); isString {
				m.deps.Theme, restyle = name, true
			}
		case clientKeyNerdFont:
			if on, isBool := v.(bool); isBool {
				m.deps.NerdFont, restyle = on, true
			}
		case clientKeyPollInterval:
			if raw, isString := v.(string); isString {
				if d, err := time.ParseDuration(raw); err == nil {
					m.deps.PollInterval = d
				}
			}
		}
	}

	if !restyle {
		return m
	}

	return m.applyTheme(m.isDark).syncTorrents()
}

// refreshSettings is R in the settings window: re-read the config store and the
// session. Unsaved changes would be overwritten, so with any it asks first.
func (m Model) refreshSettings() (Model, tea.Cmd) {
	m.settings = m.settings.closeDropdown()

	if busy := m.settings.busy(); busy != "" {
		m.toast = busy

		return m, nil
	}

	n := m.settings.dirtyCount()
	if n == 0 {
		return m.reloadSettings()
	}

	return m.push(newConfirm(countQuestion("Discard", n, "unsaved change", "and reload?"), func(m Model) (Model, tea.Cmd) {
		m.settings = m.settings.revert()

		return m.reloadSettings()
	}, nil)), nil
}

// reloadSettings re-reads both backends, discarding nothing that is not already
// clean.
func (m Model) reloadSettings() (Model, tea.Cmd) {
	if m.deps.Config != nil {
		entries, err := m.deps.Config.Entries()
		if err != nil {
			m.toast = "config: " + err.Error()
		} else {
			m.settings = m.settings.loadClient(entries)
		}
	}

	if m.client == nil {
		m.toast = "reloaded"

		return m.syncSettings(), nil
	}

	return m.startLoad()
}

// refreshHandler is R: re-read the settings in the settings window, and poll now
// everywhere else.
func refreshHandler(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.window != windowSettings {
		return m.refreshNow()
	}

	m, cmd := m.refreshSettings()

	return m.syncSettings(), cmd
}
