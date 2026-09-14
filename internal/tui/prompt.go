package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// promptOverlayKind names the prompt overlay.
const promptOverlayKind = "prompt"

// promptMaxWidth caps the prompt box, as confirmMaxWidth caps the confirm.
const promptMaxWidth = 60

// promptSubmit runs on enter with the input's text. It either closes the prompt
// and acts, or rejects the value with rejectPrompt and leaves the prompt open.
type promptSubmit func(m Model, value string) (Model, tea.Cmd)

// promptOverlay is a one-line question: L's labels, M's location, and rename's
// new name. The prompt's title states the count, so answering it is the
// confirmation — a second "are you sure?" would ask the same question twice.
//
// It owns the keyboard while it is open. Its draft is the overlay, so one esc
// closes it through the ladder's "close overlay" rung, and pop releases the
// input with it.
type promptOverlay struct {
	title, hint string

	// err is the reason the last submitted value was refused.
	err string

	input  textinput.Model
	submit promptSubmit
}

// newPrompt builds a prompt. prefill must already be safe to draw: every caller
// passes server-derived text through output.SanitizeTerminal first.
func newPrompt(st Styles, title, hint, prefill string, submit promptSubmit) promptOverlay {
	input := textinput.New()
	input.Prompt = "> "
	input.KeyMap = textInputKeyMap()
	input.SetStyles(st.Input)
	input.SetValue(prefill)
	input.CursorEnd()

	return promptOverlay{title: title, hint: hint, input: input, submit: submit}
}

// kind implements overlay.
func (promptOverlay) kind() string { return promptOverlayKind }

// contexts implements overlay. It is the text-input stack: the prompt's input
// owns the keyboard, and only enter and esc escape it.
func (promptOverlay) contexts() []Context { return []Context{ctxTextInput} }

// ownsInput names the input a prompt holds, so pop releases it.
func (promptOverlay) ownsInput() inputOwner { return ownerPrompt }

// helpBindings are the prompt's keys as it words them: esc cancels here, where
// the text-input context calls it blur.
func (promptOverlay) helpBindings() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "apply")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
	}
}

// view implements overlay.
func (o promptOverlay) view(m Model, w, _ int) string {
	width := min(overlayContentWidth(w), promptMaxWidth)

	lines := []string{m.styles.Title.Render(truncate(o.title, width)), "", o.input.View(), ""}

	if o.err != "" {
		lines = append(lines, m.styles.FieldError.Render(truncate("error: "+output.SanitizeTerminal(o.err), width)))
	} else {
		lines = append(lines, m.styles.Muted.Render(truncate(o.hint, width)))
	}

	lines = append(lines, ansi.Truncate(m.help.ShortHelpView(o.helpBindings()), width, "…"))

	return m.styles.Overlay.Render(strings.Join(lines, "\n"))
}

// openPrompt pushes a prompt and hands it the keyboard.
func (m Model) openPrompt(o promptOverlay) (Model, tea.Cmd) {
	width := min(overlayContentWidth(m.width), promptMaxWidth)

	// textinput draws one cell past its width for the cursor at the end.
	o.input.SetWidth(max(1, width-lipgloss.Width(o.input.Prompt)-1))

	cmd := o.input.Focus()

	m = m.push(o)
	m.inputFocused, m.owner = true, ownerPrompt

	return m, cmd
}

// promptForward hands a message to the prompt's input. The input lives in the
// overlay stack, so the updated copy is written back.
func (m Model) promptForward(msg tea.Msg) (Model, tea.Cmd) {
	o, ok := topAs[promptOverlay](m)
	if !ok {
		return m, nil
	}

	input, cmd := o.input.Update(msg)
	o.input = input

	return m.replaceTopOverlay(o), cmd
}

// promptSubmit is enter in a prompt.
func (m Model) promptSubmit() (Model, tea.Cmd) {
	o, ok := topAs[promptOverlay](m)
	if !ok {
		return m, nil
	}

	return o.submit(m, o.input.Value())
}

// rejectPrompt leaves the prompt open with the reason its value was refused.
func (m Model) rejectPrompt(err error) Model {
	o, ok := topAs[promptOverlay](m)
	if !ok {
		return m
	}

	o.err = err.Error()

	return m.replaceTopOverlay(o)
}

// editLabels is L: replace the labels on the selection. The prompt is prefilled
// with the set every selected torrent shares, so editing one torrent's labels
// starts from what it has; a selection whose sets differ starts empty rather
// than suggesting one of them applies to all.
func (m Model) editLabels() (Model, tea.Cmd) {
	mu, why, ok := m.selectionMutation(mutateLabels)
	if !ok {
		m.toast = why

		return m, nil
	}

	title := "Set labels on " + pluralTorrents(len(mu.sel.IDs))
	prefill := sharedLabels(m.torrents.all, mu.sel.IDs)

	return m.openPrompt(newPrompt(m.styles, title, "comma-separated; empty clears", prefill,
		func(m Model, value string) (Model, tea.Cmd) {
			labels, err := parseLabelList(value)
			if err != nil {
				return m.rejectPrompt(err), nil
			}

			mu.run = func(ctx context.Context, c *transmission.Client, sel transmission.Selector) error {
				return c.SetTorrents(ctx, sel, map[string]any{"labels": labels})
			}

			return m.pop().runMutation(mu)
		}))
}

