package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// promptTorrentsResponse is three torrents in queue order 1, 2, 3: the first two
// carry the same labels in different orders, and the third none, a download dir
// of its own, and an escape sequence in its name.
const promptTorrentsResponse = `{"arguments": {"torrents": [
	{"id": 1, "hashString": "h1", "name": "one.iso", "status": 0, "queuePosition": 0, "labels": ["b", "a"], "downloadDir": "/data/one"},
	{"id": 2, "hashString": "h2", "name": "two.iso", "status": 0, "queuePosition": 1, "labels": ["a", "b"], "downloadDir": "/data/two"},
	{"id": 3, "hashString": "h3", "name": "three\u001b[31m.iso", "status": 0, "queuePosition": 2, "labels": [], "downloadDir": "/data/three"}
]}, "result": "success", "tag": 1}`

// promptModel is a live model polled from promptTorrentsResponse, with the
// cursor on torrent 1.
func promptModel(t *testing.T) (*rpcStub, Model) {
	t.Helper()

	stub, srv := newRPCStub(t)
	stub.respond("torrent-get", promptTorrentsResponse)

	m := pollOnce(t, liveModel(t, srv, 120, 40))
	require.Equal(t, []int64{1, 2, 3}, rowIDs(m.torrents.rows))

	return stub, m
}

// topPrompt is the prompt on top, failing the test when there is none.
func topPrompt(t *testing.T, m Model) promptOverlay {
	t.Helper()

	o, ok := topAs[promptOverlay](m)
	require.True(t, ok, "no prompt is open")

	return o
}

// TestLabelsPromptPrefillsSharedSet checks L starts from the labels every
// selected torrent shares, compared as sets, and from nothing when they differ.
func TestLabelsPromptPrefillsSharedSet(t *testing.T) {
	_, m := promptModel(t)

	o := topPrompt(t, press(m, "L"))
	assert.Equal(t, "Set labels on 1 torrent", o.title)
	assert.Equal(t, "a, b", o.input.Value())

	o = topPrompt(t, press(m, "space", "j", "space", "L"))
	assert.Equal(t, "Set labels on 2 torrents", o.title)
	assert.Equal(t, "a, b", o.input.Value(), "the same set in another order is shared")

	o = topPrompt(t, press(m, "space", "j", "j", "space", "L"))
	assert.Empty(t, o.input.Value(), "different sets suggest neither")
}

// TestLabelsPromptRejectsEmptyItem checks an empty label is refused in the
// prompt, which stays open and sends nothing.
func TestLabelsPromptRejectsEmptyItem(t *testing.T) {
	stub, m := promptModel(t)

	m = press(typeText(press(m, "L", "ctrl+u"), "a,,b"), "enter")

	assert.Equal(t, "a label cannot be empty", topPrompt(t, m).err)
	assert.True(t, m.inputFocused)
	assert.Contains(t, ansi.Strip(m.render()), "error: a label cannot be empty")
	assert.Zero(t, stub.calls("torrent-set"))
}

// TestLabelsSetsOverRPC checks enter closes the prompt, releases the keyboard,
// and writes the canonical set.
func TestLabelsSetsOverRPC(t *testing.T) {
	stub, m := promptModel(t)

	m = typeText(press(m, "j", "j", "L"), "b, a,a ")

	m, cmd := pressCmd(m, "enter")

	assert.Empty(t, m.overlays)
	assert.False(t, m.inputFocused)
	assert.Equal(t, ownerNone, m.owner)

	m, _ = runCmd(t, m, cmd)

	args := stub.args(t, "torrent-set")
	require.Len(t, args, 1)
	assert.Equal(t, []any{"a", "b"}, args[0]["labels"])
	assert.Equal(t, []any{float64(3)}, ids(args[0]))
	assert.Equal(t, "Updated 1 torrent", m.toast)
}

// TestLabelsEmptyClears checks an empty prompt writes an empty set.
func TestLabelsEmptyClears(t *testing.T) {
	stub, m := promptModel(t)

	m, cmd := pressCmd(press(m, "L", "ctrl+u"), "enter")
	runCmd(t, m, cmd)

	assert.Equal(t, []any{}, stub.args(t, "torrent-set")[0]["labels"])
}

// TestParseLabelList pins the prompt's parsing.
func TestParseLabelList(t *testing.T) {
	for raw, want := range map[string][]string{
		"":             {},
		"   ":          {},
		"linux":        {"linux"},
		" b , a, b ,a": {"a", "b"},
	} {
		got, err := parseLabelList(raw)
		require.NoErrorf(t, err, "%q", raw)
		assert.Equalf(t, want, got, "%q", raw)
	}

	for _, raw := range []string{"a,,b", ",a", "a, "} {
		_, err := parseLabelList(raw)
		assert.Errorf(t, err, "%q", raw)
	}
}

