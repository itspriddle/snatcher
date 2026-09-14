package tui

import (
	"slices"
	"strings"
)

// sparkSamples is how many rate samples the status bar keeps per direction, and
// how many cells its sparkline takes.
const sparkSamples = 8

// sparkGlyphs are the eight block heights, lowest first. Each is one cell wide.
var sparkGlyphs = []rune("▁▂▃▄▅▆▇█")

// rateRing is a fixed-size ring of rate samples, in bytes per second. It is a
// value type, so pushing returns a new ring and a model copy never shares one.
type rateRing struct {
	samples [sparkSamples]int64
	n, next int
}

// push adds a sample, overwriting the oldest once the ring is full.
func (r rateRing) push(v int64) rateRing {
	r.samples[r.next] = v
	r.next = (r.next + 1) % sparkSamples

	if r.n < sparkSamples {
		r.n++
	}

	return r
}

// values returns the samples, oldest first.
func (r rateRing) values() []int64 {
	out := make([]int64, 0, r.n)
	start := (r.next - r.n + sparkSamples) % sparkSamples

	for i := range r.n {
		out = append(out, r.samples[(start+i)%sparkSamples])
	}

	return out
}

// sparkline draws values as block glyphs scaled to their own maximum,
// left-padded to width cells so the segment does not jitter as the ring fills.
// Fewer than two samples is not a line, and draws nothing.
func sparkline(values []int64, width int) string {
	if len(values) < 2 || width <= 0 {
		return ""
	}

	if len(values) > width {
		values = values[len(values)-width:]
	}

	top := slices.Max(values)

	var b strings.Builder

	b.WriteString(strings.Repeat(" ", width-len(values)))

	for _, v := range values {
		idx := 0
		if top > 0 && v > 0 {
			idx = int(v * int64(len(sparkGlyphs)-1) / top)
		}

		b.WriteRune(sparkGlyphs[idx])
	}

	return b.String()
}
