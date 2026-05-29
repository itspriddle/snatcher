package torrent

import (
	"fmt"
	"strings"
)

// Status is the vendor-neutral lifecycle state of a torrent. It abstracts over
// each backend's own status representation (Transmission's 0–6 status int, for
// example) so commands and output modes share one stable set of states. In JSON
// it serializes as a stable lowercase slug (see Slug); human-readable rendering
// goes through String().
type Status int

const (
	// StatusStopped means the torrent is paused (not actively transferring).
	StatusStopped Status = iota

	// StatusQueuedToVerify means the torrent is waiting in the queue to have
	// its local data verified.
	StatusQueuedToVerify

	// StatusVerifying means the torrent's local data is currently being
	// verified against the metadata.
	StatusVerifying

	// StatusQueuedToDownload means the torrent is waiting in the queue to start
	// downloading.
	StatusQueuedToDownload

	// StatusDownloading means the torrent is actively downloading.
	StatusDownloading

	// StatusQueuedToSeed means the torrent has finished downloading and is
	// waiting in the queue to start seeding.
	StatusQueuedToSeed

	// StatusSeeding means the torrent is complete and actively seeding.
	StatusSeeding

	// StatusError means the backend reported an error for the torrent (e.g. a
	// tracker or local data error). It takes precedence over the transfer
	// states above.
	StatusError
)

// String returns a human-readable label for the status, suitable for plain CLI
// output. Unknown values render as "Unknown".
func (s Status) String() string {
	switch s {
	case StatusStopped:
		return "Stopped"
	case StatusQueuedToVerify:
		return "Queued to verify"
	case StatusVerifying:
		return "Verifying"
	case StatusQueuedToDownload:
		return "Queued to download"
	case StatusDownloading:
		return "Downloading"
	case StatusQueuedToSeed:
		return "Queued to seed"
	case StatusSeeding:
		return "Seeding"
	case StatusError:
		return "Error"
	default:
		return "Unknown"
	}
}

// Slug returns the canonical lowercase, hyphenated token for the status. It is
// the single source of truth for the status vocabulary: it backs the JSON
// scripting contract (see MarshalText) and is reused by --filter. Unknown
// values render as "unknown".
func (s Status) Slug() string {
	switch s {
	case StatusStopped:
		return "stopped"
	case StatusQueuedToVerify:
		return "queued-to-verify"
	case StatusVerifying:
		return "verifying"
	case StatusQueuedToDownload:
		return "queued-to-download"
	case StatusDownloading:
		return "downloading"
	case StatusQueuedToSeed:
		return "queued-to-seed"
	case StatusSeeding:
		return "seeding"
	case StatusError:
		return "error"
	default:
		return "unknown"
	}
}

