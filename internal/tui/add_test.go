package tui

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// addedResponse is a torrent-add reply for a new torrent.
const addedResponse = `{"arguments": {"torrent-added": {"id": 9, "name": "new.iso", "hashString": "h9"}}, "result": "success", "tag": 1}`

// duplicateResponse is a torrent-add reply for a torrent already present. Its
// name carries an escape sequence, written as the JSON escape so the fixture
// holds no raw control byte.
const duplicateResponse = `{"arguments": {"torrent-duplicate": {"id": 2, "name": "debian\u001b[31m.iso", "hashString": "bbb222"}}, "result": "success", "tag": 1}`

// testMagnet is a magnet link the tests add.
const testMagnet = "magnet:?xt=urn:btih:abc"

// addModel is a polled live model whose server answers torrent-add with reply.
func addModel(t *testing.T, reply string) (*rpcStub, Model) {
	t.Helper()

	stub, m := polledModel(t)
	stub.respond("torrent-add", reply)

	return stub, m
}

// submitAdd presses a, types a source, and runs the add to its reply.
func submitAdd(t *testing.T, m Model, source string) Model {
	t.Helper()

	m, cmd := pressCmd(typeText(press(m, "a"), source), "enter")
	require.NotNil(t, cmd, "enter sent nothing")

	m, _ = runCmd(t, m, cmd)

	return m
}

// TestAddMagnet checks a adds a magnet link paused, as the CLI does, and
// reports the name.
func TestAddMagnet(t *testing.T) {
	stub, m := addModel(t, addedResponse)

	m = submitAdd(t, m, testMagnet)

	args := stub.args(t, "torrent-add")
	require.Len(t, args, 1)
	assert.Equal(t, testMagnet, args[0]["filename"])
	assert.Equal(t, true, args[0]["paused"])
	assert.NotContains(t, args[0], "metainfo")
	assert.Equal(t, "Added new.iso", m.toast)
	assert.Empty(t, m.overlays)
	assert.False(t, m.inputFocused)
}

// TestAddFileReadsTheBytes checks a file source is read and sent as metainfo.
func TestAddFileReadsTheBytes(t *testing.T) {
	stub, m := addModel(t, addedResponse)

	path := filepath.Join(t.TempDir(), "new.torrent")
	data := []byte("d8:announce4:test4:infod4:name3:newee")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	submitAdd(t, m, path)

	args := stub.args(t, "torrent-add")
	require.Len(t, args, 1)
	assert.Equal(t, base64.StdEncoding.EncodeToString(data), args[0]["metainfo"])
	assert.NotContains(t, args[0], "filename")
}

// TestAddMissingFileToasts checks an unreadable file is reported and nothing is
// sent.
func TestAddMissingFileToasts(t *testing.T) {
	stub, m := addModel(t, addedResponse)

	m = submitAdd(t, m, filepath.Join(t.TempDir(), "missing.torrent"))

	assert.Contains(t, m.toast, "add: reading ")
	assert.Contains(t, m.toast, "no such file")
	assert.Zero(t, stub.calls("torrent-add"))
}

// TestAddDuplicateToasts checks a torrent already present is a success, named
// sanitized.
func TestAddDuplicateToasts(t *testing.T) {
	require.Contains(t, duplicateResponse, `\u001b`, "the fixture must carry an escape sequence")

	stub, m := addModel(t, duplicateResponse)

	m = submitAdd(t, m, testMagnet)

	assert.Equal(t, 1, stub.calls("torrent-add"))
	assert.Contains(t, m.toast, "Already added: debian")
	assert.NotContains(t, m.toast, "\x1b")
}

// TestAddPromptRequiresASource checks an empty prompt is refused in place.
func TestAddPromptRequiresASource(t *testing.T) {
	stub, m := addModel(t, addedResponse)

	m, cmd := pressCmd(press(m, "a"), "enter")

	assert.Nil(t, cmd)
	assert.Equal(t, "a file path, URL, or magnet link is required", topPrompt(t, m).err)
	assert.Zero(t, stub.calls("torrent-add"))
}

// TestAddRefusalsWithoutAServer checks a and A say why they cannot add.
func TestAddRefusalsWithoutAServer(t *testing.T) {
	for _, key := range []string{"a", "A"} {
		m := press(newTestModel(120, 40), key)

		assert.Equalf(t, "no server", m.toast, "%s", key)
		assert.Emptyf(t, m.overlays, "%s opened something", key)
	}
}

