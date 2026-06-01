package torrent_test

import (
	"encoding/json"
	"testing"

	"github.com/itspriddle/snatcher/internal/torrent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusString(t *testing.T) {
	cases := map[torrent.Status]string{
		torrent.StatusStopped:          "Stopped",
		torrent.StatusQueuedToVerify:   "Queued to verify",
		torrent.StatusVerifying:        "Verifying",
		torrent.StatusQueuedToDownload: "Queued to download",
		torrent.StatusDownloading:      "Downloading",
		torrent.StatusQueuedToSeed:     "Queued to seed",
		torrent.StatusSeeding:          "Seeding",
		torrent.StatusError:            "Error",
		torrent.Status(99):             "Unknown",
	}

	for status, want := range cases {
		assert.Equal(t, want, status.String())
	}
}

func TestStatusSlug(t *testing.T) {
	cases := map[torrent.Status]string{
		torrent.StatusStopped:          "stopped",
		torrent.StatusQueuedToVerify:   "queued-to-verify",
		torrent.StatusVerifying:        "verifying",
		torrent.StatusQueuedToDownload: "queued-to-download",
		torrent.StatusDownloading:      "downloading",
		torrent.StatusQueuedToSeed:     "queued-to-seed",
		torrent.StatusSeeding:          "seeding",
		torrent.StatusError:            "error",
		torrent.Status(99):             "unknown",
	}

	for status, want := range cases {
		assert.Equal(t, want, status.Slug())
	}
}

// TestStatusTextRoundTrip verifies that MarshalText followed by UnmarshalText
// yields the original Status for every canonical state.
func TestStatusTextRoundTrip(t *testing.T) {
	states := []torrent.Status{
		torrent.StatusStopped,
		torrent.StatusQueuedToVerify,
		torrent.StatusVerifying,
		torrent.StatusQueuedToDownload,
		torrent.StatusDownloading,
		torrent.StatusQueuedToSeed,
		torrent.StatusSeeding,
		torrent.StatusError,
	}

	for _, want := range states {
		t.Run(want.Slug(), func(t *testing.T) {
			text, err := want.MarshalText()
			require.NoError(t, err)
			assert.Equal(t, want.Slug(), string(text))

			var got torrent.Status
			require.NoError(t, got.UnmarshalText(text))
			assert.Equal(t, want, got)
		})
	}
}

// TestStatusMarshalTextNeverErrors confirms an out-of-range Status marshals as
// "unknown" rather than failing, so JSON list output can't fail mid-render.
func TestStatusMarshalTextNeverErrors(t *testing.T) {
	text, err := torrent.Status(99).MarshalText()
	require.NoError(t, err)
	assert.Equal(t, "unknown", string(text))
}

