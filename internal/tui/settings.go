package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// The client keys the General and Polling tabs edit, spelled as the CLI spells
// them. internal/tui does not import internal/commands, so they are written out
// here; Deps.Config is what validates them.
const (
	clientKeyTheme        = "client.theme"
	clientKeyNerdFont     = "client.nerd_font"
	clientKeyPollInterval = "client.poll_interval"
)

// builtinThemes are the theme choices offered before Deps.Config has answered
// with its own list.
var builtinThemes = []string{"default", themeDracula, themeNord, themeSolarized}

// settingsMode is what the keyboard is doing inside the settings window.
type settingsMode int

const (
	// modeNav moves between rows and flips toggles in place.
	modeNav settingsMode = iota

	// modeEdit has the text input focused on the cursor row. Model.inputFocused
	// is set alongside it, so the router hands the input the keyboard.
	modeEdit

	// modeSelect has the cursor row's inline dropdown open.
	modeSelect
)

// settingsState is where the session settings are in their round trip.
type settingsState int

const (
	// stateIdle has not asked the server for anything yet.
	stateIdle settingsState = iota

	// stateLoading is waiting on GetSession.
	stateLoading

	// stateLoaded holds a session snapshot (or has no server to hold one of).
	stateLoaded

	// stateSaving is waiting on a save.
	stateSaving

	// stateError is a failed load.
	stateError
)

// afterSave is what a save prompt promised to do once the save succeeds.
type afterSave int

const (
	// afterNone stays put: the Save button.
	afterNone afterSave = iota

	// afterLeave returns to the torrents window: esc's "save before leaving?".
	afterLeave

	// afterQuit quits: q's "save before quitting?".
	afterQuit
)

// settingsTab is one tab of the form.
type settingsTab struct {
	// name is the tab's label.
	name string

	// category is the registry category a server tab shows, and empty for the
	// two client tabs.
	category transmission.SettingCategory

	// fields are the rows, ending with the Save and Revert buttons.
	fields []settingsField

	// cursor indexes fields; offset is the first field row scrolled into view.
	cursor, offset int
}

// session reports whether the tab shows server settings.
func (t settingsTab) session() bool {
	return t.category != ""
}

// settingsWindow is the settings window: six tabs of rows, one text input
// shared by every text row, and the load/save state of the server tabs.
type settingsWindow struct {
	tabs []settingsTab
	tab  int

	mode  settingsMode
	state settingsState

	// input edits the cursor row in modeEdit. It holds a draft, not the row's
	// value: esc discards it and only a submit commits it.
	input textinput.Model

	// selectIdx is the dropdown cursor in modeSelect.
	selectIdx int

	// noServer is set when the TUI has no dialed client, so the server tabs
	// have nothing to show.
	noServer bool

	// loadErr is the failed load behind stateError; saveErr the session-side
	// failure of the last save, shown on the hint line.
	loadErr, saveErr error

	// after is the follow-up of the save in flight.
	after afterSave
}

// newSettingsWindow builds the form. The client tabs start from the values this
// process resolved; the server tabs start empty until a session is loaded.
func newSettingsWindow(st Styles, deps Deps) settingsWindow {
	general := settingsTab{name: "General", fields: []settingsField{
		{
			key: clientKeyTheme, label: clientKeyTheme, desc: "Color theme",
			kind: fieldSelect, source: sourceClient, options: builtinThemes,
			loaded: deps.Theme, value: deps.Theme,
		},
		{
			key: clientKeyNerdFont, label: clientKeyNerdFont, desc: "Draw icons with Nerd Font glyphs",
			kind: fieldToggle, source: sourceClient,
			loaded: deps.NerdFont, value: deps.NerdFont,
		},
	}}

	polling := settingsTab{name: "Polling", fields: []settingsField{
		{
			key: clientKeyPollInterval, label: clientKeyPollInterval,
			desc: "How often server data refreshes (Go duration, 1s to 1h)",
			kind: fieldText, source: sourceClient,
			loaded: deps.PollInterval.String(), value: deps.PollInterval.String(),
		},
	}}

	if deps.Config == nil {
		for _, tab := range []*settingsTab{&general, &polling} {
			for i := range tab.fields {
				tab.fields[i].readOnly = true
				tab.fields[i].note = "(no config store)"
			}
		}
	}

	tabs := []settingsTab{general, polling}

	for _, c := range transmission.SettingCategories {
		tab := settingsTab{name: categoryTabName(c), category: c}

		for _, def := range transmission.SettingsInCategory(c) {
			tab.fields = append(tab.fields, sessionField(def))
		}

		tabs = append(tabs, tab)
	}

	for i := range tabs {
		tabs[i].fields = append(tabs[i].fields, buttonFields()...)
	}

	input := textinput.New()
	input.Prompt = ""
	input.KeyMap = textInputKeyMap()

	w := settingsWindow{tabs: tabs, input: input}

	return w.fixCursors().setStyles(st)
}

