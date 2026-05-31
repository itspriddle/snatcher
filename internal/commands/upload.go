package commands

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/torrent"
)

// errUploadFailures is returned when one or more sources fail to add. It carries
// no detail of its own: the per-source errors are already printed to stderr (and
// recorded in the JSON contract). It exists only so the command exits non-zero
// after continue-on-error processing.
var errUploadFailures = errors.New("one or more sources failed")

// NewUploadCmd builds the "upload" command, which adds torrents to the
// configured server from local .torrent files, HTTP(S) URLs, and magnet links.
// Each positional argument is auto-classified with torrent.ClassifySource;
// torrents are added paused unless --start is given. Processing is
// continue-on-error: a per-source failure is reported but does not abort the
// remaining sources, and the command exits non-zero if any source failed.
// Render precedence mirrors the other commands: --jq → --json → plaintext.
func NewUploadCmd() *cobra.Command {
	var (
		configPath  string
		server      string
		asJSON      bool
		jqExpr      string
		start       bool
		downloadDir string
		deleteFile  bool
		labels      []string
		quiet       bool
	)

	cmd := &cobra.Command{
		Use:   "upload [file|url|magnet]...",
		Short: "Upload torrents from files, URLs, or magnet links",
		Long: "Add torrents to the server from local .torrent files, HTTP(S) URLs, " +
			"or magnet links.\n\n" +
			"Each argument is auto-detected as a file, URL, or magnet link. Torrents " +
			"are added paused unless --start is given; use --download-dir to choose a " +
			"server-side destination and --label (repeatable) to tag them. Use --delete " +
			"to remove a local .torrent after it is successfully added, and --quiet to " +
			"suppress per-source confirmation lines. Multiple sources are processed " +
			"continue-on-error: a failure adding one source is reported but does not stop " +
			"the rest, and the command exits non-zero if any source failed. With no " +
			"positional arguments, sources are read from stdin (one per line); a bare " +
			"'snatcher torrent upload' with no sources and no piped stdin is an error, not a no-op.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sources, err := uploadSources(cmd, args)
			if err != nil {
				return err
			}
			if len(sources) == 0 {
				return errors.New("specify at least one file, url, or magnet link")
			}

			backend, _, err := dial(configPath, server)
			if err != nil {
				return err
			}

			opts := uploadOptions{
				start:       start,
				downloadDir: downloadDir,
				deleteFile:  deleteFile,
				labels:      labels,
			}

			results := make([]uploadResult, 0, len(sources))
			failed := false

			for _, arg := range sources {
				res := uploadSource(cmd, backend, arg, opts)
				if res.Error != "" {
					failed = true
				}
				results = append(results, res)
			}

			if err := renderUpload(cmd, results, jqExpr, asJSON, quiet); err != nil {
				return err
			}

			if failed {
				// Return the batch-failure sentinel unwrapped: the per-source
				// causes (often purely local, e.g. a missing .torrent) were already
				// written to stderr, and wrapping in "connecting to <server>" would
				// misattribute them to a transport failure. This mirrors how the
				// other commands surface non-transport errors (e.g. resolveMutation).
				return errUploadFailures
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVar(&start, "start", false, "start torrents immediately instead of adding them paused")
	cmd.Flags().StringVar(&downloadDir, "download-dir", "", "server-side destination directory")
	cmd.Flags().BoolVarP(&deleteFile, "delete", "d", false, "delete the local .torrent after a successful add")
	cmd.Flags().StringArrayVar(&labels, "label", nil, "label to apply to added torrents (repeatable)")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress per-source confirmation lines")

	return cmd
}

// uploadSources resolves the sources to process: the positional args when any
// are present, otherwise one whitespace-trimmed, non-blank source per line read
// from stdin. Positional args and stdin are never mixed — stdin is consulted
// only when no positional args are given.
func uploadSources(cmd *cobra.Command, args []string) ([]string, error) {
	if len(args) > 0 {
		return args, nil
	}

	var sources []string

	scanner := bufio.NewScanner(cmd.InOrStdin())
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		sources = append(sources, line)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("reading sources from stdin: %w", err)
	}

	return sources, nil
}

// uploadOptions carries the per-invocation add options shared across every
// source, sparing uploadSource a long positional argument list.
type uploadOptions struct {
	// start adds torrents running instead of paused.
	start bool

	// downloadDir is the server-side destination directory (empty = server default).
	downloadDir string

	// deleteFile removes a local .torrent after a successful, non-duplicate add.
	deleteFile bool

	// labels are applied to every added torrent.
	labels []string
}

// uploadSource classifies arg, builds the neutral AddRequest, adds the torrent,
// and returns a per-source uploadResult. For a file source it reads the file
// bytes (surfacing a clear error on a missing/unreadable file) before adding.
// On a per-source failure it writes the error to stderr (continue-on-error) and
// records it in the returned result; a duplicate is a success, not an error.
// With opts.deleteFile, a successful non-duplicate file add deletes the source
// .torrent; a delete failure warns on stderr but does not fail the add.
func uploadSource(cmd *cobra.Command, backend torrent.Backend, arg string, opts uploadOptions) uploadResult {
	source := torrent.ClassifySource(arg)

	res := uploadResult{Source: source.String(), Input: arg}

	req := torrent.AddRequest{
		DownloadDir: opts.downloadDir,
		Paused:      !opts.start,
		Labels:      opts.labels,
	}

	switch source {
	case torrent.SourceFile:
		data, err := os.ReadFile(arg)
		if err != nil {
			return res.withError(cmd, fmt.Errorf("reading %s: %w", arg, err))
		}
		req.Metainfo = data
	case torrent.SourceURL:
		req.URL = arg
	case torrent.SourceMagnet:
		req.Magnet = arg
	}

	added, err := backend.AddTorrent(cmd.Context(), req)
	if err != nil {
		return res.withError(cmd, fmt.Errorf("adding %s: %w", arg, err))
	}

	res.ID = added.ID
	res.Name = added.Name
	res.Hash = added.Hash
	res.Duplicate = added.Duplicate

	// --delete removes the consumed .torrent only after a successful, non-duplicate
	// file add. A delete failure warns but does not fail the add; deleted is set
	// only when the removal succeeded. URL/magnet sources and duplicates are
	// untouched.
	if opts.deleteFile && source == torrent.SourceFile && !added.Duplicate {
		if err := os.Remove(arg); err != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s\n", output.SanitizeTerminal(fmt.Sprintf("deleting %s: %v", arg, err)))
		} else {
			res.Deleted = true
		}
	}

	return res
}

