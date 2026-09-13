package tui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// menuQueueDown is the presses from the menu's first row down to "queue ▸".
var menuQueueDown = []string{"j", "j", "j", "j", "j", "j"}

// menuLabels is the labels of a menu's rows, in order.
func menuLabels(items []menuItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.label)
	}

	return out
}

// openQueueMenu presses m, walks to "queue ▸", and opens it.
func openQueueMenu(m Model) Model {
	return press(press(press(m, "m"), menuQueueDown...), "enter")
}

// TestMenuListsVisionItems pins the menu's rows to vision.md's list, in order,
// with trackers and other greyed until the milestones that implement them.
func TestMenuListsVisionItems(t *testing.T) {
	o := torrentMenu(3)

	assert.Equal(t, "Actions (3 torrents)", o.title)
	assert.Equal(t, []string{
		"force start", "start", "pause", "verify", "reannounce", "rename torrent", "queue",
		"move / set location", "set labels", "yank", "delete", "trackers", "other",
	}, menuLabels(o.items))

	assert.Equal(t, []string{"up", "down", "top", "bottom"}, menuLabels(o.items[6].sub))
	assert.Equal(t, []string{"magnet link", "info hash", "name", "download path"}, menuLabels(o.items[9].sub))

	assert.True(t, o.items[11].disabled)
	assert.Equal(t, "(T6)", o.items[11].note)
	assert.True(t, o.items[12].disabled)
	assert.Equal(t, "(T7)", o.items[12].note)
}

// TestMenuLeavesHaveHandlers walks every leaf: each runs an action with a
// handler, and each disabled row runs nothing.
func TestMenuLeavesHaveHandlers(t *testing.T) {
	var walk func(items []menuItem)

	walk = func(items []menuItem) {
		for _, it := range items {
			switch {
			case len(it.sub) > 0:
				walk(it.sub)
			case it.disabled:
				assert.Emptyf(t, it.action, "disabled %q runs something", it.label)
			default:
				_, ok := handlers[it.action]
				assert.Truef(t, ok, "%q runs %s, which has no handler", it.label, it.action)
			}
		}
	}

	walk(torrentMenu(1).items)
}

// TestMenuSkipsDisabledRows checks the cursor never rests on a greyed row, and
// that the greyed rows say why.
func TestMenuSkipsDisabledRows(t *testing.T) {
	o := torrentMenu(1)

	for range o.items {
		o = o.move(1)
	}

	assert.Equal(t, 10, o.cursor, "the cursor stops on delete, the last enabled row")

	_, m := polledModel(t)
	assert.Contains(t, ansi.Strip(press(m, "m").render()), "trackers  (T6)")
}

// TestMenuRefusals checks m opens nothing over no server or no selection.
func TestMenuRefusals(t *testing.T) {
	m := press(newTestModel(120, 40), "m")
	assert.Equal(t, "no server", m.toast)
	assert.Empty(t, m.overlays)

	_, live := polledModel(t)

	live, rung := live.escape()
	require.Equal(t, "deselect cursor", rung)

	m = press(live, "m")
	assert.Equal(t, "nothing selected", m.toast)
	assert.Empty(t, m.overlays)
}

// TestSubmenuPushesAndEscPopsOne checks a submenu opens over its menu, esc goes
// back one level, and m closes every level.
func TestSubmenuPushesAndEscPopsOne(t *testing.T) {
	_, m := polledModel(t)

	m = openQueueMenu(m)

	require.Len(t, m.overlays, 2)

	sub, ok := topAs[menuOverlay](m)
	require.True(t, ok)
	assert.Equal(t, "Actions (1 torrent) / queue", sub.title)
	assert.Contains(t, ansi.Strip(m.render()), "bottom")

	back, rung := m.escape()
	assert.Equal(t, "close overlay", rung)
	require.Len(t, back.overlays, 1)
	assert.True(t, back.hasOverlay(menuOverlayKind))

	assert.Empty(t, press(m, "m").overlays)
}

// TestMenuLeafDispatchesTheKeyHandler checks a leaf closes the menu and runs
// exactly what its key runs.
func TestMenuLeafDispatchesTheKeyHandler(t *testing.T) {
	stub, m := polledModel(t)

	byKey, _ := pressCmd(m, "s")

	byMenu, cmd := pressCmd(press(m, "m", "j"), "enter")

	assert.Empty(t, byMenu.overlays, "a leaf closes every menu level")
	assert.Equal(t, byKey.toast, byMenu.toast)

	runCmd(t, byMenu, cmd)

	assert.Equal(t, 1, stub.calls("torrent-start"))
}

// TestMenuOnlyVerbsHitTheirMethods checks force start and the queue's two ends,
// which have no key, send their own RPCs.
func TestMenuOnlyVerbsHitTheirMethods(t *testing.T) {
	cases := map[string]func(Model) Model{
		"torrent-start-now": func(m Model) Model { return press(m, "m") },
		"queue-move-top":    func(m Model) Model { return press(openQueueMenu(m), "j", "j") },
		"queue-move-bottom": func(m Model) Model { return press(openQueueMenu(m), "j", "j", "j") },
	}

	for method, open := range cases {
		t.Run(method, func(t *testing.T) {
			stub, m := polledModel(t)

			m, cmd := pressCmd(open(m), "enter")
			require.NotNil(t, cmd)
			runCmd(t, m, cmd)

			assert.Equal(t, 1, stub.calls(method))
		})
	}
}

// TestMenuLeafConfirmSitsOverTheList checks a leaf that asks first asks over the
// list, so answering returns there rather than to a menu.
func TestMenuLeafConfirmSitsOverTheList(t *testing.T) {
	_, m := polledModel(t)

	m, cmd := pressCmd(press(m, "space", "down", "space", "m"), "enter")

	assert.Nil(t, cmd)
	require.Len(t, m.overlays, 1)
	assert.True(t, m.hasOverlay(confirmOverlayKind))
	assert.Contains(t, ansi.Strip(m.render()), "Force start 2 torrents?")

	assert.Empty(t, press(m, "n").overlays)
}

// TestMenuGolden pins the menu: the cursor, the submenu markers, and the greyed
// rows.
func TestMenuGolden(t *testing.T) {
	_, m := polledModel(t)

	assertGolden(t, "menu", press(m, "m").render())
}

// TestMenuQueueGolden pins the queue submenu over the menu.
func TestMenuQueueGolden(t *testing.T) {
	_, m := polledModel(t)

	assertGolden(t, "menu_queue", openQueueMenu(m).render())
}
