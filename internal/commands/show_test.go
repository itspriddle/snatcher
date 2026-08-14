package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// Two 40-character SHA1 info-hashes used by the show fixtures so the hash
// lookup path can be exercised (ParseSelector only treats a 40-char hex token
// as a hash).
const (
	infoHash1 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	infoHash2 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// bespokeShowResponse is a torrent-get payload covering two torrents with
// distinct ids and full 40-char hashes, used to exercise the show command's
// id and hash resolution. Torrent 1 also carries every detail-only scalar so
// the show registry's non-list columns have something to render.
const bespokeShowResponse = `{
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
			"labels": ["linux"],
			"magnetLink": "magnet:?xt=urn:btih:` + infoHash1 + `",
			"comment": "Ubuntu release",
			"creator": "mktorrent 1.1",
			"dateCreated": 1700000000,
			"isPrivate": true,
			"pieceCount": 8192,
			"pieceSize": 262144,
			"downloadedEver": 1073741824,
			"uploadedEver": 536870912,
			"corruptEver": 1024,
			"haveValid": 1073741824,
			"secondsDownloading": 3661,
			"secondsSeeding": 7200,
			"recheckProgress": 0.25,
			"metadataPercentComplete": 1.0
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

// bespokeShowSanitizeResponse is a torrent-get payload whose name, labels,
// download dir, and error carry terminal control sequences, used to verify the
// sanitize-in-human-but-not-json boundary.
const bespokeShowSanitizeResponse = `{
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

// runShow executes the show command with args, returning stdout, stderr, and the
// Execute error. The command's output is a *bytes.Buffer, so isTTY reports false
// and the piped (tab-separated) plaintext contract is exercised.
func runShow(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewShowCmd()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// showConfigPath stands up a transmission stub serving the given torrent-get
// payload on the bespoke path and writes a config pointing at it. Unlike the
// plain transmissionStub, the handler filters the fixture's torrents by the
// request's "ids" argument (matching numeric id or hashString), so the server
// behaves like a real Transmission: show's detail read is keyed by id and trusts
// the server to return only the selected torrent. An empty/absent ids argument
// returns every torrent.
func showConfigPath(t *testing.T, body string) (string, func()) {
	t.Helper()

	keyring.MockInit()

	var fixture struct {
		Arguments struct {
			Torrents []json.RawMessage `json:"torrents"`
		} `json:"arguments"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &fixture))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		var req struct {
			Method    string `json:"method"`
			Arguments struct {
				IDs []json.RawMessage `json:"ids"`
			} `json:"arguments"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		if req.Method != "torrent-get" {
			_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "id": 1}`))
			return
		}

		matched := filterFixtureTorrents(fixture.Arguments.Torrents, req.Arguments.IDs)

		payload, _ := json.Marshal(map[string]any{
			"result":    "success",
			"arguments": map[string]any{"torrents": matched},
			"tag":       1,
		})
		_, _ = w.Write(payload)
	}))

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	return cfgPath, srv.Close
}

// filterFixtureTorrents returns the fixture torrents whose id or hashString
// matches one of the requested ids. An empty ids list returns every torrent,
// mirroring Transmission's "no ids = all" behavior. It runs inside the stub's
// HTTP handler, so it decodes best-effort (skipping any undecodable torrent)
// rather than asserting — the fixtures are controlled test data.
func filterFixtureTorrents(torrents, ids []json.RawMessage) []json.RawMessage {
	if len(ids) == 0 {
		return torrents
	}

	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[strings.Trim(string(id), `"`)] = struct{}{}
	}

	matched := make([]json.RawMessage, 0, len(torrents))
	for _, raw := range torrents {
		var ident struct {
			ID   int64  `json:"id"`
			Hash string `json:"hashString"`
		}
		if err := json.Unmarshal(raw, &ident); err != nil {
			continue
		}

		if _, ok := want[fmt.Sprintf("%d", ident.ID)]; ok {
			matched = append(matched, raw)
			continue
		}
		if _, ok := want[ident.Hash]; ok {
			matched = append(matched, raw)
		}
	}

	return matched
}

func TestShowResolvesByID(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeShowResponse)
	defer closeSrv()

	out, _, err := runShow(t, "--config", cfgPath, "1")
	require.NoError(t, err)

	// Piped plaintext is a header-less "key<TAB>value" block, one line per field.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, len(detailFieldKeys), "one line per default field")

	fields := showFields(t, out)
	assert.Equal(t, "1", fields["ID"])
	assert.Equal(t, infoHash1, fields["Hash"])
	assert.Equal(t, "ubuntu-24.04.iso", fields["Name"])
	assert.Equal(t, "Downloading", fields["Status"])
	assert.Equal(t, "50%", fields["Done"])
}

func TestShowResolvesByHash(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeShowResponse)
	defer closeSrv()

	// An uppercase hash resolves case-insensitively to the matching torrent.
	out, _, err := runShow(t, "--config", cfgPath, strings.ToUpper(infoHash2))
	require.NoError(t, err)

	fields := showFields(t, out)
	assert.Equal(t, "2", fields["ID"])
	assert.Equal(t, infoHash2, fields["Hash"])
	assert.Equal(t, "debian.iso", fields["Name"])
	assert.Equal(t, "Seeding", fields["Status"])
}

func TestShowJSONSingleObject(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeShowResponse)
	defer closeSrv()

	out, _, err := runShow(t, "--config", cfgPath, "2", "--json")
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

func TestShowJQ(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeShowResponse)
	defer closeSrv()

	out, _, err := runShow(t, "--config", cfgPath, "1", "--jq", ".name")
	require.NoError(t, err)

	assert.Equal(t, `"ubuntu-24.04.iso"`, strings.TrimSpace(out))
}

