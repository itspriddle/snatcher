package transmission

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Filter is a named selection over a torrent's state, used by the list
// command's --filter flag and the TUI sidebar. Most filters are coarser than
// Status (downloading and seeding match a group of related statuses), but
// active and inactive go by current transfer rate instead: a seeding torrent
// with no peers pulling from it is not active, whatever its status says.
type Filter string

const (
	// FilterAll matches every torrent (the default; no filtering).
	FilterAll Filter = "all"

	// FilterActive matches torrents transferring data right now: a non-zero
	// download or upload rate, whatever the status.
	FilterActive Filter = "active"

	// FilterInactive matches torrents that could transfer but are not: both
	// rates zero, and neither stopped nor errored. Idle seeders, stalled
	// downloads, and queued or verifying torrents all land here.
	FilterInactive Filter = "inactive"

	// FilterDownloading matches downloading torrents and those queued to
	// download.
	FilterDownloading Filter = "downloading"

	// FilterSeeding matches seeding torrents and those queued to seed.
	FilterSeeding Filter = "seeding"

	// FilterCompleted matches seeding torrents and any torrent with nothing
	// left to download, stopped or not, as TrguiNG's "Completed" group does.
	// TrguiNG compares sizeWhenDone to haveValid; the list fields carry
	// leftUntilDone instead, which is the same test for finished data. A
	// zero-size torrent (an unresolved magnet) is never complete.
	FilterCompleted Filter = "completed"

	// FilterStopped matches stopped (paused) torrents.
	FilterStopped Filter = "stopped"

	// FilterWaiting matches torrents waiting on the server, as TrguiNG's
	// "Waiting" group does: verifying, queued to verify, or queued to
	// download. Queued-to-seed torrents are complete, so FilterCompleted
	// covers them instead.
	FilterWaiting Filter = "waiting"

	// FilterVerifying matches torrents whose local data is being verified.
	FilterVerifying Filter = "verifying"

	// FilterQueuedToVerify matches torrents queued to verify.
	FilterQueuedToVerify Filter = "queued-to-verify"

	// FilterQueuedToDownload matches torrents queued to download.
	FilterQueuedToDownload Filter = "queued-to-download"

	// FilterQueuedToSeed matches torrents queued to seed.
	FilterQueuedToSeed Filter = "queued-to-seed"

	// FilterError matches torrents the server reported an error for.
	FilterError Filter = "error"
)

// filters lists every valid Filter in display order, so error messages and any
// future completion share one source of truth.
var filters = []Filter{
	FilterAll,
	FilterActive,
	FilterInactive,
	FilterDownloading,
	FilterSeeding,
	FilterCompleted,
	FilterStopped,
	FilterWaiting,
	FilterVerifying,
	FilterQueuedToVerify,
	FilterQueuedToDownload,
	FilterQueuedToSeed,
	FilterError,
}

// ParseFilter parses a --filter value (case-insensitively) into a Filter. An
// unrecognized value returns an error listing the valid options.
func ParseFilter(s string) (Filter, error) {
	f := Filter(strings.ToLower(strings.TrimSpace(s)))
	if slices.Contains(filters, f) {
		return f, nil
	}

	return "", fmt.Errorf("invalid filter %q: valid filters are %s", s, joinFilters())
}

// matches reports whether a torrent satisfies the filter.
func (f Filter) matches(t Torrent) bool {
	switch f {
	case FilterAll:
		return true
	case FilterActive:
		return t.RateDownload > 0 || t.RateUpload > 0
	case FilterInactive:
		return t.RateDownload == 0 && t.RateUpload == 0 &&
			t.Status != StatusStopped && t.Status != StatusError
	case FilterDownloading:
		return t.Status == StatusDownloading || t.Status == StatusQueuedToDownload
	case FilterSeeding:
		return t.Status == StatusSeeding || t.Status == StatusQueuedToSeed
	case FilterCompleted:
		return t.Status == StatusSeeding || (t.SizeWhenDone > 0 && t.LeftUntilDone == 0)
	case FilterStopped:
		return t.Status == StatusStopped
	case FilterWaiting:
		return t.Status == StatusVerifying || t.Status == StatusQueuedToVerify ||
			t.Status == StatusQueuedToDownload
	case FilterVerifying:
		return t.Status == StatusVerifying
	case FilterQueuedToVerify:
		return t.Status == StatusQueuedToVerify
	case FilterQueuedToDownload:
		return t.Status == StatusQueuedToDownload
	case FilterQueuedToSeed:
		return t.Status == StatusQueuedToSeed
	case FilterError:
		return t.Status == StatusError
	default:
		return false
	}
}

func joinFilters() string {
	names := make([]string, len(filters))
	for i, f := range filters {
		names[i] = string(f)
	}

	return strings.Join(names, ", ")
}

// FilterByStatus returns a new slice containing the torrents that satisfy the
// filter, preserving their input order. Despite the name, active and inactive
// read the transfer rates rather than the status. The input is never mutated,
// and the result is non-nil even when empty.
func FilterByStatus(torrents []Torrent, f Filter) []Torrent {
	out := make([]Torrent, 0, len(torrents))
	for _, t := range torrents {
		if f.matches(t) {
			out = append(out, t)
		}
	}

	return out
}

// SearchByName returns a new slice containing the torrents whose name contains
// query as a case-insensitive substring, preserving input order. An empty query
// matches everything. The input is never mutated.
func SearchByName(torrents []Torrent, query string) []Torrent {
	if query == "" {
		return slices.Clone(torrents)
	}

	needle := strings.ToLower(query)

	out := make([]Torrent, 0, len(torrents))
	for _, t := range torrents {
		if strings.Contains(strings.ToLower(t.Name), needle) {
			out = append(out, t)
		}
	}

	return out
}

