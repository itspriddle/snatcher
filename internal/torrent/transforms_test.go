package torrent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/torrent"
)

func sampleTorrents() []torrent.Torrent {
	return []torrent.Torrent{
		{ID: 1, Name: "Ubuntu", Status: torrent.StatusDownloading, QueuePosition: 2, AddedDate: 100, TotalSize: 300, PercentDone: 0.5, UploadRatio: 1.0, RateDownload: 10, RateUpload: 5},
		{ID: 2, Name: "Debian", Status: torrent.StatusSeeding, QueuePosition: 0, AddedDate: 300, TotalSize: 100, PercentDone: 1.0, UploadRatio: 2.5, RateDownload: 0, RateUpload: 50},
		{ID: 3, Name: "Arch", Status: torrent.StatusStopped, QueuePosition: 1, AddedDate: 200, TotalSize: 200, PercentDone: 0.0, UploadRatio: 0.0, RateDownload: 0, RateUpload: 0},
		{ID: 4, Name: "Fedora", Status: torrent.StatusQueuedToDownload, QueuePosition: 3, AddedDate: 400, TotalSize: 400, PercentDone: 0.1, UploadRatio: 0.2, RateDownload: 1, RateUpload: 0},
		{ID: 5, Name: "Gentoo", Status: torrent.StatusError, QueuePosition: 4, AddedDate: 500, TotalSize: 500, PercentDone: 0.9, UploadRatio: 3.0, RateDownload: 2, RateUpload: 2},
	}
}

func ids(torrents []torrent.Torrent) []int64 {
	out := make([]int64, len(torrents))
	for i, t := range torrents {
		out[i] = t.ID
	}

	return out
}

func TestParseFilter(t *testing.T) {
	t.Parallel()

	cases := map[string]torrent.Filter{
		"all":         torrent.FilterAll,
		"ACTIVE":      torrent.FilterActive,
		"downloading": torrent.FilterDownloading,
		"error":       torrent.FilterError,
	}

	for in, want := range cases {
		got, err := torrent.ParseFilter(in)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}

	// Surrounding whitespace is trimmed.
	got, err := torrent.ParseFilter("  seeding  ")
	require.NoError(t, err)
	assert.Equal(t, torrent.FilterSeeding, got)
}

func TestParseFilterInvalid(t *testing.T) {
	t.Parallel()

	_, err := torrent.ParseFilter("bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "downloading")
	assert.Contains(t, err.Error(), "active")
}

func TestFilterByStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		filter torrent.Filter
		want   []int64
	}{
		{torrent.FilterAll, []int64{1, 2, 3, 4, 5}},
		{torrent.FilterActive, []int64{1, 2, 4}},
		{torrent.FilterDownloading, []int64{1, 4}},
		{torrent.FilterSeeding, []int64{2}},
		{torrent.FilterStopped, []int64{3}},
		{torrent.FilterError, []int64{5}},
		{torrent.FilterVerifying, []int64{}},
	}

	for _, tc := range cases {
		t.Run(string(tc.filter), func(t *testing.T) {
			t.Parallel()
			got := torrent.FilterByStatus(sampleTorrents(), tc.filter)
			assert.Equal(t, tc.want, ids(got))
		})
	}
}

func TestFilterByStatusDoesNotMutate(t *testing.T) {
	t.Parallel()

	in := sampleTorrents()
	_ = torrent.FilterByStatus(in, torrent.FilterSeeding)
	assert.Equal(t, []int64{1, 2, 3, 4, 5}, ids(in))
}

func TestSearchByName(t *testing.T) {
	t.Parallel()

	got := torrent.SearchByName(sampleTorrents(), "a")
	// "Debian", "Arch", and "Fedora" all contain "a" case-insensitively;
	// "Ubuntu" and "Gentoo" do not.
	assert.Equal(t, []int64{2, 3, 4}, ids(got))
}

