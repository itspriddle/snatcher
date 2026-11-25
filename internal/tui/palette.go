package tui

import (
	"cmp"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// paletteOverlayKind names the command palette, and the reset chooser that
// reuses it.
const paletteOverlayKind = "palette"

// paletteMaxWidth caps the palette box: a label, its note, and its key.
const paletteMaxWidth = 72

// paletteNoteMiss is what a match on an entry's note costs against a match on
// its label, so a note is searchable without out-ranking a label hit.
const paletteNoteMiss = -50

// paletteEntry is one row of the palette: a name to match and run.
type paletteEntry struct {
	// label is what is matched and drawn; note is drawn muted beside it — the
	// context an action belongs to, or a setting's description — and matched
	// only when the label misses; keys is the key that runs it, if any.
	label, note, keys string

	// action is the action the entry dispatches, or empty for a chooser row.
	// The drift tests and the de-duplication read it.
	action Action

	// run is what enter on the entry does.
	run handler
}

// paletteOverlay is `:`: a fuzzy list of what the TUI can do from where the user
// is, over the same selection the keys act on.
//
// It holds a text input that owns the keyboard, as a prompt does, but its stack
// puts ctxPalette above ctxTextInput, so the arrow and page keys move the cursor
// while every other key — j, k, q, even : — types.
type paletteOverlay struct {
	title string

	// entries are every row, in base order: stack order, then registry order
	// within a context.
	entries []paletteEntry

	input textinput.Model

	// matches indexes entries in display order; cursor indexes matches.
	matches []int
	cursor  int
}

// newPalette builds a palette over entries with an empty query.
func newPalette(st Styles, title string, entries []paletteEntry) paletteOverlay {
	input := textinput.New()
	input.Prompt = ": "
	input.KeyMap = textInputKeyMap()
	input.SetStyles(st.Input)

	return paletteOverlay{title: title, entries: entries, input: input}.filter()
}

// kind implements overlay.
func (paletteOverlay) kind() string { return paletteOverlayKind }

// contexts implements overlay: the palette's cursor keys above the input's two
// escapes. A key neither binds is forwarded to the input.
func (paletteOverlay) contexts() []Context { return []Context{ctxPalette, ctxTextInput} }

// ownsInput names the input a palette holds, so pop releases it.
func (paletteOverlay) ownsInput() inputOwner { return ownerPalette }

// helpBindings are the palette's keys as it words them: esc cancels here, where
// the text-input context calls it blur.
func (paletteOverlay) helpBindings() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "move")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "run")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
	}
}

// filter rescores every entry against the query and puts the cursor back on the
// best match. Among equal scores a shorter label ranks first — it is the closer
// fit, so "speed-limit-down" finds itself before "speed-limit-down-enabled" —
// and labels of one length keep their base order. An empty query scores every
// entry zero and leaves the base order alone.
func (o paletteOverlay) filter() paletteOverlay {
	query := o.input.Value()

	type scored struct{ index, score int }

	hits := make([]scored, 0, len(o.entries))

	for i, e := range o.entries {
		if score, ok := fuzzyScore(query, e.label); ok {
			hits = append(hits, scored{i, score})

			continue
		}

		if score, ok := fuzzyScore(query, e.note); ok && e.note != "" {
			hits = append(hits, scored{i, score + paletteNoteMiss})
		}
	}

	slices.SortStableFunc(hits, func(a, b scored) int {
		if query == "" {
			return 0
		}

		return cmp.Or(
			cmp.Compare(b.score, a.score),
			cmp.Compare(lipgloss.Width(o.entries[a.index].label), lipgloss.Width(o.entries[b.index].label)),
		)
	})

	o.matches = make([]int, len(hits))
	for i, h := range hits {
		o.matches[i] = h.index
	}

	o.cursor = 0

	return o
}

// move steps the cursor, stopping at the ends as the picker does.
func (o paletteOverlay) move(delta int) paletteOverlay {
	o.cursor = clamp(o.cursor+delta, 0, max(0, len(o.matches)-1))

	return o
}

