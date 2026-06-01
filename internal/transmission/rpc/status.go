package rpc

import (
	"fmt"
	"strings"
)

// Status is the derived lifecycle state of a torrent. It abstracts Transmission's
// raw 0–6 status int (and error fields) into a stable set of states, so commands
// and output modes share one vocabulary. In JSON it serializes as a stable
// lowercase slug (see Slug); human-readable rendering goes through String(). It
// is derived once at decode time by StatusFromTransmission and carried on the
// canonical Torrent.
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

	// StatusError means the server reported an error for the torrent (e.g. a
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
		return fmt.Errorf("rpc: unknown status %q", string(b))
	}

	return nil
}

// Transmission's raw torrent-get "status" field values (0–6). They are named
// here so StatusFromTransmission documents the mapping it implements.
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
// plus the torrent's error fields to a Status. A non-zero errorCode (or a
// non-empty errorString) promotes the result to StatusError regardless of the
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
