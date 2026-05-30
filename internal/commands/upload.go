package commands

import (
	"errors"
	"fmt"
	"os"

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
		asJSON      bool
		jqExpr      string
		start       bool
		downloadDir string
	)

	cmd := &cobra.Command{
		Use:   "upload [file|url|magnet]...",
		Short: "Upload torrents from files, URLs, or magnet links",
		Long: "Add torrents to the server from local .torrent files, HTTP(S) URLs, " +
			"or magnet links.\n\n" +
			"Each argument is auto-detected as a file, URL, or magnet link. Torrents " +
			"are added paused unless --start is given; use --download-dir to choose a " +
			"server-side destination. Multiple sources are processed continue-on-error: " +
			"a failure adding one source is reported but does not stop the rest, and the " +
			"command exits non-zero if any source failed. A bare 'snatcher upload' with " +
			"no sources is an error, not a no-op.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errors.New("specify at least one file, url, or magnet link")
			}

			backend, serverURL, err := dial(configPath)
			if err != nil {
				return err
			}

			results := make([]uploadResult, 0, len(args))
			failed := false

			for _, arg := range args {
				res := uploadSource(cmd, backend, arg, start, downloadDir)
				if res.Error != "" {
					failed = true
				}
				results = append(results, res)
			}

			if err := renderUpload(cmd, results, jqExpr, asJSON); err != nil {
				return err
			}

			if failed {
				return fmt.Errorf("connecting to %s: %w", serverURL, errUploadFailures)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVar(&start, "start", false, "start torrents immediately instead of adding them paused")
	cmd.Flags().StringVar(&downloadDir, "download-dir", "", "server-side destination directory")

	return cmd
}

// uploadSource classifies arg, builds the neutral AddRequest, adds the torrent,
// and returns a per-source uploadResult. For a file source it reads the file
// bytes (surfacing a clear error on a missing/unreadable file) before adding.
// On a per-source failure it writes the error to stderr (continue-on-error) and
// records it in the returned result; a duplicate is a success, not an error.
func uploadSource(cmd *cobra.Command, backend torrent.Backend, arg string, start bool, downloadDir string) uploadResult {
	source := torrent.ClassifySource(arg)

	res := uploadResult{Source: source.String(), Input: arg}

	req := torrent.AddRequest{
		DownloadDir: downloadDir,
		Paused:      !start,
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
// commands: --jq → --json → plaintext.
func renderUpload(cmd *cobra.Command, results []uploadResult, jqExpr string, asJSON bool) error {
	if jqExpr != "" {
		return output.JQ(cmd.OutOrStdout(), results, jqExpr)
	}

	if asJSON {
		return output.JSON(cmd.OutOrStdout(), results)
	}

	writeUploadPlaintext(cmd, results)

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
