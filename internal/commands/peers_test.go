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

// bespokePeersResponse is a torrent-get payload whose first torrent carries a
// camelCase peers array (the bespoke wire spelling: clientName, rateToClient,
// rateToPeer, flagStr, isEncrypted) and whose second torrent is connected to no
// peers at all — the empty-collection path.
const bespokePeersResponse = `{
	"arguments": {"torrents": [
		{
			"id": 1,
			"hashString": "` + infoHash1 + `",
			"name": "ubuntu-24.04.iso",
			"status": 4,
			"peers": [
				{
					"address": "203.0.113.7",
					"port": 51413,
					"clientName": "Transmission 4.0.6",
					"progress": 0.5,
					"rateToClient": 1048576,
					"rateToPeer": 2048,
					"flagStr": "UDEI",
					"isEncrypted": true
				},
				{
					"address": "2001:db8::1",
					"port": 6881,
					"clientName": "qBittorrent 5.0.0",
					"progress": 1,
					"rateToClient": 0,
					"rateToPeer": 0,
					"flagStr": "dEH",
					"isEncrypted": false
				}
			]
		},
		{
			"id": 2,
			"hashString": "` + infoHash2 + `",
			"name": "debian.iso",
			"status": 6,
			"peers": []
		}
	]},
	"result": "success",
	"tag": 1
}`

// bespokePeersSanitizeResponse is a torrent-get payload whose peer address,
// client string, and flag string all carry terminal control sequences, and whose
// client string also ends in U+202E (RLO) — the direction override that would
// visually swap the Down, Up, and Encrypted cells rendered to its right on the
// same aligned row. All three are reported by an arbitrary swarm member — any
// peer can claim any client string — so they are the most hostile text the CLI
// renders.
//
// Every column peerFields marks sanitize is represented here, so removing any one
// of those flags fails TestPeersSanitizesHumanOutputNotJSON rather than passing
// unnoticed against a clean fixture value.
const bespokePeersSanitizeResponse = `{
	"arguments": {"torrents": [
		{
			"id": 1,
			"hashString": "` + infoHash1 + `",
			"name": "ubuntu-24.04.iso",
			"status": 4,
			"peers": [
				{
					"address": "\u001b[2J203.0.113.7\u0007",
					"port": 51413,
					"clientName": "\u001b[2Jevil\u0007Client\u202e",
					"progress": 0.25,
					"rateToClient": 1024,
					"rateToPeer": 0,
					"flagStr": "\u001b[31mUD\u0007",
					"isEncrypted": false
				}
			]
		}
	]},
	"result": "success",
	"tag": 1
}`

// runPeers executes the peers command with args, returning stdout, stderr, and
// the Execute error. The command's output is a *bytes.Buffer, so isTTY reports
// false and the piped (tab-separated) plaintext contract is exercised.
func runPeers(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewPeersCmd()
	AddGlobalFlags(cmd)

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

func TestPeersPipedTabSeparated(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokePeersResponse)
	defer closeSrv()

	out, _, err := runPeers(t, "--config", cfgPath, "1")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2, "piped output is header-less, one row per peer")

	first := strings.Split(lines[0], "\t")
	require.Len(t, first, len(peerFieldKeys))
	assert.Equal(t, "203.0.113.7", first[0])
	assert.Equal(t, "51413", first[1])
	assert.Equal(t, "Transmission 4.0.6", first[2])
	assert.Equal(t, "50%", first[3])
	assert.Equal(t, "1.0 MB/s", first[4])
	assert.Equal(t, "2.0 KB/s", first[5])
	assert.Equal(t, "yes", first[6])
	assert.Equal(t, "UDEI", first[7])

	second := strings.Split(lines[1], "\t")
	assert.Equal(t, "2001:db8::1", second[0])
	assert.Equal(t, "6881", second[1])
	assert.Equal(t, "qBittorrent 5.0.0", second[2])
	assert.Equal(t, "100%", second[3])
	assert.Equal(t, "no", second[6])
	assert.Equal(t, "dEH", second[7])
}

func TestPeersResolvesByHash(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokePeersResponse)
	defer closeSrv()

	// An uppercase hash resolves case-insensitively, via the same resolveTorrent
	// path show and files use.
	out, _, err := runPeers(t, "--config", cfgPath, strings.ToUpper(infoHash1))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "203.0.113.7", strings.Split(lines[0], "\t")[0])
}

