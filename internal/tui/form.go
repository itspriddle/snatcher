package tui

import (
	"slices"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/itspriddle/snatcher/internal/output"
)

// formOverlayKind names the form overlay.
const formOverlayKind = "form"

// formMaxWidth caps the form box.
const formMaxWidth = 64

// formRow is one row of a form: a text field, a toggle, or a button. It reuses
// the settings window's widget kinds and spellings ("[x]", "[ Add ]") and its
// rule that enter edits and blurring discards the draft.
type formRow struct {
	label string
	kind  fieldKind
	text  string
	on    bool
}

// formSubmit runs when the form's button is pressed. It either closes the form
// and acts, or rejects the form with rejectForm and leaves it open.
type formSubmit func(m Model, o formOverlay) (Model, tea.Cmd)

// formOverlay is a small modal form: A's add-with-options. It is deliberately
// not the settings window. Those rows carry session and client sourcing, dirty
// tracking, registry definitions, read-only notes, and per-tab cursors, none of
// which a five-row modal needs.
//
// A row being edited sits above its form: esc blurs the row, discarding the
// draft, and the next esc closes the form.
type formOverlay struct {
	title string
	rows  []formRow

	// cursor indexes rows.
	cursor int

	// input edits the cursor row while editing is set.
	input   textinput.Model
	editing bool

	// hint is the line under the rows, and err replaces it when the last
	// submit was refused.
	hint, err string

	// defaultDir is the download directory the form was prefilled with.
	defaultDir string

	submit formSubmit
}

// The add form's rows, in order.
const (
	addRowSource = iota
	addRowDir
	addRowLabels
	addRowStart
	addRowAdd
)

// newAddForm builds A's form. downloadDir is server-derived, so it is sanitized
// before it is drawn or edited.
func newAddForm(st Styles, downloadDir string) formOverlay {
	dir := output.SanitizeTerminal(downloadDir)

	input := textinput.New()
	input.Prompt = ""
	input.KeyMap = textInputKeyMap()
	input.SetStyles(st.Input)

	rows := make([]formRow, addRowAdd+1)
	rows[addRowSource] = formRow{label: "Source", kind: fieldText}
	rows[addRowDir] = formRow{label: "Download dir", kind: fieldText, text: dir}
	rows[addRowLabels] = formRow{label: "Labels", kind: fieldText}
	rows[addRowStart] = formRow{label: "Start now", kind: fieldToggle}
	rows[addRowAdd] = formRow{label: "Add", kind: fieldButton}

	return formOverlay{
		title:      "Add torrent",
		rows:       rows,
		input:      input,
		hint:       "file path, URL, or magnet; labels comma-separated",
		defaultDir: dir,
		submit:     addFormSubmit,
	}
}

// kind implements overlay.
func (formOverlay) kind() string { return formOverlayKind }

// contexts implements overlay. While a row is being edited the router gives the
// input the keyboard before it consults this stack.
func (formOverlay) contexts() []Context { return []Context{ctxForm} }

// ownsInput names the input a form holds, so pop releases it.
func (formOverlay) ownsInput() inputOwner { return ownerForm }

// move steps the cursor, wrapping, as tab does through the settings rows.
func (o formOverlay) move(delta int) formOverlay {
	o.cursor = (o.cursor + delta + len(o.rows)) % len(o.rows)

	return o
}

// toggle flips the cursor row when it is a toggle.
func (o formOverlay) toggle() formOverlay {
	if o.rows[o.cursor].kind != fieldToggle {
		return o
	}

	o.rows = slices.Clone(o.rows)
	o.rows[o.cursor].on = !o.rows[o.cursor].on

	return o
}

// labelWidth is the widest row label.
func (o formOverlay) labelWidth() int {
	w := 0

	for _, r := range o.rows {
		if r.kind != fieldButton {
			w = max(w, lipgloss.Width(r.label))
		}
	}

	return w
}

// inputWidth is the text the input shows in a frame w cells wide: the box less
// the cursor marker, the label column and its gap, and the cell textinput draws
// past its width for the cursor.
func (o formOverlay) inputWidth(w int) int {
	return max(1, min(overlayContentWidth(w), formMaxWidth)-fieldMarkerWidth-o.labelWidth()-fieldGap-1)
}

