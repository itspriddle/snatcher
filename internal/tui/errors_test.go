package tui

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// TestErrorLineCoversTheSentinels checks each backend sentinel gets its own
// sentence, and that other text is sanitized.
func TestErrorLineCoversTheSentinels(t *testing.T) {
	assert.Contains(t, errorLine(transmission.ErrAuth), "authentication failed")
	assert.Contains(t, errorLine(transmission.ErrUnreachable), "server unreachable")
	assert.Contains(t, errorLine(transmission.ErrUnsupported), "Transmission 4.0")
	assert.Contains(t, errorLine(transmission.ErrServer), "server error")
	assert.NotContains(t, errorLine(io.ErrUnexpectedEOF), "server")
	assert.NotContains(t, errorLine(&hostileError{}), "\x1b")
	assert.Empty(t, errorLine(nil))
}

// TestErrorShortAndHint pins the status bar's words and the pane hint for each
// sentinel, and that every hint says how to retry.
func TestErrorShortAndHint(t *testing.T) {
	tests := []struct {
		err   error
		short string
		hint  string
	}{
		{transmission.ErrAuth, "auth failed", "snatcher server add"},
		{transmission.ErrUnsupported, "unsupported server", "Transmission 4.0 or newer"},
		{transmission.ErrUnreachable, "unreachable", retryHint},
		{transmission.ErrServer, "server error", retryHint},
		{io.ErrUnexpectedEOF, "unexpected EOF", retryHint},
	}

	for _, tc := range tests {
		t.Run(tc.short, func(t *testing.T) {
			assert.Equal(t, tc.short, errorShort(tc.err))
			assert.Contains(t, errorHint(tc.err), tc.hint)
			assert.Contains(t, errorHint(tc.err), retryHint)
		})
	}
}

// TestErrorShortIsBoundedAndSanitized checks raw server text cannot flood the
// status bar or reach the terminal unsanitized.
func TestErrorShortIsBoundedAndSanitized(t *testing.T) {
	short := errorShort(&longError{})

	assert.LessOrEqual(t, len([]rune(short)), errorShortWidth)
	assert.NotContains(t, errorShort(&hostileError{}), "\x1b")
	assert.Empty(t, errorShort(nil))
}

// hostileError is an error whose text carries an escape sequence.
type hostileError struct{}

func (*hostileError) Error() string { return "boom\x1b[2J" }

// longError is an error whose text is far wider than the status bar allows.
type longError struct{}

func (*longError) Error() string {
	return "the tracker returned a very long explanation of what went wrong"
}
