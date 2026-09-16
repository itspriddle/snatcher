package tui

import (
	"slices"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// paneStacks is every stack the palette can open over: one per pane, plus the
// stacks a pane's tab adds.
func paneStacks() [][]Context {
	stacks := make([][]Context, 0, len(allPanes)+len(tabStacks))

	for _, p := range allPanes {
		stacks = append(stacks, paneContexts(p))
	}

	return append(stacks, tabStacks...)
}

// entryLabels is the labels of a palette's entries, in order.
func entryLabels(entries []paletteEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.label)
	}

	return out
}

// entryFor finds the entry for an action.
func entryFor(entries []paletteEntry, a Action) (paletteEntry, bool) {
	for _, e := range entries {
		if e.action == a {
			return e, true
		}
	}

	return paletteEntry{}, false
}

// topPalette returns the palette on top of the stack, failing the test when
// there is none.
func topPalette(t *testing.T, m Model) paletteOverlay {
	t.Helper()

	o, ok := topAs[paletteOverlay](m)
	require.True(t, ok, "no palette on top")

	return o
}

// TestPaletteListsTheLiveStack checks the palette lists what is live where it
// opened, most specific context first, and not what is live elsewhere.
func TestPaletteListsTheLiveStack(t *testing.T) {
	list := entryLabels(paletteEntries(paneContexts(paneList)))

	assert.Contains(t, list, "start")
	assert.Contains(t, list, "toggle alt-speed")
	assert.Contains(t, list, "quit")
	assert.NotContains(t, list, "start group")

	filter := entryLabels(paletteEntries(paneContexts(paneFilter)))

	assert.Contains(t, filter, "start group")
	assert.NotContains(t, filter, "start")

	// Stack order, then registry order, then the context's unbound rows.
	var at []int
	for _, label := range []string{"start", "pause", "force start", "yank magnet link", "toggle filter pane", "toggle detail pane", "quit"} {
		i := slices.Index(list, label)
		require.GreaterOrEqualf(t, i, 0, "%q is not listed", label)

		at = append(at, i)
	}

	assert.True(t, slices.IsSorted(at), "entries out of order: %v", at)
}

// TestPaletteHidesNavigation checks no hidden action is listed from any stack.
func TestPaletteHidesNavigation(t *testing.T) {
	for _, stack := range paneStacks() {
		for _, e := range paletteEntries(stack) {
			assert.Falsef(t, paletteHidden[e.action], "%s is hidden but listed from %v", e.action, stack)
		}
	}

	assert.NotContains(t, entryLabels(paletteEntries(paneContexts(paneList))), "down")
}

// TestPaletteListsUnboundActionsForTheirContext checks an unbound action is
// listed only where its context is live.
func TestPaletteListsUnboundActionsForTheirContext(t *testing.T) {
	has := func(stack []Context, a Action) bool {
		_, ok := entryFor(paletteEntries(stack), a)

		return ok
	}

	assert.True(t, has(paneContexts(paneList), actionTorrentForceStart))
	assert.False(t, has(paneContexts(paneFilter), actionTorrentForceStart))
	assert.False(t, has(paneContexts(paneDetail), actionTorrentForceStart))

	assert.True(t, has(detailContexts(tabFiles), actionFilePriorityHigh))
	assert.False(t, has(paneContexts(paneDetail), actionFilePriorityHigh))
	assert.False(t, has(paneContexts(paneList), actionFilePriorityHigh))

	e, ok := entryFor(paletteEntries(paneContexts(paneList)), actionTorrentForceStart)
	require.True(t, ok)
	assert.Equal(t, "torrent list", e.note)
	assert.Empty(t, e.keys, "an unbound action has no key to show")
}

// TestPaletteListsYankWithItsChordKeys checks the y chord is flattened into the
// list pane's entries, named and keyed with its prefix.
func TestPaletteListsYankWithItsChordKeys(t *testing.T) {
	e, ok := entryFor(paletteEntries(paneContexts(paneList)), actionYankMagnet)
	require.True(t, ok)

	assert.Equal(t, "yank magnet link", e.label)
	assert.Equal(t, "y m", e.keys)
	assert.Equal(t, "yank", e.note)

	_, ok = entryFor(paletteEntries(paneContexts(paneFilter)), actionYankMagnet)
	assert.False(t, ok, "the yank chord starts in the list pane only")
}