// TestMovePrefillsTheCursorDir checks M starts from the cursor torrent's
// location, relocates the files, and refuses an empty location.
func TestMovePrefillsTheCursorDir(t *testing.T) {
	stub, m := promptModel(t)

	o := topPrompt(t, press(m, "M"))
	assert.Equal(t, "Move 1 torrent to", o.title)
	assert.Equal(t, "/data/one", o.input.Value())

	empty := press(m, "M", "ctrl+u", "enter")
	assert.Equal(t, "a location is required", topPrompt(t, empty).err)

	m, cmd := pressCmd(typeText(press(m, "M", "ctrl+u"), "/new"), "enter")
	m, _ = runCmd(t, m, cmd)

	args := stub.args(t, "torrent-set-location")
	require.Len(t, args, 1)
	assert.Equal(t, "/new", args[0]["location"])
	assert.Equal(t, true, args[0]["move"])
	assert.Equal(t, []any{float64(1)}, ids(args[0]))
	assert.Equal(t, "Moved 1 torrent", m.toast)
}

// TestRenameRequiresOneTorrent checks rename refuses a wider selection rather
// than choosing one torrent of it.
func TestRenameRequiresOneTorrent(t *testing.T) {
	_, m := promptModel(t)

	got, cmd := press(m, "space", "j", "space").dispatch(actionTorrentRename, tea.KeyPressMsg{})

	assert.Nil(t, cmd)
	assert.Empty(t, got.overlays)
	assert.Equal(t, "rename: one torrent at a time", got.toast)
}

// TestRenameOverRPC checks rename starts from the name, refuses it unchanged,
// and renames the torrent's root path.
func TestRenameOverRPC(t *testing.T) {
	stub, m := promptModel(t)
	stub.respond("torrent-rename-path", `{"arguments": {"id": 1, "path": "one.iso", "name": "uno.iso"}, "result": "success", "tag": 1}`)

	m, _ = m.dispatch(actionTorrentRename, tea.KeyPressMsg{})

	assert.Equal(t, "one.iso", topPrompt(t, m).input.Value())

	m = press(m, "enter")
	assert.Equal(t, "the name is unchanged", topPrompt(t, m).err)

	m, cmd := pressCmd(typeText(press(m, "ctrl+u"), "uno.iso"), "enter")
	m, _ = runCmd(t, m, cmd)

	args := stub.args(t, "torrent-rename-path")
	require.Len(t, args, 1)
	assert.Equal(t, "one.iso", args[0]["path"])
	assert.Equal(t, "uno.iso", args[0]["name"])
	assert.Equal(t, []any{float64(1)}, ids(args[0]))
	assert.Equal(t, "Renamed 1 torrent", m.toast)
}

// TestPromptPrefillIsSanitized checks a hostile name is neutralized before it is
// put in front of the user to edit.
func TestPromptPrefillIsSanitized(t *testing.T) {
	_, m := promptModel(t)

	require.Contains(t, m.torrents.rows[2].Name, "\x1b", "the fixture must carry an escape sequence")

	m, _ = press(m, "j", "j").dispatch(actionTorrentRename, tea.KeyPressMsg{})

	assert.NotContains(t, topPrompt(t, m).input.Value(), "\x1b")
	assert.NotContains(t, m.render(), "\x1b[31m.iso")
}

// TestPromptEscCancelsAndReleasesTheInput checks one esc closes a prompt, sends
// nothing, and gives the keyboard back to the list.
func TestPromptEscCancelsAndReleasesTheInput(t *testing.T) {
	stub, m := promptModel(t)

	m = typeText(press(m, "L"), "q")
	require.Equal(t, ownerPrompt, m.owner)
	assert.Equal(t, "a, bq", topPrompt(t, m).input.Value(), "q typed into the prompt rather than quitting")

	got, rung := m.escape()

	assert.Equal(t, "close overlay", rung)
	assert.Empty(t, got.overlays)
	assert.False(t, got.inputFocused)
	assert.Equal(t, ownerNone, got.owner)
	assert.Equal(t, 1, press(got, "j").torrents.sel.cursor, "the list has the keyboard again")
	assert.Zero(t, stub.calls("torrent-set"))
}

// TestPromptHelpLineMatchesTheBox checks the help line under a prompt says esc
// cancels, as the box does, rather than the text-input context's "blur".
func TestPromptHelpLineMatchesTheBox(t *testing.T) {
	_, m := promptModel(t)

	line := ansi.Strip(press(m, "L").helpLineView(120))

	assert.Contains(t, line, "esc cancel")
	assert.NotContains(t, line, "blur")
}

// TestPromptLabelsGolden pins the labels prompt over two marked torrents.
func TestPromptLabelsGolden(t *testing.T) {
	_, m := promptModel(t)

	assertGolden(t, "prompt_labels", press(m, "space", "j", "space", "L").render())
}