// categoryTabName is a registry category as a tab label.
func categoryTabName(c transmission.SettingCategory) string {
	name := string(c)

	return strings.ToUpper(name[:1]) + name[1:]
}

// setStyles re-themes the text input, the one component in the window that
// keeps styles of its own.
func (w settingsWindow) setStyles(st Styles) settingsWindow {
	w.input.SetStyles(st.Input)

	return w
}

// cloned deep-copies the tabs. The window is a value, but its tabs are slices;
// every transition that changes a row or a cursor clones first, so a model a
// test is still holding cannot be changed underneath it.
func (w settingsWindow) cloned() settingsWindow {
	tabs := make([]settingsTab, len(w.tabs))

	for i, t := range w.tabs {
		t.fields = slices.Clone(t.fields)
		tabs[i] = t
	}

	w.tabs = tabs

	return w
}

// visible reports whether a tab's rows are drawn at all. A server tab with no
// server, or no snapshot yet, draws a status line instead. A save in flight
// keeps the rows it is saving on screen.
func (w settingsWindow) visible(tab int) bool {
	if !w.tabs[tab].session() {
		return true
	}

	return !w.noServer && (w.state == stateLoaded || w.state == stateSaving)
}

// selectable reports whether the cursor may rest on a row: a button always, a
// field only when drawn and editable.
func (w settingsWindow) selectable(tab, i int) bool {
	f := w.tabs[tab].fields[i]
	if f.kind == fieldButton {
		return true
	}

	return w.visible(tab) && !f.readOnly
}

// firstSelectable is the first row of a tab the cursor may rest on. The buttons
// guarantee there is one.
func (w settingsWindow) firstSelectable(tab int) int {
	for i := range w.tabs[tab].fields {
		if w.selectable(tab, i) {
			return i
		}
	}

	return 0
}

// fixCursors moves any cursor resting on a row it may not rest on — after a
// load, or after a client key turned out to be overridden — to its tab's first
// selectable row.
func (w settingsWindow) fixCursors() settingsWindow {
	w = w.cloned()

	for i := range w.tabs {
		if !w.selectable(i, w.tabs[i].cursor) {
			w.tabs[i].cursor = w.firstSelectable(i)
		}
	}

	return w
}

// current is the row under the cursor.
func (w settingsWindow) current() settingsField {
	t := w.tabs[w.tab]

	return t.fields[t.cursor]
}

// setField replaces the row under the cursor.
func (w settingsWindow) setField(f settingsField) settingsWindow {
	w = w.cloned()
	w.tabs[w.tab].fields[w.tabs[w.tab].cursor] = f

	return w
}

// switchTab moves step tabs, wrapping.
func (w settingsWindow) switchTab(step int) settingsWindow {
	w.tab = (w.tab + step + len(w.tabs)) % len(w.tabs)

	return w.fixCursors()
}

// moveField moves the cursor step rows within the tab, wrapping, buttons
// included and read-only rows skipped.
func (w settingsWindow) moveField(step int) settingsWindow {
	n := len(w.tabs[w.tab].fields)

	for k := 1; k <= n; k++ {
		next := ((w.tabs[w.tab].cursor+step*k)%n + n) % n
		if w.selectable(w.tab, next) {
			w = w.cloned()
			w.tabs[w.tab].cursor = next

			return w
		}
	}

	return w
}

