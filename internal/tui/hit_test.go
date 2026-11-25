package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/table"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHitTestFindsEveryPane checks the corners of every open pane, border
// included, resolve to that pane, and the chrome to nothing, at the sizes
// layout_test.go pins and with the side panes open and closed.
func TestHitTestFindsEveryPane(t *testing.T) {
	sizes := [][2]int{{120, 40}, {80, 24}, {60, 16}}

	for _, size := range sizes {
		for _, filterOpen := range []bool{true, false} {
			for _, detailOpen := range []bool{true, false} {
				m := newTestModel(size[0], size[1])
				m.torrents.filterOpen, m.torrents.detailOpen = filterOpen, detailOpen
				m = m.resize(size[0], size[1])
				l := m.layout()

				panes := map[Pane]rect{paneList: l.list, paneFilter: l.filter, paneDetail: l.detail}
				for pane, r := range panes {
					if r.w == 0 {
						continue
					}

					for _, p := range [][2]int{{r.x, r.y}, {r.x + r.w - 1, r.y + r.h - 1}} {
						got := m.hitTest(p[0], p[1])
						assert.Equal(t, pane, got.pane, "%v at %v, filter %v detail %v", pane, p, filterOpen, detailOpen)
						assert.NotEqual(t, hitNone, got.kind)
					}
				}

				assert.Equal(t, hit{}, m.hitTest(0, l.helpLine.y), "the help line")
				assert.Equal(t, hit{}, m.hitTest(size[0]-1, l.statusBar.y), "the status bar")
			}
		}
	}
}

// TestHitTestResolvesTorrentTargets checks the list's rows and headings, the
// sidebar's rows, and the footer resolve to what is drawn there.
func TestHitTestResolvesTorrentTargets(t *testing.T) {
	m := newTestModel(120, 40)

	x, y := locate(t, m, "debian-12")
	assert.Equal(t, hit{kind: hitListRow, pane: paneList, index: 1}, m.hitTest(x, y))

	x, y = locate(t, m, "Ratio")
	h := m.hitTest(x, y)
	assert.Equal(t, hitListHeader, h.kind)
	assert.Equal(t, "ratio", m.torrents.list.cols[h.index].key)

	l := m.layout()

	x, y = locateIn(t, m, l.filter, "Stopped")
	h = m.hitTest(x, y)
	assert.Equal(t, hitFilterItem, h.kind)
	assert.Equal(t, "Stopped", m.torrents.filter.items[h.index].label)

	x, y = locate(t, m, "Labels")
	assert.Equal(t, hit{kind: hitPane, pane: paneFilter}, m.hitTest(x, y), "a heading is not a filter")

	x, y = locateIn(t, m, l.list, "Downloading")
	assert.Equal(t, hitListRow, m.hitTest(x, y).kind, "the same word in the list is a row")

	m = m.applyFilterItem(m.torrents.filter.items[2])
	x, y = locate(t, m, "filter: ")
	assert.Equal(t, hit{kind: hitFooter, pane: paneList}, m.hitTest(x, y))

	_, empty := locate(t, newTestModel(120, 40), "freebsd")
	assert.Equal(t, hit{kind: hitPane, pane: paneList}, m.hitTest(60, empty+3), "below the rows")
}

// TestHitTestResolvesDetailTargets checks the detail pane's tabs and the files
// table's rows, and that a tab with no table is the pane.
func TestHitTestResolvesDetailTargets(t *testing.T) {
	m := staticFiles()
	l := m.layout()

	x, y := locateIn(t, m, l.detail, "Peers")
	assert.Equal(t, hit{kind: hitDetailTab, pane: paneDetail, index: tabPeers}, m.hitTest(x, y))

	x, y = locateIn(t, m, l.detail, "SHA256SUMS")
	assert.Equal(t, hit{kind: hitDetailRow, pane: paneDetail, index: 1}, m.hitTest(x, y))

	x, y = locateIn(t, m, l.detail, "Priority")
	assert.Equal(t, hit{kind: hitPane, pane: paneDetail}, m.hitTest(x, y), "a heading is not a row")

	m = press(m, "1")
	assert.Equal(t, hit{kind: hitPane, pane: paneDetail}, m.hitTest(x, y+1), "the overview has no rows")
}

