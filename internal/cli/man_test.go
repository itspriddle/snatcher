package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/surface"
)

// manExceptions lists command paths (e.g. "snatcher api") or flag names (e.g.
// "--raw") that are intentionally left undocumented in the man page. Keep it
// empty; add an entry with a comment explaining why only for a deliberate
// omission, mirroring a lint nolint directive.
var manExceptions = map[string]bool{}

// TestManPageCoverage fails when a command or flag exists in the CLI but is not
// mentioned in man/man1/snatcher.1. The man page is hand-written; this guard
// only flags drift so it can be updated by hand (it is not regenerated). It is a
// forward check (code -> man) and does not detect stale references in the man
// page.
func TestManPageCoverage(t *testing.T) {
	root := NewRootCmd()

	manPath := filepath.Join(repoRoot(t), "man", "man1", "snatcher.1")
	raw, err := os.ReadFile(manPath)
	require.NoError(t, err, "man page not found at %s", manPath)

	// Undo roff escapes so flags written as \-\-config and command references
	// written as auth\ show match against the plain CLI strings.
	man := strings.ReplaceAll(string(raw), `\-`, "-")
	man = strings.ReplaceAll(man, `\ `, " ")

	var missing []string

	for _, path := range surface.Commands(root) {
		if manExceptions[path] {
			continue
		}
		if !strings.Contains(man, path) {
			missing = append(missing, "command: "+path)
		}
	}

	for _, name := range surface.FlagNames(root) {
		if manExceptions[name] {
			continue
		}
		// Word-boundary match so a prefix flag (--raw) is not falsely satisfied
		// by a longer one (--raw-field).
		re := regexp.MustCompile(`(^|[^\w-])` + regexp.QuoteMeta(name) + `([^\w-]|$)`)
		if !re.MatchString(man) {
			missing = append(missing, "flag: "+name)
		}
	}

	if len(missing) > 0 {
		t.Errorf("man/man1/snatcher.1 is missing %d CLI element(s):\n\n  %s\n\n"+
			"Document them in man/man1/snatcher.1, or if intentionally omitted add "+
			"them to manExceptions in this file.",
			len(missing), strings.Join(missing, "\n  "))
	}
}