// fieldStep is up and down: they belong to the row, never to row navigation.
// In the dropdown they move its cursor; on a toggle they flip it; on a select
// they cycle its option. Text rows and buttons ignore them.
func (w settingsWindow) fieldStep(step int) settingsWindow {
	f := w.current()

	if w.mode == modeSelect {
		w.selectIdx = clamp(w.selectIdx+step, 0, len(f.options)-1)

		return w
	}

	if !w.selectable(w.tab, w.tabs[w.tab].cursor) {
		return w
	}

	switch f.kind {
	case fieldToggle:
		on, _ := f.value.(bool)
		f.value = !on
	case fieldSelect:
		if len(f.options) == 0 {
			return w
		}

		f.value = f.options[(f.optionIndex()+step+len(f.options))%len(f.options)]
	case fieldText, fieldButton:
		return w
	}

	return w.setField(f)
}

// openDropdown opens the cursor row's options with the current value selected.
func (w settingsWindow) openDropdown() settingsWindow {
	w.mode = modeSelect
	w.selectIdx = w.current().optionIndex()

	return w
}

// commitDropdown sets the cursor row to the dropdown's option and closes it.
func (w settingsWindow) commitDropdown() settingsWindow {
	f := w.current()
	w.mode = modeNav

	if w.selectIdx < 0 || w.selectIdx >= len(f.options) {
		return w
	}

	f.value = f.options[w.selectIdx]

	return w.setField(f)
}

// closeDropdown closes an open dropdown without changing the row.
func (w settingsWindow) closeDropdown() settingsWindow {
	if w.mode == modeSelect {
		w.mode = modeNav
	}

	return w
}

// loadSession fills the server tabs from a snapshot, discarding any unsaved
// server edits. A fresh load puts each server tab's cursor back on its first
// row; the re-read that settles a save leaves the cursor where the user left it.
func (w settingsWindow) loadSession(s transmission.SessionSettings) settingsWindow {
	w = w.cloned()
	reset := w.state != stateSaving
	w.state = stateLoaded

	for i := range w.tabs {
		if !w.tabs[i].session() {
			continue
		}

		for j, f := range w.tabs[i].fields {
			if f.source != sourceSession || f.kind == fieldButton {
				continue
			}

			v := f.def.Accessor(s)
			f.loaded, f.value, f.err = v, v, ""
			w.tabs[i].fields[j] = f
		}

		if reset {
			w.tabs[i].cursor, w.tabs[i].offset = 0, 0
		}
	}

	return w.fixCursors()
}

// loadClient fills the client tabs from the config store. A row with an unsaved
// edit is left alone, so leaving and re-entering the window keeps it. A key
// overridden for this run by a flag or an environment variable is read-only:
// writing the file would change nothing the user can see.
func (w settingsWindow) loadClient(entries []ClientEntry) settingsWindow {
	byKey := make(map[string]ClientEntry, len(entries))
	for _, e := range entries {
		byKey[e.Key] = e
	}

	w = w.cloned()

	for i := range w.tabs {
		for j, f := range w.tabs[i].fields {
			e, ok := byKey[f.key]
			if f.source != sourceClient || f.dirty() || !ok {
				continue
			}

			f.loaded, f.value = e.Value, e.Value
			f.readOnly, f.note = false, ""

			if e.Allowed != nil {
				f.options = e.Allowed
			}

			switch e.Source {
			case "flag":
				f.readOnly, f.note = true, fmt.Sprintf("(set by %s for this run)", e.SetBy)
			case "env":
				f.readOnly, f.note = true, fmt.Sprintf("(set by %s)", e.SetBy)
			}

			w.tabs[i].fields[j] = f
		}
	}

	return w.fixCursors()
}

// dirtyCount is how many rows hold unsaved changes, across every tab.
func (w settingsWindow) dirtyCount() int {
	n := 0

	for i := range w.tabs {
		n += w.tabDirtyCount(i)
	}

	return n
}

// tabDirtyCount is how many rows of one tab hold unsaved changes.
func (w settingsWindow) tabDirtyCount(tab int) int {
	n := 0

	for _, f := range w.tabs[tab].fields {
		if f.dirty() {
			n++
		}
	}

	return n
}

// tabNames are the tab labels, with "*" on a tab holding unsaved changes.
func (w settingsWindow) tabNames() []string {
	names := make([]string, len(w.tabs))

	for i, t := range w.tabs {
		names[i] = t.name
		if w.tabDirtyCount(i) > 0 {
			names[i] += "*"
		}
	}

	return names
}

