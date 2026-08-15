package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/output"
)

// downloadTorrentBytes stands in for the .torrent a release points at. It is
// bencode rather than JSON on purpose: the download path must hand back exactly
// what the indexer served, byte for byte.
const downloadTorrentBytes = "d8:announce0:4:infod4:name10:ubuntu.isoee"

// downloadReleasePath is the path the stub serves the torrent from, and the one
// the fixture release's downloadUrl points at.
const downloadReleasePath = "/download/1"

// prowlarrDownloadStub answers a Prowlarr search with a release pointing back at
// itself, and serves that release's bytes. Its Content-Disposition deliberately
// names a traversing filename: a derived name comes from the release title and
// never from the server's header, and this is what proves it.
func downloadConfig(t *testing.T, body func(base string) string) (string, *[]string) {
	t.Helper()

	keyring.MockInit()

	paths := new([]string)

	var base string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*paths = append(*paths, r.URL.Path)

		if r.URL.Path == downloadReleasePath {
			w.Header().Set("Content-Type", "application/x-bittorrent")
			w.Header().Set("Content-Disposition", `attachment; filename="../../evil.torrent"`)
			_, _ = w.Write([]byte(downloadTorrentBytes))

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body(base)))
	}))
	t.Cleanup(srv.Close)

	base = srv.URL

	cfgPath := writeConfigFile(t, fmt.Sprintf(
		`{"servers": {"indexers": {"url": %q, "type": "prowlarr"}}, "default_prowlarr_server": "indexers"}`,
		srv.URL))

	return cfgPath, paths
}

// downloadReleaseBody renders a one-release search page whose download url points
// at the stub itself, so the same-origin rule sends the API key and the bytes
// come back.
//
// That url carries an apikey parameter because Prowlarr's really does — it
// rewrites every release's downloadUrl into a proxy link holding the key. Any
// message quoting the url therefore has a credential to strip, which is what
// gives TestSearchDownloadNeverPrintsTheAPIKey something to fail on.
func downloadReleaseBody(title string) func(string) string {
	// The title is encoded rather than quoted with %q: a Go-quoted control byte
	// is \x1b, which is not JSON.
	encoded, err := json.Marshal(title)
	if err != nil {
		panic(err)
	}

	return func(base string) string {
		return fmt.Sprintf(
			`[{"guid":"https://tracker.example/1","title":%s,"indexer":"Example Tracker",`+
				`"indexerId":3,"size":1024,"age":1,"downloadUrl":%q}]`,
			encoded, base+downloadReleasePath+"?apikey="+searchFixtureAPIKey)
	}
}

// failingDownloadConfig starts a stub whose search succeeds and whose download
// url answers non-2xx, and registers it as the default Prowlarr server. It
// returns the config path and the stub's base url.
func failingDownloadConfig(t *testing.T, title string) (string, string) {
	t.Helper()

	keyring.MockInit()

	var base string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == downloadReleasePath {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("indexer is unreachable"))

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(downloadReleaseBody(title)(base)))
	}))
	t.Cleanup(srv.Close)

	base = srv.URL

	cfgPath := writeConfigFile(t, fmt.Sprintf(
		`{"servers": {"indexers": {"url": %q, "type": "prowlarr"}}, "default_prowlarr_server": "indexers"}`,
		srv.URL))

	return cfgPath, srv.URL
}

// downloadInDir points the command at a fresh working directory, which is where a
// derived filename lands.
func downloadInDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Chdir(dir)

	return dir
}

func TestSearchDownloadWritesTheDerivedFilename(t *testing.T) {
	cfgPath, _ := downloadConfig(t, downloadReleaseBody("Ubuntu 24.04 LTS"))
	dir := downloadInDir(t)

	out, errOut, err := runSearch(t, "--config", cfgPath, "download", "ubuntu", "1")
	require.NoError(t, err)

	path := filepath.Join(dir, "Ubuntu 24.04 LTS.torrent")

	data, err := os.ReadFile(path)
	require.NoError(t, err, "the name is derived from the release title")
	assert.Equal(t, downloadTorrentBytes, string(data), "the bytes are the indexer's, verbatim")

	assert.Equal(t, fmt.Sprintf("Wrote Ubuntu 24.04 LTS.torrent (%s)\n",
		output.Bytes(int64(len(downloadTorrentBytes)))), out)
	assert.Equal(t, "Downloading: Ubuntu 24.04 LTS [Example Tracker]\n", errOut,
		"what is being fetched is named on stderr in every mode")
}