// TestEveryPaneActionIsShownOrHidden is one half of the drift test: every action
// a pane stack binds, its chords included, is either listed from that stack or
// hidden on purpose. A new binding that is neither fails here.
func TestEveryPaneActionIsShownOrHidden(t *testing.T) {
	for _, stack := range paneStacks() {
		entries := paletteEntries(stack)

		for _, b := range registry {
			if !slices.Contains(paletteContexts(stack), b.Context) || paletteHidden[b.Action] {
				continue
			}

			_, ok := entryFor(entries, b.Action)
			assert.Truef(t, ok, "%s is bound in %s but neither listed nor hidden", b.Action, b.Context)
		}
	}
}

// TestPaletteHiddenIsHonest is the other half: every hidden action is bound in
// some pane stack or listed chord, and is not also unbound. A stale entry fails
// here.
func TestPaletteHiddenIsHonest(t *testing.T) {
	var live []Context
	for _, stack := range paneStacks() {
		live = append(live, paletteContexts(stack)...)
	}

	for action := range paletteHidden {
		bound := slices.ContainsFunc(registry, func(b Binding) bool {
			return b.Action == action && slices.Contains(live, b.Context)
		})

		assert.Truef(t, bound, "%s is hidden but no pane stack binds it", action)
		assert.Falsef(t, isUnbound(action), "%s is both hidden and unbound", action)
	}
}

// TestEveryPaletteEntryHasAHandler checks nothing the palette lists runs into
// "no handler".
func TestEveryPaletteEntryHasAHandler(t *testing.T) {
	for _, stack := range paneStacks() {
		for _, e := range paletteEntries(stack) {
			_, ok := handlers[e.action]
			assert.Truef(t, ok, "%q runs %s, which has no handler", e.label, e.action)
			assert.NotNilf(t, e.run, "%q has no run", e.label)
		}
	}
}

// TestPaletteEntriesAreUnique checks no action is listed twice from one stack.
func TestPaletteEntriesAreUnique(t *testing.T) {
	for _, stack := range paneStacks() {
		seen := map[Action]bool{}

		for _, e := range paletteEntries(stack) {
			assert.Falsef(t, seen[e.action], "%s is listed twice from %v", e.action, stack)

			seen[e.action] = true
		}
	}
}

// TestMenuLeavesAreBoundOrUnbound checks every action menu leaf can be reached by
// name too: it is bound in its pane's stack or a chord that starts there, or it
// is an unbound action of that pane.
func TestMenuLeavesAreBoundOrUnbound(t *testing.T) {
	for _, tc := range []struct {
		menu menuOverlay
		pane Pane
	}{
		{torrentMenu(1), paneList},
		{releaseMenu(), paneResults},
	} {
		t.Run(tc.menu.title, func(t *testing.T) {
			assertMenuLeavesReachable(t, tc.menu, tc.pane)
		})
	}
}

// assertMenuLeavesReachable walks a menu, checking each leaf against a pane.
func assertMenuLeavesReachable(t *testing.T, menu menuOverlay, pane Pane) {
	t.Helper()

	stack := paneContexts(pane)
	live := paletteContexts(stack)

	var walk func(items []menuItem)

	walk = func(items []menuItem) {
		for _, it := range items {
			if len(it.sub) > 0 {
				walk(it.sub)

				continue
			}

			if it.disabled {
				continue
			}

			bound := slices.ContainsFunc(registry, func(b Binding) bool {
				return b.Action == it.action && slices.Contains(live, b.Context)
			})
			unbound := slices.ContainsFunc(unboundActions, func(u unboundAction) bool {
				return u.action == it.action && u.ctx == stack[0]
			})

			assert.Truef(t, bound || unbound, "menu leaf %q (%s) is neither bound in %s nor unbound there", it.label, it.action, pane)
		}
	}

	walk(menu.items)
}

// TestPaletteListsReleaseYanks checks the Prowlarr results palette flattens its
// own y chord, and leaves the torrent verbs out.
func TestPaletteListsReleaseYanks(t *testing.T) {
	entries := paletteEntries(paneContexts(paneResults))

	e, ok := entryFor(entries, actionYankDownloadURL)
	require.True(t, ok)
	assert.Equal(t, "yank download URL", e.label)
	assert.Equal(t, "y d", e.keys)

	_, ok = entryFor(entries, actionTorrentStart)
	assert.False(t, ok)

	_, ok = entryFor(entries, actionReleaseGrab)
	assert.True(t, ok)
}

// TestPaletteFiltersAndResetsTheCursor checks typing refilters, best match
// first, and puts the cursor back on it.
func TestPaletteFiltersAndResetsTheCursor(t *testing.T) {
	_, m := polledModel(t)

	m = press(m, ":", "down", "down")
	require.Equal(t, 2, topPalette(t, m).cursor)

	m = typeText(m, "pau")

	o := topPalette(t, m)
	assert.Zero(t, o.cursor)
	assert.Equal(t, "pau", o.input.Value())

	e, ok := o.current()
	require.True(t, ok)
	assert.Equal(t, "pause", e.label)
	assert.Less(t, len(o.matches), len(o.entries))
}