// revert discards every unsaved change and every row error.
func (w settingsWindow) revert() settingsWindow {
	w = w.cloned()

	for i := range w.tabs {
		for j := range w.tabs[i].fields {
			w.tabs[i].fields[j].value = w.tabs[i].fields[j].loaded
			w.tabs[i].fields[j].err = ""
		}
	}

	return w
}

// busy is the toast a settings key gets while a round trip is in flight, or
// empty when the window is free. Edits during a load or save are blocked rather
// than reconciled.
func (w settingsWindow) busy() string {
	switch w.state {
	case stateLoading:
		return "loading…"
	case stateSaving:
		return "saving…"
	case stateIdle, stateLoaded, stateError:
	}

	return ""
}

// settingsChrome is the rows of the window that are not field rows: the tab
// strip, its rule, a blank line, the hint line, and the button row.
const settingsChrome = 5

// settingsRegionHeight is how many field rows fit in the window.
func settingsRegionHeight(l layout) int {
	return max(1, l.body.inner().h-settingsChrome)
}

// rowSpan locates the cursor row among the drawn rows — which include an open
// dropdown's option rows — and counts them. start is -1 when the cursor is on a
// button, which is not in the scrolled region.
func (w settingsWindow) rowSpan() (start, end, total int) {
	t := w.tabs[w.tab]
	start, end = -1, -1

	for i := range len(t.fields) - len(buttonFields()) {
		if i == t.cursor {
			start = total
		}

		total++

		if i == t.cursor && w.mode == modeSelect {
			total += len(t.fields[i].options)
		}

		if i == t.cursor {
			end = total - 1
		}
	}

	return start, end, total
}

// scrolledOffset is the tab's offset adjusted so the cursor row, and its open
// dropdown, are inside a region of height rows.
func (w settingsWindow) scrolledOffset(height int) int {
	start, end, total := w.rowSpan()
	offset := w.tabs[w.tab].offset

	if start >= 0 {
		if end-offset >= height {
			offset = end - height + 1
		}

		if start < offset {
			offset = start
		}
	}

	return clamp(offset, 0, max(0, total-height))
}

// scroll stores the adjusted offset, so the region moves only when the cursor
// leaves it rather than being recomputed from scratch each frame.
func (w settingsWindow) scroll(height int) settingsWindow {
	offset := w.scrolledOffset(height)
	if offset == w.tabs[w.tab].offset {
		return w
	}

	w = w.cloned()
	w.tabs[w.tab].offset = offset

	return w
}

// labelWidth is the label column's width for a tab: the longest label plus room
// for the dirty marker, capped at half the pane so values keep some room.
func labelWidth(fields []settingsField, width int) int {
	longest := 0

	for _, f := range fields {
		if f.kind != fieldButton {
			longest = max(longest, lipgloss.Width(f.label))
		}
	}

	return min(longest+1, width/2)
}

// fieldMarkerWidth is the cursor marker column, "> " or two spaces.
const fieldMarkerWidth = 2

// fieldGap is the space between the label column and the value.
const fieldGap = 2

// valueWidth is the cells left for a value on one row of a pane width wide.
func (w settingsWindow) valueWidth(width int) int {
	return max(1, width-fieldMarkerWidth-labelWidth(w.tabs[w.tab].fields, width)-fieldGap)
}

// view renders the window into the whole body region.
func (w settingsWindow) view(st Styles, l layout) string {
	inner := l.body.inner()
	height := settingsRegionHeight(l)

	lines := []string{renderTabs(st, w.tabNames(), w.tab, inner.w), tabRule(st, inner.w)}
	lines = append(lines, clipLines(w.regionLines(st, inner.w, height), height)...)
	lines = append(lines, "", w.hintLine(st, inner.w), w.buttonsLine(st))

	return paneBox(st, paneSettings, true).
		Width(l.body.w).
		Height(l.body.h).
		Render(paneContent(lines, l.body))
}

// regionLines are the scrolled field rows, or the line that stands in for them
// on a server tab with nothing to show.
func (w settingsWindow) regionLines(st Styles, width, height int) []string {
	if !w.visible(w.tab) {
		lines := w.statusLines()
		for i, line := range lines {
			lines[i] = st.Muted.Render(centered(truncate(line, width), width))
		}

		return lines
	}

	t := w.tabs[w.tab]
	labelW := labelWidth(t.fields, width)

	var rows []string

	for i, f := range t.fields[:len(t.fields)-len(buttonFields())] {
		rows = append(rows, w.fieldRow(st, f, i == t.cursor, labelW, width))

		if i == t.cursor && w.mode == modeSelect {
			rows = append(rows, w.optionRows(st, f)...)
		}
	}

	return rows[w.scrolledOffset(height):]
}

