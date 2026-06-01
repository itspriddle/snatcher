package commands

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
)

// addedResponse is a bespoke torrent-add reply reporting a newly added torrent.
const addedResponse = `{
	"arguments": {"torrent-added": {"id": 7, "name": "ubuntu-24.04.iso", "hashString": "0123456789abcdef0123456789abcdef01234567"}},
	"result": "success",
	"tag": 1
}`

// duplicateResponse is a bespoke torrent-add reply reporting a torrent that was
// already present (a success, not an error).
const duplicateResponse = `{
	"arguments": {"torrent-duplicate": {"id": 3, "name": "debian.iso", "hashString": "fedcba9876543210fedcba9876543210fedcba98"}},
	"result": "success",
	"tag": 1
}`

// runUpload executes the upload command with args, returning stdout, stderr, and
// the Execute error. The command's output is a *bytes.Buffer, so the
// piped/plaintext path is exercised.
func runUpload(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	return runUploadStdin(t, "", args...)
}

// runUploadStdin executes the upload command with the given stdin contents and
// args, returning stdout, stderr, and the Execute error.
func runUploadStdin(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewUploadCmd()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// uploadConfigPath stands up a bespoke transmission stub serving the given
// torrent-add payload and writes a config pointing at it.
func uploadConfigPath(t *testing.T, addBody string) (string, func()) {
	t.Helper()

	keyring.MockInit()

	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // bespoke path
		responses: map[string]string{
			"torrent-add": addBody,
		},
	}.handler())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	return cfgPath, srv.Close
}

// captureUploadConfigPath stands up a bespoke transmission stub that records the
// add request body and replies with addedResponse, so a test can assert which
// add parameters the command forwarded.
func captureUploadConfigPath(t *testing.T, body *string) (string, func()) {
	t.Helper()

	keyring.MockInit()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)

			return
		}

		raw, _ := io.ReadAll(r.Body)
		*body = string(raw)

		_, _ = w.Write([]byte(addedResponse))
	}))

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	return cfgPath, srv.Close
}

// writeTorrentFile writes raw bytes to a temp .torrent file and returns its path.
func writeTorrentFile(t *testing.T, data []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.torrent")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	return path
}

func TestUploadByFile(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	torrentPath := writeTorrentFile(t, []byte("d8:announce0:e"))

	out, _, err := runUpload(t, "--config", cfgPath, torrentPath)
	require.NoError(t, err)

	assert.Equal(t, "Added [file]: ubuntu-24.04.iso\n", out)
}

func TestUploadByURL(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	out, _, err := runUpload(t, "--config", cfgPath, "https://example.com/a.torrent")
	require.NoError(t, err)

	assert.Equal(t, "Added [url]: ubuntu-24.04.iso\n", out)
}

func TestUploadByMagnet(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	out, _, err := runUpload(t, "--config", cfgPath, "magnet:?xt=urn:btih:deadbeef")
	require.NoError(t, err)

	assert.Equal(t, "Added [magnet]: ubuntu-24.04.iso\n", out)
}

func TestUploadFileSendsMetainfoBase64(t *testing.T) {
	var body string
	cfgPath, closeSrv := captureUploadConfigPath(t, &body)
	defer closeSrv()

	raw := []byte("d8:announce0:e")
	torrentPath := writeTorrentFile(t, raw)

	_, _, err := runUpload(t, "--config", cfgPath, torrentPath)
	require.NoError(t, err)

	want := base64.StdEncoding.EncodeToString(raw)
	assert.Contains(t, body, `"metainfo":"`+want+`"`)
	assert.NotContains(t, body, `"filename"`)
}

// TestUploadPausedByDefault confirms torrents are added paused unless --start is
// given, by inspecting the forwarded "paused" parameter.
func TestUploadPausedByDefault(t *testing.T) {
	t.Run("default adds paused", func(t *testing.T) {
		var body string
		cfgPath, closeSrv := captureUploadConfigPath(t, &body)
		defer closeSrv()

		_, _, err := runUpload(t, "--config", cfgPath, "https://example.com/a.torrent")
		require.NoError(t, err)

		assert.Contains(t, body, `"paused":true`)
	})

	t.Run("--start adds running", func(t *testing.T) {
		var body string
		cfgPath, closeSrv := captureUploadConfigPath(t, &body)
		defer closeSrv()

		_, _, err := runUpload(t, "--config", cfgPath, "--start", "https://example.com/a.torrent")
		require.NoError(t, err)

		// paused is false, so omitempty drops it from the wire entirely.
		assert.NotContains(t, body, `"paused"`)
	})
}

