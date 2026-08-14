package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/itspriddle/snatcher/internal/config"
	"github.com/itspriddle/snatcher/internal/secrets"
)

// envSecretStore names the environment variable that overrides which secret
// store holds passwords, for every server, for one invocation.
const envSecretStore = "SNATCHER_SECRET_STORE"

// storeUnavailableAdvice is the fatal counterpart to storeUnavailableRemedy: it
// is returned when a password has been typed and there is nowhere to put it, so
// it names every way forward rather than only the preferred one. Like every
// other unavailable message it names no provider internals.
const storeUnavailableAdvice = "the OS secret store could not be reached, so the password cannot be stored and nothing was saved: " +
	"install or start an OS secret service; " +
	"or re-run with --store=file to keep the password in a plaintext file at mode 0600; " +
	"or re-run with --store=none to register the server with no stored password"

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

// storeForPassword resolves and opens the store srv's password belongs in and,
// when there is a password to store, probes up front that the store can hold
// one — without writing anything.
//
// Probing first is what keeps "server add" atomic. A password with nowhere to go
// must abort the command, naming every way forward, rather than registering a
// server that silently has no password. An empty password is not probed at all:
// there is nothing to persist, so an unreachable store cannot lose anything.
func storeForPassword(srv config.Server, password string) (secretStore, error) {
	mode, err := resolveStoreMode(srv)
	if err != nil {
		return nil, err
	}

	store, err := openStore(mode)
	if err != nil {
		return nil, err
	}

	if password == "" {
		return store, nil
	}

	if mode == storeNone {
		return nil, fmt.Errorf(
			"the secret store for %q is %q: the password cannot be stored and would be discarded, so nothing was saved; re-run with an empty password, or choose a store with --store=keyring or --store=file",
			srv.URL, storeNone,
		)
	}

	// A read is the only way to ask "is this store there?" without changing it.
	// No entry for this URL is the expected answer for a new server and still
	// proves the store answered.
	if _, err := store.Get(srv.URL); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		if errors.Is(err, secrets.ErrUnavailable) {
			return nil, errors.New(storeUnavailableAdvice)
		}

		return nil, err
	}

	return store, nil
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
