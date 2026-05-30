package surface

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Generate walks a cobra command tree and produces a deterministic, sorted
// snapshot of all commands, flags, and positional arguments. Built-in commands
// (help, completion) and the --help flag are excluded.
func Generate(root *cobra.Command) string {
	var lines []string
	walk(root, &lines)
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

// Commands returns the full command paths in the tree (e.g. "snatcher settings
// get"), sorted. The root command itself and the built-in help/completion
// commands are excluded — it is the set of invocable subcommands a man page is
// expected to document.
func Commands(root *cobra.Command) []string {
	var paths []string
	collectCommands(root, &paths)
	sort.Strings(paths)
	return paths
}

func collectCommands(cmd *cobra.Command, paths *[]string) {
	if name := cmd.Name(); name == "help" || name == "completion" {
		return
	}

	if cmd.HasParent() {
		*paths = append(*paths, fullPath(cmd))
	}

	for _, child := range cmd.Commands() {
		collectCommands(child, paths)
	}
}

// FlagNames returns the distinct long flag names in the tree as "--name" (e.g.
// "--config"), sorted. The built-in --help flag and the help/completion
// commands are excluded. Names are deduplicated, so a flag shared across
// commands (like --config) appears once.
func FlagNames(root *cobra.Command) []string {
	seen := map[string]struct{}{}
	collectFlags(root, seen)

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func collectFlags(cmd *cobra.Command, seen map[string]struct{}) {
	if name := cmd.Name(); name == "help" || name == "completion" {
		return
	}

	add := func(f *pflag.Flag) {
		if f.Name == "help" {
			return
		}
		seen["--"+f.Name] = struct{}{}
	}

	if !cmd.HasParent() {
		cmd.PersistentFlags().VisitAll(add)
	}
	cmd.Flags().VisitAll(add)

	for _, child := range cmd.Commands() {
		collectFlags(child, seen)
	}
}

func walk(cmd *cobra.Command, lines *[]string) {
	name := cmd.Name()

	// Skip Cobra built-in commands.
	if name == "help" || name == "completion" {
		return
	}

	path := fullPath(cmd)

	// CMD line
	*lines = append(*lines, fmt.Sprintf("CMD %s", path))

	// ARG lines — extracted from the Use string.
	args := parseArgs(cmd.Use)
	for i, arg := range args {
		*lines = append(*lines, fmt.Sprintf("ARG %s %d %s", path, i, arg))
	}

	// FLAG lines.
	// For the root command, emit persistent flags (they apply globally).
	// For all commands, emit local non-persistent flags.
	if !cmd.HasParent() {
		cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
			if f.Name == "help" {
				return
			}
			*lines = append(*lines, fmt.Sprintf("FLAG %s --%s type=%s", path, f.Name, f.Value.Type()))
		})
	}
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" {
			return
		}
		// Skip persistent flags (already emitted on root).
		if cmd.PersistentFlags().Lookup(f.Name) != nil {
			return
		}
		*lines = append(*lines, fmt.Sprintf("FLAG %s --%s type=%s", path, f.Name, f.Value.Type()))
	})

	for _, child := range cmd.Commands() {
		walk(child, lines)
	}
}

// fullPath returns the full command path (e.g. "snatcher list").
func fullPath(cmd *cobra.Command) string {
	parts := []string{}
	for c := cmd; c != nil; c = c.Parent() {
		parts = append([]string{c.Name()}, parts...)
	}
	return strings.Join(parts, " ")
}

// parseArgs extracts positional argument names from a cobra Use string.
// e.g. "list <torrent-id>" -> ["torrent-id"], "add <a> <b>" -> ["a", "b"]
func parseArgs(use string) []string {
	var args []string
	for _, token := range strings.Fields(use) {
		if strings.HasPrefix(token, "<") && strings.HasSuffix(token, ">") {
			args = append(args, token[1:len(token)-1])
		}
	}
	return args
}
