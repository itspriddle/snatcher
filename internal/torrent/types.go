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
	PercentDone float64 `json:"percent_done"`

	// TotalSize is the total size of the torrent's contents, in bytes.
	TotalSize int64 `json:"total_size"`

	// SizeWhenDone is the number of bytes the torrent will occupy once the
	// selected files finish downloading, in bytes.
	SizeWhenDone int64 `json:"size_when_done"`

	// LeftUntilDone is the number of bytes still to download, in bytes.
	LeftUntilDone int64 `json:"left_until_done"`

	// RateDownload is the current download rate, in bytes/second.
	RateDownload int64 `json:"rate_download"`

	// RateUpload is the current upload rate, in bytes/second.
	RateUpload int64 `json:"rate_upload"`

	// UploadRatio is the ratio of bytes uploaded to bytes downloaded.
	UploadRatio float64 `json:"upload_ratio"`

	// ETA is the estimated seconds until the download completes. A negative
	// value means unknown/not applicable, matching Transmission's convention.
	ETA int64 `json:"eta"`

	// PeersConnected is the number of peers currently connected.
	PeersConnected int64 `json:"peers_connected"`

	// PeersSendingToUs is the number of connected peers sending data to us.
	PeersSendingToUs int64 `json:"peers_sending_to_us"`

	// PeersGettingFromUs is the number of connected peers downloading from us.
	PeersGettingFromUs int64 `json:"peers_getting_from_us"`

	// AddedDate is the Unix timestamp (seconds) when the torrent was added.
	AddedDate int64 `json:"added_date"`

	// DoneDate is the Unix timestamp (seconds) when the torrent finished
	// downloading, or 0 if not complete.
	DoneDate int64 `json:"done_date"`

	// ActivityDate is the Unix timestamp (seconds) of the torrent's most recent
	// activity.
	ActivityDate int64 `json:"activity_date"`

	// DownloadDir is the directory the torrent's data is downloaded into.
	DownloadDir string `json:"download_dir"`

	// Error is the backend's error message for the torrent, or empty when there
	// is no error.
	Error string `json:"error"`

	// Labels are the user-assigned labels on the torrent.
	Labels []string `json:"labels"`

	// QueuePosition is the torrent's position in the backend's queue.
	QueuePosition int64 `json:"queue_position"`

	// IsFinished reports whether seeding is complete (the seed ratio or idle
	// limit has been reached).
	IsFinished bool `json:"is_finished"`

	// IsStalled reports whether the torrent has stalled (no data transfer for
	// longer than the stalled threshold).
	IsStalled bool `json:"is_stalled"`
}

// File is one file within a torrent, returned in TorrentDetail.Files. Wanted and
// Priority are the file's per-file selection state (whether it is downloaded and
// at what priority), normalized across both wire protocols.
type File struct {
	// Name is the file's path relative to the torrent's download directory.
	Name string `json:"name"`

	// Length is the file's total size, in bytes.
	Length int64 `json:"length"`

	// BytesCompleted is the number of bytes downloaded for the file so far.
	BytesCompleted int64 `json:"bytes_completed"`

	// Wanted reports whether the file is selected for download.
	Wanted bool `json:"wanted"`

	// Priority is the file's download priority (-1 low, 0 normal, 1 high).
	Priority int `json:"priority"`
}

// FileChanges is the vendor-neutral set of per-file selection and priority
// changes applied by Backend.SetFiles. Each field is a list of file indices
// (matching the order of TorrentDetail.Files); only the non-empty fields are
// written, and an empty FileChanges is a no-op. Wanted and Unwanted set whether
// the listed files are downloaded; PriorityHigh, PriorityNormal, and PriorityLow
// set the listed files' download priority. The indices in different fields are
// independent (e.g. a file may appear in both Wanted and PriorityHigh).
type FileChanges struct {
	// Wanted are the indices of files to mark as wanted (downloaded).
	Wanted []int

	// Unwanted are the indices of files to mark as unwanted (not downloaded).
	Unwanted []int

	// PriorityHigh are the indices of files to set to high download priority.
	PriorityHigh []int

	// PriorityNormal are the indices of files to set to normal download priority.
	PriorityNormal []int

	// PriorityLow are the indices of files to set to low download priority.
	PriorityLow []int
}

// Peer is one connected peer for a torrent, returned in TorrentDetail.Peers.
type Peer struct {
	// Address is the peer's IP address.
	Address string `json:"address"`

	// Port is the peer's port.
	Port int `json:"port"`

	// Client is the peer's BitTorrent client name/version, when known.
	Client string `json:"client"`

	// Progress is the peer's download completion fraction, in the range 0–1.
	Progress float64 `json:"progress"`

	// RateToClient is the rate at which we are downloading from the peer, in
	// bytes/second.
	RateToClient int64 `json:"rate_to_client"`

	// RateToPeer is the rate at which we are uploading to the peer, in
	// bytes/second.
	RateToPeer int64 `json:"rate_to_peer"`

	// FlagStr is the backend's compact per-peer status flag string.
	FlagStr string `json:"flag_str"`

	// IsEncrypted reports whether the peer connection is encrypted.
	IsEncrypted bool `json:"is_encrypted"`
}

