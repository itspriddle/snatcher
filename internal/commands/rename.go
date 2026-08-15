package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// renameResult is the structured contract emitted by the rename command under
// --json/--jq. It is its own type rather than a mutationResult: rename targets
// exactly one torrent and carries the renamed path, which the multi-torrent
// mutation shape has no room for.
//
// Path and Name are the server's echo of what it renamed, falling back to the
// request arguments when the server answers with them empty, so the contract is
// never blank. Every string here is server-derived and emitted verbatim (a
// structured contract consumers handle themselves); the plaintext and prompt
// paths sanitize instead.
type renameResult struct {
	// Action names the operation performed. It is always "rename".
	Action string `json:"action"`

	// Path is the torrent-relative path that was renamed.
	Path string `json:"path"`

	// Name is the new (last-component) name that path now carries.
	Name string `json:"name"`

	// Root reports whether the rename targeted the torrent's root path, which
	// renames the torrent itself.
	Root bool `json:"root"`

	// Torrent identifies the affected torrent, with its post-rename name.
	Torrent mutationTorrent `json:"torrent"`
}

// NewRenameCmd builds the "rename" command, which renames one file or directory
// inside a single torrent. It maps to the Transmission RPC torrent-rename-path
// method, which is single-torrent by design: the torrent is selected by a
// positional id/hash argument and there is no --all.
//
// <path> is the torrent-relative path of a file *or* directory. Renaming the
// torrent's root path renames the torrent itself, which is a far larger blast
// radius than renaming one file, so that case is gated behind an interactive
// confirmation unless --yes is given — and refused outright, rather than guessed
// at, when stdin is not a terminal.
//
// The torrent's detail is fetched before the rename (to resolve a hash to its
// numeric id and to learn the current name the root check compares against) and
// again after it, so the reported torrent name is post-rename truth. Render
// precedence mirrors list: --jq → --json → plaintext.
func NewRenameCmd() *cobra.Command {
	var (
		server string
		asJSON bool
		jqExpr string
		yes    bool
	)

	cmd := &cobra.Command{
		Use:   "rename <id|hash> <path> <new-name>",
		Short: "Rename a file or directory in a torrent",
		Long: "Rename a file or directory inside a single torrent.\n\n" +
			"Select the torrent by numeric id or 40-character SHA1 hash. <path> is the " +
			"torrent-relative path of a file or directory, exactly as shown by " +
			"'snatcher torrent files <id|hash>'; <new-name> replaces its last component.\n\n" +
			"Renaming the torrent's root path renames the torrent itself, so that case " +
			"prompts for confirmation (pass --yes to skip the prompt, which is required " +
			"when standard input is not a terminal). After a root rename, later paths " +
			"are relative to the new root.",
		Args:          cobra.ExactArgs(3),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := transmission.ParseSelector([]string{args[0]}, false)
			if err != nil {
				return err
			}

			// A directory path completed by a shell arrives with a trailing
			// slash, which the server can never match: Transmission compares
			// <path> exactly against the torrent's name or a file subpath. Trim
			// it once, up front, so the validation below, the root check, and
			// the call itself all see the value the server will actually be
			// asked about — a bare "/" is an empty path, not a valid one.
			path, newName := strings.TrimSuffix(args[1], "/"), args[2]

			if strings.TrimSpace(path) == "" {
				return fmt.Errorf("path must not be empty")
			}
			if strings.TrimSpace(newName) == "" {
				return fmt.Errorf("new name must not be empty")
			}

			backend, serverURL, err := dial(cmd, server)
			if err != nil {
				return err
			}

			// Fetch the detail first: it resolves a hash selector to the numeric
			// id the rename and the post-rename read are keyed by, and supplies
			// the current name the root check compares <path> against.
			before, err := resolveTorrent(cmd, backend, serverURL, sel, args[0])
			if err != nil {
				return err
			}

			// The torrent root is the torrent's own name, for both single- and
			// multi-file torrents.
			root := path == before.Name

			if root && !yes {
				confirmed, err := confirmGate(
					cmd,
					"rename the torrent root",
					rootRenamePrompt(before.Name, newName),
				)
				if err != nil {
					return err
				}
				if !confirmed {
					return nil
				}
			}

			// Rename by the resolved id rather than the original selector, so the
			// torrent whose name was just checked is the one that is renamed.
			target := transmission.Selector{IDs: []int64{before.ID}}

			echo, err := backend.RenamePath(cmd.Context(), target, path, newName)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			// Re-read the detail: a root rename changes the torrent's name, and
			// the spec directs clients to refresh after a rename rather than
			// assume.
			after, err := backend.TorrentDetail(cmd.Context(), before.ID)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			result := renameResult{
				Action:  "rename",
				Path:    firstNonEmpty(echo.Path, path),
				Name:    firstNonEmpty(echo.Name, newName),
				Root:    root,
				Torrent: mutationTorrent{ID: after.ID, Hash: after.Hash, Name: after.Name},
			}

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), result, jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), result)
			}

			writeRenamePlaintext(cmd, result)

			return nil
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	return cmd
}

// rootRenamePrompt builds the interactive confirmation for a root rename. It
// names the torrent being renamed and what it becomes, since the whole point of
// the gate is that the user may have meant a file inside the torrent. The
// torrent name is server-derived and the new name is echoed back verbatim, so
// both are sanitized before they reach the terminal.
func rootRenamePrompt(current, name string) string {
	return fmt.Sprintf(
		"Rename torrent %q to %q? [y/N] ",
		output.SanitizeTerminal(current),
		output.SanitizeTerminal(name),
	)
}

// writeRenamePlaintext prints a single-line confirmation. A root rename reads
// "Renamed torrent <old> to <new>"; anything else names the path and the torrent
// it lives in. Every string is passed through output.SanitizeTerminal: the
// torrent name is swarm-supplied, and the path and name come back off the wire
// as the server's echo. The JSON/jq paths never reach here.
func writeRenamePlaintext(cmd *cobra.Command, result renameResult) {
	out := cmd.OutOrStdout()

	if result.Root {
		_, _ = fmt.Fprintf(
			out,
			"Renamed torrent %s to %s\n",
			output.SanitizeTerminal(result.Path),
			output.SanitizeTerminal(result.Name),
		)

		return
	}

	_, _ = fmt.Fprintf(
		out,
		"Renamed %s to %s in torrent %s\n",
		output.SanitizeTerminal(result.Path),
		output.SanitizeTerminal(result.Name),
		output.SanitizeTerminal(result.Torrent.Name),
	)
}

// firstNonEmpty returns s when it is non-empty, otherwise fallback. It keeps the
// rename contract populated when a server answers a successful rename with an
// empty echo.
func firstNonEmpty(s, fallback string) string {
	if s != "" {
		return s
	}

	return fallback
}