// TestTabAtMatchesTheRenderedStrip pins tabAt to renderTabs: every tab's
// title, first cell to last, resolves to that tab.
func TestTabAtMatchesTheRenderedStrip(t *testing.T) {
	st := newTestModel(120, 40).styles

	for active := range detailTabs {
		strip := ansi.Strip(renderTabs(st, detailTabs, active, 120))
		from := 0

		for i, name := range detailTabs {
			at := from + strings.Index(strip[from:], name)
			from = at + len(name)

			for _, x := range []int{at, at + len(name) - 1} {
				got, ok := tabAt(st, detailTabs, active, 0, x)
				assert.True(t, ok)
				assert.Equal(t, i, got, "%q at %d with tab %d active", name, x, active)
			}
		}

		_, ok := tabAt(st, detailTabs, active, 0, 119)
		assert.False(t, ok, "past the last tab")
	}
}

// TestColumnAtMatchesTheRenderedHeader pins columnAt to bubbles' header, with a
// column dropped for width.
func TestColumnAtMatchesTheRenderedHeader(t *testing.T) {
	m := newTestModel(80, 24)
	list := m.torrents.list
	cols := list.table.Columns()
	header := ansi.Strip(strings.Split(list.table.View(), "\n")[0])
	frame := m.styles.Table.Header.GetHorizontalFrameSize()

	require.True(t, slices.ContainsFunc(cols, func(c table.Column) bool { return c.Width == 0 }),
		"80 columns drops a column")

	from := 0

	for i, c := range cols {
		title := strings.TrimSpace(c.Title)
		if c.Width == 0 || title == "" {
			continue
		}

		at := from + strings.Index(header[from:], title)
		from = at + len(title)

		got, ok := columnAt(cols, frame, 0, at)
		assert.True(t, ok)
		assert.Equal(t, i, got, "%q at %d", title, at)
	}

	_, ok := columnAt(cols, frame, 0, ansi.StringWidth(header))
	assert.False(t, ok, "past the last column")
}

// TestSettingsButtonsMatchTheRenderedLine pins buttonAt to buttonsLine.
func TestSettingsButtonsMatchTheRenderedLine(t *testing.T) {
	m := settingsModel(120, 40)
	w := m.settings
	line := ansi.Strip(w.buttonsLine(m.styles))

	for want, label := range map[int]string{buttonIndexSave: "Save", buttonIndexRevert: "Revert"} {
		at := strings.Index(line, label)

		got, ok := w.buttonAt(m.styles, at)
		assert.True(t, ok)
		assert.Equal(t, want, got, label)
	}

	_, ok := w.buttonAt(m.styles, strings.Index(line, "  ["))
	assert.False(t, ok, "the gap between the buttons")
}

// TestHitTestResolvesSettingsTargets checks the settings window's tabs, rows,
// open options, and buttons, scrolled and not.
func TestHitTestResolvesSettingsTargets(t *testing.T) {
	m := settingsModel(120, 40)

	x, y := locate(t, m, "Network")
	assert.Equal(t, hit{kind: hitSettingsTab, pane: paneSettings, index: tabNetwork}, m.hitTest(x, y))

	x, y = locate(t, m, clientKeyColorDepth)
	assert.Equal(t, hit{kind: hitSettingsRow, pane: paneSettings, index: 2}, m.hitTest(x, y))

	x, y = locate(t, m, "Revert")
	assert.Equal(t, hit{kind: hitSettingsButton, pane: paneSettings, index: buttonIndexRevert}, m.hitTest(x, y))

	x, y = locate(t, m, "Color theme")
	assert.Equal(t, hit{kind: hitPane, pane: paneSettings}, m.hitTest(x, y), "the hint line")

	m.settings = m.settings.openDropdown()
	x, y = locate(t, m, "nord")
	h := m.hitTest(x, y)
	assert.Equal(t, hitSettingsOption, h.kind)
	assert.Equal(t, "nord", m.settings.current().options[h.index])

	x, y = locate(t, m, clientKeyNerdFont)
	assert.Equal(t, hit{kind: hitSettingsRow, pane: paneSettings, index: 1}, m.hitTest(x, y), "rows below the options")

	// A short terminal scrolls the region; the hit follows the drawing.
	m = networkTab(settingsModel(60, 16))
	for range 8 {
		m = press(m, "tab")
	}

	key := m.settings.current().key
	x, y = locate(t, m, key)
	h = m.hitTest(x, y)
	require.Equal(t, hitSettingsRow, h.kind)
	assert.Equal(t, key, m.settings.tabs[m.settings.tab].fields[h.index].key)
}