func TestPeersJSON(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokePeersResponse)
	defer closeSrv()

	out, _, err := runPeers(t, "--config", cfgPath, "1", "--json")
	require.NoError(t, err)

	var got []struct {
		Address      string  `json:"address"`
		Port         int     `json:"port"`
		Client       string  `json:"client"`
		Progress     float64 `json:"progress"`
		RateToClient int64   `json:"rate_to_client"`
		RateToPeer   int64   `json:"rate_to_peer"`
		FlagStr      string  `json:"flag_str"`
		IsEncrypted  bool    `json:"is_encrypted"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 2)

	assert.Equal(t, "203.0.113.7", got[0].Address)
	assert.Equal(t, 51413, got[0].Port)
	assert.Equal(t, "Transmission 4.0.6", got[0].Client)
	assert.InDelta(t, 0.5, got[0].Progress, 1e-9)
	assert.Equal(t, int64(1048576), got[0].RateToClient)
	assert.Equal(t, int64(2048), got[0].RateToPeer)
	assert.Equal(t, "UDEI", got[0].FlagStr)
	assert.True(t, got[0].IsEncrypted)

	assert.Equal(t, "2001:db8::1", got[1].Address)
	assert.False(t, got[1].IsEncrypted)
}

func TestPeersJSONEmptyIsArrayNotNull(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokePeersResponse)
	defer closeSrv()

	out, _, err := runPeers(t, "--config", cfgPath, "2", "--json")
	require.NoError(t, err)
	assert.Equal(t, "[]", strings.TrimSpace(out))
}

func TestPeersJQ(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokePeersResponse)
	defer closeSrv()

	out, _, err := runPeers(t, "--config", cfgPath, "1", "--jq", ".[].address")
	require.NoError(t, err)

	assert.Equal(t, `"203.0.113.7"`+"\n"+`"2001:db8::1"`, strings.TrimSpace(out))
}

func TestPeersPipedEmptyWritesNothing(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokePeersResponse)
	defer closeSrv()

	out, _, err := runPeers(t, "--config", cfgPath, "2")
	require.NoError(t, err)
	assert.Empty(t, out, "piped output for a torrent with no peers is empty")
}

// TestPeersTableEmpty covers the TTY branch of the empty case, which isTTY makes
// unreachable through the command with a *bytes.Buffer: the table writer prints
// the peers command's own "No peers." line.
func TestPeersTableEmpty(t *testing.T) {
	cols, err := parseFields(peerFields, peerFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	writeListTable(&buf, []transmission.Peer(nil), cols, 80, "No peers.")

	assert.Equal(t, "No peers.", strings.TrimSpace(buf.String()))
}

// TestPeersTableAligned exercises the TTY table branch directly, since isTTY
// needs a real terminal: the header row is present and the rows follow it.
func TestPeersTableAligned(t *testing.T) {
	peers := []transmission.Peer{
		{Address: "203.0.113.7", Port: 51413, Client: "Transmission 4.0.6", Progress: 0.5, RateToClient: 1024, FlagStr: "UD", IsEncrypted: true},
		{Address: "2001:db8::1", Port: 6881, Client: "qBittorrent 5.0.0", Progress: 1, FlagStr: "dE"},
	}

	cols, err := parseFields(peerFields, peerFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	// width 0 disables truncation so the full client strings show.
	writeListTable(&buf, peers, cols, 0, "No peers.")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3, "header row plus one row per peer")

	assert.True(t, strings.HasPrefix(lines[0], "Address"))
	assert.Contains(t, lines[0], "Encrypted")
	assert.Contains(t, lines[0], "Status")
	assert.Contains(t, lines[1], "Transmission 4.0.6")
	assert.Contains(t, lines[2], "qBittorrent 5.0.0")
}

// TestPeersSanitizesHumanOutputNotJSON asserts the sanitize boundary on both
// plaintext paths (piped and TTY table) while leaving the structured --json
// contract byte-for-byte intact. A peer's address, client string, and flag string
// are attacker-controlled: any peer in the swarm can set them to whatever it
// likes, and the address is whatever the server reports for the connection.
//
// The fixture carries a control sequence in every column peerFields marks
// sanitize, so dropping any one of those flags fails here.
func TestPeersSanitizesHumanOutputNotJSON(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokePeersSanitizeResponse)
	defer closeSrv()

	// Piped plaintext, end to end through the command.
	piped, _, err := runPeers(t, "--config", cfgPath, "1")
	require.NoError(t, err)
	assert.NotContains(t, piped, "\x1b", "raw ESC must not reach the piped output")
	assert.NotContains(t, piped, "\x07", "raw BEL must not reach the piped output")
	assert.NotContains(t, piped, "\u202e", "no direction override reaches the piped row")
	assert.Contains(t, piped, "[2J203.0.113.7", "visible address text survives sanitization")
	assert.Contains(t, piped, "[2JevilClient", "visible client text survives sanitization")
	assert.Contains(t, piped, "[31mUD", "visible flag text survives sanitization")

	// TTY table path, exercised directly.
	cols, err := parseFields(peerFields, peerFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	writeListTable(&buf, []transmission.Peer{{
		Address: "\x1b[2J203.0.113.7\x07",
		Client:  "\x1b[2Jevil\x07Client\u202e",
		FlagStr: "\x1b[31mUD\x07",
	}}, cols, 0, "No peers.")

	table := buf.String()
	assert.NotContains(t, table, "\x1b", "raw ESC must not reach the table output")
	assert.NotContains(t, table, "\x07", "raw BEL must not reach the table output")
	assert.NotContains(t, table, "\u202e", "no direction override reaches the table row")
	assert.Contains(t, table, "[2J203.0.113.7")
	assert.Contains(t, table, "[2JevilClient")
	assert.Contains(t, table, "[31mUD")

	// JSON is a structured contract and is never sanitized.
	raw, _, err := runPeers(t, "--config", cfgPath, "1", "--json")
	require.NoError(t, err)

	var got []struct {
		Address string `json:"address"`
		Client  string `json:"client"`
		FlagStr string `json:"flag_str"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &got))
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Address, "\x1b", "JSON address keeps the raw ESC (unsanitized)")
	assert.Contains(t, got[0].Address, "\x07", "JSON address keeps the raw BEL")
	assert.Contains(t, got[0].Client, "\x1b", "JSON client keeps the raw ESC (unsanitized)")
	assert.Contains(t, got[0].Client, "\x07", "JSON client keeps the raw BEL")
	assert.Contains(t, got[0].Client, "\u202e", "JSON client keeps the raw direction override")
	assert.Contains(t, got[0].FlagStr, "\x1b", "JSON flag_str keeps the raw ESC")
	assert.Contains(t, got[0].FlagStr, "\x07", "JSON flag_str keeps the raw BEL")
}