// current is the entry under the cursor, and whether there is one.
func (o paletteOverlay) current() (paletteEntry, bool) {
	if o.cursor < 0 || o.cursor >= len(o.matches) {
		return paletteEntry{}, false
	}

	return o.entries[o.matches[o.cursor]], true
}

// paletteHead is the rows the palette draws above its list: the input line.
const paletteHead = 1

// paletteBudget is how many rows of the list a terminal of h rows shows, which
// is also how far a page key moves.
func paletteBudget(h int) int {
	return max(1, overlayContentHeight(h)-4-paletteHead)
}

// view implements overlay.
func (o paletteOverlay) view(m Model, w, h int) string {
	return m.modalBox(o.modal(m, w, h), h)
}

// modal implements modalOverlay.
func (o paletteOverlay) modal(m Model, w, _ int) modal {
	width := min(overlayContentWidth(w), paletteMaxWidth)

	labelW, keyW := 0, 0

	for _, i := range o.matches {
		labelW = max(labelW, lipgloss.Width(o.entries[i].label))
		keyW = max(keyW, lipgloss.Width(o.entries[i].keys))
	}

	rows := make([]string, 0, max(1, len(o.matches)))

	for n, i := range o.matches {
		rows = append(rows, paletteRow(m.styles, o.entries[i], n == o.cursor, labelW, keyW, width))
	}

	if len(rows) == 0 {
		rows = append(rows, m.styles.Muted.Render(padLine("  no matches", width)))
	}

	return modal{
		title: o.title,
		head:  []string{o.input.View()},
		rows:  rows,
		focus: o.cursor,
		ctx:   ctxPalette,
		help:  o.helpBindings(),
		width: width,
	}
}

// pointAt implements modalOverlay. The "no matches" line is not an entry.
func (o paletteOverlay) pointAt(i int) (overlay, bool) {
	if i < 0 || i >= len(o.matches) {
		return o, false
	}

	o.cursor = i

	return o, true
}

// clickAction implements modalOverlay: a click runs the entry, as enter does.
func (paletteOverlay) clickAction() Action { return actionInputSubmit }

// wheelActions implements modalOverlay.
func (paletteOverlay) wheelActions() (up, down Action) { return actionPaletteUp, actionPaletteDown }

// paletteRow draws one entry: the label, the note, and the key right-aligned.
// The note gives way first when the row is narrow, then the label. The cursor
// row is drawn plain under the cursor style: a muted note nested inside reverse
// video reads badly without color.
func paletteRow(st Styles, e paletteEntry, on bool, labelW, keyW, width int) string {
	keys := ""
	if keyW > 0 {
		keys = "  " + strings.Repeat(" ", keyW-lipgloss.Width(e.keys)) + e.keys
	}

	// The cursor marker takes two cells.
	leftW := max(1, width-2-lipgloss.Width(keys))
	lw := min(labelW, leftW)
	label := padLine(e.label, lw)

	noteW := leftW - lw - 2
	if noteW <= 0 {
		return cursorRow(st, on, label+strings.Repeat(" ", leftW-lw)+keys, width)
	}

	note := padLine(e.note, noteW)
	if on {
		return cursorRow(st, true, label+"  "+note+keys, width)
	}

	return cursorRow(st, false, label+"  "+st.Muted.Render(note)+keys, width)
}

// openPalette is `:`. From help it closes help first, so the palette describes
// the pane help was describing.
func (m Model) openPalette() (Model, tea.Cmd) {
	if top, ok := m.topOverlay(); ok && top.kind() == helpOverlayKind {
		m = m.pop()
	}

	if m.hasOverlay(paletteOverlayKind) {
		return m, nil
	}

	return m.pushPalette(newPalette(m.styles, "Command palette", paletteEntries(m.paneStack())))
}

