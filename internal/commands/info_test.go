package commands

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
)

// Two 40-character SHA1 info-hashes used by the info fixtures so the hash
// lookup path can be exercised (ParseSelector only treats a 40-char hex token
// as a hash).
const (
	infoHash1 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	infoHash2 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// bespokeInfoResponse is a torrent-get payload covering two torrents with
// distinct ids and full 40-char hashes, used to exercise the info command's
// id and hash resolution.
const bespokeInfoResponse = `{
	"arguments": {"torrents": [
		{
			"id": 1,
			"hashString": "` + infoHash1 + `",
			"name": "ubuntu-24.04.iso",
			"status": 4,
			"percentDone": 0.5,
			"totalSize": 2147483648,
			"rateDownload": 1572864,
			"rateUpload": 524288,
			"uploadRatio": 0.5,
			"eta": 3600,
			"downloadDir": "/downloads",
			"labels": ["linux"]
		},
		{
			"id": 2,
			"hashString": "` + infoHash2 + `",
			"name": "debian.iso",
			"status": 6,
			"percentDone": 1.0,
			"totalSize": 1073741824,
			"rateUpload": 131072,
			"uploadRatio": 2.5,
			"eta": -1,
			"downloadDir": "/downloads",
			"labels": []
		}
	]},
	"result": "success",
	"tag": 1
}`

// bespokeInfoSanitizeResponse is a torrent-get payload whose name, labels,
// download dir, and error carry terminal control sequences, used to verify the
// sanitize-in-human-but-not-json boundary.
const bespokeInfoSanitizeResponse = `{
	"arguments": {"torrents": [
		{
			"id": 1,
			"hashString": "` + infoHash1 + `",
			"name": "\u001b[2Jmalicious",
			"status": 4,
			"downloadDir": "/d\u001bir",
			"errorString": "boom\u0007",
			"labels": ["li\u001bnux"]
		}
	]},
	"result": "success",
	"tag": 1
}`

// runInfo executes the info command with args, returning stdout, stderr, and the
// Execute error. The command's output is a *bytes.Buffer, so isTTY reports false
// and the piped (tab-separated) plaintext contract is exercised.
func runInfo(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewInfoCmd()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// infoConfigPath stands up a transmission stub serving the given torrent-get
// payload on the bespoke path and writes a config pointing at it.
func infoConfigPath(t *testing.T, body string) (string, func()) {
	t.Helper()

	keyring.MockInit()

	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // bespoke path
		responses: map[string]string{
			"torrent-get": body,
		},
	}.handler())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{ServerURL: srv.URL}, cfgPath))

	return cfgPath, srv.Close
}

func TestInfoResolvesByID(t *testing.T) {
	cfgPath, closeSrv := infoConfigPath(t, bespokeInfoResponse)
	defer closeSrv()

	out, _, err := runInfo(t, "--config", cfgPath, "1")
	require.NoError(t, err)

	// Piped plaintext is a header-less "key<TAB>value" block, one line per field.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, len(allFieldKeys), "one line per registry field")

	fields := infoFields(t, out)
	assert.Equal(t, "1", fields["ID"])
	assert.Equal(t, infoHash1, fields["Hash"])
	assert.Equal(t, "ubuntu-24.04.iso", fields["Name"])
	assert.Equal(t, "Downloading", fields["Status"])
	assert.Equal(t, "50%", fields["Done"])
}

func TestInfoResolvesByHash(t *testing.T) {
	cfgPath, closeSrv := infoConfigPath(t, bespokeInfoResponse)
	defer closeSrv()

	// An uppercase hash resolves case-insensitively to the matching torrent.
	out, _, err := runInfo(t, "--config", cfgPath, strings.ToUpper(infoHash2))
	require.NoError(t, err)

	fields := infoFields(t, out)
	assert.Equal(t, "2", fields["ID"])
	assert.Equal(t, infoHash2, fields["Hash"])
	assert.Equal(t, "debian.iso", fields["Name"])
	assert.Equal(t, "Seeding", fields["Status"])
}