// Tracker is one tracker for a torrent, returned in TorrentDetail.Trackers. The
// fields mirror Transmission's per-tracker stats so the detail tracker pane can
// render announce state, swarm counts, and the next scheduled announce.
type Tracker struct {
	// Announce is the tracker's announce URL.
	Announce string `json:"announce"`

	// Host is the tracker's host (scheme + authority).
	Host string `json:"host"`

	// Sitename is the tracker's short site name, when reported.
	Sitename string `json:"sitename"`

	// AnnounceState is the backend's numeric announce state for the tracker.
	AnnounceState int `json:"announce_state"`

	// SeederCount is the number of seeders the tracker reports (-1 = unknown).
	SeederCount int `json:"seeder_count"`

	// LeecherCount is the number of leechers the tracker reports (-1 = unknown).
	LeecherCount int `json:"leecher_count"`

	// DownloadCount is the number of completed downloads the tracker reports
	// (-1 = unknown).
	DownloadCount int `json:"download_count"`

	// LastAnnounceResult is the human-readable result of the last announce.
	LastAnnounceResult string `json:"last_announce_result"`

	// NextAnnounceTime is the Unix timestamp (seconds) of the next scheduled
	// announce, or 0 when none is scheduled.
	NextAnnounceTime int64 `json:"next_announce_time"`
}

// TorrentDetail is the vendor-neutral detail view of a single torrent, returned
// by Backend.TorrentDetail. It embeds the full Torrent record (so every list
// field is present) and adds the per-torrent collections and overview scalars
// the detail panes need: files, peers, trackers, the pieces bitfield, the magnet
// link, and the transfer/info scalars. Its json tags (and the embedded Torrent's)
// form a stable scripting surface alongside the rest of the neutral types.
type TorrentDetail struct {
	Torrent

	// Files are the torrent's files, in torrent order.
	Files []File `json:"files"`

	// Peers are the torrent's currently connected peers.
	Peers []Peer `json:"peers"`

	// Trackers are the torrent's trackers and their per-tracker stats.
	Trackers []Tracker `json:"trackers"`

	// Pieces is the have/have-not bitfield, one bit per piece (bit i set means
	// piece i is complete), decoded from the backend's base64 string.
	Pieces []bool `json:"pieces"`

	// MagnetLink is the torrent's magnet URI.
	MagnetLink string `json:"magnet_link"`

	// DownloadedEver is the total bytes ever downloaded for the torrent.
	DownloadedEver int64 `json:"downloaded_ever"`

	// UploadedEver is the total bytes ever uploaded for the torrent.
	UploadedEver int64 `json:"uploaded_ever"`

	// CorruptEver is the total bytes that failed verification and were discarded.
	CorruptEver int64 `json:"corrupt_ever"`

	// HaveValid is the number of bytes of verified, complete data.
	HaveValid int64 `json:"have_valid"`

	// PieceCount is the number of pieces in the torrent.
	PieceCount int64 `json:"piece_count"`

	// PieceSize is the size of each piece, in bytes.
	PieceSize int64 `json:"piece_size"`

	// Comment is the torrent's comment field.
	Comment string `json:"comment"`

	// Creator is the torrent's creator field.
	Creator string `json:"creator"`

	// DateCreated is the Unix timestamp (seconds) the torrent was created.
	DateCreated int64 `json:"date_created"`

	// IsPrivate reports whether the torrent is private (no DHT/PEX).
	IsPrivate bool `json:"is_private"`

	// SecondsDownloading is the total time spent downloading, in seconds.
	SecondsDownloading int64 `json:"seconds_downloading"`

	// SecondsSeeding is the total time spent seeding, in seconds.
	SecondsSeeding int64 `json:"seconds_seeding"`

	// RecheckProgress is the verify (recheck) completion fraction, 0–1.
	RecheckProgress float64 `json:"recheck_progress"`

	// MetadataPercentComplete is the metadata-download completion fraction, 0–1
	// (1 once the full metadata is known; below 1 for a magnet still resolving).
	MetadataPercentComplete float64 `json:"metadata_percent_complete"`
}

// Field is a vendor-neutral, projectable Torrent field. Its string value is the
// canonical snake_case key matching the Torrent struct's json tag, which the
// backend adapter translates to each wire protocol's spelling. Callers pass a
// projection of Fields to ListTorrents so a polling client can request only the
// cheap subset it needs (see PollFields) instead of the full record. An empty
// projection means the full set (AllFields).
type Field string

