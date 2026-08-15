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
)

// bespokeLabelResponse is a torrent-get payload for the bespoke wire scheme.
// Torrent 2 omits the labels key entirely (rather than sending an empty array),
// which is the nil-slice path labelEntries has to turn into "labels": [] instead
// of null.
const bespokeLabelResponse = `{
	"arguments": {"torrents": [
		{"id": 1, "hashString": "` + infoHash1 + `", "name": "ubuntu-24.04.iso", "labels": ["hd", "movies"]},
		{"id": 2, "hashString": "` + infoHash2 + `", "name": "debian.iso"},
		{"id": 3, "hashString": "` + infoHash3 + `", "name": "fedora.iso", "labels": ["linux"]}
	]},
	"result": "success",
	"tag": 1
}`

// jsonrpcLabelResponse is the same fixture under the JSON-RPC 2.0 scheme
// (snake_case hash_string, result-wrapped), so every behavior can be asserted on
// both wire schemes.
const jsonrpcLabelResponse = `{
	"jsonrpc": "2.0",
	"result": {"torrents": [
		{"id": 1, "hash_string": "` + infoHash1 + `", "name": "ubuntu-24.04.iso", "labels": ["hd", "movies"]},
		{"id": 2, "hash_string": "` + infoHash2 + `", "name": "debian.iso"},
		{"id": 3, "hash_string": "` + infoHash3 + `", "name": "fedora.iso", "labels": ["linux"]}
	]},
	"id": 1
}`

// bespokeLabelEmptyResponse is a torrent-get payload for a server with no
// torrents at all, which is what makes the empty --json result "[]" rather than
// "null".
const bespokeLabelEmptyResponse = `{
	"arguments": {"torrents": []},
	"result": "success",
	"tag": 1
}`

// bespokeLabelSanitizeResponse carries terminal control sequences in both
// untrusted, server-derived cells: the torrent name and one of its labels.
const bespokeLabelSanitizeResponse = `{
	"arguments": {"torrents": [
		{"id": 1, "hashString": "` + infoHash1 + `", "name": "\u001b[2Jmalicious\u0007.iso", "labels": ["\u001b[2Jevil\u0007"]}
	]},
	"result": "success",
	"tag": 1
}`

// labelRequest is one recorded non-handshake request: the RPC method and the
// verbatim body, so tests can assert both the round-trip count and what was sent.
type labelRequest struct {
	method string
	body   string
}

// labelStub emulates a Transmission server for the label commands under either
// wire scheme: it answers torrent-get/torrent_get with a canned payload, answers
// every other method with a generic success envelope, and records every request
// in order.
type labelStub struct {
	rpcVersion string
	listBody   string

	mu       sync.Mutex
	requests []labelRequest
}

func (s *labelStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			if s.rpcVersion != "" {
				w.Header().Set(rpcVersionHeader, s.rpcVersion)
			}
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		body, _ := io.ReadAll(r.Body)

		var envelope struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &envelope)

		s.mu.Lock()
		s.requests = append(s.requests, labelRequest{method: envelope.Method, body: string(body)})
		s.mu.Unlock()

		if envelope.Method == "torrent-get" || envelope.Method == "torrent_get" {
			_, _ = w.Write([]byte(s.listBody))
			return
		}

		_, _ = w.Write([]byte(`{"jsonrpc": "2.0", "result": "success", "arguments": {}, "id": 1, "tag": 1}`))
	}
}

// bodies returns the recorded request bodies for any of the named methods, in
// request order. Callers pass both spellings of a method (torrent-get and
// torrent_get) so one assertion serves both wire schemes.
func (s *labelStub) bodies(methods ...string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var found []string
	for _, req := range s.requests {
		for _, method := range methods {
			if req.method == method {
				found = append(found, req.body)
				break
			}
		}
	}

	return found
}

// labelConfigPath stands up a recording transmission stub serving listBody under
// the given rpc version and writes a config pointing at it.
func labelConfigPath(t *testing.T, rpcVersion, listBody string) (string, *labelStub, func()) {
	t.Helper()

	keyring.MockInit()

	stub := &labelStub{rpcVersion: rpcVersion, listBody: listBody}
	srv := httptest.NewServer(stub.handler())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	return cfgPath, stub, srv.Close
}

