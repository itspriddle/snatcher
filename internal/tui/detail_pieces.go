package tui

import (
	"fmt"
	"strings"

	"github.com/itspriddle/snatcher/internal/output"
)

// pieceGlyphs shade a grid cell by the fraction of its pieces that are complete:
// none, up to a third, up to two thirds, more, and all. They are plain Unicode
// block elements — what the sparkline already draws unconditionally — rather
// than Nerd Font glyphs, and they read without color.
const (
	pieceNone  = "·"
	pieceThird = "░"
	pieceHalf  = "▒"
	pieceMost  = "▓"
	pieceAll   = "█"
)

// pieceLines is the pieces tab in exactly h lines of w cells: a summary line,
// then a grid sized to fit. When there are more pieces than cells, each cell
// stands for an equal run of them — the last run may be shorter — and is shaded
// by how much of its own run is complete. It never scrolls, so a 40,000-piece
// torrent and a 10-piece one both fit the pane.
//
// A torrent with no bitfield yet is a magnet still fetching its metadata, and
// says so.
func pieceLines(pieces []bool, count, size int64, metadata float64, w, h int) []string {
	if len(pieces) == 0 {
		return []string{"no pieces yet · metadata " + output.Percent(metadata)}
	}

	if count <= 0 {
		count = int64(len(pieces))
	}

	have := 0

	for _, done := range pieces {
		if done {
			have++
		}
	}

	lines := []string{fmt.Sprintf("%d of %d pieces (%s) · %s each",
		have, count, output.Percent(float64(have)/float64(count)), output.Bytes(size))}

	if w <= 0 || h <= 1 {
		return lines
	}

	cells := pieceCells(pieces, w*(h-1))

	for row := range h - 1 {
		var b strings.Builder

		for col := range w {
			i := row*w + col
			if i < len(cells) {
				b.WriteString(cells[i])
			} else {
				b.WriteString(" ")
			}
		}

		lines = append(lines, b.String())
	}

	return lines
}

// pieceCells buckets pieces into at most limit cells, each shaded by its own
// bucket's completeness.
func pieceCells(pieces []bool, limit int) []string {
	per := max(1, (len(pieces)+limit-1)/limit)
	cells := make([]string, 0, (len(pieces)+per-1)/per)

	for start := 0; start < len(pieces); start += per {
		bucket := pieces[start:min(start+per, len(pieces))]

		done := 0

		for _, ok := range bucket {
			if ok {
				done++
			}
		}

		cells = append(cells, pieceShade(done, len(bucket)))
	}

	return cells
}

// pieceShade is the glyph for done of total pieces complete, compared in whole
// numbers so a third is exactly a third.
func pieceShade(done, total int) string {
	switch {
	case done == 0:
		return pieceNone
	case done == total:
		return pieceAll
	case done*3 <= total:
		return pieceThird
	case done*3 <= total*2:
		return pieceHalf
	}

	return pieceMost
}