// statusLines are what a server tab says in place of rows it cannot draw.
func (w settingsWindow) statusLines() []string {
	switch {
	case w.noServer:
		return []string{"(no server)"}
	case w.state == stateError:
		return []string{errorLine(w.loadErr), errorHint(w.loadErr)}
	}

	return []string{"loading settings…"}
}

// fieldRow renders one row: the cursor marker, the label (with "*" when dirty),
// and the value — or the text input, while this row is being edited — followed
// by the unit and note when they fit.
//
// Every plain part is truncated before it is styled, so paneContent never has
// to cut through an escape sequence.
func (w settingsWindow) fieldRow(st Styles, f settingsField, cursor bool, labelW, width int) string {
	marker := "  "
	if cursor {
		marker = "> "
	}

	label := f.label
	if f.dirty() {
		label += "*"
	}

	label = padLine(truncate(label, labelW), labelW)
	avail := max(1, width-fieldMarkerWidth-labelW-fieldGap)

	if cursor && w.mode == modeEdit {
		return st.FieldCursor.Render(marker+label) + strings.Repeat(" ", fieldGap) + w.input.View()
	}

	value := truncate(f.display(), avail)

	extra := ""
	for _, hint := range []string{f.unit, f.note} {
		if hint != "" && lipgloss.Width(value+extra)+1+lipgloss.Width(hint) <= avail {
			extra += " " + hint
		}
	}

	head := marker + label + strings.Repeat(" ", fieldGap)

	switch {
	case f.readOnly:
		return st.FieldReadOnly.Render(head+value) + st.Muted.Render(extra)
	case cursor:
		return st.FieldCursor.Render(head) + value + st.Muted.Render(extra)
	case f.dirty():
		return st.FieldDirty.Render(head) + value + st.Muted.Render(extra)
	}

	return head + value + st.Muted.Render(extra)
}

// optionRows are an open dropdown's options, drawn under the row they belong to
// and pushing the rows below it down.
func (w settingsWindow) optionRows(st Styles, f settingsField) []string {
	rows := make([]string, 0, len(f.options))

	for i, opt := range f.options {
		if i == w.selectIdx {
			rows = append(rows, st.FieldCursor.Render("    > "+opt))

			continue
		}

		rows = append(rows, "      "+opt)
	}

	return rows
}

// hintLine is the cursor row's description, or its error.
func (w settingsWindow) hintLine(st Styles, width int) string {
	f := w.current()

	switch {
	case f.err != "":
		return st.FieldError.Render(truncate("error: "+output.SanitizeTerminal(f.err), width))
	case w.saveErr != nil:
		return st.FieldError.Render(truncate("save failed: "+errorLine(w.saveErr), width))
	}

	return st.Muted.Render(truncate(f.desc, width))
}

// buttonsLine is the Save / Revert row. The focused button is marked in its
// text as well as its style, so focus survives NO_COLOR and a stripped golden
// alike, and the row does not shift when focus moves.
func (w settingsWindow) buttonsLine(st Styles) string {
	cursorKey := w.current().key

	render := func(k, label string) string {
		if k == cursorKey {
			return st.ButtonFocused.Render("[>" + label + "<]")
		}

		return st.Button.Render("[ " + label + " ]")
	}

	return render(buttonSave, fmt.Sprintf("Save (%d)", w.dirtyCount())) + "  " + render(buttonRevert, "Revert")
}

// settingsHandler wraps a settings key's transition with what every settings
// key needs: the save guard, and keeping the cursor row scrolled into view.
//
// Only a save blocks every key. A load blocks just the actions that would race
// it — Save, Revert's reload twin R, and a second load — because the server rows
// are not selectable until it lands, and the client tabs have nothing to race.
func settingsHandler(f func(Model) (Model, tea.Cmd)) handler {
	return func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) {
		if m.settings.state == stateSaving {
			m.toast = m.settings.busy()

			return m, nil
		}

		m, cmd := f(m)

		return m.syncSettings(), cmd
	}
}

