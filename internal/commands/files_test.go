package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// infoHash3 is a third 40-character info-hash, for the files fixture's third
// torrent. Hash resolution is asserted against a torrent that both has files and
// is not the fixture's first entry, so a stub that ignored the requested ids
// (and so always answered with torrent 1) could not pass. It is not the "c"
// hash, which TestFilesNotFoundErrors relies on matching nothing.
const infoHash3 = "dddddddddddddddddddddddddddddddddddddddd"

// bespokeFilesResponse is a torrent-get payload whose first torrent carries the
// parallel files/fileStats/wanted/priorities arrays the bespoke scheme uses, and
// whose second torrent has no files at all (the empty-collection path, and the
// magnet-metadata-pending case "files update" must refuse to write to). The third
// file of torrent 1 is zero-length, which is legal in a torrent and must not
// divide by zero in the percent column.
const bespokeFilesResponse = `{
	"arguments": {"torrents": [
		{
			"id": 1,
			"hashString": "` + infoHash1 + `",
			"name": "ubuntu-24.04.iso",
			"status": 4,
			"files": [
				{"name": "ubuntu-24.04.iso", "length": 2147483648, "bytesCompleted": 1073741824},
				{"name": "SHA256SUMS", "length": 1024, "bytesCompleted": 1024},
				{"name": "empty.dat", "length": 0, "bytesCompleted": 0}
			],
			"fileStats": [
				{"bytesCompleted": 1073741824},
				{"bytesCompleted": 1024},
				{"bytesCompleted": 0}
			],
			"wanted": [1, 1, 0],
			"priorities": [1, 0, -1]
		},
		{
			"id": 2,
			"hashString": "` + infoHash2 + `",
			"name": "debian.iso",
			"status": 6,
			"files": [],
			"fileStats": [],
			"wanted": [],
			"priorities": []
		},
		{
			"id": 3,
			"hashString": "` + infoHash3 + `",
			"name": "fedora.iso",
			"status": 4,
			"files": [
				{"name": "fedora.iso", "length": 4096, "bytesCompleted": 4096},
				{"name": "CHECKSUM", "length": 512, "bytesCompleted": 512}
			],
			"fileStats": [
				{"bytesCompleted": 4096},
				{"bytesCompleted": 512}
			],
			"wanted": [1, 1],
			"priorities": [0, 0]
		}
	]},
	"result": "success",
	"tag": 1
}`

// bespokeFilesSanitizeResponse is a torrent-get payload whose file name carries
// terminal control sequences. File names come from swarm-supplied .torrent
// metadata, so they are untrusted on every plaintext path.
const bespokeFilesSanitizeResponse = `{
	"arguments": {"torrents": [
		{
			"id": 1,
			"hashString": "` + infoHash1 + `",
			"name": "ubuntu-24.04.iso",
			"status": 4,
			"files": [
				{"name": "\u001b[2Jmalicious\u0007.iso", "length": 100, "bytesCompleted": 50}
			],
			"fileStats": [{"bytesCompleted": 50}],
			"wanted": [1],
			"priorities": [0]
		}
	]},
	"result": "success",
	"tag": 1
}`

// runFiles executes the files command with args, returning stdout, stderr, and
// the Execute error. The command's output is a *bytes.Buffer, so isTTY reports
// false and the piped (tab-separated) plaintext contract is exercised.
func runFiles(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewFilesCmd()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

func TestFilesPipedTabSeparated(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	out, _, err := runFiles(t, "--config", cfgPath, "1")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3, "piped output is header-less, one row per file")

	first := strings.Split(lines[0], "\t")
	require.Len(t, first, len(fileFieldKeys))
	assert.Equal(t, "0", first[0], "indices are 0-based")
	assert.Equal(t, "ubuntu-24.04.iso", first[1])
	assert.Equal(t, "2.0 GiB", first[2])
	assert.Equal(t, "1.0 GiB", first[3])
	assert.Equal(t, "50%", first[4])
	assert.Equal(t, "high", first[5])
	assert.Equal(t, "yes", first[6])

	second := strings.Split(lines[1], "\t")
	assert.Equal(t, "1", second[0])
	assert.Equal(t, "SHA256SUMS", second[1])
	assert.Equal(t, "100%", second[4])
	assert.Equal(t, "normal", second[5])

	// A zero-length file is complete by definition rather than a division by zero.
	third := strings.Split(lines[2], "\t")
	assert.Equal(t, "2", third[0])
	assert.Equal(t, "0 B", third[2])
	assert.Equal(t, "100%", third[4], "a zero-length file renders as complete")
	assert.Equal(t, "low", third[5])
	assert.Equal(t, "no", third[6])
}