// sharedLabels is the label set every torrent named by ids carries, sanitized
// and joined for the prompt, or empty when their sets differ.
func sharedLabels(all []transmission.Torrent, ids []int64) string {
	var shared []string

	for i, id := range ids {
		t, ok := torrentByID(all, id)
		if !ok {
			return ""
		}

		set := normalizedLabels(t.Labels)

		if i > 0 && !slices.Equal(shared, set) {
			return ""
		}

		shared = set
	}

	parts := make([]string, len(shared))
	for i, l := range shared {
		parts[i] = output.SanitizeTerminal(l)
	}

	return strings.Join(parts, ", ")
}

// parseLabelList parses the labels prompt: comma-separated, each label trimmed,
// de-duplicated, and sorted — the canonical set every `torrent label` verb
// writes. An empty prompt clears the labels; an empty item between commas is
// refused, as `torrent label --label ""` is.
func parseLabelList(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{}, nil
	}

	var labels []string

	for _, item := range strings.Split(raw, ",") {
		label := strings.TrimSpace(item)
		if label == "" {
			return nil, errors.New("a label cannot be empty")
		}

		labels = append(labels, label)
	}

	return normalizedLabels(labels), nil
}

// normalizedLabels is a label set de-duplicated and sorted, as a fresh slice.
func normalizedLabels(labels []string) []string {
	set := slices.Clone(labels)
	slices.Sort(set)

	return slices.Compact(set)
}

// moveSelection is M: set the selection's download location, relocating the
// files, which is `torrent move` without --in-place. The prompt starts from the
// cursor torrent's current location.
func (m Model) moveSelection() (Model, tea.Cmd) {
	mu, why, ok := m.selectionMutation(mutateMove)
	if !ok {
		m.toast = why

		return m, nil
	}

	prefill := ""
	if row, ok := m.torrents.sel.row(m.torrents.rows); ok {
		prefill = output.SanitizeTerminal(row.DownloadDir)
	}

	title := fmt.Sprintf("Move %s to", pluralTorrents(len(mu.sel.IDs)))

	return m.openPrompt(newPrompt(m.styles, title, "a directory on the server; files are moved", prefill,
		func(m Model, value string) (Model, tea.Cmd) {
			location := strings.TrimSpace(value)
			if location == "" {
				return m.rejectPrompt(errors.New("a location is required")), nil
			}

			mu.run = func(ctx context.Context, c *transmission.Client, sel transmission.Selector) error {
				return c.MoveTorrents(ctx, sel, location, true)
			}

			return m.pop().runMutation(mu)
		}))
}

// renameSelection renames one torrent: its top-level path, which is its name.
// Transmission's rename is single-torrent by design, so a wider selection is
// refused rather than narrowed to one of it.
func (m Model) renameSelection() (Model, tea.Cmd) {
	mu, why, ok := m.selectionMutation(mutateRename)
	if !ok {
		m.toast = why

		return m, nil
	}

	if len(mu.sel.IDs) != 1 {
		m.toast = "rename: one torrent at a time"

		return m, nil
	}

	t, ok := torrentByID(m.torrents.all, mu.sel.IDs[0])
	if !ok {
		m.toast = "nothing selected"

		return m, nil
	}

	current := output.SanitizeTerminal(t.Name)

	return m.openPrompt(newPrompt(m.styles, "Rename torrent", "the torrent's name on the server", current,
		func(m Model, value string) (Model, tea.Cmd) {
			name := strings.TrimSpace(value)

			switch name {
			case "":
				return m.rejectPrompt(errors.New("a name is required")), nil
			case current:
				return m.rejectPrompt(errors.New("the name is unchanged")), nil
			}

			mu.run = func(ctx context.Context, c *transmission.Client, sel transmission.Selector) error {
				_, err := c.RenamePath(ctx, sel, t.Name, name)

				return err
			}

			return m.pop().runMutation(mu)
		}))
}

// torrentByID finds a torrent in a set.
func torrentByID(all []transmission.Torrent, id int64) (transmission.Torrent, bool) {
	for _, t := range all {
		if t.ID == id {
			return t, true
		}
	}

	return transmission.Torrent{}, false
}

// promptHandlers are L, M, and the menu-only rename.
func promptHandlers() map[Action]handler {
	return map[Action]handler{
		actionTorrentLabels: func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.editLabels() },
		actionTorrentMove:   func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.moveSelection() },
		actionTorrentRename: func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.renameSelection() },
	}
}
