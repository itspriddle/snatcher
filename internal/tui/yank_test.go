package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testMagnetLink is the magnet link the detail stub reports.
const testMagnetLink = "magnet:?xt=urn:btih:bbb222&dn=debian.iso"

// magnetDetailResponse is a torrent-get reply for the detail of torrent 2,
// carrying its magnet link.
const magnetDetailResponse = `{"arguments": {"torrents": [
	{"id": 2, "hashString": "bbb222", "name": "debian.iso", "status": 6, "queuePosition": 0,
	 "magnetLink": "magnet:?xt=urn:btih:bbb222&dn=debian.iso"}
]}, "result": "success", "tag": 1}`

// TestYankMagnetFetchesDetail checks y m reads the cursor torrent's detail and
// sends its magnet link to the clipboard.
func TestYankMagnetFetchesDetail(t *testing.T) {
	stub, m := polledModel(t)
	stub.respond("torrent-get", magnetDetailResponse)

	before := stub.calls("torrent-get")

	m, cmd := pressCmd(press(m, "y"), "m")
	require.NotNil(t, cmd)
	assert.Equal(t, "yank magnet: fetching…", m.toast)

	m, clip := runCmd(t, m, cmd)

	assert.Equal(t, before+1, stub.calls("torrent-get"))

	requests := stub.args(t, "torrent-get")
	assert.Equal(t, []any{float64(2)}, ids(requests[len(requests)-1]), "the detail is for the cursor torrent")

	assert.Equal(t, "yanked magnet link", m.toast)
	assert.NotNil(t, clip, "the link is sent to the clipboard")
}

// TestYankMagnetNeedsOneTorrent checks y m refuses more than one marked torrent
// and sends nothing.
func TestYankMagnetNeedsOneTorrent(t *testing.T) {
	stub, m := polledModel(t)
	before := stub.calls("torrent-get")

	m, cmd := pressCmd(press(m, "space", "down", "space", "y"), "m")

	assert.Nil(t, cmd)
	assert.Equal(t, "yank magnet: one torrent at a time", m.toast)
	assert.Equal(t, before, stub.calls("torrent-get"))
}

// TestYankMagnetRefusals checks y m says why it cannot act.
func TestYankMagnetRefusals(t *testing.T) {
	m, cmd := pressCmd(press(newTestModel(120, 40), "y"), "m")
	assert.Nil(t, cmd)
	assert.Equal(t, "no server", m.toast)

	_, live := polledModel(t)

	live, rung := live.escape()
	require.Equal(t, "deselect cursor", rung)

	m, cmd = pressCmd(press(live, "y"), "m")
	assert.Nil(t, cmd)
	assert.Equal(t, "nothing selected", m.toast)
}

// TestYankMagnetErrorToasts checks a failed detail read is reported and nothing
// is yanked.
func TestYankMagnetErrorToasts(t *testing.T) {
	stub, m := polledModel(t)
	stub.failMethod("torrent-get")

	m, cmd := pressCmd(press(m, "y"), "m")
	m, clip := runCmd(t, m, cmd)

	assert.Nil(t, clip)
	assert.Contains(t, m.toast, "yank magnet: server error")
}

// TestYankMagnetWithoutALink checks a torrent the server reports no magnet link
// for yanks nothing.
func TestYankMagnetWithoutALink(t *testing.T) {
	_, m := polledModel(t)

	got, cmd := m.update(magnetMsg{gen: m.live.gen})

	assert.Nil(t, cmd)
	assert.Equal(t, "yank magnet: the server reported no magnet link", got.toast)
}

// TestStaleMagnetRepliesAreDropped checks a reply from before a server switch
// yanks nothing.
func TestStaleMagnetRepliesAreDropped(t *testing.T) {
	_, m := polledModel(t)

	got, cmd := m.update(magnetMsg{gen: 99, link: testMagnetLink})

	assert.Nil(t, cmd)
	assert.Empty(t, got.toast)
}
