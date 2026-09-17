package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// helpOverlayKind names the help overlay, so toggleHelp can ask whether it is
// already open without a type assertion.
const helpOverlayKind = "help"

// overlayMargin is how many cells of the frame stay visible around an overlay on
// each side, so an overlay reads as floating over the UI rather than replacing
// it.
const overlayMargin = 4

// helpOverlay is the `?` view: every key live in the current context stack, one
// column per context, in a scrollable box.
//
// It is built from the registry and the same stack the router resolves against,
// so it lists what actually works right now rather than a hand-maintained cheat
// sheet. The filter pane's help says "apply filter", not "start", because the
// filter pane's stack is what it was rendered from.
type helpOverlay struct {
	// vp scrolls the columns. On a short terminal the full keymap does not fit,
	// and a help view that silently cut off its last six keys would be worse
	// than no help at all.
	vp viewport.Model
}

// newHelpOverlay returns the overlay with the registry's help-scroll keys.
//
// viewport.New installs DefaultKeyMap and offers no option to override it, so
// the keymap is assigned afterwards. The scroll keys come from ctxHelp rather
// than ctxNav: the overlay's stack is its own, and reading them from the
// context that is actually live keeps the component keymap test meaningful.
func newHelpOverlay() helpOverlay {
	vp := viewport.New()
	vp.KeyMap = helpViewportKeyMap()

	return helpOverlay{vp: vp}
}

// helpViewportKeyMap builds the help viewport's keymap from the registry. The
// horizontal fields stay unbound: ctxHelp registers none, and a component may
// not respond to a key the registry has not seen.
func helpViewportKeyMap() viewport.KeyMap {
	return viewport.KeyMap{
		Up:           bindingFor(ctxHelp, actionHelpScrollUp),
		Down:         bindingFor(ctxHelp, actionHelpScrollDown),
		PageUp:       bindingFor(ctxHelp, actionHelpPageUp),
		PageDown:     bindingFor(ctxHelp, actionHelpPageDown),
		HalfPageUp:   bindingFor(ctxHelp, actionHelpHalfPageUp),
		HalfPageDown: bindingFor(ctxHelp, actionHelpHalfPageDown),
	}
}

// kind implements overlay.
func (helpOverlay) kind() string { return helpOverlayKind }

// contexts implements overlay. The help overlay adds its own scroll keys and
// keeps the global ones, so `?` closes it again and `q` still quits.
func (helpOverlay) contexts() []Context { return []Context{ctxHelp, ctxGlobal} }

// helpBindings words the short-help line under the overlay. Its six scroll keys
// listed one by one would crowd `? help` and `q quit` off the line, so each
// up/down pair shares one entry.
func (helpOverlay) helpBindings() []key.Binding {
	pair := func(up, down Action, keys, desc string) key.Binding {
		both := append(bindingFor(ctxHelp, up).Keys(), bindingFor(ctxHelp, down).Keys()...)

		return key.NewBinding(key.WithKeys(both...), key.WithHelp(keys, desc))
	}

	return []key.Binding{
		pair(actionHelpScrollUp, actionHelpScrollDown, "↑↓/jk", "scroll"),
		pair(actionHelpHalfPageUp, actionHelpHalfPageDown, "ctrl+u/d", "½ page"),
		pair(actionHelpPageUp, actionHelpPageDown, "pgup/pgdn", "page"),
	}
}

// view implements overlay.
func (o helpOverlay) view(m Model, w, h int) string {
	o = o.fit(m, w, h)

	return m.styles.Overlay.Render(o.vp.View())
}

// fit sizes the viewport to the box and fills it with the help for the stack
// beneath the overlay.
//
// Content is rebuilt every frame rather than cached, because the stack beneath
// can change while the overlay is open. Only the scroll offset lives on the
// stored overlay, which is why the scroll handler calls fit before forwarding a
// key: a viewport clamps its offset against content it has been given.
func (o helpOverlay) fit(m Model, w, h int) helpOverlay {
	width := overlayContentWidth(w)
	lines := layoutHelp(m.helpBlocks(), width, m.styles).lines
	height := max(1, min(h-overlayMargin*2, len(lines)))

	o.vp.SetWidth(width)
	o.vp.SetHeight(height)
	o.vp.SetContent(strings.Join(lines, "\n"))

	return o
}

// helpColumnGap is the blank space between two help columns.
const helpColumnGap = 3

// helpBlock is one live context's help: its heading and every key it binds, in
// registry order.
type helpBlock struct {
	title string
	rows  []helpRow
}

// helpRow is one binding as help shows it.
type helpRow struct {
	key, desc string
}

// width is the block's natural width: the indented, key-aligned rows, or the
// title if that is wider.
func (b helpBlock) width() int {
	w := lipgloss.Width(b.title)
	for _, r := range b.rows {
		w = max(w, 2+b.keyWidth()+2+lipgloss.Width(r.desc))
	}

	return w
}

// keyWidth is the widest key in the block, which its rows align on.
func (b helpBlock) keyWidth() int {
	w := 0
	for _, r := range b.rows {
		w = max(w, lipgloss.Width(r.key))
	}

	return w
}

// helpBlocks is the overlay's content as data: one block per live context that
// binds anything.
func (m Model) helpBlocks() []helpBlock {
	var out []helpBlock

	for _, ctx := range m.stackBeneathHelp() {
		bindings := contextBindings(ctx)
		if len(bindings) == 0 {
			continue
		}

		b := helpBlock{title: ctx.String()}
		for _, k := range bindings {
			b.rows = append(b.rows, helpRow{key: k.Help().Key, desc: k.Help().Desc})
		}

		out = append(out, b)
	}

	return out
}

