package tui

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
)

// TestRateRingKeepsTheNewestSamples checks the ring fills, then overwrites its
// oldest sample, and reports oldest first either way.
func TestRateRingKeepsTheNewestSamples(t *testing.T) {
	var r rateRing

	assert.Empty(t, r.values())

	r = r.push(1).push(2)
	assert.Equal(t, []int64{1, 2}, r.values())

	for v := int64(3); v <= 10; v++ {
		r = r.push(v)
	}

	assert.Equal(t, []int64{3, 4, 5, 6, 7, 8, 9, 10}, r.values())
}

// TestRateRingPushDoesNotShare checks a pushed ring is a new value, so a model
// copy never sees another copy's samples.
func TestRateRingPushDoesNotShare(t *testing.T) {
	a := rateRing{}.push(1)
	b := a.push(2)

	assert.Equal(t, []int64{1}, a.values())
	assert.Equal(t, []int64{1, 2}, b.values())
}

// TestSparkline pins the scaling, the padding, and the cases that draw nothing.
func TestSparkline(t *testing.T) {
	assert.Empty(t, sparkline(nil, sparkSamples))
	assert.Empty(t, sparkline([]int64{5}, sparkSamples), "one sample is not a line")

	assert.Equal(t, "      ▁█", sparkline([]int64{0, 70}, sparkSamples))
	assert.Equal(t, "▁▂▃▄▅▆▇█", sparkline([]int64{0, 1, 2, 3, 4, 5, 6, 7}, sparkSamples))
	assert.Equal(t, "      ▁▁", sparkline([]int64{0, 0}, sparkSamples), "an idle link is a flat line")
	assert.Equal(t, "▄█", sparkline([]int64{9, 1, 2}, 2), "wider input keeps the newest samples")
}

// TestSparklineIsOneCellPerSample checks every glyph measures one cell, so the
// segment's width is fixed however the rates move.
func TestSparklineIsOneCellPerSample(t *testing.T) {
	assert.Equal(t, sparkSamples, lipgloss.Width(sparkline([]int64{3, 1, 4, 1, 5, 9, 2, 6}, sparkSamples)))
	assert.Equal(t, sparkSamples, lipgloss.Width(sparkline([]int64{3, 1}, sparkSamples)))
}
