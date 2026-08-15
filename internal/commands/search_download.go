package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/prowlarr"
)

const (
	// downloadStdout is the --output value that sends the bytes to stdout instead
	// of writing a file, spelled the way every unix tool spells it.
	downloadStdout = "-"

	// downloadExt is the extension a filename derived from a release title ends
	// in. Prowlarr also serves .nzb for a usenet release, but a derived name is a
	// convenience with an escape (--output), not a content sniffer.
	downloadExt = ".torrent"

	// downloadFilePerm is the mode a written file is created at. A .torrent for a
	// private tracker embeds a passkey, so it is nobody else's business.
	downloadFilePerm = 0o600

	// downloadNameLimit bounds a derived filename's base, in bytes, below the 255
	// most filesystems stop at. A release title is occasionally far longer than
	// anything that can be a filename.
	downloadNameLimit = 200
)

// searchDownloadResult is what a completed download reports. Its json tags are
// the scripting contract: where the bytes went, how many there were, and which
// release they came from.
type searchDownloadResult struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
	Title string `json:"title"`
	GUID  string `json:"guid"`
}

// newSearchDownloadCmd builds "search download", which writes one release's
// .torrent bytes to a local file.
//
// It is the local counterpart of "search grab": a grab delegates the release to
// Prowlarr's own download client, while a download puts the file in the caller's
// hands to feed to "snatcher torrent add", to another machine, or anywhere else.
//
// The release is named exactly as a grab names it — see search_release.go for
// both forms — but only a release a search *described* carries a download url.
// The --guid form names a release for Prowlarr to resolve rather than describing
// one, so it has no url to fetch and says so; that is the one place where the two
// commands' otherwise identical selection differs, and it is documented in the
// help text rather than left to be discovered.
func newSearchDownloadCmd() *cobra.Command {
	var (
		server  string
		asJSON  bool
		jqExpr  string
		outPath string
		sel     releaseSelector
	)

	cmd := &cobra.Command{
		Use:   "download [query] [n]",
		Short: "Write a search result's .torrent file to disk",
		Long: "Write one release's .torrent file to disk.\n\n" +
			"Nothing is sent to a download client: the bytes land locally, for " +
			"\"snatcher torrent add\" or anywhere else. Use \"snatcher search grab\" " +
			"to hand the release to Prowlarr's own download client instead.\n\n" +
			"Name the release in one of two ways:\n\n" +
			"  snatcher search download \"ubuntu\" 3\n" +
			"  snatcher search download --guid <guid> --indexer 3\n\n" +
			"The first form re-runs the search and resolves the 1-based result " +
			"number from the fresh results — snatcher caches no result state between " +
			"invocations — so it names the release you saw only when the same " +
			"--indexer, --category, --limit, --offset, --pages, and --search-type " +
			"are passed, and only while the results have not shifted. The resolved " +
			"release is echoed to stderr before it is fetched so a mismatch is on " +
			"screen.\n\n" +
			"The second form names the release outright and re-runs nothing, but " +
			"Prowlarr resolves a guid only when grabbing: a guid names a release " +
			"without describing one, and a download needs the release's own download " +
			"url, which only a search returns. Use it with \"snatcher search grab\", " +
			"and download by position.\n\n" +
			"With no --output the file is named after the release title, reduced to " +
			"one safe filename, and written to the current directory. An existing " +
			"file is never overwritten — there is no --force; write elsewhere with " +
			"--output. \"--output -\" writes the raw bytes to stdout and prints " +
			"nothing else.\n\n" +
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

			if err := checkDownloadable(plan.target, release); err != nil {
				return err
			}

			// What is about to be fetched is named on stderr in every mode: it is a
			// human safeguard, not a result, and with "--output -" it is the only
			// thing that says what the bytes are.
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Downloading: %s\n",
				output.SanitizeTerminal(releaseLabel(release)))

			data, err := client.Download(cmd.Context(), release)
			if err != nil {
				return fmt.Errorf("downloading %s: %w", releaseLabel(release), err)
			}

			// Stdout is the file: a byte of anything else would corrupt it.
			if outPath == downloadStdout {
				_, err := cmd.OutOrStdout().Write(data)

				return err
			}

			path, err := downloadDestination(outPath, release.Title)
			if err != nil {
				return err
			}

			if err := writeDownloadFile(path, data); err != nil {
				return err
			}

			result := searchDownloadResult{
				Path:  path,
				Bytes: len(data),
				Title: release.Title,
				GUID:  release.GUID,
			}

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), result, jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), result)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s (%s)\n",
				output.SanitizeTerminal(path), output.Bytes(int64(len(data))))

			return nil
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().StringVarP(&outPath, "output", "o", "",
		`destination file, or "-" for stdout (default: the release title, in the current directory)`)
	sel.bind(cmd)

	return cmd
}

