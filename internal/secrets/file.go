package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// dirPerm is the permission for created parent directories.
	dirPerm = 0o700

	// filePerm is the permission enforced on the secrets file.
	filePerm = 0o600

	// groupOtherPerm masks the permission bits that must be clear on the
	// secrets file: it holds plaintext passwords, so no group or other access
	// is tolerated.
	groupOtherPerm = 0o077
)

// FileStore is a plaintext, file-backed secret store: a JSON object of server
// URL to password, held in one file at mode 0600 under a 0700 directory. It
// offers the same Get/Set/Delete semantics as the keyring path — including
// ErrNotFound for a URL it holds no entry for — and uses the same key, so the
// two stores are interchangeable.
//
// It exists for machines with no OS keyring, where the package-level functions
// can only ever return ErrUnavailable. Passwords are stored in plaintext by
// design: this is meaningfully less safe than the OS keyring, so a FileStore is
// only ever built when an operator explicitly asks for one.
type FileStore struct {
	path string
}

// NewFileStore returns a store backed by the JSON file at path. An empty path
// resolves to DefaultFilePath, so callers that do not need to override the
// location (tests do) can pass "".
//
// Nothing is read or created until the store is used.
func NewFileStore(path string) (*FileStore, error) {
	if path == "" {
		p, err := DefaultFilePath()
		if err != nil {
			return nil, err
		}
		path = p
	}

	return &FileStore{path: path}, nil
}

// DefaultFilePath returns the default secrets file path. It resolves to
// $XDG_STATE_HOME/snatcher/secrets.json when XDG_STATE_HOME is set, otherwise
// ~/.local/state/snatcher/secrets.json. This is identical on macOS and Linux;
// as in internal/config, os.UserConfigDir()-style helpers are avoided because
// their behavior diverges across platforms.
func DefaultFilePath() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "snatcher", "secrets.json"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("secrets: resolving home directory: %w", err)
	}

	return filepath.Join(home, ".local", "state", "snatcher", "secrets.json"), nil
}

// Path returns the file this store reads and writes.
func (s *FileStore) Path() string {
	return s.path
}

// Get retrieves the password stored for serverURL. A file that does not exist,
// and a file that holds no entry for serverURL, both return ErrNotFound.
func (s *FileStore) Get(serverURL string) (string, error) {
	entries, err := s.load()
	if err != nil {
		return "", err
	}

	password, ok := entries[serverURL]
	if !ok {
		return "", missingEntry(serverURL)
	}

	return password, nil
}

// Set stores password for serverURL, overwriting any existing entry for that
// URL and leaving every other entry intact. The file is replaced atomically at
// mode 0600, creating parent directories at 0700.
//
// Because the existing entries are read back first, an over-permissive file is
// rejected here too rather than being silently rewritten.
func (s *FileStore) Set(serverURL, password string) error {
	entries, err := s.load()
	if err != nil {
		return err
	}

	entries[serverURL] = password

	return s.save(entries)
}

// Delete removes the stored password for serverURL. Deleting an entry that is
// not there — including from a file that does not exist — returns ErrNotFound,
// matching the keyring path.
func (s *FileStore) Delete(serverURL string) error {
	entries, err := s.load()
	if err != nil {
		return err
	}

	if _, ok := entries[serverURL]; !ok {
		return missingEntry(serverURL)
	}

	delete(entries, serverURL)

	return s.save(entries)
}

// load reads the secrets file into a map. A missing file yields an empty map
// rather than an error, so a first Set creates the file and a Get or Delete
// against it reports the missing entry as ErrNotFound.
//
// A file readable by anyone but its owner is refused: the contents are
// plaintext passwords, and silently trusting a widened file would hide the
// exposure.
func (s *FileStore) load() (map[string]string, error) {
	info, err := os.Stat(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}

		return nil, fmt.Errorf("secrets: reading %q: %w", s.path, err)
	}

	if perm := info.Mode().Perm(); perm&groupOtherPerm != 0 {
		return nil, fmt.Errorf(
			"secrets: refusing to read %q: mode is %04o, it must be %04o (run: chmod 600 %s)",
			s.path, perm, os.FileMode(filePerm), s.path,
		)
	}

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("secrets: reading %q: %w", s.path, err)
	}

	var entries map[string]string
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("secrets: parsing %q: %w", s.path, err)
	}

	if entries == nil {
		entries = map[string]string{}
	}

	return entries, nil
}

// save writes entries to the secrets file atomically: a temporary file in the
// same directory is created at 0600, written, and renamed over the target, so a
// failed write never truncates the entries already stored.
func (s *FileStore) save(entries map[string]string) (err error) {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("secrets: encoding %q: %w", s.path, err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("secrets: creating secrets directory %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".secrets-*.tmp")
	if err != nil {
		return fmt.Errorf("secrets: creating temporary file in %q: %w", dir, err)
	}

	// Anything that goes wrong from here on leaves the stored entries untouched
	// and takes the half-written temporary file with it, rather than leaving a
	// plaintext password lying around under a name nothing will clean up.
	name := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()

	// os.CreateTemp already creates at 0600; setting it explicitly keeps the
	// guarantee from resting on that.
	if err = tmp.Chmod(filePerm); err != nil {
		return fmt.Errorf("secrets: setting permissions on %q: %w", name, err)
	}

	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("secrets: writing %q: %w", name, err)
	}

	if err = tmp.Close(); err != nil {
		return fmt.Errorf("secrets: closing %q: %w", name, err)
	}

	if err = os.Rename(name, s.path); err != nil {
		return fmt.Errorf("secrets: replacing %q: %w", s.path, err)
	}

	return nil
}

// missingEntry builds a not-found error for serverURL. It mirrors the keyring
// path's notFound without the keyring.ErrNotFound sentinel, which would claim a
// provider that was never consulted; callers detect either store's miss with
// errors.Is against ErrNotFound.
func missingEntry(serverURL string) error {
	return fmt.Errorf("%w for %q", ErrNotFound, serverURL)
}