func TestFilesResolvesByHash(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	// An uppercase hash resolves case-insensitively, via the same resolveTorrent
	// path show uses.
	out, _, err := runFiles(t, "--config", cfgPath, strings.ToUpper(infoHash1))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "ubuntu-24.04.iso", strings.Split(lines[0], "\t")[1])
}

func TestFilesJSONMatchesIndexOrder(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	out, _, err := runFiles(t, "--config", cfgPath, "1", "--json")
	require.NoError(t, err)

	var got []struct {
		Name           string `json:"name"`
		Length         int64  `json:"length"`
		BytesCompleted int64  `json:"bytes_completed"`
		Wanted         bool   `json:"wanted"`
		Priority       int    `json:"priority"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 3)

	// Array position is the index column: element 0 is index 0.
	assert.Equal(t, "ubuntu-24.04.iso", got[0].Name)
	assert.Equal(t, int64(2147483648), got[0].Length)
	assert.Equal(t, int64(1073741824), got[0].BytesCompleted)
	assert.True(t, got[0].Wanted)
	assert.Equal(t, 1, got[0].Priority)

	assert.Equal(t, "SHA256SUMS", got[1].Name)
	assert.Equal(t, "empty.dat", got[2].Name)
	assert.False(t, got[2].Wanted)
	assert.Equal(t, -1, got[2].Priority)
}

func TestFilesJSONEmptyIsArrayNotNull(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	out, _, err := runFiles(t, "--config", cfgPath, "2", "--json")
	require.NoError(t, err)
	assert.Equal(t, "[]", strings.TrimSpace(out))
}

func TestFilesJQ(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	out, _, err := runFiles(t, "--config", cfgPath, "1", "--jq", ".[].name")
	require.NoError(t, err)

	assert.Equal(t,
		`"ubuntu-24.04.iso"`+"\n"+`"SHA256SUMS"`+"\n"+`"empty.dat"`,
		strings.TrimSpace(out))
}

func TestFilesPipedEmptyWritesNothing(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	out, _, err := runFiles(t, "--config", cfgPath, "2")
	require.NoError(t, err)
	assert.Empty(t, out, "piped output for a torrent with no files is empty")
}

// TestFilesTableEmpty covers the TTY branch of the empty case, which isTTY makes
// unreachable through the command with a *bytes.Buffer: the table writer prints
// the files command's own "No files." line, not list's "No torrents.".
func TestFilesTableEmpty(t *testing.T) {
	cols, err := parseFields(fileFields, fileFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	writeListTable(&buf, fileRows(nil), cols, 80, "No files.")

	assert.Equal(t, "No files.", strings.TrimSpace(buf.String()))
}

// TestFilesTableAligned exercises the TTY table branch directly, since isTTY
// needs a real terminal: the header row is present and the index column is
// right-aligned.
func TestFilesTableAligned(t *testing.T) {
	files := []transmission.File{
		{Name: "a.iso", Length: 2048, BytesCompleted: 1024, Wanted: true, Priority: 1},
		{Name: "b.iso", Length: 4096, BytesCompleted: 4096, Wanted: false, Priority: -1},
	}

	cols, err := parseFields(fileFields, fileFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	// width 0 disables truncation so the full names show.
	writeListTable(&buf, fileRows(files), cols, 0, "No files.")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3, "header row plus one row per file")

	assert.True(t, strings.HasPrefix(lines[0], "Index"))
	assert.Contains(t, lines[0], "Priority")
	assert.Contains(t, lines[0], "Wanted")
	assert.Contains(t, lines[1], "a.iso")
	assert.Contains(t, lines[2], "b.iso")
}

// TestFilesSanitizesHumanOutputNotJSON asserts the sanitize boundary on both
// plaintext paths (piped and TTY table) while leaving the structured --json
// contract byte-for-byte intact. File names come from the .torrent metadata, so
// they are attacker-influenced.
func TestFilesSanitizesHumanOutputNotJSON(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeFilesSanitizeResponse)
	defer closeSrv()

	// Piped plaintext, end to end through the command.
	piped, _, err := runFiles(t, "--config", cfgPath, "1")
	require.NoError(t, err)
	assert.NotContains(t, piped, "\x1b", "raw ESC must not reach the piped output")
	assert.NotContains(t, piped, "\x07", "raw BEL must not reach the piped output")
	assert.Contains(t, piped, "[2Jmalicious.iso", "visible name text survives sanitization")

	// TTY table path, exercised directly.
	cols, err := parseFields(fileFields, fileFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	writeListTable(&buf, fileRows([]transmission.File{{Name: "\x1b[2Jmalicious\x07.iso"}}), cols, 0, "No files.")

	table := buf.String()
	assert.NotContains(t, table, "\x1b", "raw ESC must not reach the table output")
	assert.NotContains(t, table, "\x07", "raw BEL must not reach the table output")
	assert.Contains(t, table, "[2Jmalicious.iso")

	// JSON is a structured contract and is never sanitized.
	raw, _, err := runFiles(t, "--config", cfgPath, "1", "--json")
	require.NoError(t, err)

	var got []struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &got))
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Name, "\x1b", "JSON name keeps the raw ESC (unsanitized)")
	assert.Contains(t, got[0].Name, "\x07", "JSON name keeps the raw BEL")
}

func TestFilesFieldsSelectsAndOrdersColumns(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	out, _, err := runFiles(t, "--config", cfgPath, "1", "--fields", "Name, index ,wanted")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3)

	first := strings.Split(lines[0], "\t")
	require.Len(t, first, 3, "only the three selected columns are rendered")
	assert.Equal(t, "ubuntu-24.04.iso", first[0], "name is first")
	assert.Equal(t, "0", first[1], "index is second")
	assert.Equal(t, "yes", first[2], "wanted is third")
}

// TestFilesFieldsRegistryIsFilesOnly asserts the per-command registry boundary:
// an unknown key errors listing only the files command's own keys, and a key that
// is valid for another command (list/show) is not silently accepted here.
func TestFilesFieldsRegistryIsFilesOnly(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	_, _, err := runFiles(t, "--config", cfgPath, "1", "--fields", "name,status")
	require.Error(t, err)
	assert.Equal(t, `invalid field "status": valid fields are `+validFieldKeys(fileFields), err.Error(),
		"the error lists only the files command's own keys")

	for _, key := range fileFieldKeys {
		_, err := parseFields(fileFields, fileFieldKeys, key)
		require.NoErrorf(t, err, "files accepts its own key %q", key)
	}

	// fileFieldKeys is both the default column set and the --fields help string,
	// so a key added to the registry but not to the slice would be invisible in
	// the table and in the help without failing anything else here.
	assert.Len(t, fileFieldKeys, len(fileFields), "the default column set covers every registry key")
}

func TestFilesWrongArgCount(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	t.Run("zero args", func(t *testing.T) {
		_, _, err := runFiles(t, "--config", cfgPath)
		require.Error(t, err, "a bare invocation with no selector is an error, not a no-op")
	})

	t.Run("two args", func(t *testing.T) {
		_, _, err := runFiles(t, "--config", cfgPath, "1", "2")
		require.Error(t, err, "more than one positional arg is a usage error")
	})
}

func TestFilesInvalidSelector(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	_, _, err := runFiles(t, "--config", cfgPath, "not-an-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid torrent selector")
}

func TestFilesNotFoundErrors(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	absent := "cccccccccccccccccccccccccccccccccccccccc"
	out, _, err := runFiles(t, "--config", cfgPath, absent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no torrent matched: "+absent)
	assert.Empty(t, out, "no output is written when nothing matched")
}

func TestFilesNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runFiles(t, "--config", cfgPath, "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// TestFilePriorityUnknownValue guards the renderer's fallback: an unrecognized
// priority int renders as its decimal value rather than being mislabeled as one
// of the three known priorities.
func TestFilePriorityUnknownValue(t *testing.T) {
	assert.Equal(t, "low", filePriority(-1))
	assert.Equal(t, "normal", filePriority(0))
	assert.Equal(t, "high", filePriority(1))
	assert.Equal(t, "7", filePriority(7))
	assert.Equal(t, "-2", filePriority(-2))
}

// TestPluralizeFiles pins both branches of the count noun. No fixture torrent
// carries exactly one file, so the singular branch is otherwise never reached and
// the helper could return "1 files" with the whole suite green.
func TestPluralizeFiles(t *testing.T) {
	cases := []struct {
		count int
		want  string
	}{
		{0, "0 files"},
		{1, "1 file"},
		{2, "2 files"},
		{3, "3 files"},
	}

	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			assert.Equal(t, tc.want, pluralizeFiles(tc.count))
		})
	}
}

// filesUpdateStub emulates a Transmission server for the files update command:
// it answers torrent-get with the fixture torrents filtered by the requested ids
// (so a hash selector resolves the way it would against a real server, and the
// re-fetch after the write returns the same torrent), and records the body of
// every non-handshake request keyed by method, so a test can assert exactly what
// was written — or that nothing was.
//
// It also counts the calls per method. The body map keeps only the last body
// seen for a method, so a duplicate write would silently overwrite the first;
// the count is what lets a test pin "exactly one torrent-set".
type filesUpdateStub struct {
	torrents []json.RawMessage

	mu       sync.Mutex
	requests map[string]string
	counts   map[string]int
}

func (s *filesUpdateStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		body, _ := io.ReadAll(r.Body)

		var req struct {
			Method    string `json:"method"`
			Arguments struct {
				IDs []json.RawMessage `json:"ids"`
			} `json:"arguments"`
		}
		_ = json.Unmarshal(body, &req)

		s.mu.Lock()
		if s.requests == nil {
			s.requests = map[string]string{}
			s.counts = map[string]int{}
		}
		s.requests[req.Method] = string(body)
		s.counts[req.Method]++
		s.mu.Unlock()

		if req.Method != "torrent-get" {
			_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
			return
		}

		payload, _ := json.Marshal(map[string]any{
			"result":    "success",
			"arguments": map[string]any{"torrents": filterFixtureTorrents(s.torrents, req.Arguments.IDs)},
			"tag":       1,
		})
		_, _ = w.Write(payload)
	}
}

func (s *filesUpdateStub) request(method string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	body, ok := s.requests[method]

	return body, ok
}

// count reports how many times the method was called.
func (s *filesUpdateStub) count(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.counts[method]
}

// filesUpdateConfigPath stands up a recording, id-filtering transmission stub
// serving the given torrent-get fixture and writes a config pointing at it,
// returning the config path, the stub for assertions, and a cleanup func.
func filesUpdateConfigPath(t *testing.T, body string) (string, *filesUpdateStub, func()) {
	t.Helper()

	keyring.MockInit()

	var fixture struct {
		Arguments struct {
			Torrents []json.RawMessage `json:"torrents"`
		} `json:"arguments"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &fixture))

	stub := &filesUpdateStub{torrents: fixture.Arguments.Torrents}
	srv := httptest.NewServer(stub.handler())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	return cfgPath, stub, srv.Close
}

