package commands

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// bespokeFilesResponse is a torrent-get payload whose first torrent carries the
// parallel files/fileStats/wanted/priorities arrays the bespoke scheme uses, and
// whose second torrent has no files at all (the empty-collection path). The third
// file is zero-length, which is legal in a torrent and must not divide by zero in
// the percent column.
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
