package clipboard

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubTool writes an executable named for this OS's first candidate into a
// fresh directory and makes that directory the whole PATH. The real tool is
// never run: it would clobber the developer's clipboard under make check.
func stubTool(t *testing.T, body string) string {
	t.Helper()

	cands := candidates(runtime.GOOS)
	if len(cands) == 0 {
		t.Skipf("no clipboard candidates on %s", runtime.GOOS)
	}

	dir := t.TempDir()
	script := "#!/bin/sh\n" + body + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, cands[0].name), []byte(script), 0o700))
	t.Setenv("PATH", dir)

	return dir
}

// TestWriteSendsTheExactPayload checks Detect finds the stub and Write hands it
// the string byte-for-byte over stdin.
func TestWriteSendsTheExactPayload(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	stubTool(t, `/bin/cat > "`+out+`"`)

	w := Detect()
	require.NotNil(t, w)

	payload := "magnet:?xt=urn:btih:abc&dn=a name\nwith a newline"
	require.NoError(t, w.Write(context.Background(), payload))

	got, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, payload, string(got))
}

// TestDetectFindsNothingOnAnEmptyPath checks there is no writer when no tool
// is installed.
func TestDetectFindsNothingOnAnEmptyPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	assert.Nil(t, Detect())
}

// TestWriteFailureNamesTheTool checks a failing tool surfaces an error that
// names it and does not echo the payload.
func TestWriteFailureNamesTheTool(t *testing.T) {
	stubTool(t, "exit 1")

	w := Detect()
	require.NotNil(t, w)

	err := w.Write(context.Background(), "secret-payload")
	require.Error(t, err)
	assert.Contains(t, err.Error(), candidates(runtime.GOOS)[0].name)
	assert.NotContains(t, err.Error(), "secret-payload")
}
