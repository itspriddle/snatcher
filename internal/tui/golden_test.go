package tui

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// update rewrites the golden files instead of comparing against them, matching
// the -update flag internal/cli/surface_test.go already uses.
var update = flag.Bool("update", false, "rewrite the golden files")

// assertGolden compares a rendered frame against testdata/<name>.golden.
//
// The frame is stripped of ANSI escapes first. A golden full of raw escape
// sequences is unreadable in review, and worse, it pins the palette — filling in
// the three themes is T2's whole job, and it must not have to rewrite every
// golden to do it. Style correctness is asserted separately and narrowly, in
// view_test.go.
func assertGolden(t *testing.T, name, rendered string) {
	t.Helper()

	path := filepath.Join("testdata", name+".golden")
	stripped := ansi.Strip(rendered)

	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o750))
		require.NoError(t, os.WriteFile(path, []byte(stripped), 0o600))

		return
	}

	want, err := os.ReadFile(path) //nolint:gosec // the path is built from a test constant
	require.NoErrorf(t, err, "missing golden %s: run go test ./internal/tui -update", path)

	assert.Equal(t, string(want), stripped)
}
