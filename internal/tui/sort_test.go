package tui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// prefsModel is the default torrents frame with a config store, for the tests
// that check what o and c persist.
func prefsModel(t *testing.T) (Model, *fakeConfig) {
	t.Helper()

	cfg := newFakeConfig()

	deps := testDeps()
	deps.Config = cfg

	return New(deps).applyTheme(true).resize(120, 40), cfg
}

// TestSortOverlayOpensOnTheCurrentField checks o opens the modal on the field in
// force, and that o and esc both close it.
func TestSortOverlayOpensOnTheCurrentField(t *testing.T) {
	m := press(newTestModel(120, 40), "o")

	o, ok := topAs[sortOverlay](m)
	require.True(t, ok)
	assert.Zero(t, o.cursor)

	frame := ansi.Strip(m.render())
	assert.Contains(t, frame, "> queue  (current)")
	assert.Contains(t, frame, "reverse: off")

	assert.False(t, press(m, "o").hasOverlay(sortOverlayKind))
	assert.False(t, press(m, "esc").hasOverlay(sortOverlayKind))
}

// TestSortOverlayAppliesAndPersists checks enter sorts the table and writes both
// keys that changed.
func TestSortOverlayAppliesAndPersists(t *testing.T) {
	m, cfg := prefsModel(t)

	m = press(m, "o", "j", "j", "j", "r", "enter")

	assert.False(t, m.hasOverlay(sortOverlayKind))
	assert.Equal(t, transmission.SortSize, m.torrents.listView.sort)
	assert.True(t, m.torrents.listView.reverse)
	assert.Equal(t, int64(1), m.torrents.rows[0].ID, "the largest torrent leads")
	assert.Equal(t, "sort: size, reversed", m.toast)

	assert.Equal(t, []string{clientKeyListSort, clientKeyListReverse}, cfg.sets)
	assert.Equal(t, "size", cfg.value(clientKeyListSort))

	reversed, _ := cfg.value(clientKeyListReverse).(bool)
	assert.True(t, reversed)
}

// TestSortOnlyWritesWhatChanged checks a key the modal did not change is not
// written, so a default stays a default in the file.
func TestSortOnlyWritesWhatChanged(t *testing.T) {
	m, cfg := prefsModel(t)

	m = press(m, "o", "j", "enter")

	assert.Equal(t, transmission.SortName, m.torrents.listView.sort)
	assert.Equal(t, []string{clientKeyListSort}, cfg.sets)

	press(m, "o", "enter")
	assert.Len(t, cfg.sets, 1, "applying the sort in force writes nothing")
}

// TestSortReverseToggle checks r flips the direction inside the modal only.
func TestSortReverseToggle(t *testing.T) {
	m := press(newTestModel(120, 40), "o", "r")

	assert.Contains(t, ansi.Strip(m.render()), "reverse: on")
	assert.False(t, m.torrents.listView.reverse, "nothing applies before enter")

	o, _ := topAs[sortOverlay](press(m, "r"))
	assert.False(t, o.reverse)
}

// TestSortEscCancels checks esc discards the modal's choices.
func TestSortEscCancels(t *testing.T) {
	m := press(newTestModel(120, 40), "o", "j", "r", "esc")

	assert.False(t, m.hasOverlay(sortOverlayKind))
	assert.Equal(t, defaultListView().sort, m.torrents.listView.sort)
	assert.False(t, m.torrents.listView.reverse)
}

// TestSortWithoutConfigIsSessionOnly checks a model with no config store still
// sorts.
func TestSortWithoutConfigIsSessionOnly(t *testing.T) {
	m := press(newTestModel(120, 40), "o", "j", "enter")

	assert.Equal(t, transmission.SortName, m.torrents.listView.sort)
	assert.Equal(t, "sort: name", m.toast)
}

// TestPrefWriteFailureStillApplies checks a failed write is toasted and the sort
// still applies for the session.
func TestPrefWriteFailureStillApplies(t *testing.T) {
	m, cfg := prefsModel(t)
	cfg.failKey = clientKeyListSort

	m = press(m, "o", "j", "enter")

	assert.Equal(t, transmission.SortName, m.torrents.listView.sort)
	assert.Equal(t, "config: disk full", m.toast)
}

// TestSortOverlayGolden pins the modal: the cursor marker, the current field, and
// the direction footer.
func TestSortOverlayGolden(t *testing.T) {
	assertGolden(t, "sort_overlay", press(newTestModel(120, 40), "o", "j").render())
}
