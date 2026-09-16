package tui

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allStacks is every context stack a key press can be resolved against: one per
// pane, plus the modal stacks that replace a pane's stack. A key that resolves
// twice in any of them is ambiguous for a user, so this is what the collision
// test iterates.
func allStacks() [][]Context {
	stacks := make([][]Context, 0, len(allPanes)+len(modalStacks))

	for _, p := range allPanes {
		stacks = append(stacks, paneContexts(p))
	}

	stacks = append(stacks, tabStacks...)

	return append(stacks, modalStacks...)
}

// TestNoDuplicateKeyInAnyStack is the TUI's .surface: within any stack a user
// can actually be in, no key may be bound twice. Shadowing is a bug, not a
// feature — a more specific context winning silently means the key the help
// view advertises is not the key that runs.
func TestNoDuplicateKeyInAnyStack(t *testing.T) {
	for _, stack := range allStacks() {
		seen := map[string]Action{}

		for _, ctx := range stack {
			for _, b := range registry {
				if b.Context != ctx {
					continue
				}

				for _, k := range b.Key.Keys() {
					prior, dup := seen[k]
					require.Falsef(t, dup, "key %q is bound to both %s and %s in stack %v", k, prior, b.Action, stack)

					seen[k] = b.Action
				}
			}
		}
	}
}

// TestNoBannedKeys pins the terminal constraints from vision.md. These are hard
// limits: a binding on ctrl+j arrives as Enter in a legacy terminal, and ctrl+s
// stops the terminal outright.
func TestNoBannedKeys(t *testing.T) {
	for _, b := range registry {
		for _, k := range b.Key.Keys() {
			assert.Falsef(t, isBannedKey(k), "%s binds the banned key %q", b.Action, k)
		}
	}
}

// TestIsBannedKeyCatchesModifiedAlt checks the prefix scan, not just the list:
// tea spells modifiers in a fixed ctrl+alt+shift order, so "ctrl+alt+x" is as
// banned as "alt+x".
func TestIsBannedKeyCatchesModifiedAlt(t *testing.T) {
	assert.True(t, isBannedKey("alt+x"))
	assert.True(t, isBannedKey("ctrl+alt+x"))
	assert.True(t, isBannedKey("ctrl+l"))
	assert.False(t, isBannedKey("ctrl+w"))
	assert.False(t, isBannedKey("salt"))
}

// TestEveryBindingHasHelp checks that every binding can be rendered in the help
// view. A binding with no description is one a user can press but never
// discover, which defeats the point of the registry feeding help.
func TestEveryBindingHasHelp(t *testing.T) {
	for _, b := range registry {
		help := b.Key.Help()

		assert.NotEmptyf(t, b.Key.Keys(), "%s binds no keys", b.Action)
		assert.NotEmptyf(t, help.Key, "%s has no help key spelling", b.Action)
		assert.NotEmptyf(t, help.Desc, "%s has no help description", b.Action)
	}
}

// TestEveryActionIsUniqueWithinItsContext checks the registry has one row per
// (context, action). Two rows would make bindingFor's answer depend on registry
// order, which is a display-order concern and must not decide behavior.
func TestEveryActionIsUniqueWithinItsContext(t *testing.T) {
	seen := map[string]bool{}

	for _, b := range registry {
		id := fmt.Sprintf("%d/%s", b.Context, b.Action)

		assert.Falsef(t, seen[id], "%s is registered twice in context %s", b.Action, b.Context)

		seen[id] = true
	}
}

// TestBindingForReturnsTheRegisteredRow checks the seam that builds component
// keymaps, including its answer for a row that does not exist.
func TestBindingForReturnsTheRegisteredRow(t *testing.T) {
	b := bindingFor(ctxNav, actionNavDown)
	assert.Equal(t, []string{"down", "j"}, b.Keys())

	assert.Empty(t, bindingFor(ctxNav, actionTorrentStart).Keys())
}

// stringKey adapts a key spelling to what lookup and key.Matches expect, so a
// test can name a key the way a user presses it.
type stringKey string

func (k stringKey) String() string { return string(k) }