// pushPalette pushes a palette and hands it the keyboard.
func (m Model) pushPalette(o paletteOverlay) (Model, tea.Cmd) {
	width := min(overlayContentWidth(m.width), paletteMaxWidth)

	// textinput draws one cell past its width for the cursor at the end.
	o.input.SetWidth(max(1, width-lipgloss.Width(o.input.Prompt)-1))

	cmd := o.input.Focus()

	m = m.push(o)
	m.inputFocused, m.owner = true, ownerPalette

	return m, cmd
}

// paletteForward hands a message to the palette's input, refiltering when the
// query changed.
func (m Model) paletteForward(msg tea.Msg) (Model, tea.Cmd) {
	o, ok := topAs[paletteOverlay](m)
	if !ok {
		return m, nil
	}

	before := o.input.Value()

	input, cmd := o.input.Update(msg)
	o.input = input

	if o.input.Value() != before {
		o = o.filter()
	}

	return m.replaceTopOverlay(o), cmd
}

// paletteSubmit is enter in the palette: run the entry under the cursor. Every
// entry's run closes the palette itself, so an entry that opens an overlay of
// its own opens it over the pane rather than over the palette.
func (m Model) paletteSubmit(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	o, ok := topAs[paletteOverlay](m)
	if !ok {
		return m, nil
	}

	e, ok := o.current()
	if !ok {
		return m, nil
	}

	return e.run(m, msg)
}

// paletteContexts is a pane stack with the chord context the palette lists
// inserted after the context whose key starts the chord.
func paletteContexts(stack []Context) []Context {
	out := make([]Context, 0, len(stack)+1)

	for _, ctx := range stack {
		out = append(out, ctx)

		if ctx == paletteChord.after {
			out = append(out, paletteChord.ctx)
		}
	}

	return out
}

// paletteEntries is every action live in a pane stack — the one beneath the
// palette — most specific context first: each context's bound actions in registry order,
// less the hidden ones, then its unbound actions. Entries are computed once, at
// open: the stack cannot change while the palette is modal, and every handler
// re-checks its own guards when it runs.
func paletteEntries(stack []Context) []paletteEntry {
	var out []paletteEntry

	seen := map[Action]bool{}
	add := func(e paletteEntry) {
		if !seen[e.action] {
			seen[e.action] = true
			out = append(out, e)
		}
	}

	for _, ctx := range paletteContexts(stack) {
		labelPrefix, keyPrefix := chordPrefixes(ctx)

		for _, b := range registry {
			if b.Context != ctx || paletteHidden[b.Action] {
				continue
			}

			h := b.Key.Help()
			add(actionEntry(b.Action, labelPrefix+h.Desc, ctx.String(), keyPrefix+h.Key))
		}

		for _, u := range unboundActions {
			if u.ctx == ctx {
				add(actionEntry(u.action, u.label, ctx.String(), ""))
			}
		}
	}

	return out
}

// chordPrefixes are the label and key prefixes a chord context's entries carry.
func chordPrefixes(ctx Context) (label, keys string) {
	if ctx == paletteChord.ctx {
		return paletteChord.labelPrefix, paletteChord.keyPrefix
	}

	return "", ""
}

// actionEntry is an entry that closes the palette and runs an action, exactly
// as its key or its menu leaf would.
func actionEntry(a Action, label, note, keys string) paletteEntry {
	return paletteEntry{
		label: label, note: note, keys: keys, action: a,
		run: func(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) { return m.pop().dispatch(a, msg) },
	}
}

// paletteHandlers are `:` and the palette's cursor keys.
func paletteHandlers() map[Action]handler {
	step := func(delta func(Model) int) handler {
		return func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) {
			o, ok := topAs[paletteOverlay](m)
			if !ok {
				return m, nil
			}

			return m.replaceTopOverlay(o.move(delta(m))), nil
		}
	}

	return map[Action]handler{
		actionPalette:         func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.openPalette() },
		actionPaletteUp:       step(func(Model) int { return -1 }),
		actionPaletteDown:     step(func(Model) int { return 1 }),
		actionPalettePageUp:   step(func(m Model) int { return -paletteBudget(m.height) }),
		actionPalettePageDown: step(func(m Model) int { return paletteBudget(m.height) }),
	}
}
