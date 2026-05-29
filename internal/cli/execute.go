package cli

import (
	"fmt"
	"os"
)

// Execute creates the root command and runs it.
// It returns 0 on success, or an appropriate exit code on error.
func Execute() int {
	cmd := NewRootCmd()
	if err := cmd.Execute(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		return 1
	}
	return 0
}
