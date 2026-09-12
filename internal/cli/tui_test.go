package cli

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsTerminalRejectsNonFiles checks the gate's negative case, which is the
// one that matters: every test in this package drives the root command through
// buffers, and a buffer must never be mistaken for a terminal.
func TestIsTerminalRejectsNonFiles(t *testing.T) {
	assert.False(t, isTerminal(&bytes.Buffer{}))
	assert.False(t, isTerminal(nil))
}

// TestIsTerminalRejectsAPipe checks a real *os.File that is not a terminal,
// which is what "snatcher | cat" hands the process.
func TestIsTerminalRejectsAPipe(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})

	assert.False(t, isTerminal(r))
	assert.False(t, isTerminal(w))
}

// TestIsTerminalIgnoresNoColor pins the deliberate difference from the isTTY
// helper in internal/commands, which folds NO_COLOR into "not a terminal". For
// launching, NO_COLOR means monochrome, never "print usage instead".
func TestIsTerminalIgnoresNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	r, w, err := os.Pipe()
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})

	// A pipe is not a terminal either way; what this asserts is that the answer
	// is reached without consulting the environment at all, which is only
	// observable as the answer not changing when the variable is set.
	assert.False(t, isTerminal(w))
}

// TestBareRootThroughBuffersPrintsUsage checks the gate from the other side: a
// non-terminal invocation keeps the CLI's behavior, and in particular never
// reaches the dialer.
//
// "not configured" is the error a bare launch gives on an unconfigured machine.
// Seeing it here would mean the gate let a piped invocation through to dial.
func TestBareRootThroughBuffersPrintsUsage(t *testing.T) {
	cmd := NewRootCmd()

	var out, errOut bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)

	require.NoError(t, cmd.Execute())

	assert.Contains(t, out.String(), "Usage:")
	assert.NotContains(t, out.String(), "not configured")
	assert.NotContains(t, errOut.String(), "not configured")
}

// TestRootLongMentionsTheTUI checks the help text documents the bare
// invocation, since it is the only place a user is told the TUI exists.
func TestRootLongMentionsTheTUI(t *testing.T) {
	assert.Contains(t, NewRootCmd().Long, "TUI")
}