// helpCell is one line of a help column: a block's title, or one of its rows.
// A row of -1 is the title; cont marks a title repeated at the top of a column
// the block continues into.
type helpCell struct {
	block, row int
	cont       bool
}

// helpLayout is the rendered help: its lines, and the column geometry they were
// cut from, which the tests read back to split a line into its columns.
type helpLayout struct {
	lines    []string
	columns  int
	colWidth int
}

// layoutHelp flows the blocks into as many equal-width columns as fit, reading
// top to bottom and then left to right.
//
// bubbles/help's FullHelpView also renders columns, but it fits them by
// truncating — at 80 columns it silently drops two of the four contexts,
// leaving a help view that does not mention `q`. This layout never drops a
// binding: when two columns do not fit it falls back to one, and the box
// scrolls. A block split across columns repeats its title, so a key is never
// shown without the context it belongs to.
func layoutHelp(blocks []helpBlock, width int, styles Styles) helpLayout {
	natural := 0
	for _, b := range blocks {
		natural = max(natural, b.width())
	}

	colWidth := min(natural, width)
	n := max(1, (width+helpColumnGap)/(natural+helpColumnGap))
	cols := flowHelp(blocks, n)

	rendered := make([][]string, len(cols))
	height := 0

	for i, col := range cols {
		for _, c := range col {
			rendered[i] = append(rendered[i], renderHelpCell(blocks, c, colWidth, styles))
		}

		height = max(height, len(col))
	}

	lines := make([]string, height)
	pad := lipgloss.NewStyle().Width(colWidth)
	gap := strings.Repeat(" ", helpColumnGap)

	for row := range height {
		var sb strings.Builder

		for i, col := range rendered {
			cell := ""
			if row < len(col) {
				cell = col[row]
			}

			if i == len(rendered)-1 {
				sb.WriteString(cell)

				continue
			}

			sb.WriteString(pad.Render(cell))
			sb.WriteString(gap)
		}

		lines[row] = strings.TrimRight(sb.String(), " ")
	}

	return helpLayout{lines: lines, columns: len(cols), colWidth: colWidth}
}

// flowHelp splits the blocks into at most n columns, as evenly as it can.
//
// It tries the shortest column height that could hold everything and grows it
// until the flow fits in n columns. Heights start at two, the least a column
// needs for a title and the row beneath it, which is also what guarantees each
// column makes progress. At the height of the whole stack the flow is a single
// column, so the loop always ends.
func flowHelp(blocks []helpBlock, n int) [][]helpCell {
	total := 0
	for i, b := range blocks {
		total += 1 + len(b.rows)
		if i > 0 {
			total++
		}
	}

	for height := max(2, (total+n-1)/n); ; height++ {
		if cols := flowHelpAt(blocks, height); len(cols) <= n {
			return cols
		}
	}
}

// flowHelpAt flows the blocks into columns of at most height lines. A title is
// never left at the foot of a column: a block starts a new column unless its
// separator, title, and first row all fit. A blank separator is only ever
// written between two blocks in the same column, never at the top of one.
func flowHelpAt(blocks []helpBlock, height int) [][]helpCell {
	cols := [][]helpCell{nil}

	for bi, b := range blocks {
		cur := len(cols) - 1

		if len(cols[cur]) > 0 {
			if len(cols[cur])+3 > height {
				cols = append(cols, nil)
				cur++
			} else {
				cols[cur] = append(cols[cur], helpCell{block: -1})
			}
		}

		cols[cur] = append(cols[cur], helpCell{block: bi, row: -1})

		for ri := range b.rows {
			if len(cols[cur]) >= height {
				cols = append(cols, []helpCell{{block: bi, row: -1, cont: true}})
				cur++
			}

			cols[cur] = append(cols[cur], helpCell{block: bi, row: ri})
		}
	}

	return cols
}

// renderHelpCell draws one cell: a blank separator, a title, or a key-aligned
// row, cut to the column width.
func renderHelpCell(blocks []helpBlock, c helpCell, width int, styles Styles) string {
	if c.block < 0 {
		return ""
	}

	b := blocks[c.block]

	if c.row < 0 {
		title := b.title
		if c.cont {
			title += " (cont.)"
		}

		return styles.Title.Render(truncate(title, width))
	}

	r := b.rows[c.row]
	line := fmt.Sprintf("  %-*s  %s", b.keyWidth(), r.key, styles.Muted.Render(r.desc))

	return truncate(line, width)
}

// scrollHelp forwards a scroll key to the help overlay's viewport. It is the
// handler behind every ctxHelp action, and it is a no-op when the topmost
// overlay is not the help view — which cannot happen, since ctxHelp is only ever
// live under this overlay, but a handler that assumed otherwise would panic in a
// user's terminal rather than in a test.
func scrollHelp(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) {
	top, ok := m.topOverlay()
	if !ok {
		return m, nil
	}

	o, ok := top.(helpOverlay)
	if !ok {
		return m, nil
	}

	o = o.fit(m, m.width, m.height)

	vp, cmd := o.vp.Update(msg)
	o.vp = vp

	return m.replaceTopOverlay(o), cmd
}

// stackBeneathHelp is the context stack the help describes: the one that would
// be live if the overlay were not open.
//
// The overlay's own stack would describe the overlay, which is not what a user
// pressing `?` is asking about. Its scroll keys are still reachable; they are
// simply not what the columns are for.
func (m Model) stackBeneathHelp() []Context {
	if m.inputFocused {
		return []Context{ctxTextInput}
	}

	return m.paneStack()
}

// contextBindings returns every binding registered in one context, in registry
// order — which is display order.
func contextBindings(ctx Context) []key.Binding {
	var out []key.Binding

	for _, b := range registry {
		if b.Context == ctx {
			out = append(out, b.Key)
		}
	}

	return out
}
