package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/itspriddle/snatcher/internal/output"
)

// Execute creates the root command and runs it.
// It returns 0 on success, or an appropriate exit code on error.
func Execute() int {
	cmd := NewRootCmd()
	if err := cmd.Execute(); err != nil {
		printError(os.Stderr, err)
		return 1
	}
	return 0
}

// printError writes err to w as a human-facing "Error: …" line. Error strings
// can embed server-supplied text (e.g. an rpc.ErrRPC message echoed verbatim
// from the server's response), so the rendered message is run through
// output.SanitizeTerminal before it reaches the terminal — a compromised or
// MITM'd server cannot inject ANSI escape sequences through it. This touches
// only the human/stderr render path; the error value, its wrapping, and
// errors.Is on the neutral sentinels are unaffected.
func printError(w io.Writer, err error) {
	_, _ = fmt.Fprintf(w, "Error: %s\n", output.SanitizeTerminal(err.Error()))
}