// runLabel executes the label command tree with args, returning stdout, stderr,
// and the Execute error. The command's output is a *bytes.Buffer, so isTTY
// reports false and the piped (tab-separated) plaintext contract is exercised.
func runLabel(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewLabelCmd()
	AddGlobalFlags(cmd)

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// labelRows splits piped plaintext output into its tab-separated cells, one row
// per line.
func labelRows(t *testing.T, out string) [][]string {
	t.Helper()

	trimmed := strings.TrimRight(out, "\n")
	if trimmed == "" {
		return nil
	}

	lines := strings.Split(trimmed, "\n")
	rows := make([][]string, len(lines))
	for i, line := range lines {
		rows[i] = strings.Split(line, "\t")
	}

	return rows
}

func TestLabelGroupPrintsHelp(t *testing.T) {
	out, _, err := runLabel(t)
	require.NoError(t, err, "a bare 'torrent label' prints help and exits 0")
	assert.Contains(t, out, "Usage:")
	assert.Contains(t, out, "list")
}

func TestLabelListPipedBespoke(t *testing.T) {
	cfgPath, _, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	out, _, err := runLabel(t, "list", "--config", cfgPath, "1")
	require.NoError(t, err)

	rows := labelRows(t, out)
	require.Len(t, rows, 1, "piped output is header-less, one row per torrent")
	assert.Equal(t, []string{"1", "ubuntu-24.04.iso", "hd,movies"}, rows[0])
}

func TestLabelListPipedJSONRPC(t *testing.T) {
	cfgPath, _, closeSrv := labelConfigPath(t, "6.0.0", jsonrpcLabelResponse)
	defer closeSrv()

	out, _, err := runLabel(t, "list", "--config", cfgPath, "1")
	require.NoError(t, err)

	rows := labelRows(t, out)
	require.Len(t, rows, 1)
	assert.Equal(t, []string{"1", "ubuntu-24.04.iso", "hd,movies"}, rows[0])
}

func TestLabelListByHash(t *testing.T) {
	cfgPath, _, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	// An uppercase hash resolves case-insensitively, via ParseSelector's
	// normalization.
	out, _, err := runLabel(t, "list", "--config", cfgPath, strings.ToUpper(infoHash3))
	require.NoError(t, err)

	rows := labelRows(t, out)
	require.Len(t, rows, 1)
	assert.Equal(t, []string{"3", "fedora.iso", "linux"}, rows[0])
}

// TestLabelListSeveralSelectors mixes an id and a hash, and covers the
// no-labels torrent: its cell is empty, not "-" or "(none)", so the piped form
// stays parseable.
func TestLabelListSeveralSelectors(t *testing.T) {
	cfgPath, _, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	out, _, err := runLabel(t, "list", "--config", cfgPath, "1", infoHash2)
	require.NoError(t, err)

	rows := labelRows(t, out)
	require.Len(t, rows, 2)
	assert.Equal(t, []string{"1", "ubuntu-24.04.iso", "hd,movies"}, rows[0])
	assert.Equal(t, []string{"2", "debian.iso", ""}, rows[1])
}

func TestLabelListAll(t *testing.T) {
	cfgPath, _, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	out, _, err := runLabel(t, "list", "--config", cfgPath, "--all")
	require.NoError(t, err)

	rows := labelRows(t, out)
	require.Len(t, rows, 3, "--all lists every torrent")
	assert.Equal(t, "1", rows[0][0])
	assert.Equal(t, "2", rows[1][0])
	assert.Equal(t, "3", rows[2][0])
}

func TestLabelListJSON(t *testing.T) {
	cfgPath, _, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	out, _, err := runLabel(t, "list", "--config", cfgPath, "--all", "--json")
	require.NoError(t, err)

	var got []labelListEntry
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 3)

	assert.Equal(t, int64(1), got[0].ID)
	assert.Equal(t, infoHash1, got[0].Hash)
	assert.Equal(t, "ubuntu-24.04.iso", got[0].Name)
	assert.Equal(t, []string{"hd", "movies"}, got[0].Labels)

	// A torrent with no labels emits an empty array, never null.
	assert.Contains(t, out, `"labels": []`)
	assert.NotContains(t, out, `"labels": null`)
	assert.Empty(t, got[1].Labels)
	assert.NotNil(t, got[1].Labels)
}

func TestLabelListJSONEmptyIsArray(t *testing.T) {
	cfgPath, _, closeSrv := labelConfigPath(t, "", bespokeLabelEmptyResponse)
	defer closeSrv()

	out, _, err := runLabel(t, "list", "--config", cfgPath, "--all", "--json")
	require.NoError(t, err)

	assert.Equal(t, "[]", strings.TrimSpace(out), "an empty result is [], never null")
}

