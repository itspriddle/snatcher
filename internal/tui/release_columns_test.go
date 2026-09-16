package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/prowlarr"
)

// TestReleaseColumnsMirrorSearch pins the results table to searchColumns in
// internal/commands/search.go: the same headings, less "#", in the same order,
// with the same alignment and renderings. internal/tui cannot import
// internal/commands, so when a search column changes there, this is what says
// the TUI's copy has to follow.
func TestReleaseColumnsMirrorSearch(t *testing.T) {
	headers := make([]string, 0, len(releaseColumnDefs))
	right := make([]bool, 0, len(releaseColumnDefs))

	for _, c := range releaseColumnDefs {
		headers = append(headers, strings.ToUpper(c.title))
		right = append(right, c.right)
	}

	assert.Equal(t, []string{"AGE", "TITLE", "INDEXER", "SIZE", "GRABS", "SEEDERS", "LEECHERS"}, headers)
	assert.Equal(t, []bool{true, false, false, true, true, true, true}, right)

	require.Len(t, allReleaseColumnKeys, len(releaseColumnDefs))

	for i, key := range allReleaseColumnKeys {
		assert.Equalf(t, key, releaseColumnDefs[i].key, "column %d", i)
		assert.Equalf(t, key == releaseTitleKey, releaseColumnDefs[i].width == 0, "%s: only title flexes", key)
	}

	seven := 7
	r := prowlarr.Release{Title: "t\x1b[2Jx", Indexer: "i\x07", Age: 12, Size: 1536, Grabs: &seven}

	cells := map[string]string{}
	for _, c := range releaseColumnDefs {
		cells[c.key] = c.render(r)
	}

	assert.Equal(t, map[string]string{
		"age": "12d", "title": "t[2Jx", "indexer": "i", "size": "1.5 KB",
		"grabs": "7", "seeders": "-", "leechers": "-",
	}, cells)
	assert.Equal(t, "-", searchAge(-1))
}

// TestTorrentWriteMirrorsSearchDownload pins the filename derivation and write
// rules to internal/commands/search_download.go, with that file's own fixtures.
func TestTorrentWriteMirrorsSearchDownload(t *testing.T) {
	esc := string(rune(0o33))

	for title, want := range map[string]string{
		"../../../tmp/evil":    "tmpevil.torrent",
		".hidden":              "hidden.torrent",
		esc + "[2Jubuntu":      "[2Jubuntu.torrent",
		"Ubuntu 24.04.torrent": "Ubuntu 24.04.torrent",
		`back\slash`:           "backslash.torrent",
		"..":                   "",
	} {
		assert.Equalf(t, want, torrentFilename(title), "%q", title)
	}

	assert.Equal(t, strings.Repeat("a", 200)+".torrent", torrentFilename(strings.Repeat("a", 400)))
	assert.Equal(t, "é", truncateBytes("éé", 3), "a cut never splits a rune")

	path := filepath.Join(t.TempDir(), "x.torrent")
	require.NoError(t, writeTorrentFile(path, []byte("data")))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	require.ErrorIs(t, writeTorrentFile(path, []byte("other")), errFileExists)

	link := filepath.Join(t.TempDir(), "dangling.torrent")
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "missing"), link))
	require.ErrorIs(t, checkTorrentPath(link), errFileExists, "a dangling symlink is something already there")

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temp file is left behind")
}