// TestShowExposesMagnetLink confirms show now fetches the detail view, so the
// detail-only magnet_link is present in --json/--jq output.
func TestShowExposesMagnetLink(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeShowResponse)
	defer closeSrv()

	out, _, err := runShow(t, "--config", cfgPath, "1", "--jq", ".magnet_link")
	require.NoError(t, err)

	assert.Equal(t, `"magnet:?xt=urn:btih:`+infoHash1+`"`, strings.TrimSpace(out))
}

// TestShowSingleRoundTrip asserts show resolves an id with exactly one
// torrent-get request — the id goes straight to the detail read, server-side
// filtered, not fetched in full and matched in memory. The CSRF handshake (the
// 409 probe) is excluded from the count; only torrent-get method calls are
// tallied. (A hash needs one extra lookup, since TorrentDetail is keyed by id.)
func TestShowSingleRoundTrip(t *testing.T) {
	keyring.MockInit()

	var torrentGets int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		var envelope struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&envelope)
		if envelope.Method == "torrent-get" {
			torrentGets++
			_, _ = w.Write([]byte(bespokeShowResponse))
			return
		}

		_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "id": 1}`))
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	out, _, err := runShow(t, "--config", cfgPath, "1")
	require.NoError(t, err)

	fields := showFields(t, out)
	assert.Equal(t, "1", fields["ID"])
	assert.Equal(t, 1, torrentGets, "show issues exactly one torrent-get round-trip for an id")
}

func TestShowNotFoundErrors(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeShowResponse)
	defer closeSrv()

	// An absent hash (well-formed but matching nothing) errors with a non-zero
	// exit and writes nothing to stdout.
	absent := "cccccccccccccccccccccccccccccccccccccccc"
	out, _, err := runShow(t, "--config", cfgPath, absent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no torrent matched: "+absent)
	assert.Empty(t, out, "no output is written when nothing matched")
}

func TestShowWrongArgCount(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeShowResponse)
	defer closeSrv()

	t.Run("zero args", func(t *testing.T) {
		_, _, err := runShow(t, "--config", cfgPath)
		require.Error(t, err, "zero positional args is a usage error")
	})

	t.Run("two args", func(t *testing.T) {
		_, _, err := runShow(t, "--config", cfgPath, "1", "2")
		require.Error(t, err, "more than one positional arg is a usage error")
	})
}

func TestShowInvalidSelector(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeShowResponse)
	defer closeSrv()

	// A token that is neither all-digits nor a 40-char hex hash is rejected by
	// the selector before any backend call.
	_, _, err := runShow(t, "--config", cfgPath, "not-an-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid torrent selector")
}

func TestShowSanitizesHumanOutputNotJSON(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeShowSanitizeResponse)
	defer closeSrv()

	// Plaintext: untrusted server fields are stripped of control sequences.
	human, _, err := runShow(t, "--config", cfgPath, "1")
	require.NoError(t, err)
	assert.NotContains(t, human, "\x1b", "raw ESC must not reach plaintext output")
	assert.NotContains(t, human, "\x07", "raw BEL must not reach plaintext output")
	assert.Contains(t, human, "[2Jmalicious", "visible name text survives sanitization")
	assert.Contains(t, human, "/dir", "download dir is sanitized")
	assert.Contains(t, human, "boom", "error is sanitized")
	assert.Contains(t, human, "linux", "labels are sanitized")

	// JSON: the structured contract is never sanitized, so the raw control
	// bytes survive in the encoded output.
	raw, _, err := runShow(t, "--config", cfgPath, "1", "--json")
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

func TestShowFieldsSubsetAndOrder(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeShowResponse)
	defer closeSrv()

	// --fields selects a subset and renders the lines in the given order,
	// case-insensitively after trimming whitespace.
	out, _, err := runShow(t, "--config", cfgPath, "1", "--fields", "Name, id ,status")
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

func TestShowFieldsInvalidKey(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeShowResponse)
	defer closeSrv()

	_, _, err := runShow(t, "--config", cfgPath, "1", "--fields", "name,bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid field "bogus"`)
	assert.Contains(t, err.Error(), "valid fields are")
}