// TestAddFormBuildsTheRequest checks every row of A's form reaches the request:
// the source, a changed directory, the labels as a canonical set, and Start now
// omitting paused.
func TestAddFormBuildsTheRequest(t *testing.T) {
	stub, m := addModel(t, addedResponse)

	m = press(m, "A")

	o, ok := topAs[formOverlay](m)
	require.True(t, ok)
	assert.NotContains(t, o.rows[addRowDir].text, "\x1b", "the session's directory is sanitized")

	m = press(typeText(press(m, "enter"), testMagnet), "enter")
	m = press(typeText(press(m, "tab", "enter", "ctrl+u"), "/srv/new"), "enter")
	m = press(typeText(press(m, "tab", "enter"), "b,a"), "enter")
	m = press(m, "tab", "space")

	m, cmd := pressCmd(press(m, "tab"), "enter")
	require.NotNil(t, cmd)

	assert.Empty(t, m.overlays)
	assert.False(t, m.inputFocused)
	assert.Equal(t, ownerNone, m.owner)

	m, _ = runCmd(t, m, cmd)

	args := stub.args(t, "torrent-add")
	require.Len(t, args, 1)
	assert.Equal(t, testMagnet, args[0]["filename"])
	assert.Equal(t, "/srv/new", args[0]["download-dir"])
	assert.Equal(t, []any{"a", "b"}, args[0]["labels"])
	assert.Equal(t, false, args[0]["paused"], "Start now adds unpaused, explicitly")
	assert.Equal(t, "Added new.iso", m.toast)
}

// TestAddFormKeepsTheSessionDirByDefault checks an untouched directory row sends
// no directory, and the form adds paused with no labels.
func TestAddFormKeepsTheSessionDirByDefault(t *testing.T) {
	stub, m := addModel(t, addedResponse)

	m = press(typeText(press(m, "A", "enter"), testMagnet), "enter")

	m, cmd := pressCmd(press(m, "tab", "tab", "tab", "tab"), "enter")
	runCmd(t, m, cmd)

	args := stub.args(t, "torrent-add")
	require.Len(t, args, 1)
	assert.NotContains(t, args[0], "download-dir")
	assert.NotContains(t, args[0], "labels")
	assert.Equal(t, true, args[0]["paused"])
}

// TestAddFormRequiresASource checks the button refuses an empty source and the
// form stays open.
func TestAddFormRequiresASource(t *testing.T) {
	stub, m := addModel(t, addedResponse)

	m, cmd := pressCmd(press(m, "A", "tab", "tab", "tab", "tab"), "enter")

	assert.Nil(t, cmd)
	require.True(t, m.hasOverlay(formOverlayKind))

	o, _ := topAs[formOverlay](m)
	assert.Equal(t, "a source is required", o.err)
	assert.Contains(t, ansi.Strip(m.render()), "error: a source is required")
	assert.Zero(t, stub.calls("torrent-add"))
}

// TestFormEscBlursRowThenCloses checks the form's two esc rungs: the first
// discards the row's draft and keeps the form, the second closes it.
func TestFormEscBlursRowThenCloses(t *testing.T) {
	_, m := addModel(t, addedResponse)

	m = typeText(press(m, "A", "enter"), "draft")
	require.Equal(t, ownerForm, m.owner)

	m, rung := m.escape()
	assert.Equal(t, "blur input", rung)
	assert.True(t, m.hasOverlay(formOverlayKind))
	assert.False(t, m.inputFocused)

	o, _ := topAs[formOverlay](m)
	assert.Empty(t, o.rows[addRowSource].text, "the draft was discarded")
	assert.False(t, o.editing)

	m, rung = m.escape()
	assert.Equal(t, "close overlay", rung)
	assert.Empty(t, m.overlays)
}

// TestFormTabWraps checks shift+tab from the first row lands on the button.
func TestFormTabWraps(t *testing.T) {
	_, m := addModel(t, addedResponse)

	o, _ := topAs[formOverlay](press(m, "A", "shift+tab"))

	assert.Equal(t, addRowAdd, o.cursor)
}

// TestFormAddGolden pins the add form with Start now checked under the cursor.
func TestFormAddGolden(t *testing.T) {
	_, m := addModel(t, addedResponse)

	assertGolden(t, "form_add", press(m, "A", "tab", "tab", "tab", "space").render())
}
