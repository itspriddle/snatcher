package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGrabConfirmsAndPostsTheRawRelease checks a asks where the release goes,
// and on yes POSTs the release byte-for-byte as the search returned it.
func TestGrabConfirmsAndPostsTheRawRelease(t *testing.T) {
	stub, m := searchModel(t, 120, 40)

	m = press(m, "a")

	o, ok := topAs[confirmOverlay](m)
	require.True(t, ok)
	assert.Equal(t, "Grab Ubuntu 24.04 Desktop [PublicTracker]?", o.question)
	assert.Equal(t, "It goes to Prowlarr's download client, not this server.", o.secondary)
	assert.Empty(t, stub.grabs(), "nothing is grabbed before yes")

	m, cmd := pressCmd(m, "y")

	assert.Equal(t, "Grabbing: Ubuntu 24.04 Desktop [PublicTracker]", m.toast)

	m = settle(t, m, cmd)

	require.Len(t, stub.grabs(), 1)
	assert.JSONEq(t, string(stub.releases[0]), string(stub.grabs()[0]))
	assert.Equal(t, string(m.prowlarr.results[0].Raw), string(stub.grabs()[0]), "the POST is the raw bytes")
	assert.Equal(t, "Grabbed: Ubuntu 24.04 Desktop [PublicTracker]", m.toast)
}

// TestGrabCanceledSendsNothing checks n and esc both leave the release alone.
func TestGrabCanceledSendsNothing(t *testing.T) {
	stub, m := searchModel(t, 120, 40)

	for _, k := range []string{"n", "esc"} {
		m, cmd := pressCmd(press(m, "a"), k)

		assert.Nil(t, cmd)
		assert.False(t, m.hasOverlay(confirmOverlayKind))
	}

	assert.Empty(t, stub.grabs())
}

// TestGrabFailureIsReported checks a failed grab says so.
func TestGrabFailureIsReported(t *testing.T) {
	stub, m := searchModel(t, 120, 40)
	stub.status.Store(500)

	m = runKey(t, press(m, "a"), "y")

	assert.Contains(t, m.toast, "grab failed: Prowlarr error")
}

// TestGrabLabelIsSanitized checks the hostile title reaches the question
// without its escape sequence.
func TestGrabLabelIsSanitized(t *testing.T) {
	_, m := searchModel(t, 120, 40)

	o, ok := topAs[confirmOverlay](press(m, "j", "j", "a"))
	require.True(t, ok)

	assert.NotContains(t, o.question, "\x1b")
	assert.Contains(t, o.question, "hostile")
}

// TestWritePromptsAndWritesThePrivateFile checks w suggests the CLI's name,
// writes the downloaded bytes at 0600, and refuses a second write to the same
// path.
func TestWritePromptsAndWritesThePrivateFile(t *testing.T) {
	_, m := searchModel(t, 120, 40)

	m = press(m, "w")

	o, ok := topAs[promptOverlay](m)
	require.True(t, ok)
	assert.Equal(t, "Write .torrent", o.title)
	assert.Equal(t, "Ubuntu 24.04 Desktop.torrent", o.input.Value())

	path := filepath.Join(t.TempDir(), "ubuntu.torrent")

	m = typeText(press(m, "ctrl+u"), path)
	m, cmd := pressCmd(m, "enter")

	assert.False(t, m.hasOverlay(promptOverlayKind))
	assert.False(t, m.inputFocused)

	m = settle(t, m, cmd)

	data, err := os.ReadFile(path) //nolint:gosec // a test temp path
	require.NoError(t, err)
	assert.Equal(t, torrentFixture, string(data))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assert.Contains(t, m.toast, "Wrote "+path)

	m = typeText(press(m, "w", "ctrl+u"), path)
	m = press(m, "enter")

	o, ok = topAs[promptOverlay](m)
	require.True(t, ok, "an existing file keeps the prompt open")
	assert.Contains(t, o.err, "already exists")
}

// TestWriteRefusesAnEmptyName checks the prompt needs a name.
func TestWriteRefusesAnEmptyName(t *testing.T) {
	_, m := searchModel(t, 120, 40)

	o, ok := topAs[promptOverlay](press(m, "w", "ctrl+u", "enter"))
	require.True(t, ok)
	assert.Equal(t, "a file name is required", o.err)
}

// TestWriteRefusesAMagnetOnlyRelease checks w points at y m when there is no
// file to write.
func TestWriteRefusesAMagnetOnlyRelease(t *testing.T) {
	_, m := searchModel(t, 120, 40)

	m = press(m, "j", "w")

	assert.False(t, m.hasOverlay(promptOverlayKind))
	assert.Equal(t, "no download url; y m yanks the magnet", m.toast)
}