// windowStep adapts a pure window transition into a settings handler.
func windowStep(f func(settingsWindow) settingsWindow) handler {
	return settingsHandler(func(m Model) (Model, tea.Cmd) {
		m.settings = f(m.settings)

		return m, nil
	})
}

// syncSettings stores the settings window's scroll offset for the current frame
// size.
func (m Model) syncSettings() Model {
	l := m.layout()
	if l.tooSmall {
		return m
	}

	m.settings = m.settings.scroll(settingsRegionHeight(l))

	return m
}

// openSettings brings the settings window forward and refreshes what it can.
//
// The client tabs are re-read from the config store every time — a local file
// read, cheap enough to do synchronously — but a row with an unsaved edit keeps
// it. The session is fetched asynchronously, and only when the window has no
// snapshot yet (or its last fetch failed): re-entering a loaded window keeps
// its edits rather than reloading over them.
func (m Model) openSettings() (Model, tea.Cmd) {
	m = m.switchWindow(windowSettings)

	if m.deps.Config != nil {
		entries, err := m.deps.Config.Entries()
		if err != nil {
			m.toast = "config: " + err.Error()
		} else {
			m.settings = m.settings.loadClient(entries)
		}
	}

	if m.client == nil {
		m.settings.noServer = true
		m.settings.state = stateLoaded
		m.settings = m.settings.fixCursors()

		return m.syncSettings(), nil
	}

	if m.settings.state == stateIdle || m.settings.state == stateError {
		m, cmd := m.startLoad()

		return m.syncSettings(), cmd
	}

	return m.syncSettings(), nil
}

// settingsEnter is enter in the settings window: flip a toggle, open or commit
// a dropdown, start editing a text row, or press a button.
func (m Model) settingsEnter() (Model, tea.Cmd) {
	w := m.settings

	if w.mode == modeSelect {
		m.settings = w.commitDropdown()

		return m, nil
	}

	if !w.selectable(w.tab, w.tabs[w.tab].cursor) {
		return m, nil
	}

	switch f := w.current(); f.kind {
	case fieldToggle:
		m.settings = w.fieldStep(1)
	case fieldSelect:
		m.settings = w.openDropdown()
	case fieldText:
		return m.beginEdit()
	case fieldButton:
		return m.pressButton(f.key)
	}

	return m, nil
}

// pressButton runs the Save or Revert button.
func (m Model) pressButton(k string) (Model, tea.Cmd) {
	if k == buttonRevert {
		n := m.settings.dirtyCount()
		m.settings = m.settings.revert()
		m.toast = fmt.Sprintf("reverted %d %s", n, plural(n, "change"))

		return m, nil
	}

	return m.startSave(afterNone)
}

// beginEdit focuses the text input on the cursor row, pre-filled with its value.
// The input's blink command is returned so the cursor blinks in a running
// program; a test that drops it just sees a solid cursor.
func (m Model) beginEdit() (Model, tea.Cmd) {
	w := m.settings
	f := w.current()

	w.input.SetValue(f.text())
	w.input.CursorEnd()

	// textinput draws one cell past its width for the cursor at the end.
	w.input.SetWidth(max(1, w.valueWidth(m.layout().body.inner().w)-1))

	cmd := w.input.Focus()
	w.mode = modeEdit

	f.err = ""
	m.settings = w.setField(f)
	m.inputFocused = true
	m.owner = ownerSettings

	return m, cmd
}

// blurInput is the ladder's "blur input" rung, and how a submitted input is
// left. It runs the owning input's own blur, then releases the keyboard.
func (m Model) blurInput() Model {
	switch m.owner {
	case ownerSearch:
		m = m.searchBlur()
	case ownerPrompt:
		// A prompt's draft is the overlay, so it is closed rather than blurred,
		// and closing it is what releases the input. Nothing to blur here.
	case ownerForm:
		m = m.formBlur()
	case ownerPalette:
		// The palette's query is the overlay, as a prompt's draft is: closing
		// it is what releases the input.
	case ownerQuery:
		m = m.queryBlur()
	case ownerNone, ownerSettings:
		m = m.settingsBlur()
	}

	m.inputFocused = false
	m.owner = ownerNone

	return m
}