// TestLookupFirstContextWins checks the precedence rule the stacks depend on: d
// in the list pane is delete, and the same d in the filter pane is the group
// delete, because the pane's own context is consulted before the shared ones.
func TestLookupFirstContextWins(t *testing.T) {
	action, ok := lookup(paneContexts(paneList), stringKey("d"))
	require.True(t, ok)
	assert.Equal(t, actionTorrentRemove, action)

	action, ok = lookup(paneContexts(paneFilter), stringKey("d"))
	require.True(t, ok)
	assert.Equal(t, actionGroupRemove, action)
}

// TestLookupFallsThroughToGlobal checks a key no pane binds still resolves.
func TestLookupFallsThroughToGlobal(t *testing.T) {
	action, ok := lookup(paneContexts(paneDetail), stringKey("q"))
	require.True(t, ok)
	assert.Equal(t, actionQuit, action)
}

// TestLookupMissReportsNotFound checks an unbound key is reported as such rather
// than resolving to the zero Action, which a caller would otherwise dispatch.
func TestLookupMissReportsNotFound(t *testing.T) {
	_, ok := lookup(paneContexts(paneList), stringKey("ctrl+x"))
	assert.False(t, ok)
}

// TestSettingsStackExcludesNavigation pins the one stack that is deliberately
// not built on ctxNav: in the settings window up/down belong to the focused
// field and tab moves fields, so a nav rung underneath would fight the form.
func TestSettingsStackExcludesNavigation(t *testing.T) {
	assert.NotContains(t, paneContexts(paneSettings), ctxNav)

	action, ok := lookup(paneContexts(paneSettings), stringKey("tab"))
	require.True(t, ok)
	assert.Equal(t, actionSettingsNextField, action)
}

// TestTextInputStackOwnsTheKeyboard pins the "a focused text input owns the
// keyboard" rule: only esc and enter resolve, and every other key — including
// the global window switches — falls through to the input itself.
func TestTextInputStackOwnsTheKeyboard(t *testing.T) {
	stack := []Context{ctxTextInput}

	action, ok := lookup(stack, stringKey("esc"))
	require.True(t, ok)
	assert.Equal(t, actionInputBlur, action)

	for _, k := range []string{"q", "?", ":", ",", "S", "ctrl+t", "ctrl+p"} {
		_, ok := lookup(stack, stringKey(k))
		assert.Falsef(t, ok, "%q must reach the focused input, not a global action", k)
	}
}

// TestConfirmStackIsModal pins the confirm overlay's swallow-everything rule: a
// stray q must not quit past "save before quitting?", and no window key may
// switch out from under a question.
func TestConfirmStackIsModal(t *testing.T) {
	stack := []Context{ctxConfirm}

	for k, want := range map[string]Action{"y": actionConfirmYes, "enter": actionConfirmYes, "n": actionConfirmNo} {
		action, ok := lookup(stack, stringKey(k))
		require.Truef(t, ok, "%q must answer the confirm", k)
		assert.Equal(t, want, action)
	}

	for _, k := range []string{"q", "?", ",", ":", "ctrl+t", "ctrl+p", "S", "R", "t"} {
		_, ok := lookup(stack, stringKey(k))
		assert.Falsef(t, ok, "%q must do nothing while a confirm is open", k)
	}

	assert.NotContains(t, confirmOverlay{}.contexts(), ctxGlobal)
}

// TestPickerStackIsModal pins the picker's swallow-everything rule, the same as
// the confirm overlay's: its keys resolve, and nothing global does.
func TestPickerStackIsModal(t *testing.T) {
	stack := []Context{ctxPicker}

	for k, want := range map[string]Action{
		"up": actionPickerUp, "k": actionPickerUp, "down": actionPickerDown, "j": actionPickerDown,
		"enter": actionPickerSelect, "S": actionServerPicker,
	} {
		action, ok := lookup(stack, stringKey(k))
		require.Truef(t, ok, "%q must resolve in the picker", k)
		assert.Equal(t, want, action)
	}

	for _, k := range []string{"q", "?", ",", ":", "ctrl+t", "ctrl+p", "R", "t", "y", "n"} {
		_, ok := lookup(stack, stringKey(k))
		assert.Falsef(t, ok, "%q must do nothing while the picker is open", k)
	}

	assert.NotContains(t, pickerOverlay{}.contexts(), ctxGlobal)
}

