// Package secrets stores snatcher credentials outside the config file. The
// default store is the OS keyring (macOS Keychain / Linux secret-service),
// driven by the package-level Set, Get, and Delete functions.
//
// Secrets are keyed by server URL under a single fixed service name, leaving
// room for multiple servers later. The non-secret config (server URL, username)
// lives in internal/config; only the password lives here.
//
// FileStore is the second, opt-in store: a JSON file at mode 0600 for machines
// with no OS keyring, where the keyring path can only ever return
// ErrUnavailable. It holds passwords in plaintext by design, which is
// meaningfully less safe than the OS keyring — anything running as that user,
// and any backup of the home directory, can read them. Nothing selects it
// automatically; it is used only when an operator explicitly asks for it.
//
// Every provider failure is classified into exactly one of three conditions:
// ErrNotFound (the store works, this URL has no entry), ErrUnavailable (there
// is no reachable store on this system at all), or a genuine error passed
// through as-is. Callers degrade on ErrUnavailable instead of aborting — a
// headless Linux box with no D-Bus session has no secret service, and that is
// an expected environment rather than a fatal surprise.
package secrets

import (
	"errors"
	"fmt"
	"net"
	"os/exec"
	"runtime"

	dbus "github.com/godbus/dbus/v5"
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

// ErrUnavailable signals that the secret store is not available on this system:
// there is no OS keyring to talk to, so nothing can be read, written, or
// deleted. This is distinct from ErrNotFound, which means the store answered and
// holds no entry for that URL.
//
// The canonical case is a headless Linux box with no D-Bus session, where every
// keyring call fails while trying to reach the secret service. Errors built from
// this sentinel never quote the underlying provider failure, so no provider
// internal reaches a user-facing message.
var ErrUnavailable = errors.New("secrets: secret store unavailable")

// dbusUnavailableNames are the D-Bus protocol error names that mean the Secret
// Service is not there to talk to, as opposed to it answering with a refusal.
// They are matched off dbus.Error.Name rather than the rendered message, which
// carries a human-readable body instead of the name.
var dbusUnavailableNames = map[string]bool{
	"org.freedesktop.DBus.Error.ServiceUnknown":        true,
	"org.freedesktop.DBus.Error.NoServer":              true,
	"org.freedesktop.DBus.Error.Disconnected":          true,
	"org.freedesktop.DBus.Error.Spawn.ServiceNotFound": true,
}

// Set stores password for serverURL in the OS keyring, overwriting any existing
// entry for that URL. When no secret store is reachable it returns an error
// satisfying errors.Is against ErrUnavailable.
func Set(serverURL, password string) error {
	if err := keyring.Set(service, serverURL, password); err != nil {
		return classify("storing", serverURL, err)
	}

	return nil
}

// Get retrieves the password stored for serverURL. When no entry exists it
// returns ErrNotFound (which wraps keyring.ErrNotFound); when no secret store is
// reachable it returns ErrUnavailable.
func Get(serverURL string) (string, error) {
	password, err := keyring.Get(service, serverURL)
	if err != nil {
		return "", classify("reading", serverURL, err)
	}

	return password, nil
}

// Delete removes the stored password for serverURL. Deleting a missing entry
// returns ErrNotFound (which wraps keyring.ErrNotFound); when no secret store is
// reachable it returns ErrUnavailable.
func Delete(serverURL string) error {
	if err := keyring.Delete(service, serverURL); err != nil {
		return classify("deleting", serverURL, err)
	}

	return nil
}

// classify maps a keyring provider error onto this package's conditions. op is
// the gerund naming the attempted operation ("reading", "storing", "deleting"),
// used only to build the passthrough message.
func classify(op, serverURL string, err error) error {
	switch {
	case errors.Is(err, keyring.ErrNotFound):
		return notFound(serverURL)
	case isUnavailable(err):
		return unavailable(serverURL)
	default:
		return fmt.Errorf("secrets: %s password for %q: %w", op, serverURL, err)
	}
}

// notFound builds a not-found error for serverURL that is detectable via
// errors.Is against both ErrNotFound and keyring.ErrNotFound.
func notFound(serverURL string) error {
	return fmt.Errorf("%w for %q: %w", ErrNotFound, serverURL, keyring.ErrNotFound)
}

// unavailable builds a store-unavailable error for serverURL. The provider error
// is deliberately not wrapped: its text names implementation details (the
// dbus-launch binary, D-Bus object paths) that mean nothing to an operator, and
// callers only need the condition.
func unavailable(serverURL string) error {
	return fmt.Errorf("%w for %q", ErrUnavailable, serverURL)
}

// isUnavailable reports whether err means the secret store itself could not be
// reached, rather than the store answering, on the platform this binary is
// running on.
func isUnavailable(err error) bool {
	return isUnavailableOn(runtime.GOOS, err)
}

// isUnavailableOn is isUnavailable with the platform injected, since one rule
// (how a helper binary's non-zero exit is read) differs between macOS and every
// other platform. It matches on error types and wrapped sentinels, never on
// message substrings, so a reworded provider message cannot silently reclassify
// a failure.
func isUnavailableOn(goos string, err error) bool {
	// No keyring provider is compiled in for this platform at all.
	if errors.Is(err, keyring.ErrUnsupportedPlatform) {
		return true
	}

	// A helper binary the provider shells out to is missing: dbus-launch on
	// Linux, /usr/bin/security on macOS. This is the reported headless-Linux
	// failure.
	var execErr *exec.Error
	if errors.As(err, &execErr) || errors.Is(err, exec.ErrNotFound) {
		return true
	}

	// The helper binary ran and exited non-zero, and what that means depends on
	// which helper it was. On Linux it is dbus-launch, which godbus runs with no
	// arguments at all (dbus/v5 conn_other.go), so a non-zero exit means it could
	// not bring up a session bus — the verified shape is dbus-launch installed
	// with its dbus-daemon missing ("Failed to execute message bus daemon", exit
	// 1). No session bus means no secret service, so the store is absent.
	//
	// Note that this is *not* the "dbus installed but no X11" case, which is the
	// commoner headless one: bare dbus-launch forks its own daemon and exits 0
	// there, so that configuration lands on the dbus.Error ServiceUnknown branch
	// below instead. Both were reproduced end-to-end in a Debian container.
	//
	// On macOS the provider shells out to /usr/bin/security, which exits non-zero
	// for a locked or user-denied keychain: the store answered and refused, which
	// is a real answer, not an absent store.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return goos != "darwin"
	}

	// The session bus socket is absent, or nothing is listening on it.
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return true
	}

	// D-Bus itself answered, but no secret service is registered on the bus.
	// Error replies arrive as a dbus.Error value; the pointer form is checked
	// too because dbus.NewError hands one back.
	var dbusErr dbus.Error
	if errors.As(err, &dbusErr) {
		return dbusUnavailableNames[dbusErr.Name]
	}

	var dbusErrPtr *dbus.Error
	if errors.As(err, &dbusErrPtr) {
		return dbusUnavailableNames[dbusErrPtr.Name]
	}

	return false
}
