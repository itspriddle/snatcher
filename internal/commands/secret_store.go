package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/itspriddle/snatcher/internal/config"
	"github.com/itspriddle/snatcher/internal/secrets"
)

// envSecretStore names the environment variable that overrides which secret
// store holds passwords, for every server, for one invocation.
const envSecretStore = "SNATCHER_SECRET_STORE"

// storeMode names which secret store a server's password lives in. The zero
// value is not a mode; use resolveStoreMode, which supplies the default.
type storeMode string

const (
	// storeKeyring is the default mode: the OS keyring, and only the OS
	// keyring. It never falls back to the file store when the keyring is
	// unreachable — a password is written in plaintext only when an operator
	// asks for that by name.
	storeKeyring storeMode = "keyring"

	// storeFile is the opt-in plaintext file store (see secrets.FileStore),
	// for machines with no OS keyring.
	storeFile storeMode = "file"

	// storeNone disables secret storage entirely: no password is read from or
	// written to any store.
	storeNone storeMode = "none"
)

// validStoreModes lists the accepted values, quoted, for error messages.
const validStoreModes = `"keyring", "file", "none"`

// secretStore is the behavior a store mode provides. It matches the signatures
// of both internal/secrets' package-level keyring functions and
// *secrets.FileStore, so the two are interchangeable behind it.
type secretStore interface {
	Get(serverURL string) (string, error)
	Set(serverURL, password string) error
	Delete(serverURL string) error
}

// parseStoreMode resolves an operator-written store name. Matching is
// case-insensitive and tolerates surrounding whitespace. "auto" is accepted as
// a synonym for "keyring" — the keyring mode is what snatcher picks
// automatically — and normalizes to "keyring", the canonical spelling written
// to config. An unrecognized value is an error naming the valid ones rather
// than a silent fallback, since silently ignoring "flie" would send a password
// somewhere the operator did not choose.
func parseStoreMode(raw string) (storeMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "keyring", "auto":
		return storeKeyring, nil
	case "file":
		return storeFile, nil
	case "none":
		return storeNone, nil
	default:
		return "", fmt.Errorf("invalid secret store %q: valid values are %s", raw, validStoreModes)
	}
}

// resolveStoreMode returns the store mode for srv, applying the fixed
// precedence: the SNATCHER_SECRET_STORE environment variable > the server's
// secret_store value in config > the keyring. An omitted or empty value at
// either level defers to the next, so a config file written before the field
// existed resolves to the keyring exactly as it always did.
func resolveStoreMode(srv config.Server) (storeMode, error) {
	if raw := os.Getenv(envSecretStore); strings.TrimSpace(raw) != "" {
		mode, err := parseStoreMode(raw)
		if err != nil {
			return "", fmt.Errorf("%s: %w", envSecretStore, err)
		}

		return mode, nil
	}

	if strings.TrimSpace(srv.SecretStore) != "" {
		mode, err := parseStoreMode(srv.SecretStore)
		if err != nil {
			return "", fmt.Errorf("config secret_store: %w", err)
		}

		return mode, nil
	}

	return storeKeyring, nil
}

// openStore builds the store named by mode. Nothing is read or created here;
// the file store resolves its path but does not touch it until it is used.
func openStore(mode storeMode) (secretStore, error) {
	switch mode {
	case storeKeyring:
		return keyringSecretStore{}, nil
	case storeFile:
		return secrets.NewFileStore("")
	case storeNone:
		return noSecretStore{}, nil
	default:
		return nil, fmt.Errorf("invalid secret store %q: valid values are %s", string(mode), validStoreModes)
	}
}

// serverStore resolves srv's store mode and opens that store. Every command
// that touches a password goes through it, so the mode is resolved the same way
// everywhere.
func serverStore(srv config.Server) (secretStore, error) {
	mode, err := resolveStoreMode(srv)
	if err != nil {
		return nil, err
	}

	return openStore(mode)
}

// keyringSecretStore adapts internal/secrets' package-level keyring functions to
// the secretStore interface. It is the default mode, and the only one that can
// report secrets.ErrUnavailable.
type keyringSecretStore struct{}

func (keyringSecretStore) Get(serverURL string) (string, error) {
	return secrets.Get(serverURL)
}

func (keyringSecretStore) Set(serverURL, password string) error {
	return secrets.Set(serverURL, password)
}

func (keyringSecretStore) Delete(serverURL string) error {
	return secrets.Delete(serverURL)
}

// noSecretStore is the "none" mode: it holds nothing. Reads report ErrNotFound,
// which every caller already treats as "no stored secret", so read paths and
// pre-clears carry on unchanged. A write is refused rather than accepted and
// dropped, so a password is never silently discarded.
type noSecretStore struct{}

func (noSecretStore) Get(serverURL string) (string, error) {
	return "", fmt.Errorf("%w for %q", secrets.ErrNotFound, serverURL)
}

func (noSecretStore) Set(serverURL, _ string) error {
	return fmt.Errorf("secret store is %q for %q: the password cannot be stored", storeNone, serverURL)
}

func (noSecretStore) Delete(serverURL string) error {
	return fmt.Errorf("%w for %q", secrets.ErrNotFound, serverURL)
}