// TestSearchDownloadIgnoresContentDisposition pins that the server has no say in
// where the bytes land: the stub's header names a traversing path, which must
// leave no trace.
func TestSearchDownloadIgnoresContentDisposition(t *testing.T) {
	cfgPath, _ := downloadConfig(t, downloadReleaseBody("Ubuntu 24.04 LTS"))
	dir := downloadInDir(t)

	_, _, err := runSearch(t, "--config", cfgPath, "download", "ubuntu", "1")
	require.NoError(t, err)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	assert.Equal(t, []string{"Ubuntu 24.04 LTS.torrent"}, names,
		"exactly one file is written, and no temp file survives")
}

func TestSearchDownloadWritesAtModeSixHundred(t *testing.T) {
	cfgPath, _ := downloadConfig(t, downloadReleaseBody("Ubuntu"))
	dir := downloadInDir(t)

	_, _, err := runSearch(t, "--config", cfgPath, "download", "ubuntu", "1")
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(dir, "Ubuntu.torrent"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"a private tracker's .torrent embeds a passkey")
}

func TestSearchDownloadHonorsOutput(t *testing.T) {
	cfgPath, _ := downloadConfig(t, downloadReleaseBody("Ubuntu 24.04 LTS"))
	dir := downloadInDir(t)

	path := filepath.Join(dir, "elsewhere", "chosen.torrent")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

	out, _, err := runSearch(t, "--config", cfgPath, "download", "-o", path, "ubuntu", "1")
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, downloadTorrentBytes, string(data))
	assert.Contains(t, out, path)

	assert.NoFileExists(t, filepath.Join(dir, "Ubuntu 24.04 LTS.torrent"),
		"--output replaces the derived name rather than adding to it")
}

// TestSearchDownloadToStdoutWritesOnlyBytes pins that "-o -" makes stdout the
// file: a confirmation line, or a JSON object, would corrupt it.
func TestSearchDownloadToStdoutWritesOnlyBytes(t *testing.T) {
	cfgPath, _ := downloadConfig(t, downloadReleaseBody("Ubuntu 24.04 LTS"))
	dir := downloadInDir(t)

	for _, args := range [][]string{
		{"download", "-o", "-", "ubuntu", "1"},
		{"--json", "download", "-o", "-", "ubuntu", "1"},
		{"--jq", ".path", "download", "-o", "-", "ubuntu", "1"},
	} {
		out, errOut, err := runSearch(t, append([]string{"--config", cfgPath}, args...)...)
		require.NoErrorf(t, err, "%v", args)

		assert.Equalf(t, downloadTorrentBytes, out, "%v", args)
		assert.Containsf(t, errOut, "Downloading: Ubuntu 24.04 LTS", "%v", args)
	}

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, `"-o -" writes no file at all`)
}

func TestSearchDownloadRefusesToOverwrite(t *testing.T) {
	cfgPath, _ := downloadConfig(t, downloadReleaseBody("Ubuntu"))
	dir := downloadInDir(t)

	path := filepath.Join(dir, "Ubuntu.torrent")
	require.NoError(t, os.WriteFile(path, []byte("mine"), 0o600))

	_, _, err := runSearch(t, "--config", cfgPath, "download", "ubuntu", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Ubuntu.torrent already exists")
	assert.Contains(t, err.Error(), "--output", "the error names the way out")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "mine", string(data), "the existing file is untouched")
}

