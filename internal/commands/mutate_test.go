package commands

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTerminal is an in-memory stdin that declares itself interactive, so
// confirmGate's prompting branch can be exercised without allocating a pty. It
// is the only implementation of terminalInput: production stdin is an *os.File,
// checked with term.IsTerminal.
type fakeTerminal struct {
	io.Reader
}

func (fakeTerminal) isTerminal() bool { return true }

// TestConfirmGateInteractive covers the prompting branch: the prompt is written
// verbatim and the answer is returned, with anything but y/yes declining.
func TestConfirmGateInteractive(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "affirmative", input: "y\n", want: true},
		{name: "affirmative word", input: "yes\n", want: true},
		{name: "declined", input: "n\n", want: false},
		{name: "empty line declines", input: "\n", want: false},
		{name: "eof declines", input: "", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRemoveCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetIn(fakeTerminal{Reader: strings.NewReader(tc.input)})

			got, err := confirmGate(cmd, "remove 1 torrent", "Remove 1 torrent? [y/N] ")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, "Remove 1 torrent? [y/N] ", out.String(), "the prompt is written verbatim")
		})
	}
}

// TestConfirmGateNonTerminalRefuses covers the other branch: a piped stdin is
// refused with an error naming the caller's subject, nothing is prompted, and
// the answer is false so the caller mutates nothing.
func TestConfirmGateNonTerminalRefuses(t *testing.T) {
	cases := []struct {
		name    string
		subject string
		want    string
	}{
		{
			name:    "remove",
			subject: "remove 1 torrent",
			want:    "refusing to remove 1 torrent without confirmation; pass --yes to proceed",
		},
		{
			name:    "rename",
			subject: "rename the torrent root",
			want:    "refusing to rename the torrent root without confirmation; pass --yes to proceed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRemoveCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetIn(strings.NewReader("y\n"))

			got, err := confirmGate(cmd, tc.subject, "Remove 1 torrent? [y/N] ")
			require.EqualError(t, err, tc.want)
			assert.False(t, got, "a refused gate never reports confirmation")
			assert.Empty(t, out.String(), "nothing is prompted when stdin is not a terminal")
		})
	}
}