func TestShowFieldsEmptyAfterParse(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeShowResponse)
	defer closeSrv()

	// A non-empty spec that resolves to no keys (only separators/whitespace) is
	// an error, not a fall-through to the default set.
	_, _, err := runShow(t, "--config", cfgPath, "1", "--fields", " , ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no fields selected")
}

func TestShowFieldsIgnoredByJSON(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeShowResponse)
	defer closeSrv()

	// --json always emits the full object regardless of --fields.
	out, _, err := runShow(t, "--config", cfgPath, "1", "--fields", "name", "--json")
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

func TestShowNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runShow(t, "--config", cfgPath, "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// TestShowRendersDetailScalars asserts the detail-only fields — the ones a list
// Torrent does not carry — are rendered in the plaintext block, so they are
// readable without piping through --json.
func TestShowRendersDetailScalars(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeShowResponse)
	defer closeSrv()

	out, _, err := runShow(t, "--config", cfgPath, "1")
	require.NoError(t, err)

	fields := showFields(t, out)
	assert.Equal(t, "Ubuntu release", fields["Comment"])
	assert.Equal(t, "mktorrent 1.1", fields["Creator"])
	assert.Equal(t, output.Date(1700000000), fields["Created"])
	assert.Equal(t, "yes", fields["Private"])
	assert.Equal(t, "8192", fields["Pieces"])
	assert.Equal(t, "256.0 KiB", fields["Piece Size"])
	assert.Equal(t, "1.0 GiB", fields["Downloaded Ever"])
	assert.Equal(t, "512.0 MiB", fields["Uploaded Ever"])
	assert.Equal(t, "1.0 KiB", fields["Corrupt Ever"])
	assert.Equal(t, "1.0 GiB", fields["Have Valid"])
	assert.Equal(t, "1h 1m 1s", fields["Downloading Time"])
	assert.Equal(t, "2h", fields["Seeding Time"])
	assert.Equal(t, "25%", fields["Recheck"])
	assert.Equal(t, "100%", fields["Metadata"])

	assert.NotContains(t, fields, "Magnet Link", "magnet_link is opt-in, never in the default block")
}