// TestSearchDownloadDerivesOneSafeSegment covers the two titles that must not be
// trusted as a filename: one that traverses and one carrying terminal control
// bytes.
func TestSearchDownloadDerivesOneSafeSegment(t *testing.T) {
	esc := string(rune(0o33))

	for _, tc := range []struct {
		title string
		want  string
	}{
		{title: "../../../tmp/evil", want: "tmpevil.torrent"},
		{title: ".hidden", want: "hidden.torrent"},
		{title: esc + "[2Jubuntu", want: "[2Jubuntu.torrent"},
		{title: "Ubuntu 24.04.torrent", want: "Ubuntu 24.04.torrent"},
		{title: `back\slash`, want: "backslash.torrent"},
	} {
		cfgPath, _ := downloadConfig(t, downloadReleaseBody(tc.title))
		dir := downloadInDir(t)

		out, _, err := runSearch(t, "--config", cfgPath, "download", "ubuntu", "1")
		require.NoErrorf(t, err, "%q", tc.title)

		assert.FileExistsf(t, filepath.Join(dir, tc.want), "%q", tc.title)
		assert.NotContainsf(t, out, esc, "%q", tc.title)

		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Lenf(t, entries, 1, "%q writes exactly one file, in the current directory", tc.title)
	}
}

func TestSearchDownloadRejectsATitleWithNoUsableName(t *testing.T) {
	cfgPath, _ := downloadConfig(t, downloadReleaseBody(".."))
	downloadInDir(t)

	_, _, err := runSearch(t, "--config", cfgPath, "download", "ubuntu", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive a filename")
	assert.Contains(t, err.Error(), "--output")
}

// TestSearchDownloadTruncatesALongTitle keeps a derived name under the length
// every filesystem stops at, which a scene release title can exceed on its own.
func TestSearchDownloadTruncatesALongTitle(t *testing.T) {
	cfgPath, _ := downloadConfig(t, downloadReleaseBody(strings.Repeat("a", 400)))
	dir := downloadInDir(t)

	_, _, err := runSearch(t, "--config", cfgPath, "download", "ubuntu", "1")
	require.NoError(t, err)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, strings.Repeat("a", downloadNameLimit)+".torrent", entries[0].Name())
}

// TestSearchDownloadRejectsTheExactForm pins the one place search download and
// search grab differ: --guid names a release without describing one, so there is
// no download url on it and never was.
func TestSearchDownloadRejectsTheExactForm(t *testing.T) {
	cfgPath, paths := downloadConfig(t, downloadReleaseBody("Ubuntu"))
	downloadInDir(t)

	_, _, err := runSearch(t, "--config", cfgPath,
		"download", "--guid", "https://tracker.example/1", "--indexer", "3")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--guid names a release without describing one")
	assert.Contains(t, err.Error(), `snatcher search download "<query>" <n>`)
	assert.Contains(t, err.Error(), "snatcher search grab --guid")

	assert.Empty(t, *paths, "nothing is fetched, and the exact form re-runs nothing")
}

func TestSearchDownloadRejectsAMagnetOnlyRelease(t *testing.T) {
	cfgPath, _ := downloadConfig(t, func(string) string {
		return `[{"guid":"g1","title":"Magnet Only","indexer":"Example Tracker","indexerId":3,` +
			`"magnetUrl":"magnet:?xt=urn:btih:aaa111"}]`
	})
	downloadInDir(t)

	_, _, err := runSearch(t, "--config", cfgPath, "download", "ubuntu", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `release "Magnet Only" publishes no download_url, only a magnet_url`)
	assert.Contains(t, err.Error(), `snatcher search "ubuntu" --jq '.[] | select(.index == 1) | .magnet_url'`,
		"the error hands over the exact command that extracts the link")
}

func TestSearchDownloadRejectsAReleaseWithNothingToFetch(t *testing.T) {
	cfgPath, _ := downloadConfig(t, func(string) string {
		return `[{"guid":"g1","title":"Nothing","indexer":"Example Tracker","indexerId":3}]`
	})
	downloadInDir(t)

	_, _, err := runSearch(t, "--config", cfgPath, "download", "ubuntu", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "publishes neither a download_url nor a magnet_url")
}