// runFilesUpdate executes "files update" through the files command, so the
// subcommand is reached the same way a real invocation reaches it.
func runFilesUpdate(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	return runFiles(t, append([]string{"update"}, args...)...)
}

// runTorrent executes args against the whole torrent group, so command routing
// (not just a single constructor) is exercised.
func runTorrent(t *testing.T, args ...string) (string, error) {
	t.Helper()

	cmd := NewTorrentCmd()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), err
}

// TestFilesUpdateRoutesPastTheParentSelector is the guard for the first command
// in this repo that mixes a positional selector with a subcommand: cobra matches
// args[0] against child names before treating it as an argument, so the literal
// word "update" reaches the child while a selector stays on the parent. A
// selector can never collide with it — ids are numeric and hashes are 40 hex
// characters.
func TestFilesUpdateRoutesPastTheParentSelector(t *testing.T) {
	t.Run("update reaches the subcommand", func(t *testing.T) {
		cfgPath, stub, closeSrv := filesUpdateConfigPath(t, bespokeFilesResponse)
		defer closeSrv()

		out, err := runTorrent(t, "files", "update", "1", "0", "--wanted", "--config", cfgPath)
		require.NoError(t, err)

		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		require.Len(t, lines, 1, "only the affected file row is rendered")
		assert.Equal(t, "0", strings.Split(lines[0], "\t")[0])

		body, ok := stub.request("torrent-set")
		require.True(t, ok, "the subcommand issued the write")
		assert.Contains(t, body, `"files-wanted":[0]`)
	})

	t.Run("a selector stays on the parent", func(t *testing.T) {
		cfgPath, stub, closeSrv := filesUpdateConfigPath(t, bespokeFilesResponse)
		defer closeSrv()

		out, err := runTorrent(t, "files", "1", "--config", cfgPath)
		require.NoError(t, err)

		assert.Len(t, strings.Split(strings.TrimRight(out, "\n"), "\n"), 3,
			"the parent listed the torrent's three files")

		_, ok := stub.request("torrent-set")
		assert.False(t, ok, "listing files writes nothing")
	})
}

