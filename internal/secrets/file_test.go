package secrets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const otherTestURL = "http://other:9091/transmission/rpc"

// newTestFileStore returns a store rooted under a temp directory, so no test
// ever reads or writes the real XDG state directory.
func newTestFileStore(t *testing.T) *FileStore {
	t.Helper()

	store, err := NewFileStore(filepath.Join(t.TempDir(), "snatcher", "secrets.json"))
	require.NoError(t, err)

	return store
}

// skipOnWindows skips tests that depend on POSIX permission bits or home
// directory layout. snatcher targets macOS and Linux.
func skipOnWindows(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("windows is not supported")
	}
}

func TestFileStoreSetGetRoundTrip(t *testing.T) {
	store := newTestFileStore(t)

	require.NoError(t, store.Set(testURL, "s3cret"))

	got, err := store.Get(testURL)
	require.NoError(t, err)
	assert.Equal(t, "s3cret", got)
}

func TestFileStoreSetOverwritesExisting(t *testing.T) {
	store := newTestFileStore(t)

	require.NoError(t, store.Set(testURL, "old"))
	require.NoError(t, store.Set(testURL, "new"))

	got, err := store.Get(testURL)
	require.NoError(t, err)
	assert.Equal(t, "new", got)
}

func TestFileStoreKeyedByServerURL(t *testing.T) {
	store := newTestFileStore(t)

	require.NoError(t, store.Set(testURL, "first"))
	require.NoError(t, store.Set(otherTestURL, "second"))

	first, err := store.Get(testURL)
	require.NoError(t, err)
	assert.Equal(t, "first", first)

	second, err := store.Get(otherTestURL)
	require.NoError(t, err)
	assert.Equal(t, "second", second)

	// The on-disk shape is a flat object keyed by the same server URL the
	// keyring path uses, so the two stores are interchangeable.
	raw, err := os.ReadFile(store.Path())
	require.NoError(t, err)

	var onDisk map[string]string
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	assert.Equal(t, map[string]string{testURL: "first", otherTestURL: "second"}, onDisk)
}

func TestFileStoreGetMissingEntryReturnsNotFound(t *testing.T) {
	store := newTestFileStore(t)

	require.NoError(t, store.Set(otherTestURL, "s3cret"))

	got, err := store.Get(testURL)
	require.ErrorIs(t, err, ErrNotFound)
	require.NotErrorIs(t, err, ErrUnavailable)
	assert.Empty(t, got)
	assert.Contains(t, err.Error(), testURL)
}

func TestFileStoreGetMissingFileReturnsNotFound(t *testing.T) {
	store := newTestFileStore(t)

	got, err := store.Get(testURL)
	require.ErrorIs(t, err, ErrNotFound)
	assert.Empty(t, got)

	// A read must not create the file.
	_, statErr := os.Stat(store.Path())
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestFileStoreDeleteRemovesEntry(t *testing.T) {
	store := newTestFileStore(t)

	require.NoError(t, store.Set(testURL, "s3cret"))
	require.NoError(t, store.Set(otherTestURL, "keep me"))
	require.NoError(t, store.Delete(testURL))

	_, err := store.Get(testURL)
	require.ErrorIs(t, err, ErrNotFound)

	// Deleting one entry must not disturb the others.
	kept, err := store.Get(otherTestURL)
	require.NoError(t, err)
	assert.Equal(t, "keep me", kept)
}

func TestFileStoreDeleteMissingReturnsNotFound(t *testing.T) {
	t.Run("entry missing", func(t *testing.T) {
		store := newTestFileStore(t)
		require.NoError(t, store.Set(otherTestURL, "s3cret"))

		err := store.Delete(testURL)
		require.ErrorIs(t, err, ErrNotFound)
		assert.Contains(t, err.Error(), testURL)
	})

	t.Run("file missing", func(t *testing.T) {
		store := newTestFileStore(t)

		err := store.Delete(testURL)
		require.ErrorIs(t, err, ErrNotFound)

		_, statErr := os.Stat(store.Path())
		assert.ErrorIs(t, statErr, os.ErrNotExist)
	})
}

func TestFileStoreCorruptFileReturnsClearError(t *testing.T) {
	store := newTestFileStore(t)

	require.NoError(t, os.MkdirAll(filepath.Dir(store.Path()), dirPerm))
	require.NoError(t, os.WriteFile(store.Path(), []byte("{not json"), filePerm))

	_, err := store.Get(testURL)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNotFound)
	require.NotErrorIs(t, err, ErrUnavailable)
	assert.Contains(t, err.Error(), store.Path())
	assert.Contains(t, err.Error(), "parsing")
}

func TestFileStoreCreatesFileAndDirectoryWithTightPermissions(t *testing.T) {
	skipOnWindows(t)

	store := newTestFileStore(t)
	require.NoError(t, store.Set(testURL, "s3cret"))

	dirInfo, err := os.Stat(filepath.Dir(store.Path()))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(dirPerm), dirInfo.Mode().Perm())

	fileInfo, err := os.Stat(store.Path())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(filePerm), fileInfo.Mode().Perm())
}

func TestFileStoreRejectsOverPermissiveFile(t *testing.T) {
	skipOnWindows(t)

	store := newTestFileStore(t)
	require.NoError(t, store.Set(testURL, "s3cret"))
	require.NoError(t, os.Chmod(store.Path(), 0o644))

	_, err := store.Get(testURL)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNotFound)
	assert.Contains(t, err.Error(), store.Path())
	assert.Contains(t, err.Error(), "0644")
	assert.Contains(t, err.Error(), "0600")

	// A widened file is refused on the write paths too, rather than silently
	// rewritten as if nothing had happened.
	require.Error(t, store.Set(testURL, "new"))
	require.Error(t, store.Delete(testURL))
}

func TestFileStoreWriteLeavesNoTemporaryFiles(t *testing.T) {
	store := newTestFileStore(t)

	require.NoError(t, store.Set(testURL, "s3cret"))
	require.NoError(t, store.Set(otherTestURL, "second"))
	require.NoError(t, store.Delete(testURL))

	entries, err := os.ReadDir(filepath.Dir(store.Path()))
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	assert.Equal(t, []string{filepath.Base(store.Path())}, names)
}

func TestFileStoreWorksWhenKeyringIsUnavailable(t *testing.T) {
	mockStoreUnavailable(t, errDBusLaunchMissing)

	store := newTestFileStore(t)
	require.NoError(t, store.Set(testURL, "s3cret"))

	got, err := store.Get(testURL)
	require.NoError(t, err)
	assert.Equal(t, "s3cret", got)
}

func TestNewFileStoreEmptyPathUsesDefault(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	store, err := NewFileStore("")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(state, "snatcher", "secrets.json"), store.Path())
}

func TestDefaultFilePathWithXDGStateSet(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	got, err := DefaultFilePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(state, "snatcher", "secrets.json"), got)
}

func TestDefaultFilePathWithXDGStateUnset(t *testing.T) {
	skipOnWindows(t)

	t.Setenv("XDG_STATE_HOME", "")

	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := DefaultFilePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".local", "state", "snatcher", "secrets.json"), got)
}