func TestSearchByNameEmptyMatchesAll(t *testing.T) {
	t.Parallel()

	got := torrent.SearchByName(sampleTorrents(), "")
	assert.Equal(t, []int64{1, 2, 3, 4, 5}, ids(got))
}

func TestSearchByNameCaseInsensitive(t *testing.T) {
	t.Parallel()

	got := torrent.SearchByName(sampleTorrents(), "UBUNTU")
	assert.Equal(t, []int64{1}, ids(got))
}

func TestParseSortField(t *testing.T) {
	t.Parallel()

	cases := map[string]torrent.SortField{
		"queue":    torrent.SortQueue,
		"NAME":     torrent.SortName,
		"size":     torrent.SortSize,
		"progress": torrent.SortProgress,
		"ratio":    torrent.SortRatio,
		"speed":    torrent.SortSpeed,
	}

	for in, want := range cases {
		got, err := torrent.ParseSortField(in)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}

	// Surrounding whitespace is trimmed.
	got, err := torrent.ParseSortField("  added  ")
	require.NoError(t, err)
	assert.Equal(t, torrent.SortAdded, got)
}

func TestParseSortFieldInvalid(t *testing.T) {
	t.Parallel()

	_, err := torrent.ParseSortField("bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "queue")
	assert.Contains(t, err.Error(), "speed")
}

func TestSortTorrents(t *testing.T) {
	t.Parallel()

	cases := []struct {
		field   torrent.SortField
		reverse bool
		want    []int64
	}{
		{torrent.SortQueue, false, []int64{2, 3, 1, 4, 5}},
		{torrent.SortName, false, []int64{3, 2, 4, 5, 1}}, // Arch, Debian, Fedora, Gentoo, Ubuntu
		{torrent.SortAdded, false, []int64{1, 3, 2, 4, 5}},
		{torrent.SortSize, false, []int64{2, 3, 1, 4, 5}},
		{torrent.SortProgress, false, []int64{3, 4, 1, 5, 2}},
		{torrent.SortRatio, false, []int64{3, 4, 1, 2, 5}},
		{torrent.SortSpeed, false, []int64{3, 4, 5, 1, 2}}, // sums: 0,1,4,15,50
		{torrent.SortSize, true, []int64{5, 4, 1, 3, 2}},
	}

	for _, tc := range cases {
		t.Run(string(tc.field), func(t *testing.T) {
			t.Parallel()
			got := torrent.SortTorrents(sampleTorrents(), tc.field, tc.reverse)
			assert.Equal(t, tc.want, ids(got))
		})
	}
}

func TestSortTorrentsStableTiebreak(t *testing.T) {
	t.Parallel()

	// Two torrents share the same primary key (size) but differ by name/id.
	in := []torrent.Torrent{
		{ID: 9, Name: "Zeta", TotalSize: 100},
		{ID: 3, Name: "Alpha", TotalSize: 100},
		{ID: 7, Name: "Alpha", TotalSize: 100},
	}

	got := torrent.SortTorrents(in, torrent.SortSize, false)
	// Equal size: tiebreak by name then id -> Alpha(3), Alpha(7), Zeta(9).
	assert.Equal(t, []int64{3, 7, 9}, ids(got))
}

func TestSortTorrentsDoesNotMutate(t *testing.T) {
	t.Parallel()

	in := sampleTorrents()
	_ = torrent.SortTorrents(in, torrent.SortName, false)
	assert.Equal(t, []int64{1, 2, 3, 4, 5}, ids(in))
}

func TestTransformsCompose(t *testing.T) {
	t.Parallel()

	in := sampleTorrents()

	// Filter to active, search for "u" (Ubuntu only), then sort by name.
	filtered := torrent.FilterByStatus(in, torrent.FilterActive)
	searched := torrent.SearchByName(filtered, "u")
	sorted := torrent.SortTorrents(searched, torrent.SortName, false)

	assert.Equal(t, []int64{1}, ids(sorted))
}