// TestFileIndexSpecAccepts covers the accepted index syntax: bare integers,
// comma-separated lists, inclusive ranges, the literal "all", and any mix of
// those across several positional arguments. The resolved set is sorted and
// de-duplicated.
func TestFileIndexSpecAccepts(t *testing.T) {
	const count = 10

	cases := []struct {
		name string
		args []string
		want []int
	}{
		{"bare integer", []string{"3"}, []int{3}},
		{"comma list", []string{"0,2,5"}, []int{0, 2, 5}},
		{"inclusive range", []string{"2-7"}, []int{2, 3, 4, 5, 6, 7}},
		{"single-element range", []string{"4-4"}, []int{4}},
		{"mixed across arguments", []string{"0,2-4", "9", "1"}, []int{0, 1, 2, 3, 4, 9}},
		{"overlapping tokens are de-duplicated", []string{"1-3", "2,3,4"}, []int{1, 2, 3, 4}},
		{"whitespace is trimmed", []string{" 5 , 6 "}, []int{5, 6}},
		{"all", []string{"all"}, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}},
		{"all is case-insensitive", []string{"ALL"}, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := parseFileIndexes(tc.args)
			require.NoError(t, err)

			indexes, err := spec.resolve(count)
			require.NoError(t, err)
			assert.Equal(t, tc.want, indexes)
		})
	}
}