// TestPaletteEnterRunsTheEntryAndCloses checks enter runs the cursor entry the
// way its key would, closes the palette, and gives the keyboard back.
func TestPaletteEnterRunsTheEntryAndCloses(t *testing.T) {
	stub, m := polledModel(t)

	m, cmd := pressCmd(typeText(press(m, ":"), "force"), "enter")

	assert.Empty(t, m.overlays)
	assert.False(t, m.inputFocused)
	assert.Equal(t, ownerNone, m.owner)

	runCmd(t, m, cmd)

	assert.Equal(t, 1, stub.calls("torrent-start-now"))
}

// TestPaletteRunsAgainstTheSelection checks an entry acts on the marks, and that
// its confirm sits over the list rather than over a palette.
func TestPaletteRunsAgainstTheSelection(t *testing.T) {
	_, m := polledModel(t)

	m = press(typeText(press(m, "space", "down", "space", ":"), "pause"), "enter")

	require.Len(t, m.overlays, 1)
	assert.True(t, m.hasOverlay(confirmOverlayKind))
	assert.Contains(t, ansi.Strip(m.render()), "Stop 2 torrents?")
}

// TestPaletteEnterOnNoMatchesDoesNothing checks enter over an empty list leaves
// the palette open with the query.
func TestPaletteEnterOnNoMatchesDoesNothing(t *testing.T) {
	_, m := polledModel(t)

	m, cmd := pressCmd(typeText(press(m, ":"), "zzzz"), "enter")

	assert.Nil(t, cmd)
	assert.Empty(t, topPalette(t, m).matches)
	assert.True(t, m.inputFocused)
	assert.Contains(t, ansi.Strip(m.render()), "no matches")
}

// TestPaletteColonIsACharacter checks : inside the palette types rather than
// opening a second palette: a lookup miss reaches the query.
func TestPaletteColonIsACharacter(t *testing.T) {
	m := press(newTestModel(120, 40), ":", ":", "j", "q")

	require.Len(t, m.overlays, 1)
	assert.Equal(t, ":jq", topPalette(t, m).input.Value())
}

// TestPaletteOpensOverHelpByClosingIt checks : from help replaces help, so the
// palette lists the pane help was describing.
func TestPaletteOpensOverHelpByClosingIt(t *testing.T) {
	m := press(newTestModel(120, 40).setFocus(paneFilter), "?", ":")

	require.Len(t, m.overlays, 1)
	assert.Contains(t, entryLabels(topPalette(t, m).entries), "start group")
}

// TestPaletteDoesNotOpenTwice checks a palette never stacks on a palette.
func TestPaletteDoesNotOpenTwice(t *testing.T) {
	m := press(newTestModel(120, 40), ":")

	m, _ = m.openPalette()

	assert.Len(t, m.overlays, 1)
}

// TestPaletteEscClosesAndReleasesTheInput checks one esc closes the palette
// through the ladder's close-overlay rung, and the list has the keyboard again.
func TestPaletteEscClosesAndReleasesTheInput(t *testing.T) {
	m := press(newTestModel(120, 40), ":")
	require.True(t, m.inputFocused)

	m, rung := m.escape()

	assert.Equal(t, "close overlay", rung)
	assert.Empty(t, m.overlays)
	assert.False(t, m.inputFocused)
	assert.Equal(t, ownerNone, m.owner)

	before := m.torrents.sel
	assert.NotEqual(t, before, press(m, "j").torrents.sel, "j must move the list again")
}

// TestPaletteHelpLineMatchesTheBox checks the help line under the frame words
// the palette's keys as its box does.
func TestPaletteHelpLineMatchesTheBox(t *testing.T) {
	m := press(newTestModel(120, 40), ":")

	assert.Equal(t, paletteOverlay{}.helpBindings(), m.shortBindings())
}

// TestPaletteGolden pins the palette over a live frame: the input, the entries
// with their notes and keys, and the palette's help line.
func TestPaletteGolden(t *testing.T) {
	_, m := polledModel(t)

	assertGolden(t, "palette", press(m, ":").render())
}

// TestPaletteFilteredGolden pins a filtered palette.
func TestPaletteFilteredGolden(t *testing.T) {
	_, m := polledModel(t)

	assertGolden(t, "palette_filtered", typeText(press(m, ":"), "lim").render())
}