// TestHitTestResolvesOverlayRows checks rows of the menu, the palette, and the
// sort modal, and the box and the outside around them.
func TestHitTestResolvesOverlayRows(t *testing.T) {
	cases := []struct {
		name  string
		open  func(Model) Model
		label string
		row   int
	}{
		{name: "menu", open: func(m Model) Model { return m.push(torrentMenu(1)) }, label: "reannounce", row: 4},
		{name: "palette", open: func(m Model) Model { return press(m, ":") }, label: "pause", row: 3},
		{name: "sort", open: func(m Model) Model { return press(m, "o") }, label: "ratio", row: 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.open(newTestModel(120, 40))
			require.NotEmpty(t, m.overlays)

			r := m.placeBox(m.overlays[0].view(m, m.width, m.height))

			x, y := locateIn(t, m, r, tc.label)
			h := m.hitTest(x, y)
			require.Equal(t, hitOverlayRow, h.kind)

			if tc.name == "palette" {
				o := topPalette(t, m)
				assert.Contains(t, o.entries[o.matches[h.index]].label, tc.label)
			} else {
				assert.Equal(t, tc.row, h.index)
			}

			assert.Equal(t, hitOverlayBox, m.hitTest(r.x+1, r.y+1).kind, "the title")
			assert.Equal(t, hitOverlayOutside, m.hitTest(0, 0).kind)
			assert.Equal(t, hitOverlayOutside, m.hitTest(r.x+r.w, r.y).kind)
		})
	}
}

// TestHitTestFollowsAScrolledModal checks a modal row is found after the modal
// has scrolled to keep its cursor in view.
func TestHitTestFollowsAScrolledModal(t *testing.T) {
	m := press(newTestModel(60, 16), "c", "G")

	for range 14 {
		m = press(m, "j")
	}

	o, ok := topAs[columnsOverlay](m)
	require.True(t, ok)

	last := o.rows[len(o.rows)-1].key
	r := m.placeBox(o.view(m, m.width, m.height))

	x, y := locateIn(t, m, r, last+" ")
	assert.Equal(t, hit{kind: hitOverlayRow, index: len(o.rows) - 1}, m.hitTest(x, y))
}

// TestEveryModalBoxOverlayIsClickable is the drift test: every overlay whose
// view draws with modalBox implements modalOverlay, so a new modal cannot ship
// with rows a click silently misses.
func TestEveryModalBoxOverlayIsClickable(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)

	fset := token.NewFileSet()
	drawsModal, hasModal := map[string]bool{}, map[string]bool{}

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}

			recv := receiverName(fn.Recv.List[0].Type)

			switch fn.Name.Name {
			case "modal":
				hasModal[recv] = true
			case "view":
				if callsModalBox(fn) {
					drawsModal[recv] = true
				}
			}
		}
	}

	require.NotEmpty(t, drawsModal)

	for recv := range drawsModal {
		assert.True(t, hasModal[recv], "%s draws with modalBox but has no modal method", recv)
	}

	// The modal method alone is not the interface; the compile-time list below
	// is, and it must name every type found above.
	for recv := range drawsModal {
		assert.Contains(t, clickableOverlays, recv)
	}
}

// clickableOverlays names every modalOverlay; the assignments below fail to
// compile if one stops implementing it.
var clickableOverlays = []string{
	"menuOverlay", "paletteOverlay", "sortOverlay", "columnsOverlay",
	"filterPickOverlay", "formOverlay", "pickerOverlay",
}

var (
	_ modalOverlay = menuOverlay{}
	_ modalOverlay = paletteOverlay{}
	_ modalOverlay = sortOverlay{}
	_ modalOverlay = columnsOverlay{}
	_ modalOverlay = filterPickOverlay{}
	_ modalOverlay = formOverlay{}
	_ modalOverlay = pickerOverlay{}
)

// receiverName is a method receiver's type name.
func receiverName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}

	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}

	return ""
}

// callsModalBox reports whether a function's body calls modalBox.
func callsModalBox(fn *ast.FuncDecl) bool {
	found := false

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "modalBox" {
			found = true
		}

		return !found
	})

	return found
}