// TestFileIndexSpecRejects covers the malformed index syntax, each error naming
// the offending argument so it can be found among several.
func TestFileIndexSpecRejects(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"reversed range", []string{"7-2"}, `invalid file index "7-2": range start must not be greater than range end`},
		{"negative index", []string{"0,-1"}, `invalid file index "-1" in "0,-1": must be a non-negative integer`},
		{"non-integer token", []string{"two"}, `invalid file index "two": must be a non-negative integer`},
		{"non-integer range bound", []string{"1-x"}, `invalid file index "1-x": must be a non-negative integer`},
		{"empty token", []string{"0,,2"}, `invalid file index "" in "0,,2": must not be empty`},
		{"empty argument", []string{""}, `invalid file index "": must not be empty`},
		{"all combined with an index", []string{"all", "3"}, `"all" must be the only file index given`},
		{"all combined within an argument", []string{"1,all"}, `"all" must be the only file index given`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseFileIndexes(tc.args)
			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
		})
	}
}

// TestFilesUpdateRejectsOutOfRangeIndex asserts the pre-write validation: an
// index the torrent does not have errors naming the index and the file count, and
// nothing is written.
func TestFilesUpdateRejectsOutOfRangeIndex(t *testing.T) {
	cases := []struct {
		name  string
		index string
		want  string
	}{
		{"bare index", "9", "file index 9 is out of range: the torrent has 3 files"},
		{"range end", "1-9", "file index 9 is out of range: the torrent has 3 files"},
		{"range start", "5-9", "file index 5 is out of range: the torrent has 3 files"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := filesUpdateConfigPath(t, bespokeFilesResponse)
			defer closeSrv()

			out, _, err := runFilesUpdate(t, "--config", cfgPath, "1", tc.index, "--unwanted")
			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
			assert.Empty(t, out)

			_, ok := stub.request("torrent-set")
			assert.False(t, ok, "an out-of-range index writes nothing")
		})
	}
}

