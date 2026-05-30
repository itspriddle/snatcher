package commands

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/itspriddle/snatcher/internal/torrent"
)

// NewRemoveCmd builds the "remove" command, which removes the torrents selected
// by positional id/hash arguments (or --all) and confirms by naming the affected
// torrents. It is the first destructive mutation command, so it adds a
// confirmation gate: a removal requires an explicit --yes/-y. With --delete-data
// the downloaded local files are deleted too. It carries the same --config,
// --json, --jq, and --all flags as the other mutation commands. Render precedence
// mirrors list: --jq → --json → plaintext.
//
// Unlike stop, remove lists the torrents *before* removing them so their names
// are available for the confirmation and output (the torrents are gone after).
//
// The confirmation gate works as follows: --yes/-y skips it entirely. Otherwise,
// on an interactive terminal it prompts [y/N] and a non-affirmative answer cancels
// (prints "Canceled.", exit 0). When stdin is not a terminal (piped input,
// scripts) it refuses with an error and removes nothing, so non-interactive
// removals must pass --yes deliberately.
func NewRemoveCmd() *cobra.Command {
	var (
		configPath string
		asJSON     bool
		jqExpr     string
		all        bool
		deleteData bool
		yes        bool
	)

	cmd := &cobra.Command{
		Use:     "remove [id|hash]...",
		Aliases: []string{"rm"},
		Short:   "Remove torrents",
		Long: "Remove the selected torrents from the session.\n\n" +
			"Select torrents by numeric id, 40-character SHA1 hash, or --all. " +
			"At least one selector is required; a bare 'snatcher torrent remove' is an error, " +
			"not a no-op. Use --delete-data to also delete the downloaded local files. " +
			"Removal is destructive and requires confirmation: pass --yes to proceed.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := torrent.ParseSelector(args, all)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(configPath)
			if err != nil {
				return err
			}

			// List before removing so torrent names are available for the
			// confirmation and output; afterward the torrents are gone.
			torrents, err := backend.ListTorrents(cmd.Context(), nil)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			result, err := resolveMutation("remove", sel, torrents)
			if err != nil {
				return err
			}

			result.DeleteData = &deleteData

			if !yes {
				confirmed, err := confirmRemoval(cmd, result, deleteData)
				if err != nil {
					return err
				}
				if !confirmed {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Canceled.")

					return nil
				}
			}

			if err := backend.RemoveTorrents(cmd.Context(), sel, deleteData); err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			return renderMutation(cmd, result, jqExpr, asJSON)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVar(&all, "all", false, "remove every torrent")
	cmd.Flags().BoolVar(&deleteData, "delete-data", false, "also delete the downloaded local files")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	return cmd
}

// confirmRemoval decides whether a removal may proceed when --yes was not given.
// On an interactive terminal it prompts [y/N] and returns the user's answer. When
// stdin is not a terminal (piped input or scripts), it refuses with an error and
// returns false so the removal is never carried out without an explicit --yes.
func confirmRemoval(cmd *cobra.Command, result mutationResult, deleteData bool) (bool, error) {
	in, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(in.Fd())) {
		return false, fmt.Errorf(
			"refusing to remove %s without confirmation; pass --yes to proceed",
			pluralizeTorrents(result.Count),
		)
	}

	return promptConfirm(cmd, in, confirmPrompt(result, deleteData))
}

// confirmPrompt builds the interactive confirmation prompt for a removal. An --all
// selection reads "Remove all N torrents?"; otherwise "Remove N torrents?". When
// deleteData is set the prompt warns that downloaded data will be deleted too.
func confirmPrompt(result mutationResult, deleteData bool) string {
	subject := pluralizeTorrents(result.Count)
	if result.all {
		subject = "all " + subject
	}

	if deleteData {
		return fmt.Sprintf("Remove %s and delete downloaded data? [y/N] ", subject)
	}

	return fmt.Sprintf("Remove %s? [y/N] ", subject)
}

// promptConfirm writes msg to the command's output, reads a single line from in,
// and reports whether the answer was affirmative. Taking an io.Reader keeps it
// unit-testable with a strings.Reader, with no real TTY required.
func promptConfirm(cmd *cobra.Command, in io.Reader, msg string) (bool, error) {
	_, _ = fmt.Fprint(cmd.OutOrStdout(), msg)

	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("reading confirmation: %w", err)
	}

	return parseAffirmative(line), nil
}

// parseAffirmative reports whether a prompt answer is an affirmative "y"/"yes"
// (case-insensitive, surrounding whitespace ignored). Everything else — including
// an empty line — declines, matching the [y/N] default.
func parseAffirmative(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}