// TestOpenReceivesTheRedactedURL checks O and P hand the opener URLs without the
// key, O falling back to the comments page.
func TestOpenReceivesTheRedactedURL(t *testing.T) {
	stub, m := searchModel(t, 120, 40)

	var opened []string

	m.deps.OpenURL = func(_ context.Context, u string) error {
		opened = append(opened, u)

		return nil
	}

	m = runKey(t, m, "O")
	m = runKey(t, press(m, "j", "j"), "O")
	runKey(t, m, "P")

	require.Len(t, opened, 3)
	assert.Equal(t, stub.url+"/details/1?apikey=REDACTED", opened[0])
	assert.Equal(t, "https://tracker.example/comments/3", opened[1])
	assert.NotContains(t, opened[2], prowlarrTestKey)
	assert.Contains(t, opened[2], stub.url)
}

// TestOpenFailureIsReported checks a failing opener says so.
func TestOpenFailureIsReported(t *testing.T) {
	_, m := searchModel(t, 120, 40)
	m.deps.OpenURL = func(context.Context, string) error { return errors.New("xdg-open: exit status 3") }

	assert.Equal(t, "open failed: xdg-open: exit status 3", runKey(t, m, "O").toast)
}

// TestOpenWithoutABrowserYanks checks O and P yank the redacted URL when no
// opener was wired.
func TestOpenWithoutABrowserYanks(t *testing.T) {
	_, m := searchModel(t, 120, 40)

	m, cmd := pressCmd(m, "O")

	assert.Equal(t, "no browser here; yanked the release page", m.toast)

	content := clipboardContent(t, cmd)
	assert.Contains(t, content, "/details/1?apikey=REDACTED")
	assert.NotContains(t, content, prowlarrTestKey)

	m, cmd = pressCmd(m, "P")

	assert.Equal(t, "no browser here; yanked Prowlarr", m.toast)
	assert.NotContains(t, clipboardContent(t, cmd), prowlarrTestKey)
}

// TestReleaseYanksStripTheKey checks y u, y m, and y d yank the redacted values,
// and say so when a release has none.
func TestReleaseYanksStripTheKey(t *testing.T) {
	_, m := searchModel(t, 120, 40)

	assert.Contains(t, press(m, "y").statusBarView(120), "pending: y")

	m, cmd := pressCmd(press(m, "y"), "u")
	assert.Equal(t, "yanked info URL", m.toast)
	assert.Contains(t, clipboardContent(t, cmd), "apikey=REDACTED")

	m, cmd = pressCmd(press(m, "y"), "m")
	assert.Equal(t, "yanked magnet link", m.toast)
	assert.Contains(t, clipboardContent(t, cmd), "magnet:?xt=urn:btih:aaa111")

	m, cmd = pressCmd(press(m, "y"), "d")
	assert.Equal(t, "yanked download URL without its API key; w writes the file", m.toast)
	assert.NotContains(t, clipboardContent(t, cmd), prowlarrTestKey)

	m = press(m, "j", "y", "d")
	assert.Equal(t, "no download URL", m.toast)
}

// TestReleaseMenuDispatchesItsLeaves checks m opens the release menu, m closes
// it, and a leaf runs the same handler as its key.
func TestReleaseMenuDispatchesItsLeaves(t *testing.T) {
	_, m := searchModel(t, 120, 40)

	m = press(m, "m")

	o, ok := topAs[menuOverlay](m)
	require.True(t, ok)
	assert.Equal(t, "Release", o.title)
	assert.False(t, press(m, "m").hasOverlay(menuOverlayKind))

	m = press(m, "enter")

	c, ok := topAs[confirmOverlay](m)
	require.True(t, ok, "the grab leaf asks, as a does")
	assert.Contains(t, c.question, "Grab ")
	assert.False(t, m.hasOverlay(menuOverlayKind))
}

// TestReleaseVerbsRefuseWithNothingToActOn checks every verb says why before a
// search.
func TestReleaseVerbsRefuseWithNothingToActOn(t *testing.T) {
	_, m := prowlarrModel(t, 120, 40)
	m = m.setFocus(paneResults)

	for _, k := range []string{"a", "w", "O", "m"} {
		after := press(m, k)

		assert.Equalf(t, "nothing selected", after.toast, "%s", k)
		assert.Emptyf(t, after.overlays, "%s", k)
	}

	assert.Equal(t, "nothing selected", press(m, "y", "u").toast)
}

