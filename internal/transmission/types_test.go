package transmission_test

import (
	"encoding/json"
	"testing"

	"github.com/itspriddle/snatcher/internal/transmission"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusString(t *testing.T) {
	cases := map[transmission.Status]string{
		transmission.StatusStopped:          "Stopped",
		transmission.StatusQueuedToVerify:   "Queued to verify",
		transmission.StatusVerifying:        "Verifying",
		transmission.StatusQueuedToDownload: "Queued to download",
		transmission.StatusDownloading:      "Downloading",
		transmission.StatusQueuedToSeed:     "Queued to seed",
		transmission.StatusSeeding:          "Seeding",
		transmission.StatusError:            "Error",
		transmission.Status(99):             "Unknown",
	}

	for status, want := range cases {
		assert.Equal(t, want, status.String())
	}
}

func TestStatusSlug(t *testing.T) {
	cases := map[transmission.Status]string{
		transmission.StatusStopped:          "stopped",
		transmission.StatusQueuedToVerify:   "queued-to-verify",
		transmission.StatusVerifying:        "verifying",
		transmission.StatusQueuedToDownload: "queued-to-download",
		transmission.StatusDownloading:      "downloading",
		transmission.StatusQueuedToSeed:     "queued-to-seed",
		transmission.StatusSeeding:          "seeding",
		transmission.StatusError:            "error",
		transmission.Status(99):             "unknown",
	}

	for status, want := range cases {
		assert.Equal(t, want, status.Slug())
	}
}

// TestStatusTextRoundTrip verifies that MarshalText followed by UnmarshalText
// yields the original Status for every canonical state.
func TestStatusTextRoundTrip(t *testing.T) {
	states := []transmission.Status{
		transmission.StatusStopped,
		transmission.StatusQueuedToVerify,
		transmission.StatusVerifying,
		transmission.StatusQueuedToDownload,
		transmission.StatusDownloading,
		transmission.StatusQueuedToSeed,
		transmission.StatusSeeding,
		transmission.StatusError,
	}

	for _, want := range states {
		t.Run(want.Slug(), func(t *testing.T) {
			text, err := want.MarshalText()
			require.NoError(t, err)
			assert.Equal(t, want.Slug(), string(text))

			var got transmission.Status
			require.NoError(t, got.UnmarshalText(text))
			assert.Equal(t, want, got)
		})
	}
}

// TestStatusMarshalTextNeverErrors confirms an out-of-range Status marshals as
// "unknown" rather than failing, so JSON list output can't fail mid-render.
func TestStatusMarshalTextNeverErrors(t *testing.T) {
	text, err := transmission.Status(99).MarshalText()
	require.NoError(t, err)
	assert.Equal(t, "unknown", string(text))
}

func TestStatusUnmarshalText(t *testing.T) {
	t.Run("case insensitive", func(t *testing.T) {
		var got transmission.Status
		require.NoError(t, got.UnmarshalText([]byte("Downloading")))
		assert.Equal(t, transmission.StatusDownloading, got)
	})

	t.Run("unknown slug errors", func(t *testing.T) {
		var got transmission.Status
		err := got.UnmarshalText([]byte("bogus"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bogus")
	})

	t.Run("integers are rejected", func(t *testing.T) {
		var got transmission.Status
		require.Error(t, got.UnmarshalText([]byte("4")))
	})
}

func TestStatusFromTransmission(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		errorCode   int
		errorString string
		want        transmission.Status
	}{
		{name: "stopped", status: 0, want: transmission.StatusStopped},
		{name: "queued to verify", status: 1, want: transmission.StatusQueuedToVerify},
		{name: "verifying", status: 2, want: transmission.StatusVerifying},
		{name: "queued to download", status: 3, want: transmission.StatusQueuedToDownload},
		{name: "downloading", status: 4, want: transmission.StatusDownloading},
		{name: "queued to seed", status: 5, want: transmission.StatusQueuedToSeed},
		{name: "seeding", status: 6, want: transmission.StatusSeeding},
		{
			name:      "error code promotes to error",
			status:    4, // would otherwise be downloading
			errorCode: 3,
			want:      transmission.StatusError,
		},
		{
			name:        "error string promotes to error",
			status:      6, // would otherwise be seeding
			errorString: "tracker gave HTTP response code 404",
			want:        transmission.StatusError,
		},
		{
			name:   "unrecognized status falls back to stopped",
			status: 42,
			want:   transmission.StatusStopped,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := transmission.StatusFromTransmission(tc.status, tc.errorCode, tc.errorString)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestStatusConstantValues pins the iota ordering so a reorder that would shift
// the slug mapping or the Transmission status mapping is caught.
func TestStatusConstantValues(t *testing.T) {
	assert.Equal(t, transmission.StatusStopped, transmission.Status(0))
	assert.Equal(t, transmission.StatusQueuedToVerify, transmission.Status(1))
	assert.Equal(t, transmission.StatusVerifying, transmission.Status(2))
	assert.Equal(t, transmission.StatusQueuedToDownload, transmission.Status(3))
	assert.Equal(t, transmission.StatusDownloading, transmission.Status(4))
	assert.Equal(t, transmission.StatusQueuedToSeed, transmission.Status(5))
	assert.Equal(t, transmission.StatusSeeding, transmission.Status(6))
	assert.Equal(t, transmission.StatusError, transmission.Status(7))
}

// TestTorrentJSONContract locks the stable scripting surface: the json field
// names and that Status serializes as its canonical lowercase slug.
func TestTorrentJSONContract(t *testing.T) {
	tor := transmission.Torrent{
		ID:                 7,
		Hash:               "abc123",
		Name:               "ubuntu.iso",
		Status:             transmission.StatusDownloading,
		PercentDone:        0.5,
		TotalSize:          2048,
		SizeWhenDone:       2048,
		LeftUntilDone:      1024,
		RateDownload:       512,
		RateUpload:         128,
		UploadRatio:        1.25,
		ETA:                60,
		PeersConnected:     5,
		PeersSendingToUs:   3,
		PeersGettingFromUs: 2,
		AddedDate:          1700000000,
		DoneDate:           0,
		ActivityDate:       1700000100,
		DownloadDir:        "/downloads",
		Error:              "",
		Labels:             []string{"linux", "iso"},
		QueuePosition:      0,
		IsFinished:         false,
		IsStalled:          false,
	}

	raw, err := json.Marshal(tor)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	// Every documented field is present under its expected key.
	for _, key := range []string{
		"id", "hash", "name", "status", "percent_done", "total_size",
		"size_when_done", "left_until_done", "rate_download", "rate_upload",
		"upload_ratio", "eta", "peers_connected", "peers_sending_to_us",
		"peers_getting_from_us", "added_date", "done_date", "activity_date",
		"download_dir", "error", "labels", "queue_position", "is_finished",
		"is_stalled",
	} {
		assert.Contains(t, got, key, "missing json key %q", key)
	}

	// Status serializes as its canonical lowercase slug, a quoted JSON string
	// (US-003 handles display labels).
	assert.Equal(t, "downloading", got["status"])
	assert.Contains(t, string(raw), `"status":"downloading"`)
	assert.Equal(t, "abc123", got["hash"])
	assert.Equal(t, float64(7), got["id"])
}

// TestTorrentDetailJSONContract confirms TorrentDetail's json tags are all
// snake_case (matching the rest of the neutral types) and that the embedded
// Torrent flattens into the same object — so a single info --json blob carries
// both the list fields and the detail fields under stable keys.
func TestTorrentDetailJSONContract(t *testing.T) {
	detail := transmission.TorrentDetail{
		Torrent:    transmission.Torrent{ID: 7, Hash: "abc123", Name: "ubuntu.iso"},
		Files:      []transmission.File{{Name: "ubuntu.iso", Length: 2048, BytesCompleted: 1024, Wanted: true, Priority: 1}},
		Peers:      []transmission.Peer{{Address: "10.0.0.1", Port: 51413}},
		Trackers:   []transmission.Tracker{{Announce: "http://t/announce", SeederCount: 42}},
		Pieces:     []bool{true, false},
		MagnetLink: "magnet:?xt=urn:btih:abc123",
	}

	raw, err := json.Marshal(detail)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	// The embedded Torrent's keys flatten into the same object.
	assert.Equal(t, float64(7), got["id"], "embedded Torrent flattens into the detail object")
	assert.Equal(t, "abc123", got["hash"])

	// Every detail-only key is present and snake_case.
	for _, key := range []string{
		"files", "peers", "trackers", "pieces", "magnet_link", "downloaded_ever",
		"uploaded_ever", "corrupt_ever", "have_valid", "piece_count", "piece_size",
		"comment", "creator", "date_created", "is_private", "seconds_downloading",
		"seconds_seeding", "recheck_progress", "metadata_percent_complete",
	} {
		assert.Contains(t, got, key, "missing detail json key %q", key)
	}

	// The nested File/Peer/Tracker objects use snake_case keys too.
	files, ok := got["files"].([]any)
	require.True(t, ok)
	require.Len(t, files, 1)
	file, ok := files[0].(map[string]any)
	require.True(t, ok)
	for _, key := range []string{"name", "length", "bytes_completed", "wanted", "priority"} {
		assert.Contains(t, file, key, "missing File json key %q", key)
	}

	tracker, ok := got["trackers"].([]any)[0].(map[string]any)
	require.True(t, ok)
	for _, key := range []string{
		"announce", "host", "sitename", "announce_state", "seeder_count",
		"leecher_count", "download_count", "last_announce_result",
		"next_announce_time",
	} {
		assert.Contains(t, tracker, key, "missing Tracker json key %q", key)
	}

	peer, ok := got["peers"].([]any)[0].(map[string]any)
	require.True(t, ok)
	for _, key := range []string{
		"address", "port", "client", "progress", "rate_to_client", "rate_to_peer",
		"flag_str", "is_encrypted",
	} {
		assert.Contains(t, peer, key, "missing Peer json key %q", key)
	}
}

// TestFieldKeysDefaultsToAll confirms an empty projection (nil or len 0) expands
// to the full AllFields set, in order, so a bare ListTorrents call still fills
// every Torrent field.
func TestFieldKeysDefaultsToAll(t *testing.T) {
	want := make([]string, len(transmission.AllFields))
	for i, f := range transmission.AllFields {
		want[i] = string(f)
	}

	assert.Equal(t, want, transmission.FieldKeys(nil), "nil projection expands to AllFields")
	assert.Equal(t, want, transmission.FieldKeys([]transmission.Field{}), "empty projection expands to AllFields")
}

// TestFieldKeysProjectsSubset confirms a non-empty projection is translated to
// its canonical keys in the given order, untouched.
func TestFieldKeysProjectsSubset(t *testing.T) {
	got := transmission.FieldKeys([]transmission.Field{
		transmission.FieldName, transmission.FieldID, transmission.FieldStatus,
	})
	assert.Equal(t, []string{"name", "id", "status"}, got)
}

// TestAllFieldsCoversTorrentJSONContract pins AllFields to exactly the Torrent
// struct's json tags: every projectable field maps to a real key and no key is
// missed, so a full projection decodes the whole record.
func TestAllFieldsCoversTorrentJSONContract(t *testing.T) {
	raw, err := json.Marshal(transmission.Torrent{})
	require.NoError(t, err)

	var obj map[string]any
	require.NoError(t, json.Unmarshal(raw, &obj))

	fieldKeys := make(map[string]struct{}, len(transmission.AllFields))
	for _, f := range transmission.AllFields {
		key := string(f)
		assert.Contains(t, obj, key, "Field %q has no matching Torrent json tag", key)
		fieldKeys[key] = struct{}{}
	}

	for key := range obj {
		assert.Contains(t, fieldKeys, key, "Torrent json key %q is not a projectable Field", key)
	}
}

// TestPollFieldsAreValidSubset confirms PollFields is a strict, deduplicated
// subset of AllFields (every poll field is a real, projectable Field), so a poll
// projection never requests an unknown key.
func TestPollFieldsAreValidSubset(t *testing.T) {
	all := make(map[transmission.Field]struct{}, len(transmission.AllFields))
	for _, f := range transmission.AllFields {
		all[f] = struct{}{}
	}

	require.NotEmpty(t, transmission.PollFields)
	require.Less(t, len(transmission.PollFields), len(transmission.AllFields), "PollFields is a strict subset")

	seen := make(map[transmission.Field]struct{}, len(transmission.PollFields))
	for _, f := range transmission.PollFields {
		assert.Contains(t, all, f, "poll field %q is not in AllFields", f)
		_, dup := seen[f]
		assert.False(t, dup, "poll field %q is duplicated", f)
		seen[f] = struct{}{}
	}
}