// checkDownloadable reports why the resolved release cannot be fetched, or nil
// when it can. Prowlarr's own error for a release with no download url is
// accurate but unactionable; these say what to do instead, which needs the
// target the caller named the release with.
func checkDownloadable(target searchTarget, release prowlarr.Release) error {
	if strings.TrimSpace(release.DownloadURL) != "" {
		return nil
	}

	// The exact form is a reference, not a description: guid plus indexer is the
	// whole payload, and Prowlarr resolves the rest only when it grabs. There is
	// no url on it to fetch and never was one.
	if target.exact() {
		return errors.New(
			"--guid names a release without describing one, and Prowlarr resolves it only when grabbing: " +
				"a download needs the release's own download url, which only a search returns. " +
				`Download by position (snatcher search download "<query>" <n>), ` +
				"or hand the release to Prowlarr with \"snatcher search grab --guid\"")
	}

	if release.MagnetURL != "" {
		return fmt.Errorf(
			"release %q publishes no download_url, only a magnet_url: there is no file to write. "+
				"Read the link with: snatcher search %q --jq '.[] | select(.index == %d) | .magnet_url'",
			release.Title, target.query, target.index)
	}

	return fmt.Errorf(
		"release %q publishes neither a download_url nor a magnet_url: the indexer offered nothing to fetch",
		release.Title)
}

// downloadDestination settles the path to write. An explicit --output is taken
// as given — the caller's own path, in whatever directory they named. With none,
// the name is derived from the release title and lands in the current directory.
//
// The name is never taken from a Content-Disposition header: that is a
// server-controlled string naming a file on the caller's disk, and honoring it
// would let an indexer choose where the bytes go.
func downloadDestination(outPath, title string) (string, error) {
	if outPath != "" {
		return outPath, nil
	}

	name := safeFilename(title)
	if name == "" {
		return "", fmt.Errorf(
			"cannot derive a filename from release title %q: name the file with --output", title)
	}

	// A release title already ending in .torrent keeps the one it has rather than
	// collecting a second.
	if !strings.EqualFold(filepath.Ext(name), downloadExt) {
		name += downloadExt
	}

	return name, nil
}

// safeFilename reduces a server-supplied title to a single path segment, or to
// the empty string when nothing usable survives.
//
// Three things are stripped, each for its own reason: path separators, so the
// result names a file in the current directory and never a path out of it;
// leading dots, so ".." cannot climb and a title starting with a dot does not
// write a hidden file; and control bytes, since a filename is echoed back by
// every ls, shell prompt, and completion listing, where an escape sequence would
// be interpreted.
func safeFilename(title string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r < ' ', r == 0x7f:
			return -1
		case r == '/', r == '\\':
			return -1
		default:
			return r
		}
	}, title)

	// Trimming both ends of dots and spaces is what disposes of "..", of a
	// leading dot, and of a trailing dot or space that some filesystems silently
	// drop from a name.
	cleaned = strings.Trim(cleaned, " .")

	return strings.Trim(truncateBytes(cleaned, downloadNameLimit), " .")
}

// truncateBytes cuts s to at most limit bytes, backing up to a rune boundary so
// the result is never invalid UTF-8.
func truncateBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}

	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}

	return s[:cut]
}

// writeDownloadFile writes data to path without replacing anything already
// there and without ever leaving a partial file behind.
//
// The bytes go to a temp file in the destination's own directory and are renamed
// over the target, so an interrupted write leaves no half-written .torrent for a
// client to choke on, and the mode is 0600 no matter what the umask says. The
// existence check is a check, not a lock: a file appearing between the two is
// vanishingly unlikely and would be replaced. Nothing here is a --force, which
// this command deliberately does not have.
func writeDownloadFile(path string, data []byte) error {
	// Lstat, not Stat: a dangling symlink at the path is something already there,
	// and following it is exactly what must not happen.
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s already exists: snatcher will not overwrite it, write elsewhere with --output", path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checking %s: %w", path, err)
	}

	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".snatcher-*.torrent")
	if err != nil {
		return fmt.Errorf("creating temp file in %q: %w", dir, err)
	}

	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		// A no-op once the rename below has succeeded; the cleanup that matters is
		// the one on every early return.
		_ = os.Remove(tmpName)
	}()

	// CreateTemp already creates at 0600, but say so explicitly: this is the mode
	// the renamed file inherits.
	if err := tmp.Chmod(downloadFilePerm); err != nil {
		return fmt.Errorf("setting permissions on %q: %w", tmpName, err)
	}

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("writing %q: %w", tmpName, err)
	}

	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("syncing %q: %w", tmpName, err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %q: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}
