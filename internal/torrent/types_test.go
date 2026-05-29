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
// the JSON integer contract is caught.
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
// names and that Status serializes as its underlying integer.
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
		"id", "hash", "name", "status", "percentDone", "totalSize",
		"sizeWhenDone", "leftUntilDone", "rateDownload", "rateUpload",
		"uploadRatio", "eta", "peersConnected", "peersSendingToUs",
		"peersGettingFromUs", "addedDate", "doneDate", "activityDate",
		"downloadDir", "error", "labels", "queuePosition", "isFinished",
		"isStalled",
	} {
		assert.Contains(t, got, key, "missing json key %q", key)
	}

	// Status serializes as its underlying integer (US-003 handles display).
	assert.InDelta(t, float64(torrent.StatusDownloading), got["status"], 0)
	assert.Equal(t, "abc123", got["hash"])
	assert.Equal(t, float64(7), got["id"])
}
