// Package secrets stores snatcher credentials in the OS keyring (macOS Keychain
// / Linux secret-service) rather than on disk, so passwords are never written
// in plaintext.
//
// Secrets are keyed by server URL under a single fixed service name, leaving
// room for multiple servers later. The non-secret config (server URL, username)
// lives in internal/config; only the password lives here.
package secrets

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// service is the fixed keyring service name under which all snatcher secrets are
// stored. The account is the server URL, so each server gets its own entry.
const service = "snatcher"

// ErrNotFound signals that no secret exists for the given server URL. The
// returned error also wraps keyring.ErrNotFound, so callers can detect a missing
// entry with errors.Is against either sentinel without depending on the keyring
// provider directly.
var ErrNotFound = errors.New("secrets: not found")

// Set stores password for serverURL in the OS keyring, overwriting any existing
// entry for that URL.
func Set(serverURL, password string) error {
	if err := keyring.Set(service, serverURL, password); err != nil {
		return fmt.Errorf("secrets: storing password for %q: %w", serverURL, err)
	}

	return nil
}

// Get retrieves the password stored for serverURL. When no entry exists it
// returns ErrNotFound (which wraps keyring.ErrNotFound).
func Get(serverURL string) (string, error) {
	password, err := keyring.Get(service, serverURL)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", notFound(serverURL)
		}

		return "", fmt.Errorf("secrets: reading password for %q: %w", serverURL, err)
	}

	return password, nil
}

// Delete removes the stored password for serverURL. Deleting a missing entry
// returns ErrNotFound (which wraps keyring.ErrNotFound).
func Delete(serverURL string) error {
	if err := keyring.Delete(service, serverURL); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return notFound(serverURL)
		}

		return fmt.Errorf("secrets: deleting password for %q: %w", serverURL, err)
	}

	return nil
}

// notFound builds a not-found error for serverURL that is detectable via
// errors.Is against both ErrNotFound and keyring.ErrNotFound.
func notFound(serverURL string) error {
	return fmt.Errorf("%w for %q: %w", ErrNotFound, serverURL, keyring.ErrNotFound)
}
