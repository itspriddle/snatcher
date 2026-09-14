package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPieceLinesFit checks a small and a very large bitfield both draw exactly h
// lines, every grid row exactly w cells, and that bucketing is exact at the last
// cell: 1,000 complete pieces in buckets of 7 leave a last bucket of 6, which is
// still complete and must not shade as partial.
func TestPieceLinesFit(t *testing.T) {
	const w, h = 40, 5

	for _, n := range []int{10, 1000, 100_000} {
		pieces := make([]bool, n)
		for i := range pieces {
			pieces[i] = true
		}

		lines := pieceLines(pieces, int64(n), 1<<20, 1, w, h)

		require.Lenf(t, lines, h, "%d pieces", n)

		for i, line := range lines[1:] {
			assert.Equalf(t, w, lipgloss.Width(line), "%d pieces: grid row %d", n, i)
		}

		grid := strings.Join(lines[1:], "")

		assert.NotContainsf(t, grid, pieceMost, "%d complete pieces shaded a cell as partial", n)
		assert.LessOrEqualf(t, strings.Count(grid, pieceAll), w*(h-1), "%d pieces overflowed the grid", n)
	}

	empty := pieceLines(make([]bool, 10), 10, 1, 1, w, h)
	assert.Equal(t, 10, strings.Count(strings.Join(empty[1:], ""), pieceNone), "one cell per piece while they fit")
}

// TestPieceBucketsShadeByFraction checks each shade: 20 pieces in 5 cells of 4,
// with 0, 1, 2, 3, and 4 complete.
func TestPieceBucketsShadeByFraction(t *testing.T) {
	var pieces []bool

	for done := range 5 {
		for i := range 4 {
			pieces = append(pieces, i < done)
		}
	}

	lines := pieceLines(pieces, 20, 1<<20, 1, 5, 2)

	require.Len(t, lines, 2)
	assert.Equal(t, "10 of 20 pieces (50%) · 1.0 MiB each", lines[0])
	assert.Equal(t, pieceNone+pieceThird+pieceHalf+pieceMost+pieceAll, lines[1])
}

// TestNoPiecesSaysSo checks a magnet still fetching its metadata says so rather
// than drawing an empty grid.
func TestNoPiecesSaysSo(t *testing.T) {
	assert.Equal(t, []string{"no pieces yet · metadata 40%"}, pieceLines(nil, 0, 0, 0.4, 40, 5))
}

// TestDetailPiecesGolden pins the pieces tab.
func TestDetailPiecesGolden(t *testing.T) {
	assertGolden(t, "detail_pieces", withMockDetail(press(newTestModel(120, 40), "enter", "5")).render())
}
