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

	// postWriteBody, when set, is served for torrent-get once a torrent-set has
	// been seen, so the write verbs' re-read reports the server's post-write view
	// rather than the pre-write fixture.
	postWriteBody string

	// failSet makes torrent-set answer 500, to exercise the write-failure path.
	failSet bool

	mu       sync.Mutex
	written  bool
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

		isSet := envelope.Method == "torrent-set" || envelope.Method == "torrent_set"

		s.mu.Lock()
		s.requests = append(s.requests, labelRequest{method: envelope.Method, body: string(body)})
		s.written = s.written || isSet
		listBody := s.listBody
		if s.written && s.postWriteBody != "" {
			listBody = s.postWriteBody
		}
		s.mu.Unlock()

		if isSet && s.failSet {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if envelope.Method == "torrent-get" || envelope.Method == "torrent_get" {
			_, _ = w.Write([]byte(listBody))
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

	return labelStubConfigPath(t, &labelStub{rpcVersion: rpcVersion, listBody: listBody})
}

// labelStubConfigPath serves a caller-built stub, for the write tests that need
// a post-write payload or a failing torrent-set.
func labelStubConfigPath(t *testing.T, stub *labelStub) (string, *labelStub, func()) {
	t.Helper()

	keyring.MockInit()

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

// bespokeLabelArchivedResponse is the post-write torrent-get payload for a
// server that has just had torrent 1's labels replaced with "archive".
const bespokeLabelArchivedResponse = `{
	"arguments": {"torrents": [
		{"id": 1, "hashString": "` + infoHash1 + `", "name": "ubuntu-24.04.iso", "labels": ["archive"]},
		{"id": 2, "hashString": "` + infoHash2 + `", "name": "debian.iso"},
		{"id": 3, "hashString": "` + infoHash3 + `", "name": "fedora.iso", "labels": ["linux"]}
	]},
	"result": "success",
	"tag": 1
}`

// jsonrpcLabelArchivedResponse is the same post-write payload under the JSON-RPC
// 2.0 scheme.
const jsonrpcLabelArchivedResponse = `{
	"jsonrpc": "2.0",
	"result": {"torrents": [
		{"id": 1, "hash_string": "` + infoHash1 + `", "name": "ubuntu-24.04.iso", "labels": ["archive"]},
		{"id": 2, "hash_string": "` + infoHash2 + `", "name": "debian.iso"},
		{"id": 3, "hash_string": "` + infoHash3 + `", "name": "fedora.iso", "labels": ["linux"]}
	]},
	"id": 1
}`

// bespokeLabelClearedResponse is the post-write payload for a server that has
// just had torrent 1's labels cleared.
const bespokeLabelClearedResponse = `{
	"arguments": {"torrents": [
		{"id": 1, "hashString": "` + infoHash1 + `", "name": "ubuntu-24.04.iso", "labels": []},
		{"id": 2, "hashString": "` + infoHash2 + `", "name": "debian.iso"},
		{"id": 3, "hashString": "` + infoHash3 + `", "name": "fedora.iso", "labels": ["linux"]}
	]},
	"result": "success",
	"tag": 1
}`

// labelSetArgs is the part of a torrent-set request the label tests assert on.
type labelSetArgs struct {
	IDs    []int64  `json:"ids"`
	Labels []string `json:"labels"`
}

// labelSetBody decodes a captured torrent-set request into the ids it targets
// and the labels it writes, reading from "arguments" (bespoke) or "params"
// (JSON-RPC 2.0) so one assertion serves both wire schemes.
func labelSetBody(t *testing.T, body string) labelSetArgs {
	t.Helper()

	var req struct {
		Arguments labelSetArgs `json:"arguments"`
		Params    labelSetArgs `json:"params"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &req))

	if len(req.Arguments.IDs) == 0 && req.Arguments.Labels == nil {
		return req.Params
	}

	return req.Arguments
}

// labelSetBodies returns the captured torrent-set requests under either wire
// scheme, in request order.
func labelSetBodies(stub *labelStub) []string {
	return stub.bodies("torrent-set", "torrent_set")
}

// TestLabelSetReplacesLabels covers the happy path on both wire schemes: exactly
// one torrent-set naming the selected torrent and carrying the requested labels,
// and rendered rows read back from the server rather than echoed locally.
func TestLabelSetReplacesLabels(t *testing.T) {
	cases := []struct {
		name      string
		rpcVer    string
		listBody  string
		afterBody string
	}{
		{"bespoke", "", bespokeLabelResponse, bespokeLabelArchivedResponse},
		{"jsonrpc", "6.0.0", jsonrpcLabelResponse, jsonrpcLabelArchivedResponse},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := labelStubConfigPath(t, &labelStub{
				rpcVersion:    tc.rpcVer,
				listBody:      tc.listBody,
				postWriteBody: tc.afterBody,
			})
			defer closeSrv()

			out, _, err := runLabel(t, "set", "--config", cfgPath, "1", "--label", "archive")
			require.NoError(t, err)

			bodies := labelSetBodies(stub)
			require.Len(t, bodies, 1, "one write for one resulting label set")

			args := labelSetBody(t, bodies[0])
			assert.Equal(t, []int64{1}, args.IDs)
			assert.Equal(t, []string{"archive"}, args.Labels)

			rows := labelRows(t, out)
			require.Len(t, rows, 1)
			assert.Equal(t, []string{"1", "ubuntu-24.04.iso", "archive"}, rows[0],
				"the rendered labels come from the post-write re-read")

			assert.Len(t, stub.bodies("torrent-get", "torrent_get"), 2,
				"one read to compute the change, one to report it")
		})
	}
}

// TestLabelSetNormalizesRequestedLabels pins the normalization contract on what
// is actually sent: labels given out of order and duplicated arrive sorted and
// unique in the request body.
func TestLabelSetNormalizesRequestedLabels(t *testing.T) {
	cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	_, _, err := runLabel(t, "set", "--config", cfgPath, "1",
		"--label", "zebra", "--label", "apple", "--label", "zebra", "--label", " padded ")
	require.NoError(t, err)

	bodies := labelSetBodies(stub)
	require.Len(t, bodies, 1)

	args := labelSetBody(t, bodies[0])
	assert.Equal(t, []string{"apple", "padded", "zebra"}, args.Labels,
		"the written set is trimmed, de-duplicated, and byte-sorted")
}

// TestLabelSetIsCaseSensitive pins Decision 3: Foo and foo are two labels, so
// neither de-duplication nor any later matching may fold case.
func TestLabelSetIsCaseSensitive(t *testing.T) {
	cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	_, _, err := runLabel(t, "set", "--config", cfgPath, "1", "--label", "Foo", "--label", "foo")
	require.NoError(t, err)

	bodies := labelSetBodies(stub)
	require.Len(t, bodies, 1)
	assert.Equal(t, []string{"Foo", "foo"}, labelSetBody(t, bodies[0]).Labels)
}

// TestLabelSetWithNoLabelsClears covers the documented footgun: a bare
// "label set <sel>" writes an empty array rather than erroring.
func TestLabelSetWithNoLabelsClears(t *testing.T) {
	cfgPath, stub, closeSrv := labelStubConfigPath(t, &labelStub{
		listBody:      bespokeLabelResponse,
		postWriteBody: bespokeLabelClearedResponse,
	})
	defer closeSrv()

	out, _, err := runLabel(t, "set", "--config", cfgPath, "1")
	require.NoError(t, err)

	bodies := labelSetBodies(stub)
	require.Len(t, bodies, 1)

	args := labelSetBody(t, bodies[0])
	assert.Equal(t, []int64{1}, args.IDs)
	assert.NotNil(t, args.Labels, "an empty array is sent, not a null or a missing key")
	assert.Empty(t, args.Labels)

	rows := labelRows(t, out)
	require.Len(t, rows, 1)
	assert.Equal(t, []string{"1", "ubuntu-24.04.iso", ""}, rows[0])
}

// TestLabelSetAllGroupsIntoOneWrite covers --all: three torrents holding three
// different label sets all land on the same resulting set, which is one bucket
// and therefore one write. It also pins Decision 4 — no confirmation prompt.
func TestLabelSetAllGroupsIntoOneWrite(t *testing.T) {
	cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	_, _, err := runLabel(t, "set", "--config", cfgPath, "--all", "--label", "archive")
	require.NoError(t, err)

	bodies := labelSetBodies(stub)
	require.Len(t, bodies, 1, "one resulting label set is one write, however many torrents")

	args := labelSetBody(t, bodies[0])
	assert.Equal(t, []int64{1, 2, 3}, args.IDs)
	assert.Equal(t, []string{"archive"}, args.Labels)
}

// TestLabelSetNoOpIssuesNoWrite proves the skip rule by request count: a torrent
// already in the requested state is excluded from the write entirely, and when
// every matched torrent is, nothing is sent at all.
func TestLabelSetNoOpIssuesNoWrite(t *testing.T) {
	cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	// Given in the opposite order to the fixture, to prove the comparison is
	// against the normalized sets rather than the raw slices.
	out, _, err := runLabel(t, "set", "--config", cfgPath, "1", "--label", "movies", "--label", "hd")
	require.NoError(t, err)

	assert.Empty(t, labelSetBodies(stub), "an unchanged torrent is not written")

	rows := labelRows(t, out)
	require.Len(t, rows, 1, "the rows still render")
	assert.Equal(t, []string{"1", "ubuntu-24.04.iso", "hd,movies"}, rows[0])
}

// TestLabelSetRejectsInvalidLabels covers the three validation rules, each
// asserting that nothing at all reached the wire — validation runs before dial.
func TestLabelSetRejectsInvalidLabels(t *testing.T) {
	cases := []struct {
		name  string
		label string
		want  string
	}{
		{"empty", "", `invalid label "": must not be empty`},
		{"whitespace", "   ", `invalid label "   ": must not be empty`},
		{"comma", "hd,movies", `invalid label "hd,movies": must not contain a comma`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
			defer closeSrv()

			_, _, err := runLabel(t, "set", "--config", cfgPath, "1", "--label", tc.label)
			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error(), "the error names the offending argument")

			assert.Empty(t, stub.bodies("torrent-get", "torrent_get", "torrent-set", "torrent_set"),
				"a bad label is rejected before any round-trip")
		})
	}
}

// TestLabelSetLabelFlagDoesNotSplitOnCommas pins StringArrayVar over
// StringSliceVar: one occurrence is one label, so a comma is a rejected
// character rather than a separator.
func TestLabelSetLabelFlagDoesNotSplitOnCommas(t *testing.T) {
	cmd := NewLabelCmd()

	set, _, err := cmd.Find([]string{"set"})
	require.NoError(t, err)

	flag := set.Flags().Lookup("label")
	require.NotNil(t, flag)
	assert.Equal(t, "stringArray", flag.Value.Type())
}

// TestLabelSetRegistersFlags pins the flag surface, including that the
// persistent root flags are not re-declared locally.
func TestLabelSetRegistersFlags(t *testing.T) {
	cmd := NewLabelCmd()

	set, _, err := cmd.Find([]string{"set"})
	require.NoError(t, err)

	for _, name := range []string{"server", "json", "jq", "all", "label", "fields"} {
		assert.NotNil(t, set.Flags().Lookup(name), "--%s is registered", name)
	}

	for _, name := range []string{"config", "theme", "no-nerd-font"} {
		assert.Nil(t, set.Flags().Lookup(name), "--%s is inherited from the root, not re-declared", name)
	}
}

func TestLabelSetJSON(t *testing.T) {
	cfgPath, _, closeSrv := labelStubConfigPath(t, &labelStub{
		listBody:      bespokeLabelResponse,
		postWriteBody: bespokeLabelArchivedResponse,
	})
	defer closeSrv()

	out, _, err := runLabel(t, "set", "--config", cfgPath, "1", "--label", "archive", "--json")
	require.NoError(t, err)

	var got mutationResult
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	assert.Equal(t, "label-set", got.Action)
	assert.Equal(t, 1, got.Count)
	require.Len(t, got.Torrents, 1)
	assert.Equal(t, int64(1), got.Torrents[0].ID)
	assert.Equal(t, infoHash1, got.Torrents[0].Hash)
	require.NotNil(t, got.Torrents[0].Labels)
	assert.Equal(t, []string{"archive"}, *got.Torrents[0].Labels)
}

// TestLabelSetJSONClearedIsEmptyArray pins the pointer's whole purpose: a
// cleared torrent reports "labels": [], not null and not an absent key.
func TestLabelSetJSONClearedIsEmptyArray(t *testing.T) {
	cfgPath, _, closeSrv := labelStubConfigPath(t, &labelStub{
		listBody:      bespokeLabelResponse,
		postWriteBody: bespokeLabelClearedResponse,
	})
	defer closeSrv()

	out, _, err := runLabel(t, "set", "--config", cfgPath, "1", "--json")
	require.NoError(t, err)

	assert.Contains(t, out, `"labels": []`)
	assert.NotContains(t, out, `"labels": null`)
}

// TestLabelSetJQWinsOverJSON pins the fixed output precedence on the write path
// too: --jq → --json → plaintext.
func TestLabelSetJQWinsOverJSON(t *testing.T) {
	cfgPath, _, closeSrv := labelStubConfigPath(t, &labelStub{
		listBody:      bespokeLabelResponse,
		postWriteBody: bespokeLabelArchivedResponse,
	})
	defer closeSrv()

	out, _, err := runLabel(t, "set", "--config", cfgPath, "1", "--label", "archive",
		"--json", "--jq", ".torrents[].labels[]")
	require.NoError(t, err)

	assert.Equal(t, `"archive"`, strings.TrimSpace(out))
}

func TestLabelSetRequiresSelector(t *testing.T) {
	cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	_, _, err := runLabel(t, "set", "--config", cfgPath, "--label", "archive")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "specify torrent ids or hashes, or use --all")

	assert.Empty(t, stub.bodies("torrent-get", "torrent_get"),
		"a bare invocation is an error, not a no-op")
}

// TestLabelSetNothingMatchedWritesNothing proves selector resolution happens
// before any write, so a typo'd id never half-applies a batch.
func TestLabelSetNothingMatchedWritesNothing(t *testing.T) {
	cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	out, _, err := runLabel(t, "set", "--config", cfgPath, "99", "--label", "archive")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no torrents matched: 99")
	assert.Empty(t, out)

	assert.Empty(t, labelSetBodies(stub), "nothing is written when no selector matched")
}

func TestLabelSetWriteFailureWrapsServerURL(t *testing.T) {
	cfgPath, stub, closeSrv := labelStubConfigPath(t, &labelStub{
		listBody: bespokeLabelResponse,
		failSet:  true,
	})
	defer closeSrv()

	out, _, err := runLabel(t, "set", "--config", cfgPath, "1", "--label", "archive")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to ")
	assert.Empty(t, out, "nothing is rendered on a write failure")

	require.Len(t, labelSetBodies(stub), 1, "the failure is the write itself")
}

// TestMutationTorrentLabelsOmittedByOtherCommands guards the JSON contract of
// every non-label mutation command: the labels key the label verbs added must
// stay absent from theirs.
func TestMutationTorrentLabelsOmittedByOtherCommands(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runStop(t, "--config", cfgPath, "1", "--json")
	require.NoError(t, err)

	assert.NotContains(t, out, "labels", "torrent stop's JSON output is unchanged")

	var got mutationResult
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got.Torrents, 1)
	assert.Nil(t, got.Torrents[0].Labels)
}

// bespokeLabelAddedResponse is the post-write torrent-get payload for a server
// that has just had "archive" added to torrent 1's existing labels.
const bespokeLabelAddedResponse = `{
	"arguments": {"torrents": [
		{"id": 1, "hashString": "` + infoHash1 + `", "name": "ubuntu-24.04.iso", "labels": ["archive", "hd", "movies"]},
		{"id": 2, "hashString": "` + infoHash2 + `", "name": "debian.iso"},
		{"id": 3, "hashString": "` + infoHash3 + `", "name": "fedora.iso", "labels": ["linux"]}
	]},
	"result": "success",
	"tag": 1
}`

// jsonrpcLabelAddedResponse is the same post-write payload under the JSON-RPC
// 2.0 scheme.
const jsonrpcLabelAddedResponse = `{
	"jsonrpc": "2.0",
	"result": {"torrents": [
		{"id": 1, "hash_string": "` + infoHash1 + `", "name": "ubuntu-24.04.iso", "labels": ["archive", "hd", "movies"]},
		{"id": 2, "hash_string": "` + infoHash2 + `", "name": "debian.iso"},
		{"id": 3, "hash_string": "` + infoHash3 + `", "name": "fedora.iso", "labels": ["linux"]}
	]},
	"id": 1
}`

// bespokeLabelSharedResponse holds two torrents carrying identical labels, so a
// single added label lands both on the same resulting set — the one-bucket,
// one-write case.
const bespokeLabelSharedResponse = `{
	"arguments": {"torrents": [
		{"id": 1, "hashString": "` + infoHash1 + `", "name": "ubuntu-24.04.iso", "labels": ["hd"]},
		{"id": 2, "hashString": "` + infoHash2 + `", "name": "debian.iso", "labels": ["hd"]}
	]},
	"result": "success",
	"tag": 1
}`

// TestLabelAddPreservesExistingLabels is the story's headline behavior, on both
// wire schemes: the added label joins the torrent's existing ones in the written
// set rather than replacing them, and the rendered rows come from the re-read.
func TestLabelAddPreservesExistingLabels(t *testing.T) {
	cases := []struct {
		name      string
		rpcVer    string
		listBody  string
		afterBody string
	}{
		{"bespoke", "", bespokeLabelResponse, bespokeLabelAddedResponse},
		{"jsonrpc", "6.0.0", jsonrpcLabelResponse, jsonrpcLabelAddedResponse},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := labelStubConfigPath(t, &labelStub{
				rpcVersion:    tc.rpcVer,
				listBody:      tc.listBody,
				postWriteBody: tc.afterBody,
			})
			defer closeSrv()

			out, _, err := runLabel(t, "add", "--config", cfgPath, "1", "--label", "archive")
			require.NoError(t, err)

			bodies := labelSetBodies(stub)
			require.Len(t, bodies, 1, "one resulting label set is one write")

			args := labelSetBody(t, bodies[0])
			assert.Equal(t, []int64{1}, args.IDs)
			assert.Equal(t, []string{"archive", "hd", "movies"}, args.Labels,
				"the torrent's existing labels survive the write")

			rows := labelRows(t, out)
			require.Len(t, rows, 1)
			assert.Equal(t, []string{"1", "ubuntu-24.04.iso", "archive,hd,movies"}, rows[0])

			assert.Len(t, stub.bodies("torrent-get", "torrent_get"), 2,
				"one read to compute the merge, one to report it")
		})
	}
}

// TestLabelAddToTorrentWithNoLabels covers the nil-labels path: a torrent whose
// payload omits the key entirely still merges into a plain one-element set.
func TestLabelAddToTorrentWithNoLabels(t *testing.T) {
	cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	_, _, err := runLabel(t, "add", "--config", cfgPath, "2", "--label", "archive")
	require.NoError(t, err)

	bodies := labelSetBodies(stub)
	require.Len(t, bodies, 1)

	args := labelSetBody(t, bodies[0])
	assert.Equal(t, []int64{2}, args.IDs)
	assert.Equal(t, []string{"archive"}, args.Labels)
}

// TestLabelAddDuplicateIssuesNoWrite proves idempotence by request count: adding
// a label the torrent already has sends nothing at all, and the rows still
// render.
func TestLabelAddDuplicateIssuesNoWrite(t *testing.T) {
	cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	out, _, err := runLabel(t, "add", "--config", cfgPath, "1", "--label", "hd")
	require.NoError(t, err)

	assert.Empty(t, labelSetBodies(stub), "a redundant add is not written")

	rows := labelRows(t, out)
	require.Len(t, rows, 1, "the rows still render")
	assert.Equal(t, []string{"1", "ubuntu-24.04.iso", "hd,movies"}, rows[0])
}

// TestLabelAddMixedSelectionWritesOnlyChanged covers a selection where one
// torrent already has the label and one does not: only the one that changes is
// named in the write.
func TestLabelAddMixedSelectionWritesOnlyChanged(t *testing.T) {
	cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	_, _, err := runLabel(t, "add", "--config", cfgPath, "1", "2", "--label", "hd")
	require.NoError(t, err)

	bodies := labelSetBodies(stub)
	require.Len(t, bodies, 1, "only the changing torrent is written")

	args := labelSetBody(t, bodies[0])
	assert.Equal(t, []int64{2}, args.IDs)
	assert.Equal(t, []string{"hd"}, args.Labels)
}

// TestLabelAddGroupsByResultingSet pins the grouped write: three torrents
// holding three different label sets land on three distinct resulting sets, so
// three writes are issued, one per set, in key-sorted (deterministic) order.
func TestLabelAddGroupsByResultingSet(t *testing.T) {
	cases := []struct {
		name     string
		rpcVer   string
		listBody string
	}{
		{"bespoke", "", bespokeLabelResponse},
		{"jsonrpc", "6.0.0", jsonrpcLabelResponse},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := labelConfigPath(t, tc.rpcVer, tc.listBody)
			defer closeSrv()

			_, _, err := runLabel(t, "add", "--config", cfgPath, "--all", "--label", "new")
			require.NoError(t, err)

			bodies := labelSetBodies(stub)
			require.Len(t, bodies, 3, "one write per distinct resulting label set")

			// Buckets are written in NUL-joined key order: "hd\x00movies\x00new"
			// then "linux\x00new" then "new".
			want := []labelSetArgs{
				{IDs: []int64{1}, Labels: []string{"hd", "movies", "new"}},
				{IDs: []int64{3}, Labels: []string{"linux", "new"}},
				{IDs: []int64{2}, Labels: []string{"new"}},
			}

			for i, expected := range want {
				args := labelSetBody(t, bodies[i])
				assert.Equal(t, expected.IDs, args.IDs, "request %d targets the right torrent", i)
				assert.Equal(t, expected.Labels, args.Labels, "request %d writes the right set", i)
			}
		})
	}
}

// TestLabelAddOneResultingSetIsOneWrite covers the common case: torrents that
// all land on the same resulting set are a single bucket, so however many are
// selected, one request is sent.
func TestLabelAddOneResultingSetIsOneWrite(t *testing.T) {
	cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelSharedResponse)
	defer closeSrv()

	_, _, err := runLabel(t, "add", "--config", cfgPath, "--all", "--label", "movies")
	require.NoError(t, err)

	bodies := labelSetBodies(stub)
	require.Len(t, bodies, 1, "one resulting label set is one write, however many torrents")

	args := labelSetBody(t, bodies[0])
	assert.Equal(t, []int64{1, 2}, args.IDs)
	assert.Equal(t, []string{"hd", "movies"}, args.Labels)
}

// TestLabelAddIsCaseSensitive pins Decision 3 on the merge: "HD" does not fold
// into an existing "hd", so the torrent ends up holding both.
func TestLabelAddIsCaseSensitive(t *testing.T) {
	cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	_, _, err := runLabel(t, "add", "--config", cfgPath, "1", "--label", "HD")
	require.NoError(t, err)

	bodies := labelSetBodies(stub)
	require.Len(t, bodies, 1)
	assert.Equal(t, []string{"HD", "hd", "movies"}, labelSetBody(t, bodies[0]).Labels)
}

// TestLabelAddRequiresLabels covers the one place add and set diverge: there is
// no coherent "add nothing", so a missing --label is an error rather than a
// clear, and it is returned before anything is dialed.
func TestLabelAddRequiresLabels(t *testing.T) {
	cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	_, _, err := runLabel(t, "add", "--config", cfgPath, "1")
	require.Error(t, err)
	assert.Equal(t, "nothing to add: provide --label", err.Error())

	assert.Empty(t, stub.bodies("torrent-get", "torrent_get", "torrent-set", "torrent_set"),
		"a missing --label is rejected before any round-trip")
}

// TestLabelAddRejectsInvalidLabels re-runs the shared validation rules on add,
// each asserting that nothing at all reached the wire.
func TestLabelAddRejectsInvalidLabels(t *testing.T) {
	cases := []struct {
		name  string
		label string
		want  string
	}{
		{"empty", "", `invalid label "": must not be empty`},
		{"whitespace", "  ", `invalid label "  ": must not be empty`},
		{"comma", "hd,movies", `invalid label "hd,movies": must not contain a comma`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
			defer closeSrv()

			_, _, err := runLabel(t, "add", "--config", cfgPath, "1", "--label", tc.label)
			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error(), "the error names the offending argument")

			assert.Empty(t, stub.bodies("torrent-get", "torrent_get", "torrent-set", "torrent_set"),
				"a bad label is rejected before any round-trip")
		})
	}
}

func TestLabelAddJSON(t *testing.T) {
	cfgPath, _, closeSrv := labelStubConfigPath(t, &labelStub{
		listBody:      bespokeLabelResponse,
		postWriteBody: bespokeLabelAddedResponse,
	})
	defer closeSrv()

	out, _, err := runLabel(t, "add", "--config", cfgPath, "1", "--label", "archive", "--json")
	require.NoError(t, err)

	var got mutationResult
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	assert.Equal(t, "label-add", got.Action)
	assert.Equal(t, 1, got.Count)
	require.Len(t, got.Torrents, 1)
	assert.Equal(t, int64(1), got.Torrents[0].ID)
	require.NotNil(t, got.Torrents[0].Labels)
	assert.Equal(t, []string{"archive", "hd", "movies"}, *got.Torrents[0].Labels,
		"the reported labels are the server's post-write view")
}

// TestLabelAddJQWinsOverJSON pins the fixed output precedence on add too.
func TestLabelAddJQWinsOverJSON(t *testing.T) {
	cfgPath, _, closeSrv := labelStubConfigPath(t, &labelStub{
		listBody:      bespokeLabelResponse,
		postWriteBody: bespokeLabelAddedResponse,
	})
	defer closeSrv()

	out, _, err := runLabel(t, "add", "--config", cfgPath, "1", "--label", "archive",
		"--json", "--jq", ".action")
	require.NoError(t, err)

	assert.Equal(t, `"label-add"`, strings.TrimSpace(out))
}

// TestLabelAddNothingMatchedWritesNothing proves selector resolution happens
// before any write, so a typo'd id never half-applies a batch.
func TestLabelAddNothingMatchedWritesNothing(t *testing.T) {
	cfgPath, stub, closeSrv := labelConfigPath(t, "", bespokeLabelResponse)
	defer closeSrv()

	out, _, err := runLabel(t, "add", "--config", cfgPath, "99", "--label", "archive")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no torrents matched: 99")
	assert.Empty(t, out)

	assert.Empty(t, labelSetBodies(stub), "nothing is written when no selector matched")
}

func TestLabelAddWriteFailureWrapsServerURL(t *testing.T) {
	cfgPath, stub, closeSrv := labelStubConfigPath(t, &labelStub{
		listBody: bespokeLabelResponse,
		failSet:  true,
	})
	defer closeSrv()

	out, _, err := runLabel(t, "add", "--config", cfgPath, "1", "--label", "archive")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to ")
	assert.Empty(t, out, "nothing is rendered on a write failure")

	require.Len(t, labelSetBodies(stub), 1, "the failure is the write itself")
}

// TestLabelAddRegistersFlags pins add's flag surface, which matches set's:
// --label is repeatable (a stringArray, not a comma-splitting stringSlice), and
// the persistent root flags are not re-declared locally.
func TestLabelAddRegistersFlags(t *testing.T) {
	cmd := NewLabelCmd()

	add, _, err := cmd.Find([]string{"add"})
	require.NoError(t, err)

	for _, name := range []string{"server", "json", "jq", "all", "label", "fields"} {
		assert.NotNil(t, add.Flags().Lookup(name), "--%s is registered", name)
	}

	for _, name := range []string{"config", "theme", "no-nerd-font"} {
		assert.Nil(t, add.Flags().Lookup(name), "--%s is inherited from the root, not re-declared", name)
	}

	flag := add.Flags().Lookup("label")
	require.NotNil(t, flag)
	assert.Equal(t, "stringArray", flag.Value.Type())
}
