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
// half-page and horizontal fields stay unbound: ctxHelp registers neither, and a
// component may not respond to a key the registry has not seen.
func helpViewportKeyMap() viewport.KeyMap {
	return viewport.KeyMap{
		Up:       bindingFor(ctxHelp, actionHelpScrollUp),
		Down:     bindingFor(ctxHelp, actionHelpScrollDown),
		PageUp:   bindingFor(ctxHelp, actionHelpPageUp),
		PageDown: bindingFor(ctxHelp, actionHelpPageDown),
	}
}

// kind implements overlay.
func (helpOverlay) kind() string { return helpOverlayKind }

// contexts implements overlay. The help overlay adds its own scroll keys and
// keeps the global ones, so `?` closes it again and `q` still quits.
func (helpOverlay) contexts() []Context { return []Context{ctxHelp, ctxGlobal} }

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
	height := max(1, min(h-overlayMargin*2, len(o.lines(m, width))))

	o.vp.SetWidth(width)
	o.vp.SetHeight(height)
	o.vp.SetContent(strings.Join(o.lines(m, width), "\n"))

	return o
}

// lines is the overlay's content: one titled block per live context, listing
// every key that context binds.
//
// The blocks are stacked rather than laid out as side-by-side columns, and the
// box scrolls. bubbles/help's FullHelpView does render columns, but it fits them
// by truncating — at 80 columns it silently drops two of the four contexts,
// leaving a help view that does not mention `q`. A stacked list never lies about
// what is bound; that is what the scroll keys are for.
func (o helpOverlay) lines(m Model, width int) []string {
	var out []string

	for _, ctx := range m.stackBeneathHelp() {
		bindings := contextBindings(ctx)
		if len(bindings) == 0 {
			continue
		}

		if len(out) > 0 {
			out = append(out, "")
		}

		out = append(out, m.styles.Title.Render(truncate(ctx.String(), width)))

		keyWidth := 0
		for _, b := range bindings {
			keyWidth = max(keyWidth, lipgloss.Width(b.Help().Key))
		}

		for _, b := range bindings {
			help := b.Help()
			line := fmt.Sprintf("  %-*s  %s", keyWidth, help.Key, m.styles.Muted.Render(help.Desc))
			out = append(out, truncate(line, width))
		}
	}

	return out
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

	return paneContexts(m.focusedPane())
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
