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

// envServer names the environment variable that selects which configured server
// a command dials. dial reads it when --server is omitted, so the precedence is
// --server > this variable > the configured default server.
//
// It is not a secret-store setting; it lives beside the two that are so that
// every variable a command reads is declared in one place, and so the test
// suite can neutralize them together.
const envServer = "SNATCHER_SERVER"

// envPassword names the environment variable that supplies a server password for
// one invocation.
//
// It is not a store: nothing ever writes to it, "server add" never consults it,
// and it persists nothing. It is a read override in the same category as
// --password-stdin, sitting above every store, so that an unattended run (a
// container, a CI job) can dial with no password at rest anywhere — the usable
// counterpart to SNATCHER_SECRET_STORE=none.
const envPassword = "SNATCHER_PASSWORD"

// passwordFromEnv returns the password envPassword supplies and whether it
// supplied one. An unset or empty value supplies nothing and defers to the
// store; any other value is used verbatim, since leading or trailing whitespace
// can be part of a password.
func passwordFromEnv() (string, bool) {
	password := os.Getenv(envPassword)

	return password, password != ""
}

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

	return recordedStoreMode(srv)
}

// recordedStoreMode returns the store mode srv itself names — its secret_store
// value, or the keyring when that is omitted — ignoring SNATCHER_SECRET_STORE.
//
// This is the mode to use when the question is "where does this server's
// existing secret live", rather than "which store should this invocation read".
// "server add" always saves the store it actually wrote the password to, so the
// server's own value is the authoritative answer, and an exported environment
// variable pointing somewhere else cannot make a secret move. Honoring the
// override when deleting a secret would delete from the wrong store, and when
// reporting one would report that none is stored, both while a live password —
// plaintext, under the file store — stayed on disk.
func recordedStoreMode(srv config.Server) (storeMode, error) {
	if strings.TrimSpace(srv.SecretStore) == "" {
		return storeKeyring, nil
	}

	mode, err := parseStoreMode(srv.SecretStore)
	if err != nil {
		return "", fmt.Errorf("config secret_store: %w", err)
	}

	return mode, nil
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
// that reads or writes a password for this invocation goes through it, so the
// mode is resolved the same way everywhere.
func serverStore(srv config.Server) (secretStore, error) {
	mode, err := resolveStoreMode(srv)
	if err != nil {
		return nil, err
	}

	return openStore(mode)
}

// recordedStore opens the store srv itself names (see recordedStoreMode). It is
// the counterpart to serverStore for the paths that act on a secret a server
// already has rather than on a password for this invocation: "server remove"
// deleting it, and serverSecretStored reporting whether it exists. The only
// correct target for both is the store actually holding the secret.
func recordedStore(srv config.Server) (secretStore, error) {
	mode, err := recordedStoreMode(srv)
	if err != nil {
		return nil, err
	}

	return openStore(mode)
}

// addStoreMode resolves the store mode "server add" both writes the password to
// and records with the server.
//
// An explicit --store outranks SNATCHER_SECRET_STORE here, unlike every read
// path: the flag is the more deliberate signal, and letting an exported
// environment variable quietly overrule it would write the password to one store
// while saving the name of another. With the flag omitted the usual precedence
// applies — the environment, then the store the alias already used, then the
// keyring — and the resolved mode is saved, so a later run resolves the store
// that actually holds the password even once the variable is gone.
func addStoreMode(storeFlag string, flagChanged bool, prev config.Server) (storeMode, error) {
	if flagChanged {
		return parseStoreMode(storeFlag)
	}

	return resolveStoreMode(prev)
}

// storeForPassword opens the store mode names and, when there is a password to
// store, probes up front that the store can hold one — without writing anything.
//
// Probing first is what keeps "server add" atomic. A password with nowhere to go
// must abort the command, naming every way forward, rather than registering a
// server that silently has no password. An empty password is not probed at all:
// there is nothing to persist, so an unreachable store cannot lose anything.
func storeForPassword(mode storeMode, serverURL, password string) (secretStore, error) {
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
			serverURL, storeNone,
		)
	}

	// A read is the only way to ask "is this store there?" without changing it.
	// No entry for this URL is the expected answer for a new server and still
	// proves the store answered.
	if _, err := store.Get(serverURL); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		if errors.Is(err, secrets.ErrUnavailable) {
			return nil, errors.New(storeUnavailableAdvice)
		}

		return nil, err
	}

	return store, nil
}

// serverSecretStored answers "does this server have a stored password" for the
// two commands that report it, "server list" and "server show". It reads
// nothing but the presence of an entry; the password itself is never returned.
//
// The store is the one srv itself names (recordedStore), not the one this
// invocation resolves to. The question is about the server, so an exported
// SNATCHER_SECRET_STORE — which only ever says where to read a password *for
// this run* — must not change the answer. Reading through the override makes
// the documented unattended pattern, SNATCHER_SECRET_STORE=none plus
// SNATCHER_PASSWORD, report that no secret is stored for every server while
// plaintext passwords sit in secrets.json. Warning instead of ignoring the
// override could not repair that: the bool is what --json and --jq consumers
// read, and these two commands are how an operator audits which passwords are
// on disk.
//
// unavailable reports that no store could be reached, so the honest answer is
// "cannot tell" rather than "no". Callers still render stored as false —
// secret_stored is a bool on a stable JSON contract — and say so on stderr with
// secretStatusUnknown. A server with no URL has no key to look anything up by.
func serverSecretStored(srv config.Server) (stored, unavailable bool, err error) {
	if srv.URL == "" {
		return false, false, nil
	}

	store, err := recordedStore(srv)
	if err != nil {
		return false, false, err
	}

	_, err = store.Get(srv.URL)
	switch {
	case err == nil:
		return true, false, nil
	case errors.Is(err, secrets.ErrUnavailable):
		return false, true, nil
	case errors.Is(err, secrets.ErrNotFound):
		return false, false, nil
	default:
		return false, false, err
	}
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
