// Package clipboard copies text to the OS clipboard through a native tool.
//
// It is write-only: snatcher never reads the clipboard (docs/vision.md,
// "Terminal constraints"). It shells out rather than linking a clipboard
// library because release builds are CGO_ENABLED=0, and it reads no
// environment of its own; whether to use it at all is the launcher's decision.
package clipboard

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// writeTimeout bounds one copy, so a wedged tool cannot hold a yank forever.
const writeTimeout = 3 * time.Second

// Writer copies text to the OS clipboard through a native tool.
type Writer struct {
	name string
	path string
	args []string
}

// candidate is one native tool and the arguments that make it write the
// clipboard from stdin.
type candidate struct {
	name string
	args []string
}

// candidates lists the tools to try for goos, in order of preference.
func candidates(goos string) []candidate {
	switch goos {
	case "darwin":
		return []candidate{{name: "pbcopy"}}
	case "linux":
		return []candidate{
			{name: "wl-copy"},
			{name: "xclip", args: []string{"-selection", "clipboard"}},
			{name: "xsel", args: []string{"--clipboard", "--input"}},
		}
	}

	return nil
}

// Detect finds the first native clipboard tool on PATH for this OS and
// returns nil when there is none.
func Detect() *Writer {
	for _, c := range candidates(runtime.GOOS) {
		path, err := exec.LookPath(c.name)
		if err != nil {
			continue
		}

		return &Writer{name: c.name, path: path, args: c.args}
	}

	return nil
}

// Write copies s. The payload goes over stdin, never argv, and never appears
// in the returned error.
//
// Stdout and Stderr are deliberately left nil, so the child gets /dev/null:
// xclip forks a process that holds the selection, and an inherited pipe would
// keep Wait blocked for as long as that process lives.
func (w *Writer) Write(ctx context.Context, s string) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, w.path, w.args...)
	cmd.Stdin = strings.NewReader(s)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", w.name, err)
	}

	return nil
}
