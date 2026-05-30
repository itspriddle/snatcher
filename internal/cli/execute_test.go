package cli

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/torrent"
)

func TestPrintErrorSanitizesServerSuppliedString(t *testing.T) {
	// Model a server-supplied error string carrying a terminal escape, wrapped
	// the way an rpc.ErrRPC message surfaces through the adapter's neutral
	// ErrUnreachable sentinel.
	serverErr := errors.New("server error: requ\x1b[2Jired")
	err := fmt.Errorf("%w: %w", torrent.ErrUnreachable, serverErr)

	var buf bytes.Buffer
	printError(&buf, err)
	out := buf.String()

	assert.NotContains(t, out, "\x1b", "raw ESC from the server error must not reach the stderr path")
	assert.Contains(t, out, "requ[2Jired", "visible text survives sanitization")
	assert.Contains(t, out, "Error: ", "the human-facing prefix is preserved")
}

func TestPrintErrorPreservesErrorsIs(t *testing.T) {
	// Sanitization is applied only to the rendered string; the error value and
	// its wrapping (errors.Is on the neutral sentinels) are unaffected.
	err := fmt.Errorf("%w: server error: boom\x1b]", torrent.ErrUnreachable)

	require.ErrorIs(t, err, torrent.ErrUnreachable, "wrapping is untouched by the render-path sanitization")

	var buf bytes.Buffer
	printError(&buf, err)
	assert.NotContains(t, buf.String(), "\x1b", "control byte stripped on the stderr path")
}

func TestPrintErrorLeavesCleanErrorUnchanged(t *testing.T) {
	var buf bytes.Buffer
	printError(&buf, errors.New("plain failure"))

	assert.Equal(t, "Error: plain failure\n", buf.String())
}