func TestLabelListJQ(t *testing.T) {
	cfgPath, _, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	out, _, err := runLabel(t, "list", "--config", cfgPath, "--all",
		"--jq", `.[] | select(.labels | index("hd")) | .name`)
	require.NoError(t, err)

	assert.Equal(t, `"ubuntu-24.04.iso"`, strings.TrimSpace(out))
}

// TestLabelListJQWinsOverJSON pins the fixed output precedence: --jq → --json →
// plaintext.
func TestLabelListJQWinsOverJSON(t *testing.T) {
	cfgPath, _, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	out, _, err := runLabel(t, "list", "--config", cfgPath, "1", "--json", "--jq", ".[].name")
	require.NoError(t, err)

	assert.Equal(t, `"ubuntu-24.04.iso"`, strings.TrimSpace(out))
}

func TestLabelListFields(t *testing.T) {
	cfgPath, _, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	out, _, err := runLabel(t, "list", "--config", cfgPath, "1", "--fields", "hash,labels")
	require.NoError(t, err)

	rows := labelRows(t, out)
	require.Len(t, rows, 1)
	assert.Equal(t, []string{infoHash1, "hd,movies"}, rows[0])
}

func TestLabelListInvalidField(t *testing.T) {
	cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	_, _, err := runLabel(t, "list", "--config", cfgPath, "1", "--fields", "bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid field "bogus"`)
	assert.Contains(t, err.Error(), "valid fields are hash, id, labels, name")

	assert.Empty(t, stub.bodies("torrent-get", "torrent_get"),
		"a bad field is rejected before any round-trip")
}

// TestLabelListSingleProjectedRoundTrip pins the read budget and the projection:
// exactly one torrent-get, asking for exactly the four fields a label row needs
// rather than the full record.
func TestLabelListSingleProjectedRoundTrip(t *testing.T) {
	cases := []struct {
		name       string
		rpcVersion string
		listBody   string
		want       []string
	}{
		{"bespoke", "", bespokeLabelResponse, []string{"id", "hashString", "name", "labels"}},
		{"jsonrpc", "6.0.0", jsonrpcLabelResponse, []string{"id", "hash_string", "name", "labels"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := labelConfigPath(t, tc.rpcVersion, tc.listBody)
			defer closeSrv()

			_, _, err := runLabel(t, "list", "--config", cfgPath, "1")
			require.NoError(t, err)

			bodies := stub.bodies("torrent-get", "torrent_get")
			require.Len(t, bodies, 1, "the labels are read in exactly one round-trip")

			var req struct {
				Arguments struct {
					Fields []string `json:"fields"`
				} `json:"arguments"`
				Params struct {
					Fields []string `json:"fields"`
				} `json:"params"`
			}
			require.NoError(t, json.Unmarshal([]byte(bodies[0]), &req))

			fields := req.Arguments.Fields
			if len(fields) == 0 {
				fields = req.Params.Fields
			}

			assert.Equal(t, tc.want, fields, "only the four row fields are projected")
		})
	}
}

func TestLabelListRequiresSelector(t *testing.T) {
	cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	_, _, err := runLabel(t, "list", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "specify torrent ids or hashes, or use --all")

	assert.Empty(t, stub.bodies("torrent-get", "torrent_get"),
		"a bare invocation is an error, not a no-op read")
}

func TestLabelListNothingMatchedErrors(t *testing.T) {
	cfgPath, _, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	out, _, err := runLabel(t, "list", "--config", cfgPath, "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no torrents matched: 99")
	assert.Empty(t, out, "nothing is rendered when no selector matched")
}

// TestLabelListPartialMatchRendersMatched covers the deliberate asymmetry: the
// matched rows render, and the unmatched token is not reported on the plaintext
// path, which keeps the piped output a clean tab-separated contract.
func TestLabelListPartialMatchRendersMatched(t *testing.T) {
	cfgPath, _, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	out, _, err := runLabel(t, "list", "--config", cfgPath, "1", "99")
	require.NoError(t, err)

	rows := labelRows(t, out)
	require.Len(t, rows, 1)
	assert.Equal(t, "1", rows[0][0])
	assert.NotContains(t, out, "99", "unmatched tokens stay out of the piped rows")
}

func TestLabelListFetchFailureWrapsServerURL(t *testing.T) {
	keyring.MockInit()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	out, _, err := runLabel(t, "list", "--config", cfgPath, "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to "+srv.URL)
	assert.Empty(t, out, "nothing is rendered on a read failure")
}

// TestLabelListTableEmpty covers the TTY branch of the empty case, which isTTY
// makes unreachable through the command with a *bytes.Buffer.
func TestLabelListTableEmpty(t *testing.T) {
	cols, err := parseFields(labelFields, labelDefaultFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	writeListTable(&buf, []labelListEntry(nil), cols, 80, "No torrents.")

	assert.Equal(t, "No torrents.", strings.TrimSpace(buf.String()))
}

// TestLabelListTableAligned exercises the TTY table branch directly, since isTTY
// needs a real terminal: the header row is present and the id column is
// right-aligned.
func TestLabelListTableAligned(t *testing.T) {
	entries := []labelListEntry{
		{ID: 5, Hash: infoHash1, Name: "Ubuntu 24.04 LTS", Labels: []string{"hd", "movies"}},
		{ID: 12, Hash: infoHash2, Name: "Debian 12", Labels: []string{}},
	}

	cols, err := parseFields(labelFields, labelDefaultFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	// width 0 disables truncation so the full names show.
	writeListTable(&buf, entries, cols, 0, "No torrents.")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3, "header row plus one row per torrent")

	assert.True(t, strings.HasPrefix(lines[0], "ID"))
	assert.Contains(t, lines[0], "Name")
	assert.Contains(t, lines[0], "Labels")
	assert.Contains(t, lines[1], "Ubuntu 24.04 LTS")
	assert.Contains(t, lines[1], "hd,movies")
	assert.Contains(t, lines[2], "Debian 12")
}

// TestLabelListSanitizesHumanOutputNotJSON asserts the sanitize boundary on both
// plaintext paths (piped and TTY table) while leaving the structured --json
// contract byte-for-byte intact. Both the torrent name and its labels are
// server-derived and untrusted.
func TestLabelListSanitizesHumanOutputNotJSON(t *testing.T) {
	cfgPath, _, closeSrv := labelConfigPath(t, "", bespokeLabelSanitizeResponse)
	defer closeSrv()

	// Piped plaintext, end to end through the command.
	piped, _, err := runLabel(t, "list", "--config", cfgPath, "1")
	require.NoError(t, err)
	assert.NotContains(t, piped, "\x1b", "raw ESC must not reach the piped output")
	assert.NotContains(t, piped, "\x07", "raw BEL must not reach the piped output")
	assert.Contains(t, piped, "[2Jmalicious.iso", "visible name text survives sanitization")
	assert.Contains(t, piped, "[2Jevil", "visible label text survives sanitization")

	// TTY table path, exercised directly.
	cols, err := parseFields(labelFields, labelDefaultFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	writeListTable(&buf, []labelListEntry{{
		ID:     1,
		Name:   "\x1b[2Jmalicious\x07.iso",
		Labels: []string{"\x1b[2Jevil\x07"},
	}}, cols, 0, "No torrents.")

	table := buf.String()
	assert.NotContains(t, table, "\x1b", "raw ESC must not reach the table output")
	assert.NotContains(t, table, "\x07", "raw BEL must not reach the table output")
	assert.Contains(t, table, "[2Jmalicious.iso")
	assert.Contains(t, table, "[2Jevil")

	// JSON is a structured contract and is never sanitized.
	raw, _, err := runLabel(t, "list", "--config", cfgPath, "1", "--json")
	require.NoError(t, err)

	var got []labelListEntry
	require.NoError(t, json.Unmarshal([]byte(raw), &got))
	require.Len(t, got, 1)
	require.Len(t, got[0].Labels, 1)
	assert.Contains(t, got[0].Name, "\x1b", "JSON name keeps the raw ESC (unsanitized)")
	assert.Contains(t, got[0].Name, "\x07", "JSON name keeps the raw BEL")
	assert.Contains(t, got[0].Labels[0], "\x1b", "JSON labels keep the raw ESC")
	assert.Contains(t, got[0].Labels[0], "\x07", "JSON labels keep the raw BEL")
}

// TestLabelFieldKeysCoverRegistry is the label registry's copy of the drift
// guard every other field registry carries: the --fields help string is built
// from the hand-written key slice while parseFields validates against the
// registry map, so a column added to labelFields but forgotten in
// labelAllFieldKeys would be silently unselectable.
func TestLabelFieldKeysCoverRegistry(t *testing.T) {
	assert.Len(t, labelAllFieldKeys, len(labelFields), "the help names every registry key")
	assert.Subset(t, labelAllFieldKeys, labelDefaultFieldKeys,
		"the default column set is drawn from the help set")
}