func TestPeersFieldsSelectsAndOrdersColumns(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokePeersResponse)
	defer closeSrv()

	out, _, err := runPeers(t, "--config", cfgPath, "1", "--fields", "Client, address ,port")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2)

	first := strings.Split(lines[0], "\t")
	require.Len(t, first, 3, "only the three selected columns are rendered")
	assert.Equal(t, "Transmission 4.0.6", first[0], "client is first")
	assert.Equal(t, "203.0.113.7", first[1], "address is second")
	assert.Equal(t, "51413", first[2], "port is third")
}

// TestPeersFieldsRegistryIsPeersOnly asserts the per-command registry boundary:
// an unknown key errors listing only the peers command's own keys, and a key that
// is valid for another command (list/show/files) is not silently accepted here.
func TestPeersFieldsRegistryIsPeersOnly(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokePeersResponse)
	defer closeSrv()

	_, _, err := runPeers(t, "--config", cfgPath, "1", "--fields", "address,name")
	require.Error(t, err)
	assert.Equal(t, `invalid field "name": valid fields are `+validFieldKeys(peerFields), err.Error(),
		"the error lists only the peers command's own keys")

	for _, key := range peerFieldKeys {
		_, err := parseFields(peerFields, peerFieldKeys, key)
		require.NoErrorf(t, err, "peers accepts its own key %q", key)
	}

	// peerFieldKeys is both the default column set and the --fields help string,
	// so a key added to the registry but not to the slice would be invisible in
	// the table and in the help without failing anything else here.
	assert.Len(t, peerFieldKeys, len(peerFields), "the default column set covers every registry key")
}

func TestPeersWrongArgCount(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokePeersResponse)
	defer closeSrv()

	t.Run("zero args", func(t *testing.T) {
		_, _, err := runPeers(t, "--config", cfgPath)
		require.Error(t, err, "a bare invocation with no selector is an error, not a no-op")
	})

	t.Run("two args", func(t *testing.T) {
		_, _, err := runPeers(t, "--config", cfgPath, "1", "2")
		require.Error(t, err, "more than one positional arg is a usage error")
	})
}

func TestPeersInvalidSelector(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokePeersResponse)
	defer closeSrv()

	_, _, err := runPeers(t, "--config", cfgPath, "not-an-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid torrent selector")
}

func TestPeersNotFoundErrors(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokePeersResponse)
	defer closeSrv()

	absent := "cccccccccccccccccccccccccccccccccccccccc"
	out, _, err := runPeers(t, "--config", cfgPath, absent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no torrent matched: "+absent)
	assert.Empty(t, out, "no output is written when nothing matched")
}

func TestPeersNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runPeers(t, "--config", cfgPath, "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// TestPeersRegisteredOnTorrentGroup asserts the command is reachable at its real
// path, "snatcher torrent peers", not just through its constructor.
func TestPeersRegisteredOnTorrentGroup(t *testing.T) {
	cfgPath, closeSrv := showConfigPath(t, bespokePeersResponse)
	defer closeSrv()

	out, errOut, err := runTorrent(t, "peers", "1", "--config", cfgPath)
	require.NoError(t, err)
	assert.Empty(t, errOut, "nothing is written to stderr")

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "203.0.113.7", strings.Split(lines[0], "\t")[0])
}

// TestPeersTableTruncatesClient is the guard for the client column's truncate
// flag: a narrow width forces the ellipsis, so deleting the flag fails here
// rather than passing silently at width 0 (which disables truncation).
func TestPeersTableTruncatesClient(t *testing.T) {
	long := "SomeVeryLongClientNameThatNoRealPeerWouldEverAdvertise 99.99.99"
	peers := []transmission.Peer{{Address: "203.0.113.7", Port: 51413, Client: long, FlagStr: "UD"}}

	cols, err := parseFields(peerFields, peerFieldKeys, "")
	require.NoError(t, err)

	var buf bytes.Buffer
	writeListTable(&buf, peers, cols, 40, "No peers.")

	out := buf.String()
	assert.Contains(t, out, "…", "long client string is truncated with an ellipsis")
	assert.NotContains(t, out, long)
}