// TestFilesUpdateRejectsIndexesAgainstAFilelessTorrent asserts that a torrent
// with no files — the realistic case is a magnet whose metadata has not arrived
// yet — rejects every index form, "all" included, and writes nothing. Without the
// "all" guard the command would send a torrent-set carrying only ids and exit 0,
// reporting a change that could not have happened.
func TestFilesUpdateRejectsIndexesAgainstAFilelessTorrent(t *testing.T) {
	cases := []struct {
		name  string
		index string
		want  string
	}{
		{"bare index", "0", "file index 0 is out of range: the torrent has 0 files"},
		{"all", "all", "cannot update all files: the torrent has 0 files"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := filesUpdateConfigPath(t, bespokeFilesResponse)
			defer closeSrv()

			// Torrent 2 in the fixture has no files.
			out, _, err := runFilesUpdate(t, "--config", cfgPath, "2", tc.index, "--unwanted")
			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
			assert.Empty(t, out)

			_, ok := stub.request("torrent-set")
			assert.False(t, ok, "a fileless torrent is never written to")
		})
	}
}

// TestFilesUpdateRequiresAFieldFlag asserts the flag guards: --wanted and
// --unwanted are mutually exclusive, and an invocation with no field flag is an
// error rather than a silent no-op. Both are caught before the command dials, so
// nothing is written.
func TestFilesUpdateRequiresAFieldFlag(t *testing.T) {
	cases := []struct {
		name  string
		flags []string
		want  string
	}{
		{"no field flag", nil, "nothing to change: provide --priority, --wanted, or --unwanted"},
		{"wanted and unwanted", []string{"--wanted", "--unwanted"}, "--wanted and --unwanted cannot be used together"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := filesUpdateConfigPath(t, bespokeFilesResponse)
			defer closeSrv()

			args := append([]string{"--config", cfgPath, "1", "0"}, tc.flags...)

			out, _, err := runFilesUpdate(t, args...)
			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
			assert.Empty(t, out)

			_, ok := stub.request("torrent-set")
			assert.False(t, ok, "no write is issued")
		})
	}
}

// TestFilesUpdateWantedFalseIsNotUnwanted pins the switch semantics of --wanted
// and --unwanted. They are read by value, not through Flags().Changed, so
// "--wanted=false" reads as "not given" and falls into the "nothing to change"
// error rather than marking the file unwanted. That is deliberate: reading them
// the way --priority is read would make "--wanted=false" write *wanted*, the
// opposite mutation. A later consistency refactor must not silently invert this.
func TestFilesUpdateWantedFalseIsNotUnwanted(t *testing.T) {
	cases := []struct {
		name string
		flag string
	}{
		{"wanted=false", "--wanted=false"},
		{"unwanted=false", "--unwanted=false"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := filesUpdateConfigPath(t, bespokeFilesResponse)
			defer closeSrv()

			out, _, err := runFilesUpdate(t, "--config", cfgPath, "1", "0", tc.flag)
			require.Error(t, err)
			assert.Equal(t, "nothing to change: provide --priority, --wanted, or --unwanted", err.Error())
			assert.Empty(t, out)

			assert.Zero(t, stub.count("torrent-set"), "a false switch never writes")
		})
	}
}

