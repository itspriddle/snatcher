package transmission_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

func sampleTorrents() []transmission.Torrent {
	return []transmission.Torrent{
		{ID: 1, Name: "Ubuntu", Status: transmission.StatusDownloading, QueuePosition: 2, AddedDate: 100, TotalSize: 300, PercentDone: 0.5, UploadRatio: 1.0, RateDownload: 10, RateUpload: 5},
		{ID: 2, Name: "Debian", Status: transmission.StatusSeeding, QueuePosition: 0, AddedDate: 300, TotalSize: 100, PercentDone: 1.0, UploadRatio: 2.5, RateDownload: 0, RateUpload: 50},
		{ID: 3, Name: "Arch", Status: transmission.StatusStopped, QueuePosition: 1, AddedDate: 200, TotalSize: 200, PercentDone: 0.0, UploadRatio: 0.0, RateDownload: 0, RateUpload: 0},
		{ID: 4, Name: "Fedora", Status: transmission.StatusQueuedToDownload, QueuePosition: 3, AddedDate: 400, TotalSize: 400, PercentDone: 0.1, UploadRatio: 0.2, RateDownload: 1, RateUpload: 0},
		{ID: 5, Name: "Gentoo", Status: transmission.StatusError, QueuePosition: 4, AddedDate: 500, TotalSize: 500, PercentDone: 0.9, UploadRatio: 3.0, RateDownload: 2, RateUpload: 2},
	}
}

func ids(torrents []transmission.Torrent) []int64 {
	out := make([]int64, len(torrents))
	for i, t := range torrents {
		out[i] = t.ID
	}

	return out
}

func TestParseFilter(t *testing.T) {
	t.Parallel()

	cases := map[string]transmission.Filter{
		"all":         transmission.FilterAll,
		"ACTIVE":      transmission.FilterActive,
		"downloading": transmission.FilterDownloading,
		"error":       transmission.FilterError,
	}

	for in, want := range cases {
		got, err := transmission.ParseFilter(in)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}

	// Surrounding whitespace is trimmed.
	got, err := transmission.ParseFilter("  seeding  ")
	require.NoError(t, err)
	assert.Equal(t, transmission.FilterSeeding, got)
}

func TestParseFilterInvalid(t *testing.T) {
	t.Parallel()

	_, err := transmission.ParseFilter("bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "downloading")
	assert.Contains(t, err.Error(), "active")
}

func TestFilterByStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		filter transmission.Filter
		want   []int64
	}{
		{transmission.FilterAll, []int64{1, 2, 3, 4, 5}},
		{transmission.FilterActive, []int64{1, 2, 4}},
		{transmission.FilterDownloading, []int64{1, 4}},
		{transmission.FilterSeeding, []int64{2}},
		{transmission.FilterStopped, []int64{3}},
		{transmission.FilterError, []int64{5}},
		{transmission.FilterVerifying, []int64{}},
	}

	for _, tc := range cases {
		t.Run(string(tc.filter), func(t *testing.T) {
			t.Parallel()
			got := transmission.FilterByStatus(sampleTorrents(), tc.filter)
			assert.Equal(t, tc.want, ids(got))
		})
	}
}

func TestFilterByStatusDoesNotMutate(t *testing.T) {
	t.Parallel()

	in := sampleTorrents()
	_ = transmission.FilterByStatus(in, transmission.FilterSeeding)
	assert.Equal(t, []int64{1, 2, 3, 4, 5}, ids(in))
}

func TestSearchByName(t *testing.T) {
	t.Parallel()

	got := transmission.SearchByName(sampleTorrents(), "a")
	// "Debian", "Arch", and "Fedora" all contain "a" case-insensitively;
	// "Ubuntu" and "Gentoo" do not.
	assert.Equal(t, []int64{2, 3, 4}, ids(got))
}

func TestSearchByNameEmptyMatchesAll(t *testing.T) {
	t.Parallel()

	got := transmission.SearchByName(sampleTorrents(), "")
	assert.Equal(t, []int64{1, 2, 3, 4, 5}, ids(got))
}

func TestSearchByNameCaseInsensitive(t *testing.T) {
	t.Parallel()

	got := transmission.SearchByName(sampleTorrents(), "UBUNTU")
	assert.Equal(t, []int64{1}, ids(got))
}

func TestTop(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		n    int
		want []int64
	}{
		{"fewer than the set", 2, []int64{1, 2}},
		{"exactly the set", 5, []int64{1, 2, 3, 4, 5}},
		{"more than the set", 99, []int64{1, 2, 3, 4, 5}},
		{"one", 1, []int64{1}},
		{"zero is unlimited", 0, []int64{1, 2, 3, 4, 5}},
		{"negative is unlimited", -3, []int64{1, 2, 3, 4, 5}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := transmission.Top(sampleTorrents(), tc.n)
			assert.Equal(t, tc.want, ids(got))
		})
	}
}