func TestInfoJSONSingleObject(t *testing.T) {
	cfgPath, closeSrv := infoConfigPath(t, bespokeInfoResponse)
	defer closeSrv()

	out, _, err := runInfo(t, "--config", cfgPath, "2", "--json")
	require.NoError(t, err)

	// --json emits a single object, not an array.
	trimmed := strings.TrimSpace(out)
	assert.True(t, strings.HasPrefix(trimmed, "{"), "JSON output is a single object")
	assert.False(t, strings.HasPrefix(trimmed, "["), "JSON output is not an array")

	var got struct {
		ID     int64  `json:"id"`
		Hash   string `json:"hash"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, int64(2), got.ID)
	assert.Equal(t, infoHash2, got.Hash)
	assert.Equal(t, "debian.iso", got.Name)
	assert.Equal(t, "seeding", got.Status, "status serializes as its slug")
}

func TestInfoJQ(t *testing.T) {
	cfgPath, closeSrv := infoConfigPath(t, bespokeInfoResponse)
	defer closeSrv()

	out, _, err := runInfo(t, "--config", cfgPath, "1", "--jq", ".name")
	require.NoError(t, err)

	assert.Equal(t, `"ubuntu-24.04.iso"`, strings.TrimSpace(out))
}

func TestInfoNotFoundErrors(t *testing.T) {
	cfgPath, closeSrv := infoConfigPath(t, bespokeInfoResponse)
	defer closeSrv()

	// An absent hash (well-formed but matching nothing) errors with a non-zero
	// exit and writes nothing to stdout.
	absent := "cccccccccccccccccccccccccccccccccccccccc"
	out, _, err := runInfo(t, "--config", cfgPath, absent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no torrent matched: "+absent)
	assert.Empty(t, out, "no output is written when nothing matched")
}

func TestInfoWrongArgCount(t *testing.T) {
	cfgPath, closeSrv := infoConfigPath(t, bespokeInfoResponse)
	defer closeSrv()

	t.Run("zero args", func(t *testing.T) {
		_, _, err := runInfo(t, "--config", cfgPath)
		require.Error(t, err, "zero positional args is a usage error")
	})

	t.Run("two args", func(t *testing.T) {
		_, _, err := runInfo(t, "--config", cfgPath, "1", "2")
		require.Error(t, err, "more than one positional arg is a usage error")
	})
}

func TestInfoInvalidSelector(t *testing.T) {
	cfgPath, closeSrv := infoConfigPath(t, bespokeInfoResponse)
	defer closeSrv()

	// A token that is neither all-digits nor a 40-char hex hash is rejected by
	// the selector before any backend call.
	_, _, err := runInfo(t, "--config", cfgPath, "not-an-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid torrent selector")
}

func TestInfoSanitizesHumanOutputNotJSON(t *testing.T) {
	cfgPath, closeSrv := infoConfigPath(t, bespokeInfoSanitizeResponse)
	defer closeSrv()

	// Plaintext: untrusted server fields are stripped of control sequences.
	human, _, err := runInfo(t, "--config", cfgPath, "1")
	require.NoError(t, err)
	assert.NotContains(t, human, "\x1b", "raw ESC must not reach plaintext output")
	assert.NotContains(t, human, "\x07", "raw BEL must not reach plaintext output")
	assert.Contains(t, human, "[2Jmalicious", "visible name text survives sanitization")
	assert.Contains(t, human, "/dir", "download dir is sanitized")
	assert.Contains(t, human, "boom", "error is sanitized")
	assert.Contains(t, human, "linux", "labels are sanitized")

	// JSON: the structured contract is never sanitized, so the raw control
	// bytes survive in the encoded output.
	raw, _, err := runInfo(t, "--config", cfgPath, "1", "--json")
	require.NoError(t, err)

	var got struct {
		Name        string   `json:"name"`
		DownloadDir string   `json:"download_dir"`
		Error       string   `json:"error"`
		Labels      []string `json:"labels"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &got))
	assert.Contains(t, got.Name, "\x1b", "JSON name keeps the raw ESC (unsanitized)")
	assert.Contains(t, got.DownloadDir, "\x1b", "JSON download dir keeps the raw ESC")
	assert.Contains(t, got.Error, "\x07", "JSON error keeps the raw BEL")
	require.Len(t, got.Labels, 1)
	assert.Contains(t, got.Labels[0], "\x1b", "JSON label keeps the raw ESC")
}

func TestInfoFieldsSubsetAndOrder(t *testing.T) {
	cfgPath, closeSrv := infoConfigPath(t, bespokeInfoResponse)
	defer closeSrv()

	// --fields selects a subset and renders the lines in the given order,
	// case-insensitively after trimming whitespace.
	out, _, err := runInfo(t, "--config", cfgPath, "1", "--fields", "Name, id ,status")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3, "only the selected fields are shown")

	// Each piped line is "header<TAB>value"; assert the header order.
	headers := make([]string, len(lines))
	for i, line := range lines {
		key, _, ok := strings.Cut(line, "\t")
		require.True(t, ok, "each plaintext line is key<TAB>value: %q", line)
		headers[i] = key
	}
	assert.Equal(t, []string{"Name", "ID", "Status"}, headers)
}

func TestInfoFieldsInvalidKey(t *testing.T) {
	cfgPath, closeSrv := infoConfigPath(t, bespokeInfoResponse)
	defer closeSrv()

	_, _, err := runInfo(t, "--config", cfgPath, "1", "--fields", "name,bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid field "bogus"`)
	assert.Contains(t, err.Error(), "valid fields are")
}

func TestInfoFieldsEmptyAfterParse(t *testing.T) {
	cfgPath, closeSrv := infoConfigPath(t, bespokeInfoResponse)
	defer closeSrv()

	// A non-empty spec that resolves to no keys (only separators/whitespace) is
	// an error, not a fall-through to the default set.
	_, _, err := runInfo(t, "--config", cfgPath, "1", "--fields", " , ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no fields selected")
}

func TestInfoFieldsIgnoredByJSON(t *testing.T) {
	cfgPath, closeSrv := infoConfigPath(t, bespokeInfoResponse)
	defer closeSrv()

	// --json always emits the full object regardless of --fields.
	out, _, err := runInfo(t, "--config", cfgPath, "1", "--fields", "name", "--json")
	require.NoError(t, err)

	var got struct {
		ID     int64  `json:"id"`
		Hash   string `json:"hash"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, int64(1), got.ID, "id present despite --fields=name")
	assert.Equal(t, infoHash1, got.Hash, "hash present despite --fields=name")
	assert.Equal(t, "ubuntu-24.04.iso", got.Name)
	assert.Equal(t, "downloading", got.Status, "status present despite --fields=name")
}

func TestInfoNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runInfo(t, "--config", cfgPath, "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// infoFields parses the piped "key<TAB>value" plaintext block into a map keyed
// by the field's header label.
func infoFields(t *testing.T, out string) map[string]string {
	t.Helper()

	fields := make(map[string]string)
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		key, value, ok := strings.Cut(line, "\t")
		require.True(t, ok, "each plaintext line is key<TAB>value: %q", line)
		fields[key] = value
	}

	return fields
}