// The full set of projectable Torrent fields, one per Torrent struct field. The
// string values are the canonical snake_case keys (matching the json tags), so
// FieldKeys can hand them straight to the adapter.
const (
	FieldID                 Field = "id"
	FieldHash               Field = "hash"
	FieldName               Field = "name"
	FieldStatus             Field = "status"
	FieldPercentDone        Field = "percent_done"
	FieldTotalSize          Field = "total_size"
	FieldSizeWhenDone       Field = "size_when_done"
	FieldLeftUntilDone      Field = "left_until_done"
	FieldRateDownload       Field = "rate_download"
	FieldRateUpload         Field = "rate_upload"
	FieldUploadRatio        Field = "upload_ratio"
	FieldETA                Field = "eta"
	FieldPeersConnected     Field = "peers_connected"
	FieldPeersSendingToUs   Field = "peers_sending_to_us"
	FieldPeersGettingFromUs Field = "peers_getting_from_us"
	FieldAddedDate          Field = "added_date"
	FieldDoneDate           Field = "done_date"
	FieldActivityDate       Field = "activity_date"
	FieldDownloadDir        Field = "download_dir"
	FieldError              Field = "error"
	FieldLabels             Field = "labels"
	FieldQueuePosition      Field = "queue_position"
	FieldIsFinished         Field = "is_finished"
	FieldIsStalled          Field = "is_stalled"
)

// AllFields is every projectable Torrent field, in Torrent struct order. It is
// the default projection used when ListTorrents is called with no fields (an
// empty projection), so a bare list request still fills every Torrent field.
var AllFields = []Field{
	FieldID, FieldHash, FieldName, FieldStatus, FieldPercentDone,
	FieldTotalSize, FieldSizeWhenDone, FieldLeftUntilDone, FieldRateDownload,
	FieldRateUpload, FieldUploadRatio, FieldETA, FieldPeersConnected,
	FieldPeersSendingToUs, FieldPeersGettingFromUs, FieldAddedDate,
	FieldDoneDate, FieldActivityDate, FieldDownloadDir, FieldError,
	FieldLabels, FieldQueuePosition, FieldIsFinished, FieldIsStalled,
}

// PollFields is the cheap field subset a polling client (the TUI list pane)
// refreshes on every tick: identity, lifecycle state, progress, transfer rates,
// and the size/left bytes a progress bar needs. It deliberately omits the
// expensive or rarely-changing fields (peers, dates, labels, download dir) so a
// frequent poll stays light. Its exact membership is not contract-locked the way
// the json tags are and may be tuned once the poll loop exists.
var PollFields = []Field{
	FieldID, FieldHash, FieldStatus, FieldPercentDone, FieldRateDownload,
	FieldRateUpload, FieldLeftUntilDone, FieldSizeWhenDone,
}

// FieldKeys converts a Field projection to the canonical snake_case key slice
// the backend adapter consumes. An empty projection (nil or len 0) expands to
// AllFields, so callers can pass no fields to mean "the full record". The result
// preserves the given order.
func FieldKeys(fields []Field) []string {
	if len(fields) == 0 {
		fields = AllFields
	}

	keys := make([]string, len(fields))
	for i, f := range fields {
		keys[i] = string(f)
	}

	return keys
}

