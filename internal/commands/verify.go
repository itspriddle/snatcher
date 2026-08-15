package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// NewVerifyCmd builds the "verify" command, which queues local-data
// verification (a re-hash of downloaded files against torrent metadata) for the
// torrents selected by positional id/hash arguments, --all, or
// --all-with-errors, and confirms by naming the affected torrents. It mirrors
// stop's mutation pattern and carries the same --json and --jq flags. Render
// precedence mirrors list: --jq → --json → plaintext.
//
// Unlike stop, verify lists the torrents *before* issuing the verify call on
// every path: --all-with-errors resolves its selection from that list, and
// listing first means an unmatched id aborts before a wasted RPC call instead of
// after it.
//
// The bulk forms (--all and --all-with-errors) are gated: a full-session re-hash
// is expensive and effectively un-cancellable from the CLI, so blast radius earns
// a prompt even though verification is not destructive. --yes/-y skips it;
// otherwise an interactive terminal is prompted [y/N] and a non-affirmative
// answer cancels (prints "Canceled.", exit 0), while a non-terminal stdin is
// refused and nothing is verified. An explicit id/hash selection is never gated —
// the caller already enumerated the torrents.
func NewVerifyCmd() *cobra.Command {
	var (
		server        string
		asJSON        bool
		jqExpr        string
		all           bool
		allWithErrors bool
		yes           bool
	)

	cmd := &cobra.Command{
		Use:   "verify [id|hash]...",
		Short: "Verify local torrent data",
		Long: "Verify local torrent data by queueing a re-hash of downloaded files.\n\n" +
			"Select torrents by numeric id, 40-character SHA1 hash, --all, or " +
			"--all-with-errors, which selects every torrent the server currently " +
			"reports an error for. At least one selector is required; a bare " +
			"'snatcher torrent verify' is an error, not a no-op. Exactly one selection " +
			"mode may be given.\n\n" +
			"The bulk forms (--all and --all-with-errors) confirm the torrent count " +
			"before verifying: pass --yes to skip the prompt. An explicit id or hash " +
			"selection is never prompted.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := verifySelector(args, all, allWithErrors)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(cmd, server)
			if err != nil {
				return err
			}

			// List before verifying: --all-with-errors resolves its selection from
			// this list, and an unmatched selector aborts before the verify call.
			torrents, err := backend.ListTorrents(cmd.Context(), transmission.Selector{All: true})
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			if allWithErrors {
				matched, errSel := filterSelection(torrents, transmission.FilterError)
				if len(matched) == 0 {
					return renderNoErroredTorrents(cmd, jqExpr, asJSON)
				}

				sel = errSel
			}

			result, err := resolveMutation("verify", sel, torrents)
			if err != nil {
				return err
			}

			// Only the bulk forms are gated, and only when they resolved to something:
			// there is nothing to confirm about verifying zero torrents.
			if (all || allWithErrors) && !yes && result.Count > 0 {
				confirmed, err := confirmGate(
					cmd,
					"verify "+pluralizeTorrents(result.Count),
					verifyPrompt(result.Count, allWithErrors),
				)
				if err != nil {
					return err
				}
				if !confirmed {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Canceled.")

					return nil
				}
			}

			if err := backend.VerifyTorrents(cmd.Context(), sel); err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			return renderMutation(cmd, result, jqExpr, asJSON)
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVar(&all, "all", false, "verify every torrent")
	cmd.Flags().BoolVar(&allWithErrors, "all-with-errors", false, "verify every torrent the server reports an error for")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	return cmd
}

// verifySelector resolves verify's selection flags into a Selector, enforcing
// that exactly one selection mode is given. --all-with-errors cannot name its
// torrents until the list has been fetched, so it returns the zero Selector for
// the caller to fill in; every other form goes through the shared ParseSelector.
//
// The mutual-exclusion errors are raised here, before the server is dialed, so a
// contradictory invocation costs no round trip.
func verifySelector(args []string, all, allWithErrors bool) (transmission.Selector, error) {
	if !allWithErrors {
		return transmission.ParseSelector(args, all)
	}

	if len(args) > 0 {
		return transmission.Selector{}, fmt.Errorf("cannot combine torrent selectors with --all-with-errors")
	}

	if all {
		return transmission.Selector{}, fmt.Errorf("cannot combine --all with --all-with-errors")
	}

	return transmission.Selector{}, nil
}

// verifyPrompt builds the interactive confirmation prompt for a bulk verify,
// naming the resolved count so the blast radius is on screen before the answer.
// --all-with-errors says so explicitly, since its count is a subset the caller
// did not enumerate.
func verifyPrompt(count int, allWithErrors bool) string {
	if allWithErrors {
		return fmt.Sprintf("Verify %s with errors? [y/N] ", pluralizeTorrents(count))
	}

	return fmt.Sprintf("Verify all %s? [y/N] ", pluralizeTorrents(count))
}

// renderNoErroredTorrents reports that --all-with-errors matched nothing. That is
// not an error — the filter legitimately selected none — so it exits 0 having
// issued no verify call at all. The structured paths emit the standard
// mutationResult contract with a count of 0 and an empty (never null) torrent
// array; the plaintext path needs its own line here because
// writeMutationPlaintext would otherwise print a dangling
// "Queued verification of 0 torrents: ".
func renderNoErroredTorrents(cmd *cobra.Command, jqExpr string, asJSON bool) error {
	if jqExpr == "" && !asJSON {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No torrents with errors.")

		return nil
	}

	result := mutationResult{Action: "verify", Torrents: []mutationTorrent{}}

	return renderMutation(cmd, result, jqExpr, asJSON)
}