func TestSearchDownloadJSONReportsWhereTheBytesWent(t *testing.T) {
	cfgPath, _ := downloadConfig(t, downloadReleaseBody("Ubuntu 24.04 LTS"))
	downloadInDir(t)

	out, _, err := runSearch(t, "--config", cfgPath, "--json", "download", "ubuntu", "1")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	assert.Equal(t, "Ubuntu 24.04 LTS.torrent", got["path"])
	assert.InDelta(t, float64(len(downloadTorrentBytes)), got["bytes"], 0)
	assert.Equal(t, "Ubuntu 24.04 LTS", got["title"])
	assert.Equal(t, "https://tracker.example/1", got["guid"])
}

func TestSearchDownloadJQTakesPrecedenceOverJSON(t *testing.T) {
	cfgPath, _ := downloadConfig(t, downloadReleaseBody("Ubuntu 24.04 LTS"))
	downloadInDir(t)

	out, _, err := runSearch(t, "--config", cfgPath, "--json", "--jq", ".bytes", "download", "ubuntu", "1")
	require.NoError(t, err)

	assert.Equal(t, fmt.Sprint(len(downloadTorrentBytes)), strings.TrimSpace(out))
}

// TestSearchDownloadSanitizesPlaintextOnly pins the sanitizer boundary on both
// the stderr echo and the confirmation line, and that --json is left verbatim.
func TestSearchDownloadSanitizesPlaintextOnly(t *testing.T) {
	esc := string(rune(0o33))

	cfgPath, _ := downloadConfig(t, downloadReleaseBody(esc+"[2Jmalicious.iso"))
	downloadInDir(t)

	out, errOut, err := runSearch(t, "--config", cfgPath, "download", "ubuntu", "1")
	require.NoError(t, err)

	assert.NotContains(t, out, esc)
	assert.NotContains(t, errOut, esc)
	assert.Contains(t, errOut, "Downloading: [2Jmalicious.iso [Example Tracker]")

	structured, _, err := runSearch(t, "--config", cfgPath, "--json",
		"-o", filepath.Join(t.TempDir(), "out.torrent"), "download", "ubuntu", "1")
	require.NoError(t, err)

	var got struct {
		Title string `json:"title"`
	}
	require.NoError(t, json.Unmarshal([]byte(structured), &got))
	assert.Contains(t, got.Title, esc, "--json is a structured contract and is never sanitized")
}

// TestSearchDownloadNeverPrintsTheAPIKey covers every render mode plus the one
// path that quotes the release's key-bearing download url back at the user: a
// fetch that fails.
//
// The fixture is pinned first, because a guard whose fixture holds no key
// passes just as happily against code with every redaction removed.
func TestSearchDownloadNeverPrintsTheAPIKey(t *testing.T) {
	require.Contains(t, downloadReleaseBody("Ubuntu")("http://prowlarr.example"),
		"apikey="+searchFixtureAPIKey,
		"the fixture must carry the key in a URL, or this guard passes vacuously")

	cfgPath, _ := downloadConfig(t, downloadReleaseBody("Ubuntu"))
	t.Setenv(envAPIKey, searchFixtureAPIKey)

	for _, args := range [][]string{
		{"download", "ubuntu", "1"},
		{"--json", "download", "ubuntu", "1"},
		{"--jq", ".", "download", "ubuntu", "1"},
		{"download", "-o", "-", "ubuntu", "1"},
	} {
		downloadInDir(t)

		out, errOut, err := runSearch(t, append([]string{"--config", cfgPath}, args...)...)
		require.NoErrorf(t, err, "%v", args)
		assert.NotContainsf(t, out+errOut, searchFixtureAPIKey, "%v", args)
	}

	failPath, base := failingDownloadConfig(t, "Ubuntu")
	downloadInDir(t)

	_, _, err := runSearch(t, "--config", failPath, "download", "ubuntu", "1")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), searchFixtureAPIKey,
		"the url quoted in a failed fetch is the one place the key reaches a message")
	assert.Contains(t, err.Error(), base+downloadReleasePath+"?apikey=REDACTED")
}