// assertModalStack checks an overlay's stack is modal: every key it lists
// resolves to its action, and none of the global keys it does not list resolves
// at all.
func assertModalStack(t *testing.T, o overlay, want map[string]Action) {
	t.Helper()

	stack := o.contexts()

	for k, action := range want {
		got, ok := lookup(stack, stringKey(k))
		require.Truef(t, ok, "%q must resolve in %s", k, o.kind())
		assert.Equalf(t, action, got, "%q in %s", k, o.kind())
	}

	for _, k := range []string{"q", "?", ",", ":", "ctrl+t", "ctrl+p", "S", "R", "t", "s", "d", "f", "/"} {
		if _, listed := want[k]; listed {
			continue
		}

		_, ok := lookup(stack, stringKey(k))
		assert.Falsef(t, ok, "%q must do nothing while %s is open", k, o.kind())
	}

	assert.NotContains(t, stack, ctxGlobal)
}

// TestSortStackIsModal pins the sort modal's keys and its swallow-everything
// rule.
func TestSortStackIsModal(t *testing.T) {
	assertModalStack(t, sortOverlay{}, map[string]Action{
		"up": actionSortUp, "k": actionSortUp, "down": actionSortDown, "j": actionSortDown,
		"r": actionSortReverse, "enter": actionSortSelect, "o": actionTorrentSort,
	})
}

// TestColumnsStackIsModal pins the columns modal's keys and its
// swallow-everything rule.
func TestColumnsStackIsModal(t *testing.T) {
	assertModalStack(t, columnsOverlay{}, map[string]Action{
		"up": actionColumnsUp, "k": actionColumnsUp, "down": actionColumnsDown, "j": actionColumnsDown,
		"space": actionColumnsToggle, "K": actionColumnsMoveUp, "J": actionColumnsMoveDown,
		"enter": actionColumnsApply, "c": actionTorrentColumns,
	})
}

// TestFilterPickStackIsModal pins the filter modal's keys and its
// swallow-everything rule.
func TestFilterPickStackIsModal(t *testing.T) {
	assertModalStack(t, filterPickOverlay{}, map[string]Action{
		"up": actionFilterPickUp, "k": actionFilterPickUp, "down": actionFilterPickDown, "j": actionFilterPickDown,
		"enter": actionFilterPickSelect, "f": actionTorrentFilter,
	})
}

// TestFormStackIsModal pins the form's keys and its swallow-everything rule,
// including that up and down are left to the row, as in the settings window.
func TestFormStackIsModal(t *testing.T) {
	assertModalStack(t, formOverlay{}, map[string]Action{
		"tab": actionFormNext, "shift+tab": actionFormPrev, "enter": actionFormEnter, "space": actionFormToggle,
	})

	for _, k := range []string{"up", "down", "j", "k", "A", "a"} {
		_, ok := lookup(formOverlay{}.contexts(), stringKey(k))
		assert.Falsef(t, ok, "%q must do nothing in a form", k)
	}
}

// TestMenuStackIsModal pins the action menu's keys and its swallow-everything
// rule: no torrent verb reaches the list while the menu is open.
func TestMenuStackIsModal(t *testing.T) {
	assertModalStack(t, menuOverlay{}, map[string]Action{
		"up": actionMenuUp, "k": actionMenuUp, "down": actionMenuDown, "j": actionMenuDown,
		"enter": actionMenuSelect, "m": actionMenuOpen,
	})
}

// TestPaletteStackIsModal pins the palette's keys and its swallow-everything
// rule. In the palette a key that resolves to nothing is not swallowed but
// forwarded to the query (TestPaletteColonIsACharacter pins that); here it is
// enough that no global or pane action resolves.
func TestPaletteStackIsModal(t *testing.T) {
	assertModalStack(t, paletteOverlay{}, map[string]Action{
		"up": actionPaletteUp, "down": actionPaletteDown, "pgup": actionPalettePageUp, "pgdown": actionPalettePageDown,
		"enter": actionInputSubmit,
	})

	for _, k := range []string{"j", "k", "m", "y"} {
		_, ok := lookup(paletteOverlay{}.contexts(), stringKey(k))
		assert.Falsef(t, ok, "%q must type into the palette", k)
	}
}