// TestShowDefaultFieldOrder asserts the rendered line order is exactly
// detailFieldKeys: list's 15 fields in list's order, then the detail scalars,
// with magnet_link absent.
func TestShowDefaultFieldOrder(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeShowResponse)
	defer closeSrv()

	out, _, err := runShow(t, "--config", cfgPath, "1")
	require.NoError(t, err)

	want := make([]string, len(detailFieldKeys))
	for i, key := range detailFieldKeys {
		want[i] = detailFields[key].header
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	got := make([]string, len(lines))
	for i, line := range lines {
		key, _, ok := strings.Cut(line, "\t")
		require.True(t, ok, "each plaintext line is key<TAB>value: %q", line)
		got[i] = key
	}

	assert.Equal(t, want, got)
	assert.Equal(t, allFieldKeys, detailFieldKeys[:len(allFieldKeys)], "list's fields come first, in list order")
	assert.NotContains(t, detailFieldKeys, "magnet_link", "magnet_link is excluded from the default set")
}

// TestShowMagnetLinkField asserts "--fields magnet_link" prints the magnet URI
// and nothing else on both plaintext paths. This is the CLI equivalent of the
// TUI's "y m" yank-magnet key, so the output has to stay a single clean line
// that a shell can capture verbatim.
func TestShowMagnetLinkField(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:" + infoHash1

	cfgPath, closeSrv := showConfigPath(t, bespokeShowResponse)
	defer closeSrv()

	// Piped path, end to end through the command.
	out, _, err := runShow(t, "--config", cfgPath, "1", "--fields", "magnet_link")
	require.NoError(t, err)
	assert.Equal(t, "Magnet Link\t"+magnet+"\n", out, "piped output is exactly the magnet line")

	// TTY path. isTTY needs a real terminal, so the TTY writer is exercised
	// directly, as the list tests do for writeListTable.
	cols, err := parseFields(detailFields, detailFieldKeys, "magnet_link")
	require.NoError(t, err)

	var buf bytes.Buffer
	writeShowBlock(&buf, transmission.TorrentDetail{MagnetLink: magnet}, cols)
	assert.Equal(t, "Magnet Link:  "+magnet+"\n", buf.String(), "TTY output is exactly the magnet line")
}

// TestDetailFieldsAreShowOnly asserts the registry boundary in both directions:
// show accepts every detail key, and list rejects them with an error naming only
// list's own keys.
func TestDetailFieldsAreShowOnly(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokeShowResponse)
	defer closeSrv()

	t.Run("show accepts every detail key", func(t *testing.T) {
		out, _, err := runShow(t, "--config", cfgPath, "1", "--fields", strings.Join(allDetailFieldKeys, ","))
		require.NoError(t, err)

		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		assert.Len(t, lines, len(allDetailFieldKeys), "one line per selectable field, magnet_link included")
	})

	t.Run("list rejects the detail-only keys", func(t *testing.T) {
		for _, key := range []string{"magnet_link", "comment", "piece_count", "seconds_seeding"} {
			_, err := parseFields(listFields, defaultFieldKeys, key)
			require.Error(t, err, key)
			assert.Equal(t, `invalid field "`+key+`": valid fields are `+validFieldKeys(listFields), err.Error(),
				"list's error lists only list's own keys")
		}
	})
}

// showFields parses the piped "key<TAB>value" plaintext block into a map keyed
// by the field's header label.
func showFields(t *testing.T, out string) map[string]string {
	t.Helper()

	fields := make(map[string]string)
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		key, value, ok := strings.Cut(line, "\t")
		require.True(t, ok, "each plaintext line is key<TAB>value: %q", line)
		fields[key] = value
	}

	return fields
}