func TestUploadDownloadDirForwarded(t *testing.T) {
	var body string
	cfgPath, closeSrv := captureUploadConfigPath(t, &body)
	defer closeSrv()

	_, _, err := runUpload(t, "--config", cfgPath, "--download-dir", "/srv/iso", "https://example.com/a.torrent")
	require.NoError(t, err)

	assert.Contains(t, body, `"download-dir":"/srv/iso"`)
}

func TestUploadDuplicate(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, duplicateResponse)
	defer closeSrv()

	out, _, err := runUpload(t, "--config", cfgPath, "magnet:?xt=urn:btih:cafebabe")
	require.NoError(t, err, "a duplicate is a success, not an error")

	assert.Equal(t, "Duplicate [magnet]: debian.iso\n", out)
}

func TestUploadMissingFileErrors(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	missing := filepath.Join(t.TempDir(), "nope.torrent")

	out, errOut, err := runUpload(t, "--config", cfgPath, missing)
	require.Error(t, err, "a per-source failure exits non-zero")
	assert.Empty(t, out, "no success line is printed for a failed source")
	assert.Contains(t, errOut, "reading "+missing)
}

// TestUploadFailureNotAttributedToServer guards against regressing the batch
// sentinel back into a "connecting to <server>" wrap: a purely local failure (a
// missing .torrent, server reachable) must surface errUploadFailures unwrapped,
// not as a transport error.
func TestUploadFailureNotAttributedToServer(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	missing := filepath.Join(t.TempDir(), "nope.torrent")

	_, _, err := runUpload(t, "--config", cfgPath, missing)
	require.ErrorIs(t, err, errUploadFailures)
	assert.NotContains(t, err.Error(), "connecting to",
		"a local-only failure must not be reported as a connection error")
}

// TestUploadContinuesOnError confirms that a failure on one source does not abort
// the remaining sources, and that the command still exits non-zero.
func TestUploadContinuesOnError(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	missing := filepath.Join(t.TempDir(), "nope.torrent")

	out, errOut, err := runUpload(t, "--config", cfgPath, missing, "https://example.com/a.torrent")
	require.Error(t, err, "any source failing exits non-zero")

	// The good source still succeeded after the bad one failed.
	assert.Equal(t, "Added [url]: ubuntu-24.04.iso\n", out)
	assert.Contains(t, errOut, "reading "+missing)
}

