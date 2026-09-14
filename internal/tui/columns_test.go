package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// TestColumnKeysMirrorTheCLI pins the column vocabulary to the CLI's. These two
// lists are copies of allFieldKeys and defaultFieldKeys in
// internal/commands/list.go (lines 96 and 106): internal/tui cannot import
// internal/commands, so when a list column is added or renamed there, this test
// is what says the TUI's copy has to follow.
func TestColumnKeysMirrorTheCLI(t *testing.T) {
	assert.Equal(t, []string{
		"id", "hash", "name", "status", "done", "size", "down", "up", "ratio",
		"eta", "peers", "added", "download_dir", "error", "labels",
	}, allColumnKeys)

	assert.Equal(t, []string{"id", "name", "status", "done", "size", "down", "up", "ratio", "eta"}, defaultColumnKeys)
}

// TestEveryColumnKeyHasADef checks the registry and the key list are one set, in
// one order, and that name is the only flex column.
func TestEveryColumnKeyHasADef(t *testing.T) {
	require.Len(t, columnDefs, len(allColumnKeys))

	for i, key := range allColumnKeys {
		assert.Equalf(t, key, columnDefs[i].key, "column %d", i)
		assert.NotEmptyf(t, columnDefs[i].title, "%s has no title", key)
		assert.NotNilf(t, columnDefs[i].render, "%s has no renderer", key)
		assert.Equalf(t, key == nameKey, columnDefs[i].width == 0, "%s: only name flexes", key)
	}
}

// TestColumnsForSkipsUnknownKeys checks the prefix cells always lead and a key
// the TUI cannot draw is dropped rather than failing.
func TestColumnsForSkipsUnknownKeys(t *testing.T) {
	cols := columnsFor([]string{"ratio", "bogus", "name"})

	require.Len(t, cols, 4)
	assert.Empty(t, cols[markColumn].key)
	assert.Empty(t, cols[glyphColumn].key)
	assert.Equal(t, "ratio", cols[2].key)
	assert.Equal(t, "name", cols[3].key)
}

// TestFitColumnsDropsFromTheRight checks the narrowing rule over a user-chosen
// set: columns drop right to left in display order, the prefix cells and the name
// never drop, and a set with no name drops until it fits.
func TestFitColumnsDropsFromTheRight(t *testing.T) {
	cols := columnsFor(defaultColumnKeys)

	wide := fitColumns(cols, 200)
	for i, c := range wide {
		assert.Positivef(t, c.Width, "column %d dropped at 200 cells", i)
	}

	narrow := fitColumns(cols, 60)

	assert.Equal(t, 1, narrow[markColumn].Width)
	assert.Equal(t, 1, narrow[glyphColumn].Width)
	assert.GreaterOrEqual(t, narrow[3].Width, minNameWidth, "name keeps its minimum")
	assert.Zero(t, narrow[len(narrow)-1].Width, "eta, the rightmost, drops first")

	dropped := false

	for i := len(prefixColumns); i < len(narrow); i++ {
		if cols[i].key == nameKey {
			continue
		}

		if dropped {
			assert.Zerof(t, narrow[i].Width, "%s survived with a column to its left dropped", cols[i].key)
		}

		dropped = dropped || narrow[i].Width == 0
	}

	noName := fitColumns(columnsFor([]string{"id", "hash", "status"}), 30)

	assert.Equal(t, 4, noName[2].Width, "id fits once the others drop")
	assert.Zero(t, noName[3].Width, "hash drops second")
	assert.Zero(t, noName[4].Width, "status drops first")
}

// TestRightAlignedCellsArePadded checks right alignment is padding in the cell,
// and its heading's, since the table cannot align.
func TestRightAlignedCellsArePadded(t *testing.T) {
	id, ok := columnDef("id")
	require.True(t, ok)

	assert.Equal(t, "   7", id.cellText(transmission.Torrent{ID: 7}, newIcons(false)))
	assert.Equal(t, "12345", id.cellText(transmission.Torrent{ID: 12345}, newIcons(false)), "a wide value is left to the table")
	assert.Equal(t, "  ID", tableColumns([]listColumn{id})[0].Title)

	name, _ := columnDef(nameKey)
	assert.Equal(t, "a", name.cellText(transmission.Torrent{Name: "a"}, newIcons(false)))
}