// SessionSettings is the vendor-neutral snapshot of a server's configurable
// session settings, returned by Backend.GetSession. It carries one typed field
// per setting in the snatcher registry, grouped by category (download, network,
// bandwidth, queue). Backends fill every field, substituting the zero value
// when the server omits one. The json tags mirror the other neutral session
// types (ServerInfo, Stats), but the stable scripting contract consumers depend
// on is the per-key {key, category, value} shape the settings command emits,
// not this struct directly.
type SessionSettings struct {
	// Download settings.

	// DownloadDir is the default download directory.
	DownloadDir string `json:"download_dir"`

	// StartAddedTorrents reports whether torrents start automatically when added.
	StartAddedTorrents bool `json:"start_added_torrents"`

	// RenamePartialFiles reports whether incomplete files get a ".part" suffix.
	RenamePartialFiles bool `json:"rename_partial_files"`

	// IncompleteDirEnabled reports whether incomplete torrents use a separate dir.
	IncompleteDirEnabled bool `json:"incomplete_dir_enabled"`

	// IncompleteDir is the directory for incomplete torrents.
	IncompleteDir string `json:"incomplete_dir"`

	// SeedRatioLimited reports whether the global seed-ratio limit is enforced.
	SeedRatioLimited bool `json:"seed_ratio_limited"`

	// SeedRatioLimit is the global seed-ratio limit.
	SeedRatioLimit float64 `json:"seed_ratio_limit"`

	// IdleSeedingLimitEnabled reports whether idle seeds are stopped after a limit.
	IdleSeedingLimitEnabled bool `json:"idle_seeding_limit_enabled"`

	// IdleSeedingLimit is the idle-seeding limit, in minutes.
	IdleSeedingLimit int `json:"idle_seeding_limit"`

	// CacheSizeMiB is the disk cache size, in MiB.
	CacheSizeMiB int `json:"cache_size_mib"`

	// Network settings.

	// PeerPort is the port for incoming peer connections.
	PeerPort int `json:"peer_port"`

	// PeerPortRandomOnStart reports whether the peer port is randomized at start.
	PeerPortRandomOnStart bool `json:"peer_port_random_on_start"`

	// PortForwardingEnabled reports whether UPnP/NAT-PMP port forwarding is on.
	PortForwardingEnabled bool `json:"port_forwarding_enabled"`

	// Encryption is the peer-encryption mode: "allowed", "preferred", or
	// "required".
	Encryption string `json:"encryption"`

	// PeerLimitGlobal is the global limit on connected peers.
	PeerLimitGlobal int `json:"peer_limit_global"`

	// PeerLimitPerTorrent is the per-torrent limit on connected peers.
	PeerLimitPerTorrent int `json:"peer_limit_per_torrent"`

	// PexEnabled reports whether Peer Exchange is enabled.
	PexEnabled bool `json:"pex_enabled"`

	// DHTEnabled reports whether the Distributed Hash Table is enabled.
	DHTEnabled bool `json:"dht_enabled"`

	// LPDEnabled reports whether Local Peer Discovery is enabled.
	LPDEnabled bool `json:"lpd_enabled"`

	// UTPEnabled reports whether uTP (Micro Transport Protocol) is enabled.
	UTPEnabled bool `json:"utp_enabled"`

	// BlocklistEnabled reports whether the peer blocklist is enabled.
	BlocklistEnabled bool `json:"blocklist_enabled"`

	// BlocklistURL is the URL the blocklist is fetched from.
	BlocklistURL string `json:"blocklist_url"`

	// BlocklistSize is the number of rules in the blocklist (read-only).
	BlocklistSize int `json:"blocklist_size"`

	// Bandwidth settings.

	// SpeedLimitDownEnabled reports whether the global download limit is enforced.
	SpeedLimitDownEnabled bool `json:"speed_limit_down_enabled"`

	// SpeedLimitDown is the global download speed limit, in KB/s.
	SpeedLimitDown int64 `json:"speed_limit_down"`

	// SpeedLimitUpEnabled reports whether the global upload limit is enforced.
	SpeedLimitUpEnabled bool `json:"speed_limit_up_enabled"`

	// SpeedLimitUp is the global upload speed limit, in KB/s.
	SpeedLimitUp int64 `json:"speed_limit_up"`

	// AltSpeedEnabled reports whether the alternate speed limits are active.
	AltSpeedEnabled bool `json:"alt_speed_enabled"`

	// AltSpeedDown is the alternate download speed limit, in KB/s.
	AltSpeedDown int64 `json:"alt_speed_down"`

	// AltSpeedUp is the alternate upload speed limit, in KB/s.
	AltSpeedUp int64 `json:"alt_speed_up"`

	// AltSpeedTimeEnabled reports whether scheduled alt-speed times are used.
	AltSpeedTimeEnabled bool `json:"alt_speed_time_enabled"`

	// AltSpeedTimeBegin is when alt speeds turn on (minutes after midnight).
	AltSpeedTimeBegin int `json:"alt_speed_time_begin"`

	// AltSpeedTimeEnd is when alt speeds turn off (minutes after midnight).
	AltSpeedTimeEnd int `json:"alt_speed_time_end"`

	// AltSpeedTimeDay is the day bitmask for the alt-speed schedule.
	AltSpeedTimeDay int `json:"alt_speed_time_day"`

	// Queue settings.

	// DownloadQueueEnabled reports whether the download queue is enabled.
	DownloadQueueEnabled bool `json:"download_queue_enabled"`

	// DownloadQueueSize is the maximum number of simultaneously downloading torrents.
	DownloadQueueSize int `json:"download_queue_size"`

	// SeedQueueEnabled reports whether the seed queue is enabled.
	SeedQueueEnabled bool `json:"seed_queue_enabled"`

	// SeedQueueSize is the maximum number of simultaneously seeding torrents.
	SeedQueueSize int `json:"seed_queue_size"`

	// QueueStalledEnabled reports whether stalled torrents are ignored by the queue.
	QueueStalledEnabled bool `json:"queue_stalled_enabled"`

	// QueueStalledMinutes is the idle time before a torrent counts as stalled.
	QueueStalledMinutes int `json:"queue_stalled_minutes"`
}