func TestUploadNoArgsErrors(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	_, _, err := runUpload(t, "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "specify at least one file, url, or magnet link")
}

func TestUploadJSON(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	out, _, err := runUpload(t, "--config", cfgPath, "--json", "https://example.com/a.torrent")
	require.NoError(t, err)

	var got []struct {
		Source    string `json:"source"`
		Input     string `json:"input"`
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Hash      string `json:"hash"`
		Duplicate bool   `json:"duplicate"`
		Error     string `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	require.Len(t, got, 1)
	assert.Equal(t, "url", got[0].Source)
	assert.Equal(t, "https://example.com/a.torrent", got[0].Input)
	assert.Equal(t, int64(7), got[0].ID)
	assert.Equal(t, "ubuntu-24.04.iso", got[0].Name)
	assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", got[0].Hash)
	assert.False(t, got[0].Duplicate)
	assert.Empty(t, got[0].Error)
}

// TestUploadJSONReportsErrorPerSource confirms the per-source array carries an
// error element for a failed source alongside a success element, and that the
// command exits non-zero.
func TestUploadJSONReportsErrorPerSource(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	missing := filepath.Join(t.TempDir(), "nope.torrent")

	out, _, err := runUpload(t, "--config", cfgPath, "--json", missing, "https://example.com/a.torrent")
	require.Error(t, err)

	var got []struct {
		Source string `json:"source"`
		Input  string `json:"input"`
		Name   string `json:"name"`
		Error  string `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	require.Len(t, got, 2)
	assert.Equal(t, "file", got[0].Source)
	assert.Contains(t, got[0].Error, "reading "+missing)
	assert.Empty(t, got[0].Name)

	assert.Equal(t, "url", got[1].Source)
	assert.Empty(t, got[1].Error)
	assert.Equal(t, "ubuntu-24.04.iso", got[1].Name)
}

func TestUploadJSONReportsDuplicate(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, duplicateResponse)
	defer closeSrv()

	out, _, err := runUpload(t, "--config", cfgPath, "--json", "magnet:?xt=urn:btih:cafebabe")
	require.NoError(t, err)

	var got []struct {
		Duplicate bool   `json:"duplicate"`
		Error     string `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	require.Len(t, got, 1)
	assert.True(t, got[0].Duplicate, "a duplicate is reported distinctly, not as an error")
	assert.Empty(t, got[0].Error)
}

func TestUploadJQ(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	out, _, err := runUpload(t, "--config", cfgPath, "--jq", ".[].name", "https://example.com/a.torrent")
	require.NoError(t, err)

	assert.Equal(t, `"ubuntu-24.04.iso"`, strings.TrimSpace(out))
}

func TestUploadNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runUpload(t, "--config", cfgPath, "https://example.com/a.torrent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// TestUploadSanitizesNameInHumanButNotJSON guards the security boundary: a
// torrent name carrying an ANSI escape is neutralized in the human confirmation
// but preserved verbatim in the structured --json contract.
func TestUploadSanitizesNameInHumanButNotJSON(t *testing.T) {
	const body = `{
		"arguments": {"torrent-added": {"id": 1, "name": "\u001b[2Jmalicious", "hashString": "0123456789abcdef0123456789abcdef01234567"}},
		"result": "success",
		"tag": 1
	}`

	cfgPath, closeSrv := uploadConfigPath(t, body)
	defer closeSrv()

	human, _, err := runUpload(t, "--config", cfgPath, "https://example.com/a.torrent")
	require.NoError(t, err)
	assert.NotContains(t, human, "\x1b", "raw ESC must not reach the human confirmation")
	assert.Contains(t, human, "[2Jmalicious", "visible text survives sanitization")

	jsonOut, _, err := runUpload(t, "--config", cfgPath, "--json", "https://example.com/a.torrent")
	require.NoError(t, err)
	assert.Contains(t, jsonOut, "\\u001b", "the raw escape is preserved in the JSON contract")
}

// TestUploadDeleteRemovesFileOnSuccess confirms --delete removes the source
// .torrent after a successful, non-duplicate file add and reports deleted=true.
func TestUploadDeleteRemovesFileOnSuccess(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	torrentPath := writeTorrentFile(t, []byte("d8:announce0:e"))

	out, _, err := runUpload(t, "--config", cfgPath, "--delete", "--json", torrentPath)
	require.NoError(t, err)

	assert.NoFileExists(t, torrentPath, "the consumed .torrent is deleted")

	var got []struct {
		Deleted bool `json:"deleted"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 1)
	assert.True(t, got[0].Deleted, "deleted is reported when removal succeeds")
}

// TestUploadDeleteShortFlag confirms -d is an alias for --delete.
func TestUploadDeleteShortFlag(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	torrentPath := writeTorrentFile(t, []byte("d8:announce0:e"))

	_, _, err := runUpload(t, "--config", cfgPath, "-d", torrentPath)
	require.NoError(t, err)

	assert.NoFileExists(t, torrentPath, "-d deletes the consumed .torrent")
}

// TestUploadDeleteKeepsFileOnDuplicate confirms --delete leaves the source
// .torrent in place when the add was a duplicate (not a fresh add).
func TestUploadDeleteKeepsFileOnDuplicate(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, duplicateResponse)
	defer closeSrv()

	torrentPath := writeTorrentFile(t, []byte("d8:announce0:e"))

	out, _, err := runUpload(t, "--config", cfgPath, "--delete", "--json", torrentPath)
	require.NoError(t, err)

	assert.FileExists(t, torrentPath, "a duplicate add does not delete the source file")

	var got []struct {
		Duplicate bool `json:"duplicate"`
		Deleted   bool `json:"deleted"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 1)
	assert.True(t, got[0].Duplicate)
	assert.False(t, got[0].Deleted)
}

// TestUploadDeleteIgnoresURLAndMagnet confirms --delete has no effect on
// URL/magnet sources (there is no local file to remove).
func TestUploadDeleteIgnoresURLAndMagnet(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	out, _, err := runUpload(t, "--config", cfgPath, "--delete", "--json", "https://example.com/a.torrent", "magnet:?xt=urn:btih:deadbeef")
	require.NoError(t, err)

	var got []struct {
		Deleted bool `json:"deleted"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 2)
	assert.False(t, got[0].Deleted, "deleted is never set for a URL source")
	assert.False(t, got[1].Deleted, "deleted is never set for a magnet source")
}