// view implements overlay.
func (o formOverlay) view(m Model, w, h int) string {
	width := min(overlayContentWidth(w), formMaxWidth)
	labelW := o.labelWidth()

	rows := make([]string, 0, len(o.rows))
	for i, r := range o.rows {
		rows = append(rows, o.rowView(m.styles, i, r, labelW, width))
	}

	footer := m.styles.Muted.Render(truncate(o.hint, width))
	if o.err != "" {
		footer = m.styles.FieldError.Render(truncate("error: "+output.SanitizeTerminal(o.err), width))
	}

	ctx := ctxForm
	if o.editing {
		ctx = ctxTextInput
	}

	return m.modalBox(modal{title: o.title, rows: rows, focus: o.cursor, footer: []string{footer}, ctx: ctx, width: width}, h)
}

// rowView draws one row. The row being edited shows the input in place of its
// value, as a settings row does.
func (o formOverlay) rowView(st Styles, i int, r formRow, labelW, width int) string {
	cursor := i == o.cursor
	head := padLine(r.label, labelW) + "  "

	switch r.kind {
	case fieldText:
		if cursor && o.editing {
			return st.FieldCursor.Render("> "+head) + o.input.View()
		}

		return cursorRow(st, cursor, head+r.text, width)
	case fieldToggle:
		box := "[ ]"
		if r.on {
			box = "[x]"
		}

		return cursorRow(st, cursor, head+box, width)
	case fieldButton:
		if cursor {
			return cursorRow(st, cursor, "[>"+r.label+"<]", width)
		}

		return cursorRow(st, cursor, "[ "+r.label+" ]", width)
	case fieldSelect:
	}

	return cursorRow(st, cursor, head, width)
}

// formEnter is enter on a form row: edit a text row, flip a toggle, or press the
// button.
func (m Model) formEnter() (Model, tea.Cmd) {
	o, ok := topAs[formOverlay](m)
	if !ok {
		return m, nil
	}

	switch o.rows[o.cursor].kind {
	case fieldText:
		o.input.SetValue(o.rows[o.cursor].text)
		o.input.CursorEnd()
		o.input.SetWidth(o.inputWidth(m.width))

		cmd := o.input.Focus()
		o.editing, o.err = true, ""

		m = m.replaceTopOverlay(o)
		m.inputFocused, m.owner = true, ownerForm

		return m, cmd
	case fieldToggle:
		return m.replaceTopOverlay(o.toggle()), nil
	case fieldButton:
		return o.submit(m, o)
	case fieldSelect:
	}

	return m, nil
}

// formSubmitInput is enter in a form row's input: commit the draft to the row
// and give the keyboard back to the form.
func (m Model) formSubmitInput() Model {
	o, ok := topAs[formOverlay](m)
	if ok {
		o.rows = slices.Clone(o.rows)
		o.rows[o.cursor].text = o.input.Value()
		o.input.Blur()
		o.editing = false

		m = m.replaceTopOverlay(o)
	}

	m.inputFocused, m.owner = false, ownerNone

	return m
}

// formBlur is esc in a form row's input: discard the draft. The row keeps what
// it had, and blurInput releases the keyboard.
func (m Model) formBlur() Model {
	o, ok := topAs[formOverlay](m)
	if !ok {
		return m
	}

	o.input.Blur()
	o.input.SetValue("")
	o.editing = false

	return m.replaceTopOverlay(o)
}

// formForward hands a message to the row being edited. The input lives in the
// overlay stack, so the updated copy is written back.
func (m Model) formForward(msg tea.Msg) (Model, tea.Cmd) {
	o, ok := topAs[formOverlay](m)
	if !ok {
		return m, nil
	}

	input, cmd := o.input.Update(msg)
	o.input = input

	return m.replaceTopOverlay(o), cmd
}

// rejectForm leaves the form open with the reason its submit was refused.
func (m Model) rejectForm(err error) Model {
	o, ok := topAs[formOverlay](m)
	if !ok {
		return m
	}

	o.err = err.Error()

	return m.replaceTopOverlay(o)
}