// TestSearchDownloadSharesTheSelectionValidation proves the release is named
// through the same helper search grab uses, rather than through a second
// implementation that could drift from it.
func TestSearchDownloadSharesTheSelectionValidation(t *testing.T) {
	cfgPath, paths := downloadConfig(t, downloadReleaseBody("Ubuntu"))
	downloadInDir(t)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"download"}, want: "name a release by position with a query and a result number"},
		{args: []string{"download", "ubuntu"}, want: "name a release by position with a query and a result number"},
		{args: []string{"download", "", "1"}, want: "the query is empty"},
		{args: []string{"download", "   ", "1"}, want: "the query is empty"},
		{args: []string{"download", "ubuntu", "0"}, want: "invalid result number 0"},
		{args: []string{"download", "--pages", "0", "ubuntu", "1"}, want: "invalid --pages 0"},
		{args: []string{"download", "--search-type", "anime", "ubuntu", "1"}, want: `invalid --search-type "anime"`},
		{args: []string{"download", "--guid", "g1"}, want: "--guid needs the indexer that issued it"},
	} {
		_, _, err := runSearch(t, append([]string{"--config", cfgPath}, tc.args...)...)
		require.Errorf(t, err, "%v", tc.args)
		assert.Containsf(t, err.Error(), tc.want, "%v", tc.args)
	}

	assert.Empty(t, *paths, "the selection is settled before dialing")
}

func TestSearchDownloadRejectsAnOutOfRangeNumber(t *testing.T) {
	cfgPath, _ := downloadConfig(t, downloadReleaseBody("Ubuntu"))
	dir := downloadInDir(t)

	_, _, err := runSearch(t, "--config", cfgPath, "download", "ubuntu", "5")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no result 5: the search returned 1 result")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing is written when the number names no result")
}

// TestSearchDownloadReportsAFetchFailure is the download half of the grab's
// server-error guard: the search succeeds and the release resolves, but its
// download url answers non-2xx. The error names the release and the url that
// refused, and — the part that matters on disk — nothing is left behind, not
// even the temp file the atomic write goes through.
func TestSearchDownloadReportsAFetchFailure(t *testing.T) {
	cfgPath, base := failingDownloadConfig(t, "Ubuntu")
	dir := downloadInDir(t)

	_, _, err := runSearch(t, "--config", cfgPath, "download", "ubuntu", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "downloading Ubuntu [Example Tracker]")
	assert.Contains(t, err.Error(), "unexpected status 500")
	assert.Contains(t, err.Error(), base+downloadReleasePath, "the error names the url that failed")
	assert.Contains(t, err.Error(), "indexer is unreachable", "the server's own explanation survives the wrap")

	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	assert.Empty(t, entries, "a failed fetch writes no file, and leaves no partial one")
}

func TestSearchDownloadRefusesATransmissionServer(t *testing.T) {
	keyring.MockInit()

	cfgPath := writeConfigFile(t,
		`{"servers": {"local": {"url": "http://local:9091/transmission/rpc"}}, "default_server": "local"}`)

	_, _, err := runSearch(t, "--config", cfgPath, "--server", "local", "download", "ubuntu", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `server "local" is a Transmission server`)
}

// TestSearchDownloadDocumentsItsContract pins the prose neither check-surface nor
// check-man looks at: what a download is not (a grab), that nothing is
// overwritten, and why --guid cannot download.
func TestSearchDownloadDocumentsItsContract(t *testing.T) {
	var long string

	for _, sub := range NewSearchCmd().Commands() {
		if sub.Name() == "download" {
			long = sub.Long
		}
	}

	require.NotEmpty(t, long)
	assert.Contains(t, long, "snatcher search grab", "it points at the command that does delegate")
	assert.Contains(t, long, "never overwritten")
	assert.Contains(t, long, "--force", "the absence of a --force is stated, not left to be discovered")
	assert.Contains(t, long, "Prowlarr resolves a guid only when grabbing")
}
