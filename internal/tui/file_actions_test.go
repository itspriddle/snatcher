package tui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveFilesModel is a polled live model with the detail pane focused on the
// files tab of torrent 2, whose detail — two files — has been fetched.
func liveFilesModel(t *testing.T, w, h int) (*rpcStub, Model) {
	t.Helper()

	stub, srv := newRPCStub(t)
	stub.respondWhen("torrent-get", isDetailRequest, detailResponse)

	m := fireDetailTick(t, updateKeys(pollOnce(t, liveModel(t, srv, w, h)), "enter"))
	m = press(m, "2")

	require.True(t, m.torrents.detail.hasDetail)
	require.Len(t, m.torrents.detail.detail.Files, 2)

	return stub, m
}

// staticFiles is the static model's detail pane, focused on the files tab of
// mockDetail.
func staticFiles() Model {
	return withMockDetail(press(newTestModel(120, 40), "enter", "2"))
}

// TestSpaceMarksAFileWithoutMovingTheCursor checks space marks the cursor file,
// draws the mark, leaves the cursor, and is not a torrent mark.
func TestSpaceMarksAFileWithoutMovingTheCursor(t *testing.T) {
	m := press(staticFiles(), "j", "space")

	p := m.torrents.detail

	assert.Equal(t, 1, p.files.cursor())
	assert.True(t, p.fileSel.isMarked(1))
	assert.Equal(t, 1, p.fileSel.markCount())
	assert.Equal(t, m.icons.mark(true), p.files.table.Rows()[1][0])
	assert.Zero(t, m.torrents.sel.markCount(), "a file mark is not a torrent mark")
}

// TestFileMarksClearWhenTheTorrentChanges checks marks go with the torrent they
// were made on: another torrent's index 1 is a different file.
func TestFileMarksClearWhenTheTorrentChanges(t *testing.T) {
	m := press(staticFiles(), "space")
	require.Equal(t, 1, m.torrents.detail.fileSel.markCount())

	m = press(m.setFocus(paneList), "j")

	assert.Zero(t, m.torrents.detail.fileSel.markCount())
}

// TestFileMarksSurviveAPoll checks a poll and a refetch of the same torrent keep
// the marks and the cursor.
func TestFileMarksSurviveAPoll(t *testing.T) {
	m := press(staticFiles(), "j", "space")

	m.torrents = m.torrents.setAll(mockTorrents(), m.icons)
	m.torrents.detail = m.torrents.detail.setDetail(mockDetail())

	assert.True(t, m.torrents.detail.fileSel.isMarked(1))
	assert.Equal(t, 1, m.torrents.detail.files.cursor())
}

// TestFileMenuLeavesHitSetFiles walks the five changes: each sends one
// torrent-set naming the one torrent and the marked indexes under its own key.
func TestFileMenuLeavesHitSetFiles(t *testing.T) {
	cases := []struct {
		down int
		key  string
		done string
	}{
		{0, "priority-high", "Set 2 files to high priority"},
		{1, "priority-normal", "Set 2 files to normal priority"},
		{2, "priority-low", "Set 2 files to low priority"},
		{3, "files-wanted", "Marked 2 files wanted"},
		{4, "files-unwanted", "Marked 2 files unwanted"},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			stub, m := liveFilesModel(t, 120, 40)

			m = press(m, "space", "j", "space", "m")
			require.True(t, m.hasOverlay(menuOverlayKind))

			for range tc.down {
				m = press(m, "j")
			}

			m, cmd := pressCmd(m, "enter")

			assert.Empty(t, m.overlays)
			assert.Equal(t, "Updating 2 files…", m.toast)

			m, _ = runCmd(t, m, cmd)

			sets := stub.args(t, "torrent-set")
			require.Len(t, sets, 1)
			assert.Equal(t, []any{float64(0), float64(1)}, sets[0][tc.key])
			assert.Equal(t, []any{float64(2)}, ids(sets[0]))
			assert.Equal(t, tc.done, m.toast)
		})
	}
}

// TestFileChangeRefetchesTheDetailAndPolls checks a change's reply fetches the
// detail at once and polls, rather than editing rows.
func TestFileChangeRefetchesTheDetailAndPolls(t *testing.T) {
	stub, m := liveFilesModel(t, 120, 40)

	m, cmd := pressCmd(press(m, "m"), "enter")
	details := stub.detailCalls(t)

	m, cmd = runCmd(t, m, cmd)

	assert.Equal(t, "Set 1 file to high priority", m.toast)
	assert.Equal(t, int64(2), m.live.detailInFlight)

	m = settle(t, m, cmd)

	assert.Equal(t, details+1, stub.detailCalls(t))
	assert.True(t, m.live.inFlight, "and polls")
}

