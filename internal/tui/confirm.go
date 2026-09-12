package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// confirmOverlayKind names the confirm overlay.
const confirmOverlayKind = "confirm"

// confirmMaxWidth caps the confirm box, so a question on a wide terminal reads as
// a dialog rather than a banner.
const confirmMaxWidth = 60

// confirmAction runs after the confirm overlay has been popped. It is a
// transition over the whole model, because what a confirmation leads to — a
// save, a quit, a window switch — is rarely confined to the window that asked.
type confirmAction func(Model) (Model, tea.Cmd)

// confirmOverlay is a yes/no question. It is the shared dialog every
// "confirmation scales with blast radius" prompt uses: the settings save
// prompts first, the torrent deletes in T5.
//
// It is modal. Its context stack is ctxConfirm alone, with no ctxGlobal
// underneath, so q, ?, and the window keys do nothing while a question is
// open — a stray q must not quit past a "save before quitting?" prompt. esc
// closes it through the ladder's "close overlay" rung, and ctrl+c still quits.
type confirmOverlay struct {
	// question is the prompt, stating the count when there is one.
	question string

	// secondary is an optional muted line of detail under the question.
	secondary string

	// yes and no run on the matching answer. A nil action just closes.
	yes, no confirmAction
}

// newConfirm builds a confirm overlay.
func newConfirm(question string, yes, no confirmAction) confirmOverlay {
	return confirmOverlay{question: question, yes: yes, no: no}
}

// countQuestion phrases a counted question: countQuestion("Save", 3, "change",
// "before leaving?") is "Save 3 changes before leaving?".
func countQuestion(verb string, n int, noun, tail string) string {
	return fmt.Sprintf("%s %d %s %s", verb, n, plural(n, noun), tail)
}

// kind implements overlay.
func (confirmOverlay) kind() string { return confirmOverlayKind }

// contexts implements overlay.
func (confirmOverlay) contexts() []Context { return []Context{ctxConfirm} }

// view implements overlay.
func (o confirmOverlay) view(m Model, w, _ int) string {
	width := min(overlayContentWidth(w), confirmMaxWidth)

	lines := []string{m.styles.Title.Render(truncate(o.question, width))}

	if o.secondary != "" {
		lines = append(lines, m.styles.Muted.Render(truncate(o.secondary, width)))
	}

	lines = append(lines, "", m.help.ShortHelpView(contextBindings(ctxConfirm)))

	return m.styles.Overlay.Render(strings.Join(lines, "\n"))
}

// confirmHandler answers the topmost confirm overlay: pop it, then run the
// chosen action. The overlay is popped first so the action sees the model the
// user will be left in, and can push an overlay of its own.
func confirmHandler(pick func(confirmOverlay) confirmAction) handler {
	return func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) {
		top, ok := m.topOverlay()
		if !ok {
			return m, nil
		}

		o, ok := top.(confirmOverlay)
		if !ok {
			return m, nil
		}

		m = m.pop()

		if action := pick(o); action != nil {
			return action(m)
		}

		return m, nil
	}
}

// confirmHandlers are the confirm overlay's answers.
func confirmHandlers() map[Action]handler {
	return map[Action]handler{
		actionConfirmYes: confirmHandler(func(o confirmOverlay) confirmAction { return o.yes }),
		actionConfirmNo:  confirmHandler(func(o confirmOverlay) confirmAction { return o.no }),
	}
}
