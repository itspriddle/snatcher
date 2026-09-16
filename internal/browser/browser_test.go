package browser

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubOpener writes an executable named for this OS's opener into a fresh
// directory and makes that directory the whole PATH. The real opener is never
// run: it would open a browser under make check.
func stubOpener(t *testing.T, body string) {
	t.Helper()

	cands := candidates(runtime.GOOS)
	if len(cands) == 0 {
		t.Skipf("no opener candidates on %s", runtime.GOOS)
	}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, cands[0]), []byte("#!/bin/sh\n"+body+"\n"), 0o700))
	t.Setenv("PATH", dir)
}

// TestCandidatesPerOS pins the one opener per supported OS, and none elsewhere.
func TestCandidatesPerOS(t *testing.T) {
	assert.Equal(t, []string{"open"}, candidates("darwin"))
	assert.Equal(t, []string{"xdg-open"}, candidates("linux"))
	assert.Empty(t, candidates("windows"))
}

// TestOpenPassesTheURLAsOneArgument checks Detect finds the stub and Open hands
// it the URL as a single argv entry, byte for byte.
func TestOpenPassesTheURLAsOneArgument(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	stubOpener(t, `printf '%s|%s' "$#" "$1" > "`+out+`"`)

	o := Detect()
	require.NotNil(t, o)

	u := "https://tracker.example/details?id=1&x=a b"
	require.NoError(t, o.Open(context.Background(), u))

	got, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "1|"+u, string(got))
}

// TestOpenRefusesOtherSchemes checks a server-supplied URL of any other shape
// never reaches the opener.
func TestOpenRefusesOtherSchemes(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	stubOpener(t, `touch "`+out+`"`)

	o := Detect()
	require.NotNil(t, o)

	for _, u := range []string{
		"file:///etc/passwd", "javascript:alert(1)", "magnet:?xt=urn:btih:abc",
		"-a Calculator", "//host/path", "http://", "ftp://host/x", "",
	} {
		require.ErrorIsf(t, o.Open(context.Background(), u), ErrScheme, "%q", u)
	}

	assert.NoFileExists(t, out)
}

// TestDetectFindsNothingOnAnEmptyPath checks there is no opener when none is
// installed.
func TestDetectFindsNothingOnAnEmptyPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	assert.Nil(t, Detect())
}

// TestOpenFailureNamesTheOpenerAndNotTheURL checks a failing opener surfaces an
// error naming it, without echoing a URL that may carry a credential.
func TestOpenFailureNamesTheOpenerAndNotTheURL(t *testing.T) {
	stubOpener(t, "exit 3")

	o := Detect()
	require.NotNil(t, o)

	err := o.Open(context.Background(), "https://host/x?apikey=secret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), candidates(runtime.GOOS)[0])
	assert.NotContains(t, err.Error(), "secret")
}
