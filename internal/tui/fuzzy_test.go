package tui

import (
	"cmp"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFuzzyScoreMatchesSubsequences pins what matches at all: a subsequence,
// in any case, with the empty query matching everything.
func TestFuzzyScoreMatchesSubsequences(t *testing.T) {
	cases := []struct {
		query, text string
		want        bool
	}{
		{"pau", "pause", true},
		{"PAU", "pause", true},
		{"pse", "pause", true},
		{"dlimit", "download limit…", true},
		{"", "anything", true},
		{"", "", true},
		{"paux", "pause", false},
		{"esuap", "pause", false},
		{"x", "", false},
	}

	for _, tc := range cases {
		_, ok := fuzzyScore(tc.query, tc.text)
		assert.Equalf(t, tc.want, ok, "%q in %q", tc.query, tc.text)
	}

	score, _ := fuzzyScore("", "anything")
	assert.Zero(t, score)
}

// TestFuzzyScorePrefersWordStarts checks initials beat a scattered match.
func TestFuzzyScorePrefersWordStarts(t *testing.T) {
	assertRanksAbove(t, "dl", "download limit…", "toggle detail pane")
	assertRanksAbove(t, "tp", "test port", "stop")
}

// TestFuzzyScorePrefersRuns checks consecutive letters beat the same letters
// spread apart.
func TestFuzzyScorePrefersRuns(t *testing.T) {
	assertRanksAbove(t, "sta", "start", "sxtxa")
	assertRanksAbove(t, "pause", "pause", "paxuxsxe")
}

// TestFuzzyScorePrefersEarlier checks a match further in costs the runes it
// skipped.
func TestFuzzyScorePrefersEarlier(t *testing.T) {
	assertRanksAbove(t, "v", "verify", "reverify")
}

// TestFuzzyRankIsStable checks equal scores keep their base order, which is what
// the palette relies on to keep the registry's order among ties.
func TestFuzzyRankIsStable(t *testing.T) {
	texts := []string{"start group", "start", "start now"}

	type ranked struct {
		text  string
		score int
	}

	rows := make([]ranked, 0, len(texts))

	for _, text := range texts {
		score, ok := fuzzyScore("start", text)
		assert.True(t, ok)

		rows = append(rows, ranked{text, score})
	}

	slices.SortStableFunc(rows, func(a, b ranked) int { return cmp.Compare(b.score, a.score) })

	assert.Equal(t, []string{"start group", "start", "start now"}, []string{rows[0].text, rows[1].text, rows[2].text})
}

// assertRanksAbove checks query scores better against better than worse.
func assertRanksAbove(t *testing.T, query, better, worse string) {
	t.Helper()

	b, ok := fuzzyScore(query, better)
	assert.Truef(t, ok, "%q must match %q", query, better)

	w, ok := fuzzyScore(query, worse)
	assert.Truef(t, ok, "%q must match %q", query, worse)

	assert.Greaterf(t, b, w, "%q: %q must rank above %q", query, better, worse)
}