func TestStatusUnmarshalText(t *testing.T) {
	t.Run("case insensitive", func(t *testing.T) {
		var got torrent.Status
		require.NoError(t, got.UnmarshalText([]byte("Downloading")))
		assert.Equal(t, torrent.StatusDownloading, got)
	})

	t.Run("unknown slug errors", func(t *testing.T) {
		var got torrent.Status
		err := got.UnmarshalText([]byte("bogus"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bogus")
	})

	t.Run("integers are rejected", func(t *testing.T) {
		var got torrent.Status
		require.Error(t, got.UnmarshalText([]byte("4")))
	})
}

func TestStatusFromTransmission(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		errorCode   int
		errorString string
		want        torrent.Status
	}{
		{name: "stopped", status: 0, want: torrent.StatusStopped},
		{name: "queued to verify", status: 1, want: torrent.StatusQueuedToVerify},
		{name: "verifying", status: 2, want: torrent.StatusVerifying},
		{name: "queued to download", status: 3, want: torrent.StatusQueuedToDownload},
		{name: "downloading", status: 4, want: torrent.StatusDownloading},
		{name: "queued to seed", status: 5, want: torrent.StatusQueuedToSeed},
		{name: "seeding", status: 6, want: torrent.StatusSeeding},
		{
			name:      "error code promotes to error",
			status:    4, // would otherwise be downloading
			errorCode: 3,
			want:      torrent.StatusError,
		},
		{
			name:        "error string promotes to error",
			status:      6, // would otherwise be seeding
			errorString: "tracker gave HTTP response code 404",
			want:        torrent.StatusError,
		},
		{
			name:   "unrecognized status falls back to stopped",
			status: 42,
			want:   torrent.StatusStopped,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := torrent.StatusFromTransmission(tc.status, tc.errorCode, tc.errorString)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestStatusConstantValues pins the iota ordering so a reorder that would shift
// the slug mapping or the Transmission status mapping is caught.
func TestStatusConstantValues(t *testing.T) {
	assert.Equal(t, torrent.StatusStopped, torrent.Status(0))
	assert.Equal(t, torrent.StatusQueuedToVerify, torrent.Status(1))
	assert.Equal(t, torrent.StatusVerifying, torrent.Status(2))
	assert.Equal(t, torrent.StatusQueuedToDownload, torrent.Status(3))
	assert.Equal(t, torrent.StatusDownloading, torrent.Status(4))
	assert.Equal(t, torrent.StatusQueuedToSeed, torrent.Status(5))
	assert.Equal(t, torrent.StatusSeeding, torrent.Status(6))
	assert.Equal(t, torrent.StatusError, torrent.Status(7))
}

// TestTorrentJSONContract locks the stable scripting surface: the json field
// names and that Status serializes as its canonical lowercase slug.
func TestTorrentJSONContract(t *testing.T) {
	tor := torrent.Torrent{
		ID:                 7,
		Hash:               "abc123",
		Name:               "ubuntu.iso",
		Status:             torrent.StatusDownloading,
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

// TestFieldKeysDefaultsToAll confirms an empty projection (nil or len 0) expands
// to the full AllFields set, in order, so a bare ListTorrents call still fills
// every Torrent field.
func TestFieldKeysDefaultsToAll(t *testing.T) {
	want := make([]string, len(torrent.AllFields))
	for i, f := range torrent.AllFields {
		want[i] = string(f)
	}

	assert.Equal(t, want, torrent.FieldKeys(nil), "nil projection expands to AllFields")
	assert.Equal(t, want, torrent.FieldKeys([]torrent.Field{}), "empty projection expands to AllFields")
}

// TestFieldKeysProjectsSubset confirms a non-empty projection is translated to
// its canonical keys in the given order, untouched.
func TestFieldKeysProjectsSubset(t *testing.T) {
	got := torrent.FieldKeys([]torrent.Field{
		torrent.FieldName, torrent.FieldID, torrent.FieldStatus,
	})
	assert.Equal(t, []string{"name", "id", "status"}, got)
}

// TestAllFieldsCoversTorrentJSONContract pins AllFields to exactly the Torrent
// struct's json tags: every projectable field maps to a real key and no key is
// missed, so a full projection decodes the whole record.
func TestAllFieldsCoversTorrentJSONContract(t *testing.T) {
	raw, err := json.Marshal(torrent.Torrent{})
	require.NoError(t, err)

	var obj map[string]any
	require.NoError(t, json.Unmarshal(raw, &obj))

	fieldKeys := make(map[string]struct{}, len(torrent.AllFields))
	for _, f := range torrent.AllFields {
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
	all := make(map[torrent.Field]struct{}, len(torrent.AllFields))
	for _, f := range torrent.AllFields {
		all[f] = struct{}{}
	}

	require.NotEmpty(t, torrent.PollFields)
	require.Less(t, len(torrent.PollFields), len(torrent.AllFields), "PollFields is a strict subset")

	seen := make(map[torrent.Field]struct{}, len(torrent.PollFields))
	for _, f := range torrent.PollFields {
		assert.Contains(t, all, f, "poll field %q is not in AllFields", f)
		_, dup := seen[f]
		assert.False(t, dup, "poll field %q is duplicated", f)
		seen[f] = struct{}{}
	}
}
