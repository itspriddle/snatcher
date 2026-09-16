package tui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// The .torrent write rules, internal/commands' downloadExt, downloadFilePerm,
// and downloadNameLimit (search_download.go). internal/tui does not import
// internal/commands, so these and the functions below are copies pinned by
// TestTorrentWriteMirrorsSearchDownload.
const (
	torrentExt       = ".torrent"
	torrentFilePerm  = 0o600
	torrentNameLimit = 200
)

// torrentFilename is the name w suggests for a release: its title reduced to one
// safe path segment, with .torrent added unless it already ends in it. It is
// empty when nothing usable survives.
func torrentFilename(title string) string {
	name := safeFilename(title)
	if name == "" {
		return ""
	}

	if !strings.EqualFold(filepath.Ext(name), torrentExt) {
		name += torrentExt
	}

	return name
}

// safeFilename reduces a server-supplied title to a single path segment: no
// separators, no leading or trailing dots or spaces, no control bytes. It is
// internal/commands' safeFilename.
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

	cleaned = strings.Trim(cleaned, " .")

	return strings.Trim(truncateBytes(cleaned, torrentNameLimit), " .")
}

// truncateBytes cuts s to at most limit bytes on a rune boundary. It is
// internal/commands' truncateBytes.
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

// errFileExists is why a write is refused: nothing is ever overwritten.
var errFileExists = errors.New("already exists; snatcher will not overwrite it")

// checkTorrentPath refuses a path that already names something. Lstat, so a
// dangling symlink counts as something.
func checkTorrentPath(path string) error {
	_, err := os.Lstat(path)

	switch {
	case err == nil:
		return fmt.Errorf("%s %w", path, errFileExists)
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("checking %s: %w", path, err)
	}

	return nil
}

// writeTorrentFile writes data to path at 0600, through a temp file in the same
// directory and a rename, refusing to replace anything already there. It is
// internal/commands' writeDownloadFile.
func writeTorrentFile(path string, data []byte) error {
	if err := checkTorrentPath(path); err != nil {
		return err
	}

	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".snatcher-*.torrent")
	if err != nil {
		return fmt.Errorf("creating temp file in %q: %w", dir, err)
	}

	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(torrentFilePerm); err != nil {
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