// MainTracker returns the host of the tracker a torrent is grouped under: the
// first tracker in its lowest tier, or "" when it has none. Transmission
// announces to one tracker per tier and keeps the rest as fallbacks, so
// grouping by every listed host would count a torrent under each fallback too,
// and the group counts would no longer add up to the torrents.
func MainTracker(t Torrent) string {
	if len(t.Trackers) == 0 {
		return ""
	}

	main := t.Trackers[0]
	for _, tr := range t.Trackers[1:] {
		if tr.Tier < main.Tier {
			main = tr
		}
	}

	return main.Host
}

// FilterByTracker returns a new slice containing the torrents whose
// MainTracker is host, compared case-insensitively, preserving input order. A
// torrent that merely lists host as a fallback does not match. An empty host
// matches everything. The input is never mutated, and the result is non-nil
// even when empty.
func FilterByTracker(torrents []Torrent, host string) []Torrent {
	if host == "" {
		return slices.Clone(torrents)
	}

	out := make([]Torrent, 0, len(torrents))
	for _, t := range torrents {
		if main := MainTracker(t); main != "" && strings.EqualFold(main, host) {
			out = append(out, t)
		}
	}

	return out
}

// Top returns a new slice holding at most the first n torrents, preserving
// input order. It is meant to be the last transform applied — after
// FilterByStatus, FilterByTracker, SearchByName, and SortTorrents — so "the first n" means the
// first n rows of the rendered order rather than of the server's own.
//
// A non-positive n means unlimited, so an unset --top is indistinguishable from
// no truncation at all, and an n at or beyond the length returns everything
// rather than erroring: asking for more rows than exist is a legitimate
// question with a shorter answer. The input is never mutated and the result is
// non-nil even when empty, so callers can rely on a stable slice.
func Top(torrents []Torrent, n int) []Torrent {
	if n <= 0 || n > len(torrents) {
		n = len(torrents)
	}

	out := make([]Torrent, n)
	copy(out, torrents)

	return out
}

// SortField names a torrent attribute the list can be ordered by, used by the
// --sort flag.
type SortField string

const (
	// SortQueue orders by the server queue position (the default).
	SortQueue SortField = "queue"

	// SortName orders by name (case-insensitive).
	SortName SortField = "name"

	// SortAdded orders by the date the torrent was added.
	SortAdded SortField = "added"

	// SortSize orders by total size.
	SortSize SortField = "size"

	// SortProgress orders by download completion (PercentDone).
	SortProgress SortField = "progress"

	// SortRatio orders by upload ratio.
	SortRatio SortField = "ratio"

	// SortSpeed orders by combined transfer speed (download + upload rate).
	SortSpeed SortField = "speed"
)

// sortFields lists every valid SortField in display order, so error messages
// and any future completion share one source of truth.
var sortFields = []SortField{
	SortQueue,
	SortName,
	SortAdded,
	SortSize,
	SortProgress,
	SortRatio,
	SortSpeed,
}

// ParseSortField parses a --sort value (case-insensitively) into a SortField.
// An unrecognized value returns an error listing the valid options.
func ParseSortField(s string) (SortField, error) {
	f := SortField(strings.ToLower(strings.TrimSpace(s)))
	if slices.Contains(sortFields, f) {
		return f, nil
	}

	return "", fmt.Errorf("invalid sort field %q: valid sort fields are %s", s, joinSortFields())
}

func joinSortFields() string {
	names := make([]string, len(sortFields))
	for i, f := range sortFields {
		names[i] = string(f)
	}

	return strings.Join(names, ", ")
}

// SortTorrents returns a new slice sorted by the given field. The sort is
// stable, with a deterministic name-then-id tiebreak, so equal keys keep a
// predictable order across runs. When reverse is true the primary key is
// inverted while the tiebreak stays ascending, so output remains deterministic.
// The input is never mutated.
func SortTorrents(torrents []Torrent, field SortField, reverse bool) []Torrent {
	out := slices.Clone(torrents)

	slices.SortStableFunc(out, func(a, b Torrent) int {
		c := compareField(a, b, field)
		if reverse {
			c = -c
		}
		if c != 0 {
			return c
		}

		return tiebreak(a, b)
	})

	return out
}

// compareField compares two torrents by the primary sort key (ascending).
func compareField(a, b Torrent, field SortField) int {
	switch field {
	case SortName:
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	case SortAdded:
		return cmp.Compare(a.AddedDate, b.AddedDate)
	case SortSize:
		return cmp.Compare(a.TotalSize, b.TotalSize)
	case SortProgress:
		return cmp.Compare(a.PercentDone, b.PercentDone)
	case SortRatio:
		return cmp.Compare(a.UploadRatio, b.UploadRatio)
	case SortSpeed:
		return cmp.Compare(a.RateDownload+a.RateUpload, b.RateDownload+b.RateUpload)
	case SortQueue:
		return cmp.Compare(a.QueuePosition, b.QueuePosition)
	default:
		return cmp.Compare(a.QueuePosition, b.QueuePosition)
	}
}

// tiebreak gives a deterministic order to torrents whose primary key is equal:
// by name (case-insensitive), then by numeric id. It is always ascending so the
// overall order is stable regardless of reverse.
func tiebreak(a, b Torrent) int {
	if c := cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
		return c
	}

	return cmp.Compare(a.ID, b.ID)
}