// TestProwlarrModalStacksAreModal pins the Prowlarr window's three modals and
// its y chord: their keys resolve, and nothing global does.
func TestProwlarrModalStacksAreModal(t *testing.T) {
	assertModalStack(t, catalogPickOverlay{}, map[string]Action{
		"up": actionCatalogUp, "k": actionCatalogUp, "down": actionCatalogDown, "j": actionCatalogDown,
		"space": actionCatalogToggle, "enter": actionCatalogApply, "f": actionReleaseFilter,
	})
	assertModalStack(t, releaseSortOverlay{}, map[string]Action{
		"up": actionReleaseSortUp, "k": actionReleaseSortUp, "down": actionReleaseSortDown, "j": actionReleaseSortDown,
		"r": actionReleaseSortReverse, "enter": actionReleaseSortSelect, "o": actionReleaseSort,
	})
	assertModalStack(t, releaseColumnsOverlay{}, map[string]Action{
		"up": actionReleaseColumnsUp, "k": actionReleaseColumnsUp, "down": actionReleaseColumnsDown,
		"j": actionReleaseColumnsDown, "space": actionReleaseColumnsToggle, "K": actionReleaseColumnsMoveUp,
		"J": actionReleaseColumnsMoveDown, "enter": actionReleaseColumnsApply, "c": actionReleaseColumns,
	})

	chord := []Context{chordYankRelease.context()}

	for k, want := range map[string]Action{"u": actionYankInfoURL, "m": actionYankReleaseMagnet, "d": actionYankDownloadURL} {
		got, ok := lookup(chord, stringKey(k))
		require.Truef(t, ok, "%q must resolve in the release yank chord", k)
		assert.Equal(t, want, got)
	}

	for _, k := range []string{"q", "h", "n", "p", "y"} {
		_, ok := lookup(chord, stringKey(k))
		assert.Falsef(t, ok, "%q must cancel the release yank chord", k)
	}
}

// TestProwlarrPaneStacks pins what each Prowlarr pane responds to: the release
// verbs live in the results and the detail, and the search pane has none.
func TestProwlarrPaneStacks(t *testing.T) {
	for _, pane := range []Pane{paneResults, paneResultDetail} {
		action, ok := lookup(paneContexts(pane), stringKey("a"))
		require.Truef(t, ok, "a in %s", pane)
		assert.Equal(t, actionReleaseGrab, action)
	}

	_, ok := lookup(paneContexts(paneSearch), stringKey("a"))
	assert.False(t, ok, "the search pane carries no release verbs")

	action, _ := lookup(paneContexts(paneSearch), stringKey("enter"))
	assert.Equal(t, actionSearchSubmit, action)

	action, _ = lookup(paneContexts(paneResults), stringKey("enter"))
	assert.Equal(t, actionReleaseDetailOpen, action)

	action, _ = lookup(paneContexts(paneResults), stringKey("o"))
	assert.Equal(t, actionReleaseSort, action, "o sorts releases here, not torrents")
}

// TestPaneContextsCoversEveryPane checks the static table answers for every
// member of the enum, so a pane added later cannot quietly fall back to a
// global-only stack.
func TestPaneContextsCoversEveryPane(t *testing.T) {
	for _, p := range allPanes {
		stack := paneContexts(p)

		require.NotEmptyf(t, stack, "%s has no context stack", p)
		assert.Equalf(t, ctxGlobal, stack[len(stack)-1], "%s does not end at the global context", p)
	}

	for _, stack := range tabStacks {
		assert.Equalf(t, ctxGlobal, stack[len(stack)-1], "%v does not end at the global context", stack)
	}

	assert.Equal(t, paneContexts(paneDetail), detailContexts(tabOverview), "a tab without keys of its own is the pane's stack")
	assert.Equal(t, ctxTorrentFiles, detailContexts(tabFiles)[0])
}