// uploadResult is one element of the per-source results array emitted under
// --json/--jq. Its json tags are a deliberate scripting contract: every element
// carries source and input; a success additionally carries id/name/hash and the
// duplicate flag, while a failure carries only an error string. Name is a
// server-derived, untrusted string — emitted verbatim in JSON, but passed
// through output.SanitizeTerminal on the human path (see writeUploadPlaintext).
type uploadResult struct {
	// Source is the classified source kind: "file", "url", or "magnet".
	Source string `json:"source"`

	// Input is the original positional argument, verbatim.
	Input string `json:"input"`

	// ID is the backend's numeric id for the added torrent. Omitted on failure.
	ID int64 `json:"id,omitempty"`

	// Name is the torrent's display name. Omitted on failure.
	Name string `json:"name,omitempty"`

	// Hash is the torrent's info-hash. Omitted on failure.
	Hash string `json:"hash,omitempty"`

	// Duplicate reports that the torrent was already present on the server (a
	// success, not an error). Omitted when false.
	Duplicate bool `json:"duplicate,omitempty"`

	// Deleted reports that the source .torrent was deleted after a successful add
	// (only set under --delete for a non-duplicate file source). Omitted when false.
	Deleted bool `json:"deleted,omitempty"`

	// Error is a human-readable description of why this source failed. Empty on
	// success.
	Error string `json:"error,omitempty"`
}

// withError records err on the result and prints it to stderr so the human path
// surfaces per-source failures during continue-on-error processing. The error
// text is sanitized before reaching the terminal since it can embed a
// server-derived string. It returns the mutated result for one-line use.
func (r uploadResult) withError(cmd *cobra.Command, err error) uploadResult {
	r.Error = err.Error()
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", output.SanitizeTerminal(r.Error))

	return r
}

// renderUpload writes the per-source results in the appropriate output mode:
// --jq filters the JSON array, --json emits the structured array, and otherwise
// a plain human line is printed per source. Render precedence mirrors the other
// commands: --jq → --json → plaintext. --quiet suppresses only the plaintext
// confirmation lines; it never alters the JSON/jq contract.
func renderUpload(cmd *cobra.Command, results []uploadResult, jqExpr string, asJSON, quiet bool) error {
	if jqExpr != "" {
		return output.JQ(cmd.OutOrStdout(), results, jqExpr)
	}

	if asJSON {
		return output.JSON(cmd.OutOrStdout(), results)
	}

	if !quiet {
		writeUploadPlaintext(cmd, results)
	}

	return nil
}

// writeUploadPlaintext prints one line per successful source on stdout, e.g.
// "Added [file]: ubuntu.iso" or "Duplicate [magnet]: ubuntu.iso". Failures are
// omitted here — they were already written to stderr as they occurred. Every
// server-derived name is passed through output.SanitizeTerminal, since names are
// untrusted swarm-supplied strings.
func writeUploadPlaintext(cmd *cobra.Command, results []uploadResult) {
	out := cmd.OutOrStdout()

	for _, r := range results {
		if r.Error != "" {
			continue
		}

		verb := "Added"
		if r.Duplicate {
			verb = "Duplicate"
		}

		_, _ = fmt.Fprintf(out, "%s [%s]: %s\n", verb, r.Source, output.SanitizeTerminal(r.Name))
	}
}
