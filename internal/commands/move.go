package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// NewMoveCmd builds the "move" command, which changes the download location of a
// single torrent selected by a positional id/hash argument. It maps to the
// Transmission RPC torrent-set-location method: by default it relocates the
// existing files to the new location (move=true); with --in-place it instead
// repoints Transmission at a path where the files already exist and searches it
// (move=false), e.g. after a manual move on disk.
//
// Unlike the bulk mutation commands, move targets exactly one torrent and takes
// no --all selector. It carries the same --config, --json, and --jq flags. Render
// precedence mirrors list: --jq → --json → plaintext. The "mv" alias is provided
// for muscle memory.
func NewMoveCmd() *cobra.Command {
	var (
		configPath string
		server     string
		asJSON     bool
		jqExpr     string
		inPlace    bool
	)

	cmd := &cobra.Command{
		Use:     "move <id|hash> <location>",
		Aliases: []string{"mv"},
		Short:   "Move a torrent's download location",
		Long: "Move a single torrent's download location.\n\n" +
			"Select the torrent by numeric id or 40-character SHA1 hash. By default " +
			"the existing files are relocated to <location>. Use --in-place to repoint " +
			"Transmission at a path where the files already exist (after a manual move) " +
			"without relocating them.",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := transmission.ParseSelector([]string{args[0]}, false)
			if err != nil {
				return err
			}

			location := args[1]
			if strings.TrimSpace(location) == "" {
				return fmt.Errorf("location must not be empty")
			}

			backend, serverURL, err := dial(configPath, server)
			if err != nil {
				return err
			}

			// move=true relocates the files; --in-place sets move=false to repoint
			// without moving.
			move := !inPlace

			if err := backend.MoveTorrents(cmd.Context(), sel, location, move); err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			torrents, err := backend.ListTorrents(cmd.Context(), transmission.Selector{All: true})
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			result, err := resolveMutation("move", sel, torrents)
			if err != nil {
				return err
			}

			result.Location = location
			result.Move = &move

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), result, jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), result)
			}

			writeMovePlaintext(cmd, result, location, move)

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVar(&inPlace, "in-place", false, "repoint to an existing path without relocating files")

	return cmd
}

// writeMovePlaintext prints a single-line confirmation naming the affected
// torrent and its new location, reflecting the move mode. The default (relocate)
// reads "Moved 1 torrent to <location>: <name>"; --in-place reads "Set location
// of 1 torrent to <location>: <name>" since no files were relocated. The
// server-derived torrent name and the user-supplied location are both passed
// through output.SanitizeTerminal, since names are untrusted swarm-supplied
// strings and the location is echoed back verbatim.
func writeMovePlaintext(cmd *cobra.Command, result mutationResult, location string, move bool) {
	verb := "Moved"
	if !move {
		verb = "Set location of"
	}

	names := make([]string, len(result.Torrents))
	for i, t := range result.Torrents {
		names[i] = output.SanitizeTerminal(t.Name)
	}

	_, _ = fmt.Fprintf(
		cmd.OutOrStdout(),
		"%s %s to %s: %s\n",
		verb,
		pluralizeTorrents(result.Count),
		output.SanitizeTerminal(location),
		strings.Join(names, ", "),
	)
}