// settingsBlur blurs the settings input. Its contents are a draft rather than
// the row's state, so blurring discards them: the row keeps its last committed
// value.
func (m Model) settingsBlur() Model {
	if m.settings.mode != modeEdit {
		return m
	}

	w := m.settings
	w.input.Blur()
	w.input.SetValue("")
	w.mode = modeNav

	f := w.current()
	f.err = ""
	m.settings = w.setField(f)

	return m
}

// submitInput is enter in a focused text input. The search field's text is
// already its state, so submitting it is blurring it.
func (m Model) submitInput() Model {
	if m.owner == ownerSearch {
		return m.blurInput()
	}

	return m.settingsSubmit()
}

// settingsSubmit is enter in the settings input: parse and validate the draft,
// and either commit it and leave the input, or report why not and stay.
func (m Model) settingsSubmit() Model {
	if m.window != windowSettings || m.settings.mode != modeEdit {
		return m.blurInput()
	}

	w := m.settings
	f := w.current()

	value, err := parseFieldInput(f, w.input.Value(), m.deps.Config)
	if err != nil {
		f.err = err.Error()
		m.settings = w.setField(f)

		return m
	}

	f.value, f.err = value, ""
	m.settings = w.setField(f)

	return m.blurInput()
}

// forwardToInput hands a message the router does not claim — keystrokes, a
// bracketed paste, the cursor's blink ticks — to the input that owns the
// keyboard.
func (m Model) forwardToInput(msg tea.Msg) (Model, tea.Cmd) {
	switch m.owner {
	case ownerSearch:
		return m.searchForward(msg)
	case ownerPrompt:
		return m.promptForward(msg)
	case ownerForm:
		return m.formForward(msg)
	case ownerPalette:
		return m.paletteForward(msg)
	case ownerQuery:
		return m.queryForward(msg)
	case ownerNone, ownerSettings:
	}

	return m.settingsForward(msg)
}

// settingsForward hands a message to the settings input while it is being
// edited. Anywhere else it is a no-op.
func (m Model) settingsForward(msg tea.Msg) (Model, tea.Cmd) {
	if m.window != windowSettings || m.settings.mode != modeEdit {
		return m, nil
	}

	w := m.settings

	input, cmd := w.input.Update(msg)
	w.input = input
	m.settings = w

	return m, cmd
}

// dirtyTabList names the tabs holding unsaved changes, for a prompt's detail
// line.
func (w settingsWindow) dirtyTabList() string {
	var names []string

	for i, t := range w.tabs {
		if w.tabDirtyCount(i) > 0 {
			names = append(names, t.name)
		}
	}

	return "unsaved on: " + strings.Join(names, ", ")
}

// savePrompt is the question asked before unsaved settings would be left
// behind. Yes saves and then runs after; no is what happens to the changes
// otherwise.
func (m Model) savePrompt(tail string, after afterSave, no confirmAction) confirmOverlay {
	n := m.settings.dirtyCount()

	yes := func(m Model) (Model, tea.Cmd) { return m.startSave(after) }

	o := newConfirm(countQuestion("Save", n, "change", tail), yes, no)
	o.secondary = m.settings.dirtyTabList()

	return o
}

// promptSaveBeforeLeaving is the ladder's "prompt to save" rung. Yes saves and
// leaves once the save succeeds; no discards the changes and leaves.
func (m Model) promptSaveBeforeLeaving() Model {
	return m.push(m.savePrompt("before leaving?", afterLeave, func(m Model) (Model, tea.Cmd) {
		m.settings = m.settings.revert()

		return m.switchWindow(windowTorrents), nil
	}))
}

// quitHandler is q. With unsaved settings — from any window, since leaving the
// settings window does not save them — it asks first, and no quits without
// saving. It never discards edits silently.
//
// ctrl+c shares the action but never reaches here: the router quits on it before
// the registry is consulted, so it can never be trapped behind this prompt.
func quitHandler(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) {
	m.settings = m.settings.closeDropdown()

	if m.settings.dirtyCount() == 0 {
		return m, tea.Quit
	}

	return m.push(m.savePrompt("before quitting?", afterQuit, func(m Model) (Model, tea.Cmd) {
		return m, tea.Quit
	})), nil
}

// plural returns noun, or noun with an "s", for a count.
func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}

	return noun + "s"
}
