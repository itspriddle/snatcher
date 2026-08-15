package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
)

// newSearchGrabCmd builds "search grab", which hands one release from a search
// back to Prowlarr to grab.
//
// A grab is a delegation, not an add: Prowlarr passes the release to *its own*
// configured download client, which is not necessarily the Transmission server
// snatcher is pointed at. That is the single most important thing about this
// command and it is stated in the Short text, the Long text, and the man page.
//
// The release is named either by its position in a re-run of the search or
// exactly by guid; see search_release.go for both forms. Neither is prompted
// for: a grab names one release, and the repo's rule is that confirmation scales
// with blast radius. What the positional form does get is an echo of the
// resolved release before the grab is issued, since a result set that shifted
// between the search and the grab would otherwise silently move the number.
func newSearchGrabCmd() *cobra.Command {
	var (
		server string
		asJSON bool
		jqExpr string
		sel    releaseSelector
	)

	cmd := &cobra.Command{
		Use:   "grab [query] [n]",
		Short: "Tell Prowlarr to grab a search result",
		Long: "Hand one release from a search back to Prowlarr to grab.\n\n" +
			"A grab is handed to Prowlarr's own configured download client, which is " +
			"not necessarily the Transmission server snatcher is pointed at. This " +
			"means \"tell Prowlarr to grab it\", not \"add it to this server\" — to " +
			"add a release to the snatcher server, feed its download or magnet url " +
			"to \"snatcher torrent add\".\n\n" +
			"Name the release in one of two ways:\n\n" +
			"  snatcher search grab \"ubuntu\" 3\n" +
			"  snatcher search grab --guid <guid> --indexer 3\n\n" +
			"The first form re-runs the search and resolves the 1-based result " +
			"number from the fresh results — snatcher caches no result state between " +
			"invocations — so it names the release you saw only when the same " +
			"--indexer, --category, --limit, --offset, --pages, and --search-type " +
			"are passed, and only while the results have not shifted. The resolved " +
			"release is echoed to stderr before it is grabbed so a mismatch is on " +
			"screen.\n\n" +
			"The second form has neither caveat and costs no re-run: it names the " +
			"release outright. Every guid is in \"snatcher search --json\" output for " +
			"exactly this reason, and the single --indexer that issued it is " +
			"required, since a guid is unique only within one indexer.\n\n" +
			"A single named release is never prompted for.\n\n" +
			"--server names a configured Prowlarr instance, not a Transmission " +
			"server; with it omitted the default Prowlarr server is used.",
		// Cobra validates the argument/flag combination before RunE, so a
		// contradictory invocation is refused before a secret store is opened.
		Args: func(_ *cobra.Command, args []string) error {
			_, err := sel.plan(args)

			return err
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := sel.plan(args)
			if err != nil {
				return err
			}

			client, serverURL, err := dialProwlarr(cmd, server)
			if err != nil {
				return err
			}

			release, err := plan.resolve(cmd.Context(), client, serverURL)
			if err != nil {
				return err
			}

			// What is about to be acted on is named before it is acted on. It goes
			// to stderr in every mode: it is a human safeguard, not a result, and
			// stdout under --json is exactly the grabbed release and nothing else.
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Grabbing: %s\n",
				output.SanitizeTerminal(releaseLabel(release)))

			grabbed, err := client.Grab(cmd.Context(), release)
			if err != nil {
				return fmt.Errorf("grabbing on %s: %w", serverURL, err)
			}

			// Prowlarr echoes the release with its own proxy downloadUrl, which
			// carries the API key as a query parameter; the same strip search's
			// structured output does applies here, and for the same reason.
			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), grabbed.Redacted(), jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), grabbed.Redacted())
			}

			// Prowlarr echoes the release it accepted, so this line can name a
			// release the exact form only had a guid for.
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Grabbed: %s\n",
				output.SanitizeTerminal(releaseLabel(grabbed)))

			return nil
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	sel.bind(cmd)

	return cmd
}
