package tui

import (
	"slices"
	"strings"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// filterKind is what sort of filter a viewFilter applies.
type filterKind int

const (
	// filterByNone shows every torrent.
	filterByNone filterKind = iota

	// filterByStatus applies a transmission.Filter.
	filterByStatus

	// filterByLabel keeps torrents carrying one label, or carrying none.
	filterByLabel
)

// viewFilter is the one filter in force on the list. There is only ever one: the
// sidebar and the f modal both apply a single row, and the CLI's --filter is a
// single value too.
type viewFilter struct {
	kind filterKind

	// status is the filter a filterByStatus applies.
	status transmission.Filter

	// label is the raw, unsanitized label a filterByLabel matches, or empty for
	// "no label". It is raw because it is compared against the torrent's own
	// labels; only its display is sanitized.
	label string
}

// matches reports whether a torrent passes the filter.
func (f viewFilter) matches(t transmission.Torrent) bool {
	switch f.kind {
	case filterByStatus:
		return len(transmission.FilterByStatus([]transmission.Torrent{t}, f.status)) == 1
	case filterByLabel:
		if f.label == "" {
			return len(t.Labels) == 0
		}

		return slices.Contains(t.Labels, f.label)
	case filterByNone:
	}

	return true
}

// listView is how the polled torrents become the rows the table shows: the
// filter in force, the sort, the columns, and the `/` search text.
type listView struct {
	filter  viewFilter
	sort    transmission.SortField
	reverse bool

	// columns are CLI field keys, in display order.
	columns []string

	// search is the `/` query. It jumps the cursor; it never hides a row.
	search string
}

// defaultListView is the view `snatcher list` renders with no client settings
// stored: queue order, ascending, the CLI's default columns.
func defaultListView() listView {
	return listView{sort: transmission.SortQueue, columns: slices.Clone(defaultColumnKeys)}
}

// apply filters and then sorts, the order `snatcher list` applies them in. The
// search does not filter, so it is not applied here.
func (v listView) apply(all []transmission.Torrent) []transmission.Torrent {
	rows := make([]transmission.Torrent, 0, len(all))

	for _, t := range all {
		if v.filter.matches(t) {
			rows = append(rows, t)
		}
	}

	return transmission.SortTorrents(rows, v.sort, v.reverse)
}

// The client keys the list preferences are stored under, spelled as the CLI
// spells them.
const (
	clientKeyListColumns = "client.list_columns"
	clientKeyListSort    = "client.list_sort"
	clientKeyListReverse = "client.list_reverse"
)

// listPrefs reads the stored list preferences. No store means the defaults. A
// store that cannot be read also means the defaults, with the error returned so
// the caller can say why the user's preferences did not apply.
//
// A value of the wrong type, an unknown sort field, or an unknown column is
// ignored rather than failing: the store validates what it writes, so these can
// only come from a hand-edited file, and the TUI must still draw.
func listPrefs(cfg ClientConfig) (listView, error) {
	v := defaultListView()
	if cfg == nil {
		return v, nil
	}

	entries, err := cfg.Entries()
	if err != nil {
		return v, err
	}

	for _, e := range entries {
		switch e.Key {
		case clientKeyListSort:
			if raw, ok := e.Value.(string); ok {
				if field, err := transmission.ParseSortField(raw); err == nil {
					v.sort = field
				}
			}
		case clientKeyListReverse:
			if on, ok := e.Value.(bool); ok {
				v.reverse = on
			}
		case clientKeyListColumns:
			if keys := knownColumns(e.Value); len(keys) > 0 {
				v.columns = keys
			}
		}
	}

	return v, nil
}

// persistPref writes one list preference through the config store. With no store
// the change is for this session only. A write that fails is toasted, and the
// session value still applies: the modal that asked has already closed, so
// nothing is left half-applied.
//
// It is synchronous, like the Entries read in openSettings: the store is a
// local file.
func (m Model) persistPref(key string, value any) Model {
	if m.deps.Config == nil {
		return m
	}

	if err := m.deps.Config.Set(key, value); err != nil {
		m.toast = "config: " + errorLine(err)
	}

	return m
}

// listHandlers are the list-view modals: o sort and c columns.
func listHandlers() map[Action]handler {
	return map[Action]handler{
		actionTorrentSort: plain(Model.toggleSort),
		actionSortUp:      stepTop(func(o sortOverlay) sortOverlay { return o.move(-1) }),
		actionSortDown:    stepTop(func(o sortOverlay) sortOverlay { return o.move(1) }),
		actionSortReverse: stepTop(sortOverlay.flipReverse),
		actionSortSelect:  plain(Model.applySort),

		actionTorrentColumns:  plain(Model.toggleColumns),
		actionColumnsUp:       stepTop(func(o columnsOverlay) columnsOverlay { return o.move(-1) }),
		actionColumnsDown:     stepTop(func(o columnsOverlay) columnsOverlay { return o.move(1) }),
		actionColumnsToggle:   stepTop(columnsOverlay.toggle),
		actionColumnsMoveUp:   stepTop(func(o columnsOverlay) columnsOverlay { return o.swap(-1) }),
		actionColumnsMoveDown: stepTop(func(o columnsOverlay) columnsOverlay { return o.swap(1) }),
		actionColumnsApply:    plain(Model.applyColumns),
	}
}

// knownColumns normalizes a stored column list — lowercased and trimmed, as the
// CLI matches --fields — keeping only keys the TUI can draw.
func knownColumns(value any) []string {
	raw, ok := value.([]string)
	if !ok {
		return nil
	}

	keys := make([]string, 0, len(raw))

	for _, k := range raw {
		k = strings.ToLower(strings.TrimSpace(k))
		if _, ok := columnDef(k); ok && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}

	return keys
}