// TestCatalogLoadsLazilyAndApplies checks f fetches the catalog once, lists the
// tree and the indexers with the disabled one unselectable, and applies the
// checked ids to the next search.
func TestCatalogLoadsLazilyAndApplies(t *testing.T) {
	stub, m := prowlarrModel(t, 120, 40)

	m, cmd := pressCmd(m, "f")

	o, ok := topAs[catalogPickOverlay](m)
	require.True(t, ok)
	assert.False(t, o.loaded())
	assert.Contains(t, ansi.Strip(m.render()), "loading…")

	m = settle(t, m, cmd)

	o, _ = topAs[catalogPickOverlay](m)
	require.True(t, o.loaded())
	assert.True(t, stub.requested("/api/v1/indexer/categories"))

	frame := ansi.Strip(m.render())
	assert.Contains(t, frame, "Movies/HD (2040)")
	assert.Contains(t, frame, "OtherTracker  torrent/private  (disabled)")

	// Movies, down to Movies/HD, down twice to PublicTracker, and down once
	// more, which stays put: OtherTracker is disabled.
	m = press(m, "space", "j", "j", "j", "j", "space", "j", "space")

	o, _ = topAs[catalogPickOverlay](m)
	assert.Equal(t, []int{2000}, o.selected(catalogCategory))
	assert.Empty(t, o.selected(catalogIndexer), "the cursor never reached the disabled row")

	m = press(m, "space", "enter")

	assert.Equal(t, []int{2000}, m.prowlarr.params.Categories)
	assert.Equal(t, []int{1}, m.prowlarr.params.IndexerIDs)
	assert.Contains(t, ansi.Strip(m.render()), "categories: 1  indexers: 1")
	assert.False(t, m.hasOverlay(confirmOverlayKind), "no search to resubmit yet")

	_, cmd = pressCmd(m, "f")
	assert.Nil(t, cmd, "a loaded catalog is kept")

	runSearchFor(t, m, "ubuntu")

	q := stub.lastQuery(t)
	assert.Equal(t, []string{"2000"}, q["categories"])
	assert.Equal(t, []string{"1"}, q["indexerIds"])
}

// TestCatalogApplyOffersToResubmit checks a change with results on screen asks
// before searching again.
func TestCatalogApplyOffersToResubmit(t *testing.T) {
	stub, m := searchModel(t, 120, 40)

	m = runKey(t, m, "f")
	m = press(m, "space", "enter")

	c, ok := topAs[confirmOverlay](m)
	require.True(t, ok)
	assert.Equal(t, "Resubmit the search with the new selection?", c.question)

	m = runKey(t, m, "y")

	assert.Equal(t, 2, stub.searches())
	assert.Equal(t, []string{"2000"}, stub.lastQuery(t)["categories"])
	assert.Len(t, m.prowlarr.rows, 3)
}

// TestCatalogErrorRetriesOnTheNextF checks a failed fetch is shown in the modal
// and the next f fetches again.
func TestCatalogErrorRetriesOnTheNextF(t *testing.T) {
	stub, m := prowlarrModel(t, 120, 40)
	stub.status.Store(500)

	m = runKey(t, m, "f")

	assert.Equal(t, catalogFailed, m.prowlarr.catalog.state)
	assert.Contains(t, ansi.Strip(m.render()), "press f again to retry")

	m = press(m, "enter")
	assert.False(t, m.hasOverlay(catalogPickKind))

	stub.status.Store(0)

	m = runKey(t, m, "f")

	assert.Equal(t, catalogLoaded, m.prowlarr.catalog.state)
}

// TestCatalogReplyAfterASwitchIsDropped checks a catalog from another server is
// not installed.
func TestCatalogReplyAfterASwitchIsDropped(t *testing.T) {
	_, m := prowlarrModel(t, 120, 40)

	m, _ = m.update(catalogMsg{gen: m.prowlarr.gen - 1})

	assert.Equal(t, catalogIdle, m.prowlarr.catalog.state)
}

// TestFlattenCategoriesMirrorsTheCLI pins the walk order internal/commands'
// flattenCategories uses: each parent just before its children.
func TestFlattenCategoriesMirrorsTheCLI(t *testing.T) {
	_, m := prowlarrModel(t, 120, 40)

	cats, err := m.prowlarr.client.Categories(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []flatCategory{
		{id: 2000, name: "Movies"},
		{id: 2040, name: "Movies/HD", depth: 1},
		{id: 4000, name: "PC"},
	}, flattenCategories(cats))
}