// TestFileRenameNeedsOneFile checks rename refuses more than one file.
func TestFileRenameNeedsOneFile(t *testing.T) {
	stub, m := liveFilesModel(t, 120, 40)

	m = press(m, "space", "j", "space", "m", "j", "j", "j", "j", "j")
	m, cmd := pressCmd(m, "enter")

	assert.Nil(t, cmd)
	assert.Equal(t, "rename: one file at a time", m.toast)
	assert.Empty(t, m.overlays)
	assert.Zero(t, stub.calls("torrent-rename-path"))
}

// TestFileRenameOverRPC checks rename prompts with the file's last component,
// refuses an unchanged name, and sends the torrent-relative path with the new
// name, reporting it in the CLI's words.
func TestFileRenameOverRPC(t *testing.T) {
	stub, m := liveFilesModel(t, 120, 40)

	m = press(m, "j", "m", "j", "j", "j", "j", "j")
	m, _ = pressCmd(m, "enter")

	o := topPrompt(t, m)
	assert.Equal(t, "SHA256SUMS", o.input.Value())

	m, cmd := o.submit(m, "SHA256SUMS")
	assert.Nil(t, cmd)
	assert.Equal(t, "the name is unchanged", topPrompt(t, m).err)

	m, cmd = o.submit(m, "SUMS")
	require.NotNil(t, cmd)
	assert.Empty(t, m.overlays)

	m, _ = runCmd(t, m, cmd)

	renames := stub.args(t, "torrent-rename-path")
	require.Len(t, renames, 1)
	assert.Equal(t, "debian/SHA256SUMS", renames[0]["path"])
	assert.Equal(t, "SUMS", renames[0]["name"])
	assert.Equal(t, []any{float64(2)}, ids(renames[0]))
	assert.Equal(t, "Renamed debian/SHA256SUMS to SUMS in torrent debian.iso", m.toast)
}

// TestFileMenuRefusals checks m on the files tab opens nothing over no server or
// a detail that has not arrived.
func TestFileMenuRefusals(t *testing.T) {
	m := press(staticFiles(), "m")
	assert.Equal(t, "no server", m.toast)
	assert.Empty(t, m.overlays)

	_, live := detailModel(t)

	m = press(live, "enter", "2", "m")
	assert.Equal(t, "no files", m.toast)
	assert.Empty(t, m.overlays)
}

// TestFileMenuStackIsModal pins the file menu's keys and its swallow-everything
// rule: it is the action menu's overlay, so m closes it.
func TestFileMenuStackIsModal(t *testing.T) {
	assertModalStack(t, fileMenu(2), map[string]Action{
		"up": actionMenuUp, "k": actionMenuUp, "down": actionMenuDown, "j": actionMenuDown,
		"enter": actionMenuSelect, "m": actionMenuOpen,
	})

	_, m := liveFilesModel(t, 120, 40)
	assert.Empty(t, press(m, "m", "m").overlays)
}

// TestFileMenuLeavesHaveHandlers checks every file menu leaf runs an action with
// a handler.
func TestFileMenuLeavesHaveHandlers(t *testing.T) {
	for _, it := range fileMenu(1).items {
		_, ok := handlers[it.action]
		assert.Truef(t, ok, "%q runs %s, which has no handler", it.label, it.action)
	}
}

// TestEscClearsFileMarksBeforeLeavingThePane checks the file marks unwind before
// focus leaves the pane.
func TestEscClearsFileMarksBeforeLeavingThePane(t *testing.T) {
	m := press(staticFiles(), "space")

	m, rung := m.escape()
	assert.Equal(t, "clear file marks", rung)
	assert.Equal(t, paneDetail, m.focusedPane())

	_, rung = m.escape()
	assert.Equal(t, "focus main pane", rung)
}

// TestStatusBarShowsFileMarks checks the bar counts file marks, apart from
// torrent marks, only while there are any.
func TestStatusBarShowsFileMarks(t *testing.T) {
	m := staticFiles()
	assert.NotContains(t, m.statusBarView(120), "file marks")

	m = press(m, "space", "j", "space")
	assert.Contains(t, m.statusBarView(120), "file marks: 2")
	assert.NotContains(t, ansi.Strip(m.statusBarView(120)), "│ marks:")
}

// TestDetailFilesGolden pins the files tab: plain, with marks, and at 80
// columns, where columns drop from the right.
func TestDetailFilesGolden(t *testing.T) {
	assertGolden(t, "detail_files", staticFiles().render())
	assertGolden(t, "detail_files_marked", press(staticFiles(), "space", "j", "space").render())
	assertGolden(t, "detail_files_80x24", withMockDetail(press(newTestModel(80, 24), "enter", "2")).render())
}

// TestFileMenuGolden pins the file menu over two marked files.
func TestFileMenuGolden(t *testing.T) {
	_, m := liveFilesModel(t, 120, 40)

	assertGolden(t, "file_menu", press(m, "space", "j", "space", "m").render())
}
