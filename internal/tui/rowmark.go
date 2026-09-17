package tui

import (
	"regexp"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/table"
)

// Row markers tie a screen row back to the table row drawn on it.
//
// bubbles/v2/table keeps its scroll offset private, so geometry alone cannot say
// which row sits on a given screen line once the table has scrolled. Every cell
// of a row therefore carries a zero-width marker naming the row's index: a CSI
// sequence with the private final byte 'z', which x/ansi measures as zero cells
// and which the table's truncation, the viewport's cut, and lipgloss padding all
// carry through untouched. A click re-renders the table (View is pure) and reads
// the marker off the clicked line; no per-frame store is kept.
//
// The markers never reach the terminal: render strips them from the body before
// the overlays are composited over it.

// rowMarkFinal is the marker's final byte. No sequence the UI draws ends in it.
const rowMarkFinal = 'z'

// rowMarkPattern matches one marker.
var rowMarkPattern = regexp.MustCompile("\x1b\\[([0-9]+)" + string(rowMarkFinal))

// rowMark is the marker for row i.
func rowMark(i int) string {
	return "\x1b[" + strconv.Itoa(i) + string(rowMarkFinal)
}

// markRow prefixes every cell of row i with its marker, in place. Every cell
// rather than the first: a column the user hid or the pane dropped has a width
// of zero and is not drawn at all, so a marker in it alone would vanish.
func markRow(i int, row table.Row) table.Row {
	mark := rowMark(i)

	for c := range row {
		row[c] = mark + row[c]
	}

	return row
}

// markRows marks every row with its index.
func markRows(rows []table.Row) []table.Row {
	for i, row := range rows {
		markRow(i, row)
	}

	return rows
}

// rowAt reads the row index marked on one line of a table's rendered view, whose
// line 0 is the header.
func rowAt(view string, line int) (int, bool) {
	lines := strings.Split(view, "\n")
	if line < 1 || line >= len(lines) {
		return 0, false
	}

	match := rowMarkPattern.FindStringSubmatch(lines[line])
	if match == nil {
		return 0, false
	}

	i, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}

	return i, true
}

// stripMarkers removes every row marker from s, leaving every other sequence
// alone.
func stripMarkers(s string) string {
	if !strings.ContainsRune(s, rowMarkFinal) {
		return s
	}

	return rowMarkPattern.ReplaceAllString(s, "")
}