func TestFilesUpdateRejectsInvalidPriority(t *testing.T) {
	cfgPath, stub, closeSrv := filesUpdateConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	_, _, err := runFilesUpdate(t, "--config", cfgPath, "1", "0", "--priority", "urgent")
	require.Error(t, err)
	assert.Equal(t, "invalid value for --priority: must be low, normal, or high", err.Error())

	_, ok := stub.request("torrent-set")
	assert.False(t, ok, "an invalid priority writes nothing")
}

// TestFilesUpdateSendsFileChanges asserts the flags map 1:1 onto
// transmission.FileChanges, in a single torrent-set call carrying the resolved
// index set and a one-torrent selector.
func TestFilesUpdateSendsFileChanges(t *testing.T) {
	cases := []struct {
		name    string
		indexes []string
		flags   []string
		want    []string
		absent  []string
	}{
		{
			name:    "wanted",
			indexes: []string{"0,2"},
			flags:   []string{"--wanted"},
			want:    []string{`"files-wanted":[0,2]`},
			absent:  []string{"files-unwanted", "priority-"},
		},
		{
			name:    "unwanted",
			indexes: []string{"2"},
			flags:   []string{"--unwanted"},
			want:    []string{`"files-unwanted":[2]`},
			absent:  []string{"files-wanted", "priority-"},
		},
		{
			name:    "priority high",
			indexes: []string{"0-1"},
			flags:   []string{"--priority", "high"},
			want:    []string{`"priority-high":[0,1]`},
			absent:  []string{"files-wanted", "files-unwanted", "priority-normal", "priority-low"},
		},
		{
			name:    "priority normal",
			indexes: []string{"1"},
			flags:   []string{"--priority", "normal"},
			want:    []string{`"priority-normal":[1]`},
			absent:  []string{"priority-high", "priority-low"},
		},
		{
			name:    "priority low",
			indexes: []string{"1"},
			flags:   []string{"--priority", "LOW"},
			want:    []string{`"priority-low":[1]`},
			absent:  []string{"priority-high", "priority-normal"},
		},
		{
			name:    "priority and selection together",
			indexes: []string{"all"},
			flags:   []string{"--priority", "low", "--unwanted"},
			want:    []string{`"files-unwanted":[0,1,2]`, `"priority-low":[0,1,2]`},
			absent:  []string{"files-wanted", "priority-high", "priority-normal"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := filesUpdateConfigPath(t, bespokeFilesResponse)
			defer closeSrv()

			args := append([]string{"--config", cfgPath, "1"}, tc.indexes...)
			args = append(args, tc.flags...)

			_, _, err := runFilesUpdate(t, args...)
			require.NoError(t, err)

			body, ok := stub.request("torrent-set")
			require.True(t, ok, "a torrent-set call is issued")
			assert.Equal(t, 1, stub.count("torrent-set"), "exactly one torrent-set call is issued")

			assert.Contains(t, body, `"ids":[1]`, "the write targets a single torrent")
			for _, want := range tc.want {
				assert.Contains(t, body, want)
			}
			for _, absent := range tc.absent {
				assert.NotContains(t, body, absent)
			}
		})
	}
}

// TestFilesUpdateRendersOnlyAffectedRows asserts the post-write render: the
// re-fetched detail is filtered to the affected indices, in ascending index
// order, through the same registry and renderer as "torrent files".
func TestFilesUpdateRendersOnlyAffectedRows(t *testing.T) {
	cfgPath, _, closeSrv := filesUpdateConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	out, _, err := runFilesUpdate(t, "--config", cfgPath, "1", "2,0", "--priority", "high")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2, "only the two affected files are rendered")

	first := strings.Split(lines[0], "\t")
	require.Len(t, first, len(fileFieldKeys), "the files registry's full default column set")
	assert.Equal(t, "0", first[0])
	assert.Equal(t, "ubuntu-24.04.iso", first[1])

	second := strings.Split(lines[1], "\t")
	assert.Equal(t, "2", second[0], "rows are in ascending index order, not argument order")
	assert.Equal(t, "empty.dat", second[1])
}

