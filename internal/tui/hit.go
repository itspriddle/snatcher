package tui

import (
	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"
)

// hitKind names what a point on the screen lands on.
type hitKind int

const (
	// hitNone is nothing a click acts on: the chrome, or a gap.
	hitNone hitKind = iota

	// hitPane is somewhere in a pane that is not a more specific target,
	// border included.
	hitPane

	// hitListRow and hitListHeader are a torrent row and a torrent column
	// heading; index is the row or the column.
	hitListRow
	hitListHeader

	// hitFooter is the torrent list's footer line: the filter and the search.
	hitFooter

	// hitFilterItem is a selectable sidebar row; index indexes its items.
	hitFilterItem

	// hitDetailTab and hitDetailRow are a detail tab and a row of the current
	// tab's table.
	hitDetailTab
	hitDetailRow

	// hitResultRow and hitResultHeader are a release row and a results column
	// heading.
	hitResultRow
	hitResultHeader

	// hitSearch is the Prowlarr search pane.
	hitSearch

	// hitSettingsTab, hitSettingsRow, hitSettingsOption, and hitSettingsButton
	// are a settings tab, a field row, an open dropdown's option, and the Save
	// (index 0) or Revert (index 1) button.
	hitSettingsTab
	hitSettingsRow
	hitSettingsOption
	hitSettingsButton

	// hitOverlayRow is a row of the topmost modal, hitOverlayBox anywhere else
	// inside it, and hitOverlayOutside anywhere outside it.
	hitOverlayRow
	hitOverlayBox
	hitOverlayOutside
)

// hit is what hitTest resolved a point to: the kind of target, the pane it is
// in, and the row, column, tab, or option it names.
type hit struct {
	kind  hitKind
	pane  Pane
	index int
}

