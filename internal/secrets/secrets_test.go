package secrets

import (
	"errors"
	"fmt"
	"net"
	"os/exec"
	"syscall"
	"testing"

	dbus "github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const testURL = "http://localhost:9091/transmission/rpc"

// errDBusLaunchMissing is the error a headless Linux box produces on every
// keyring call: go-keyring asks D-Bus for a session bus, D-Bus shells out to
// dbus-launch, and the lookup fails. Reproduced here as the real *exec.Error
// rather than a message string, since that is what the classifier matches.
var errDBusLaunchMissing = &exec.Error{Name: "dbus-launch", Err: exec.ErrNotFound}

// mockStoreUnavailable points the keyring provider at a mock that fails every
// call with err, simulating a system with no reachable secret store, and
// restores the working in-memory mock afterwards.
func mockStoreUnavailable(t *testing.T, err error) {
	t.Helper()

	keyring.MockInitWithError(err)
	t.Cleanup(keyring.MockInit)
}

func TestSetGetRoundTrip(t *testing.T) {
	keyring.MockInit()

	require.NoError(t, Set(testURL, "s3cret"))

	got, err := Get(testURL)
	require.NoError(t, err)
	assert.Equal(t, "s3cret", got)
}

func TestSetOverwritesExisting(t *testing.T) {
	keyring.MockInit()

	require.NoError(t, Set(testURL, "old"))
	require.NoError(t, Set(testURL, "new"))

	got, err := Get(testURL)
	require.NoError(t, err)
	assert.Equal(t, "new", got)
}

func TestGetMissingReturnsNotFound(t *testing.T) {
	keyring.MockInit()

	got, err := Get(testURL)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, err, keyring.ErrNotFound)
	assert.Empty(t, got)
}

func TestDeleteRemovesEntry(t *testing.T) {
	keyring.MockInit()

	require.NoError(t, Set(testURL, "s3cret"))
	require.NoError(t, Delete(testURL))

	_, err := Get(testURL)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestDeleteMissingReturnsNotFound(t *testing.T) {
	keyring.MockInit()

	err := Delete(testURL)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, err, keyring.ErrNotFound)
}

func TestSecretsKeyedByServerURL(t *testing.T) {
	keyring.MockInit()

	const otherURL = "http://other:9091/transmission/rpc"

	require.NoError(t, Set(testURL, "first"))
	require.NoError(t, Set(otherURL, "second"))

	first, err := Get(testURL)
	require.NoError(t, err)
	assert.Equal(t, "first", first)

	second, err := Get(otherURL)
	require.NoError(t, err)
	assert.Equal(t, "second", second)
}

func TestStoreUnavailableOnEveryOperation(t *testing.T) {
	ops := map[string]func() error{
		"set":    func() error { return Set(testURL, "s3cret") },
		"get":    func() error { _, err := Get(testURL); return err },
		"delete": func() error { return Delete(testURL) },
	}

	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			mockStoreUnavailable(t, errDBusLaunchMissing)

			err := op()
			require.ErrorIs(t, err, ErrUnavailable)
			require.NotErrorIs(t, err, ErrNotFound)
			require.NotErrorIs(t, err, keyring.ErrNotFound)

			// The provider internal must never reach a user-facing message.
			assert.NotContains(t, err.Error(), "dbus-launch")
			assert.Contains(t, err.Error(), testURL)
		})
	}
}

func TestUnavailableIsDistinctFromNotFound(t *testing.T) {
	keyring.MockInit()

	_, missing := Get(testURL)
	require.ErrorIs(t, missing, ErrNotFound)
	require.NotErrorIs(t, missing, ErrUnavailable)
}

func TestGenericProviderErrorIsNeitherSentinel(t *testing.T) {
	mockStoreUnavailable(t, errors.New("keyring exploded"))

	_, err := Get(testURL)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNotFound)
	require.NotErrorIs(t, err, ErrUnavailable)
	assert.Contains(t, err.Error(), "keyring exploded")
}

func TestIsUnavailable(t *testing.T) {
	tests := map[string]struct {
		err  error
		want bool
	}{
		"dbus-launch missing": {
			err:  errDBusLaunchMissing,
			want: true,
		},
		"dbus-launch missing, wrapped": {
			err:  fmt.Errorf("opening session: %w", errDBusLaunchMissing),
			want: true,
		},
		"exec lookup sentinel": {
			err:  exec.ErrNotFound,
			want: true,
		},
		"platform has no provider": {
			err:  keyring.ErrUnsupportedPlatform,
			want: true,
		},
		"session bus socket refuses": {
			err:  &net.OpError{Op: "dial", Net: "unix", Err: syscall.ECONNREFUSED},
			want: true,
		},
		"no secret service on the bus": {
			err: dbus.Error{
				Name: "org.freedesktop.DBus.Error.ServiceUnknown",
				Body: []any{"The name org.freedesktop.secrets was not provided by any .service files"},
			},
			want: true,
		},
		"no secret service on the bus, pointer form": {
			err:  dbus.NewError("org.freedesktop.DBus.Error.ServiceUnknown", nil),
			want: true,
		},
		"secret service answered with a refusal": {
			err:  dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied", Body: []any{"denied"}},
			want: false,
		},
		"entry missing": {
			err:  keyring.ErrNotFound,
			want: false,
		},
		"unrelated failure": {
			err:  errors.New("keyring exploded"),
			want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, isUnavailable(tc.err))
		})
	}
}
