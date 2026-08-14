package commands

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTorrentGroupLongNamesEverySubcommand pins the subcommand enumeration in
// the group's Long text to the set actually registered with AddCommand. Neither
// check-surface (command paths and flag names only) nor check-man (forward
// coverage of the man page) looks at help prose, so the sentence drifted three
// times while this branch added files, peers, and trackers — each rewrite
// leaving the pre-existing "queue" omission in place, which made "snatcher
// torrent --help" contradict the man page.
//
// The assertion parses the parenthesized list rather than matching substrings
// against the whole Long: "list" is a substring of the word "listing" in the
// surrounding prose, so a Contains-based guard would pass with "list" missing
// from the enumeration.
func TestTorrentGroupLongNamesEverySubcommand(t *testing.T) {
	cmd := NewTorrentCmd()

	open := strings.Index(cmd.Long, "(")
	closing := strings.Index(cmd.Long, ")")
	require.Greater(t, closing, open, "Long carries a parenthesized subcommand list")

	named := strings.Split(cmd.Long[open+1:closing], ", ")

	registered := make([]string, 0, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		registered = append(registered, sub.Name())
	}

	assert.ElementsMatch(t, registered, named,
		"the Long enumeration names exactly the registered subcommands")
}