// TestUploadDeleteToleratesFailure confirms a delete failure (here, the file
// was already removed out from under us) warns on stderr without failing the
// add and leaves deleted=false.
func TestUploadDeleteToleratesFailure(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	// A read-only directory: os.ReadFile succeeds on the file, but os.Remove of
	// the entry fails because the parent directory is not writable.
	dir := t.TempDir()
	torrentPath := filepath.Join(dir, "ro.torrent")
	require.NoError(t, os.WriteFile(torrentPath, []byte("d8:announce0:e"), 0o600))
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	out, errOut, err := runUpload(t, "--config", cfgPath, "--delete", "--json", torrentPath)
	require.NoError(t, err, "a delete failure does not fail the add")

	assert.Contains(t, errOut, "deleting "+torrentPath, "the delete failure warns on stderr")

	var got []struct {
		Deleted bool `json:"deleted"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 1)
	assert.False(t, got[0].Deleted, "deleted stays false when removal fails")
}

// TestUploadLabelsForwarded confirms repeated --label values are sent on the add
// request.
func TestUploadLabelsForwarded(t *testing.T) {
	var body string
	cfgPath, closeSrv := captureUploadConfigPath(t, &body)
	defer closeSrv()

	_, _, err := runUpload(t, "--config", cfgPath, "--label", "iso", "--label", "linux", "https://example.com/a.torrent")
	require.NoError(t, err)

	assert.Contains(t, body, `"labels":["iso","linux"]`)
}

// TestUploadQuietSuppressesSuccessLines confirms --quiet drops the per-source
// confirmation lines on stdout while still surfacing errors on stderr.
func TestUploadQuietSuppressesSuccessLines(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	missing := filepath.Join(t.TempDir(), "nope.torrent")

	out, errOut, err := runUpload(t, "--config", cfgPath, "--quiet", missing, "https://example.com/a.torrent")
	require.Error(t, err, "a failed source still exits non-zero under --quiet")

	assert.Empty(t, out, "--quiet suppresses the success line")
	assert.Contains(t, errOut, "reading "+missing, "--quiet does not suppress errors")
}

// TestUploadQuietShortFlag confirms -q is an alias for --quiet.
func TestUploadQuietShortFlag(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	out, _, err := runUpload(t, "--config", cfgPath, "-q", "https://example.com/a.torrent")
	require.NoError(t, err)

	assert.Empty(t, out, "-q suppresses the success line")
}

// TestUploadQuietDoesNotAlterJSON confirms --quiet leaves the JSON contract
// untouched.
func TestUploadQuietDoesNotAlterJSON(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	out, _, err := runUpload(t, "--config", cfgPath, "--quiet", "--json", "https://example.com/a.torrent")
	require.NoError(t, err)

	var got []struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "ubuntu-24.04.iso", got[0].Name, "--quiet does not change JSON output")
}

// TestUploadReadsSourcesFromStdin confirms that, with no positional args, sources
// are read one-per-line from stdin (blank lines and surrounding whitespace
// ignored).
func TestUploadReadsSourcesFromStdin(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	stdin := "  https://example.com/a.torrent  \n\nmagnet:?xt=urn:btih:deadbeef\n"

	out, _, err := runUploadStdin(t, stdin, "--config", cfgPath, "--json")
	require.NoError(t, err)

	var got []struct {
		Source string `json:"source"`
		Input  string `json:"input"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	require.Len(t, got, 2, "blank lines are ignored")
	assert.Equal(t, "url", got[0].Source)
	assert.Equal(t, "https://example.com/a.torrent", got[0].Input, "surrounding whitespace is trimmed")
	assert.Equal(t, "magnet", got[1].Source)
	assert.Equal(t, "magnet:?xt=urn:btih:deadbeef", got[1].Input)
}

// TestUploadPositionalArgsTakePrecedenceOverStdin confirms stdin is consulted
// only when no positional args are present.
func TestUploadPositionalArgsTakePrecedenceOverStdin(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	out, _, err := runUploadStdin(t, "magnet:?xt=urn:btih:fromstdin\n", "--config", cfgPath, "--json", "https://example.com/a.torrent")
	require.NoError(t, err)

	var got []struct {
		Input string `json:"input"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	require.Len(t, got, 1, "stdin is not mixed with positional args")
	assert.Equal(t, "https://example.com/a.torrent", got[0].Input)
}

// TestUploadNoArgsNoStdinErrors confirms a bare invocation with neither
// positional args nor piped stdin remains an error.
func TestUploadNoArgsNoStdinErrors(t *testing.T) {
	cfgPath, closeSrv := uploadConfigPath(t, addedResponse)
	defer closeSrv()

	_, _, err := runUploadStdin(t, "", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "specify at least one file, url, or magnet link")
}