func TestFilesUpdateJSONEmitsAffectedFiles(t *testing.T) {
	cfgPath, _, closeSrv := filesUpdateConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	out, _, err := runFilesUpdate(t, "--config", cfgPath, "1", "2,0", "--unwanted", "--json")
	require.NoError(t, err)

	var got []struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 2)
	assert.Equal(t, "ubuntu-24.04.iso", got[0].Name)
	assert.Equal(t, "empty.dat", got[1].Name)
}

func TestFilesUpdateJQ(t *testing.T) {
	cfgPath, _, closeSrv := filesUpdateConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	out, _, err := runFilesUpdate(t, "--config", cfgPath, "1", "1", "--wanted", "--jq", ".[].name")
	require.NoError(t, err)
	assert.Equal(t, `"SHA256SUMS"`, strings.TrimSpace(out))
}

func TestFilesUpdateFieldsUsesTheFilesRegistry(t *testing.T) {
	cfgPath, _, closeSrv := filesUpdateConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	out, _, err := runFilesUpdate(t, "--config", cfgPath, "1", "0", "--unwanted", "--fields", "name,index")
	require.NoError(t, err)
	assert.Equal(t, "ubuntu-24.04.iso\t0", strings.TrimRight(out, "\n"))

	_, _, err = runFilesUpdate(t, "--config", cfgPath, "1", "0", "--unwanted", "--fields", "status")
	require.Error(t, err)
	assert.Equal(t, `invalid field "status": valid fields are `+validFieldKeys(fileFields), err.Error(),
		"the error lists only the files command's own keys")
}

func TestFilesUpdateResolvesByHash(t *testing.T) {
	cfgPath, stub, closeSrv := filesUpdateConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	out, _, err := runFilesUpdate(t, "--config", cfgPath, strings.ToUpper(infoHash3), "all", "--wanted")
	require.NoError(t, err)
	assert.Len(t, strings.Split(strings.TrimRight(out, "\n"), "\n"), 2,
		`"all" selected both of torrent 3's files`)

	body, ok := stub.request("torrent-set")
	require.True(t, ok)
	assert.Contains(t, body, `"ids":[3]`, "the hash resolved to its numeric id")
	assert.Contains(t, body, `"files-wanted":[0,1]`)
}

func TestFilesUpdateWrongArgCount(t *testing.T) {
	cfgPath, _, closeSrv := filesUpdateConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	t.Run("selector only", func(t *testing.T) {
		_, _, err := runFilesUpdate(t, "--config", cfgPath, "1", "--wanted")
		require.Error(t, err, "at least one index is required")
	})

	t.Run("no args", func(t *testing.T) {
		_, _, err := runFilesUpdate(t, "--config", cfgPath, "--wanted")
		require.Error(t, err)
	})
}

func TestFilesUpdateInvalidSelector(t *testing.T) {
	cfgPath, stub, closeSrv := filesUpdateConfigPath(t, bespokeFilesResponse)
	defer closeSrv()

	_, _, err := runFilesUpdate(t, "--config", cfgPath, "not-an-id", "0", "--wanted")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid torrent selector")

	_, ok := stub.request("torrent-set")
	assert.False(t, ok, "a malformed selector writes nothing")
}

func TestFilesUpdateNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runFilesUpdate(t, "--config", cfgPath, "1", "0", "--wanted")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// TestSelectFileRowsSkipsMissingIndices guards the post-write re-fetch: the
// second round-trip could in principle come back with fewer files, and a stale
// index must not panic the renderer.
func TestSelectFileRowsSkipsMissingIndices(t *testing.T) {
	files := []transmission.File{{Name: "a.iso"}, {Name: "b.iso"}}

	rows := selectFileRows(files, []int{0, 5})
	require.Len(t, rows, 1)
	assert.Equal(t, 0, rows[0].Index)
	assert.Equal(t, "a.iso", rows[0].Name)

	assert.Empty(t, rowFiles(selectFileRows(nil, []int{0})))
}