// in reports whether a point lies inside r.
func (r rect) in(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

// hitTest resolves a screen point to what is drawn there. It is a pure function
// of the model, like the key router: the pane rectangles come from layout, the
// rows under a scrolled table from its row markers, and everything else from
// the same arithmetic the views draw with.
//
// An open overlay owns the whole screen: nothing beneath it is reachable.
func (m Model) hitTest(x, y int) hit {
	if len(m.overlays) > 0 {
		return m.hitOverlay(x, y)
	}

	l := m.layout()

	switch m.window {
	case windowSettings:
		return m.hitSettings(l, x, y)
	case windowProwlarr:
		return m.hitProwlarr(l, x, y)
	case windowTorrents:
	}

	return m.hitTorrents(l, x, y)
}

// hitTorrents resolves a point in the torrents window.
func (m Model) hitTorrents(l layout, x, y int) hit {
	w := m.torrents

	switch {
	case w.filterOpen && l.filter.in(x, y):
		return w.filter.hit(l.filter, y)
	case l.list.in(x, y):
		inner := l.list.inner()
		line := y - inner.y

		// The footer, while there is one, is the pane's last row.
		if w.footer() != "" && line == inner.h-1 {
			return hit{kind: hitFooter, pane: paneList}
		}

		return tableHit(w.list.table, m.styles, inner, x, line, paneList, hitListHeader, hitListRow)
	case w.detailOpen && l.detail.in(x, y):
		return w.detail.hit(m.styles, l.detail, x, y)
	}

	return hit{}
}

// hitProwlarr resolves a point in the Prowlarr window.
func (m Model) hitProwlarr(l layout, x, y int) hit {
	w := m.prowlarr

	switch {
	case l.search.in(x, y):
		return hit{kind: hitSearch, pane: paneSearch}
	case l.list.in(x, y):
		inner := l.list.inner()

		return tableHit(w.table, m.styles, inner, x, y-inner.y, paneResults, hitResultHeader, hitResultRow)
	case w.detailOpen && l.detail.in(x, y):
		return hit{kind: hitPane, pane: paneResultDetail}
	}

	return hit{}
}

// tableHit resolves a line of a table drawn at the top of inner: line 0 is its
// header, and every line after it is whichever row its marker names. A point on
// the border, past the rows, or on a placeholder is the pane.
func tableHit(t table.Model, st Styles, inner rect, x, line int, pane Pane, header, row hitKind) hit {
	body := hit{kind: hitPane, pane: pane}

	if x < inner.x || x >= inner.x+inner.w || line < 0 || line >= inner.h {
		return body
	}

	if line == 0 {
		col, ok := columnAt(t.Columns(), st.Table.Header.GetHorizontalFrameSize(), inner.x, x)
		if !ok {
			return body
		}

		return hit{kind: header, pane: pane, index: col}
	}

	i, ok := rowAt(t.View(), line)
	if !ok {
		return body
	}

	return hit{kind: row, pane: pane, index: i}
}

// columnAt is the column a table header draws at x, when the header starts at
// x0. It walks the columns as bubbles' headersView lays them out: a column of
// width zero is not drawn, and every drawn one is its width plus the header
// style's frame.
func columnAt(cols []table.Column, frame, x0, x int) (int, bool) {
	for i, c := range cols {
		if c.Width <= 0 {
			continue
		}

		end := x0 + c.Width + frame
		if x >= x0 && x < end {
			return i, true
		}

		x0 = end
	}

	return 0, false
}

// tabAt is the tab a strip drawn by renderTabs puts at x, when the strip starts
// at x0. Each tab is its name plus its style's frame.
func tabAt(st Styles, tabs []string, active, x0, x int) (int, bool) {
	for i, name := range tabs {
		style := st.Tab
		if i == active {
			style = st.TabActive
		}

		end := x0 + lipgloss.Width(name) + style.GetHorizontalFrameSize()
		if x >= x0 && x < end {
			return i, true
		}

		x0 = end
	}

	return 0, false
}

// hit resolves a point in the sidebar drawn into r. Its rows are drawn one per
// line from the top of the border, and never scroll.
func (p filterPane) hit(r rect, y int) hit {
	i := y - r.inner().y
	if i >= 0 && i < r.inner().h && i < len(p.items) && p.items[i].selectable() {
		return hit{kind: hitFilterItem, pane: paneFilter, index: i}
	}

	return hit{kind: hitPane, pane: paneFilter}
}

// hit resolves a point in the detail pane drawn into r: the tab strip on its
// first line, the rule under it, and the current tab's table, when the tab
// draws one, below that.
func (p detailPane) hit(st Styles, r rect, x, y int) hit {
	inner := r.inner()
	line := y - inner.y
	body := hit{kind: hitPane, pane: paneDetail}

	if line == 0 {
		if tab, ok := tabAt(st, detailTabs, p.tab, inner.x, x); ok {
			return hit{kind: hitDetailTab, pane: paneDetail, index: tab}
		}

		return body
	}

	t, ok := p.currentTable()
	if !ok || len(p.body(Styles{})) > 0 {
		return body
	}

	below := rect{x: inner.x, y: inner.y + 2, w: inner.w, h: inner.h - 2}

	h := tableHit(t.table, st, below, x, line-2, paneDetail, hitPane, hitDetailRow)
	if h.kind == hitPane {
		return body
	}

	return h
}

// hitSettings resolves a point in the settings window, which fills the body:
// the tab strip, a rule, the scrolled field rows, a blank line, the hint, and
// the buttons, in the order view draws them.
func (m Model) hitSettings(l layout, x, y int) hit {
	w := m.settings
	inner := l.body.inner()
	line := y - inner.y
	height := settingsRegionHeight(l)
	body := hit{kind: hitPane, pane: paneSettings}

	if !l.body.in(x, y) {
		return hit{}
	}

	if x < inner.x || x >= inner.x+inner.w {
		return body
	}

	switch {
	case line == 0:
		if tab, ok := tabAt(m.styles, w.tabNames(), w.tab, inner.x, x); ok {
			return hit{kind: hitSettingsTab, pane: paneSettings, index: tab}
		}
	case line == 2+height+2:
		if button, ok := w.buttonAt(m.styles, x-inner.x); ok {
			return hit{kind: hitSettingsButton, pane: paneSettings, index: button}
		}
	case line >= 2 && line < 2+height && w.visible(w.tab):
		if h, ok := w.rowAt(line - 2 + w.scrolledOffset(height)); ok {
			return h
		}
	}

	return body
}

// rowAt is the field row or dropdown option drawn at a line of the scrolled
// region, counted from its unscrolled top. It walks the rows as regionLines
// draws them: one line a field, with an open dropdown's options under its row.
func (w settingsWindow) rowAt(drawn int) (hit, bool) {
	t := w.tabs[w.tab]
	line := 0

	for i := range len(t.fields) - len(buttonFields()) {
		if line == drawn {
			return hit{kind: hitSettingsRow, pane: paneSettings, index: i}, true
		}

		line++

		if i != t.cursor || w.mode != modeSelect {
			continue
		}

		if opt := drawn - line; opt >= 0 && opt < len(t.fields[i].options) {
			return hit{kind: hitSettingsOption, pane: paneSettings, index: opt}, true
		}

		line += len(t.fields[i].options)
	}

	return hit{}, false
}

// The settings buttons, as hitSettingsButton indexes them.
const (
	buttonIndexSave = iota
	buttonIndexRevert
)

// buttonAt is the button buttonsLine draws at x cells into its line. The two
// pieces are measured as rendered, so a label's width change moves the hit
// with it.
func (w settingsWindow) buttonAt(st Styles, x int) (int, bool) {
	save, revert := w.buttonPieces(st)
	saveW := lipgloss.Width(save)
	revertAt := saveW + lipgloss.Width(buttonGap)

	switch {
	case x >= 0 && x < saveW:
		return buttonIndexSave, true
	case x >= revertAt && x < revertAt+lipgloss.Width(revert):
		return buttonIndexRevert, true
	}

	return 0, false
}

// hitOverlay resolves a point while an overlay is open. Only the topmost
// overlay is reachable; a point outside it is outside.
func (m Model) hitOverlay(x, y int) hit {
	top := len(m.overlays) - 1
	o := m.overlays[top]
	box := o.view(m, m.width, m.height)
	r := m.placeBox(box)

	if !r.in(x, y) {
		return hit{kind: hitOverlayOutside}
	}

	mo, ok := o.(modalOverlay)
	if !ok {
		return hit{kind: hitOverlayBox}
	}

	b := mo.modal(m, m.width, m.height)
	start, end := modalWindow(b, m.height)
	contentY := r.y + m.styles.Overlay.GetBorderTopSize() + m.styles.Overlay.GetPaddingTop()
	row := y - contentY - modalRowsTop(b)

	if row < 0 || row >= end-start {
		return hit{kind: hitOverlayBox}
	}

	return hit{kind: hitOverlayRow, index: start + row}
}
