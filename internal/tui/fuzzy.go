package tui

import (
	"strings"
)

// The fuzzy score's weights. A word start is worth more than a run, so "dl"
// finds "download limit" by its initials, and a run is worth more than a bare
// match, so a typed word finds itself.
const (
	fuzzyMatch     = 1
	fuzzyWordStart = 3
	fuzzyRun       = 2
	fuzzyLead      = -1
)

// fuzzyScore reports whether query is a case-insensitive subsequence of text,
// and how well it fits. The match is greedy and leftmost: deterministic, cheap,
// and good enough for a list of a few dozen names.
//
//   - +1 per matched rune
//   - +3 when the rune starts a word (index 0, or after ' ' '-' '_' '.' ':' '/')
//   - +2 when it directly follows the previous match
//   - -1 per rune skipped before the first match
//
// An empty query matches everything with a score of 0.
func fuzzyScore(query, text string) (int, bool) {
	q := []rune(strings.ToLower(query))
	if len(q) == 0 {
		return 0, true
	}

	t := []rune(strings.ToLower(text))
	score, qi, last := 0, 0, -1

	for i := 0; i < len(t) && qi < len(q); i++ {
		if t[i] != q[qi] {
			continue
		}

		score += fuzzyMatch

		if i == 0 || strings.ContainsRune(" -_.:/", t[i-1]) {
			score += fuzzyWordStart
		}

		switch {
		case last < 0:
			score += fuzzyLead * i
		case i == last+1:
			score += fuzzyRun
		}

		last = i
		qi++
	}

	if qi < len(q) {
		return 0, false
	}

	return score, true
}