func TestTopDoesNotMutate(t *testing.T) {
	t.Parallel()

	in := sampleTorrents()
	got := transmission.Top(in, 2)
	require.Len(t, got, 2)

	// Writing through the returned slice must not reach the input, so the
	// truncated view is a copy rather than a reslice of the caller's backing
	// array.
	got[0].Name = "clobbered"

	assert.Equal(t, []int64{1, 2, 3, 4, 5}, ids(in))
	assert.Equal(t, "Ubuntu", in[0].Name)
}

func TestTopEmptyIsNonNil(t *testing.T) {
	t.Parallel()

	// Every transform returns a non-nil slice so the command layer can hand the
	// result straight to JSON output without a nil check.
	assert.NotNil(t, transmission.Top(nil, 5))
	assert.Empty(t, transmission.Top(nil, 5))
	assert.NotNil(t, transmission.Top([]transmission.Torrent{}, 0))
}

func TestTopAppliesAfterSort(t *testing.T) {
	t.Parallel()

	// Truncating last is what makes --top "the first N rows of the rendered
	// order": the two largest torrents, not the first two the server sent.
	sorted := transmission.SortTorrents(sampleTorrents(), transmission.SortSize, true)
	assert.Equal(t, []int64{5, 4}, ids(transmission.Top(sorted, 2)))
}

func TestParseSortField(t *testing.T) {
	t.Parallel()

	cases := map[string]transmission.SortField{
		"queue":    transmission.SortQueue,
		"NAME":     transmission.SortName,
		"size":     transmission.SortSize,
		"progress": transmission.SortProgress,
		"ratio":    transmission.SortRatio,
		"speed":    transmission.SortSpeed,
	}

	for in, want := range cases {
		got, err := transmission.ParseSortField(in)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}

	// Surrounding whitespace is trimmed.
	got, err := transmission.ParseSortField("  added  ")
	require.NoError(t, err)
	assert.Equal(t, transmission.SortAdded, got)
}

func TestParseSortFieldInvalid(t *testing.T) {
	t.Parallel()

	_, err := transmission.ParseSortField("bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "queue")
	assert.Contains(t, err.Error(), "speed")
}

func TestSortTorrents(t *testing.T) {
	t.Parallel()

	cases := []struct {
		field   transmission.SortField
		reverse bool
		want    []int64
	}{
		{transmission.SortQueue, false, []int64{2, 3, 1, 4, 5}},
		{transmission.SortName, false, []int64{3, 2, 4, 5, 1}}, // Arch, Debian, Fedora, Gentoo, Ubuntu
		{transmission.SortAdded, false, []int64{1, 3, 2, 4, 5}},
		{transmission.SortSize, false, []int64{2, 3, 1, 4, 5}},
		{transmission.SortProgress, false, []int64{3, 4, 1, 5, 2}},
		{transmission.SortRatio, false, []int64{3, 4, 1, 2, 5}},
		{transmission.SortSpeed, false, []int64{3, 4, 5, 1, 2}}, // sums: 0,1,4,15,50
		{transmission.SortSize, true, []int64{5, 4, 1, 3, 2}},
	}

	for _, tc := range cases {
		t.Run(string(tc.field), func(t *testing.T) {
			t.Parallel()
			got := transmission.SortTorrents(sampleTorrents(), tc.field, tc.reverse)
			assert.Equal(t, tc.want, ids(got))
		})
	}
}

func TestSortTorrentsStableTiebreak(t *testing.T) {
	t.Parallel()

	// Two torrents share the same primary key (size) but differ by name/id.
	in := []transmission.Torrent{
		{ID: 9, Name: "Zeta", TotalSize: 100},
		{ID: 3, Name: "Alpha", TotalSize: 100},
		{ID: 7, Name: "Alpha", TotalSize: 100},
	}

	got := transmission.SortTorrents(in, transmission.SortSize, false)
	// Equal size: tiebreak by name then id -> Alpha(3), Alpha(7), Zeta(9).
	assert.Equal(t, []int64{3, 7, 9}, ids(got))
}

func TestSortTorrentsDoesNotMutate(t *testing.T) {
	t.Parallel()

	in := sampleTorrents()
	_ = transmission.SortTorrents(in, transmission.SortName, false)
	assert.Equal(t, []int64{1, 2, 3, 4, 5}, ids(in))
}

func TestTransformsCompose(t *testing.T) {
	t.Parallel()

	in := sampleTorrents()

	// Filter to active, search for "u" (Ubuntu only), then sort by name.
	filtered := transmission.FilterByStatus(in, transmission.FilterActive)
	searched := transmission.SearchByName(filtered, "u")
	sorted := transmission.SortTorrents(searched, transmission.SortName, false)

	assert.Equal(t, []int64{1}, ids(sorted))
}