// MarshalText implements encoding.TextMarshaler so Status serializes as its
// canonical slug in JSON (and any other text-based encoding). It never errors:
// out-of-range values marshal as "unknown" so list output can't fail
// mid-render.
func (s Status) MarshalText() ([]byte, error) {
	return []byte(s.Slug()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler, parsing one of the eight
// canonical slugs (case-insensitively) back into a Status. Integers are not
// accepted. An unrecognized slug returns an error.
func (s *Status) UnmarshalText(b []byte) error {
	switch strings.ToLower(string(b)) {
	case "stopped":
		*s = StatusStopped
	case "queued-to-verify":
		*s = StatusQueuedToVerify
	case "verifying":
		*s = StatusVerifying
	case "queued-to-download":
		*s = StatusQueuedToDownload
	case "downloading":
		*s = StatusDownloading
	case "queued-to-seed":
		*s = StatusQueuedToSeed
	case "seeding":
		*s = StatusSeeding
	case "error":
		*s = StatusError
	default:
		return fmt.Errorf("torrent: unknown status %q", string(b))
	}

	return nil
}

// Transmission's raw torrent-get "status" field values (0–6). They are named
// here so StatusFromTransmission documents the mapping it implements; the
// Transmission adapter passes the raw int through.
const (
	transmissionStopped          = 0
	transmissionQueuedToVerify   = 1
	transmissionVerifying        = 2
	transmissionQueuedToDownload = 3
	transmissionDownloading      = 4
	transmissionQueuedToSeed     = 5
	transmissionSeeding          = 6
)

// StatusFromTransmission maps a raw Transmission torrent-get "status" int (0–6)
// plus the torrent's error fields to a neutral Status. A non-zero errorCode (or
// a non-empty errorString) promotes the result to StatusError regardless of the
// transfer state, mirroring how Transmission surfaces errored torrents. An
// unrecognized status int falls back to StatusStopped.
func StatusFromTransmission(status, errorCode int, errorString string) Status {
	if errorCode != 0 || errorString != "" {
		return StatusError
	}

	switch status {
	case transmissionStopped:
		return StatusStopped
	case transmissionQueuedToVerify:
		return StatusQueuedToVerify
	case transmissionVerifying:
		return StatusVerifying
	case transmissionQueuedToDownload:
		return StatusQueuedToDownload
	case transmissionDownloading:
		return StatusDownloading
	case transmissionQueuedToSeed:
		return StatusQueuedToSeed
	case transmissionSeeding:
		return StatusSeeding
	default:
		return StatusStopped
	}
}

// Torrent is the vendor-neutral description of a single torrent, returned in
// collections by Backend.ListTorrents. Its json tags form a stable scripting
// surface for --json/--jq output, so they are chosen deliberately here and
// later commands and users depend on them.
//
// Both the human-friendly numeric ID and the immutable hash are captured: the
// ID is the default displayed identifier, while the hash is available via
// --json and column selection for stable, server-independent identity.
type Torrent struct {
	// ID is the backend's numeric identifier for the torrent (Transmission's
	// per-session id). It is the default displayed identifier.
	ID int64 `json:"id"`

	// Hash is the torrent's info-hash string, a stable identity independent of
	// the session-scoped ID.
	Hash string `json:"hash"`

	// Name is the torrent's display name.
	Name string `json:"name"`

	// Status is the neutral lifecycle state.
	Status Status `json:"status"`

	// PercentDone is the download completion fraction in the range 0–1.
	PercentDone float64 `json:"percentDone"`

	// TotalSize is the total size of the torrent's contents, in bytes.
	TotalSize int64 `json:"totalSize"`

	// SizeWhenDone is the number of bytes the torrent will occupy once the
	// selected files finish downloading, in bytes.
	SizeWhenDone int64 `json:"sizeWhenDone"`

	// LeftUntilDone is the number of bytes still to download, in bytes.
	LeftUntilDone int64 `json:"leftUntilDone"`

	// RateDownload is the current download rate, in bytes/second.
	RateDownload int64 `json:"rateDownload"`

	// RateUpload is the current upload rate, in bytes/second.
	RateUpload int64 `json:"rateUpload"`

	// UploadRatio is the ratio of bytes uploaded to bytes downloaded.
	UploadRatio float64 `json:"uploadRatio"`

	// ETA is the estimated seconds until the download completes. A negative
	// value means unknown/not applicable, matching Transmission's convention.
	ETA int64 `json:"eta"`

	// PeersConnected is the number of peers currently connected.
	PeersConnected int64 `json:"peersConnected"`

	// PeersSendingToUs is the number of connected peers sending data to us.
	PeersSendingToUs int64 `json:"peersSendingToUs"`

	// PeersGettingFromUs is the number of connected peers downloading from us.
	PeersGettingFromUs int64 `json:"peersGettingFromUs"`

	// AddedDate is the Unix timestamp (seconds) when the torrent was added.
	AddedDate int64 `json:"addedDate"`

	// DoneDate is the Unix timestamp (seconds) when the torrent finished
	// downloading, or 0 if not complete.
	DoneDate int64 `json:"doneDate"`

	// ActivityDate is the Unix timestamp (seconds) of the torrent's most recent
	// activity.
	ActivityDate int64 `json:"activityDate"`

	// DownloadDir is the directory the torrent's data is downloaded into.
	DownloadDir string `json:"downloadDir"`

	// Error is the backend's error message for the torrent, or empty when there
	// is no error.
	Error string `json:"error"`

	// Labels are the user-assigned labels on the torrent.
	Labels []string `json:"labels"`

	// QueuePosition is the torrent's position in the backend's queue.
	QueuePosition int64 `json:"queuePosition"`

	// IsFinished reports whether seeding is complete (the seed ratio or idle
	// limit has been reached).
	IsFinished bool `json:"isFinished"`

	// IsStalled reports whether the torrent has stalled (no data transfer for
	// longer than the stalled threshold).
	IsStalled bool `json:"isStalled"`
}
