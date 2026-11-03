// This file contains the protocol abstraction: a Protocol interface with two
// implementations that own the differences between the two documented
// Transmission RPC schemes.
//
//   - JSON-RPC 2.0 (Transmission >= 4.1.0, rpc-version-semver >= 6.0.0): request
//     envelope {jsonrpc,method,params,id}, response {jsonrpc,result|error,id},
//     snake_case method names (session_get) and snake_case field names.
//   - Bespoke (Transmission <= 4.0.6): request envelope {method,arguments,tag},
//     response {result,arguments,tag}, kebab-case method names (session-get) and
//     a kebab-case/camelCase field mix.
//
// Each implementation owns its wire envelope, method naming, and field-name
// decoding, converting to shared canonical structs (e.g. Session) so the rest of
// the client calls one typed API regardless of Transmission version.
package rpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Protocol names returned in Session.Protocol and used to label the negotiated
// scheme.
const (
	// ProtocolJSONRPC is the JSON-RPC 2.0 scheme (Transmission >= 4.1.0).
	ProtocolJSONRPC = "jsonrpc-2.0"

	// ProtocolBespoke is the legacy bespoke scheme (Transmission <= 4.0.6).
	ProtocolBespoke = "bespoke"
)

// Method names per protocol. The two schemes spell the same operations
// differently (snake_case vs kebab-case).
const (
	methodSessionGetJSONRPC = "session_get"
	methodSessionGetBespoke = "session-get"

	methodSessionSetJSONRPC = "session_set"
	methodSessionSetBespoke = "session-set"

	methodSessionStatsJSONRPC = "session_stats"
	methodSessionStatsBespoke = "session-stats"

	methodTorrentGetJSONRPC = "torrent_get"
	methodTorrentGetBespoke = "torrent-get"

	methodTorrentAddJSONRPC = "torrent_add"
	methodTorrentAddBespoke = "torrent-add"

	methodTorrentStartJSONRPC = "torrent_start"
	methodTorrentStartBespoke = "torrent-start"

	methodTorrentStartNowJSONRPC = "torrent_start_now"
	methodTorrentStartNowBespoke = "torrent-start-now"

	methodTorrentStopJSONRPC = "torrent_stop"
	methodTorrentStopBespoke = "torrent-stop"

	methodTorrentVerifyJSONRPC = "torrent_verify"
	methodTorrentVerifyBespoke = "torrent-verify"

	methodTorrentReannounceJSONRPC = "torrent_reannounce"
	methodTorrentReannounceBespoke = "torrent-reannounce"

	methodTorrentRemoveJSONRPC = "torrent_remove"
	methodTorrentRemoveBespoke = "torrent-remove"

	methodTorrentSetLocationJSONRPC = "torrent_set_location"
	methodTorrentSetLocationBespoke = "torrent-set-location"

	methodTorrentRenamePathJSONRPC = "torrent_rename_path"
	methodTorrentRenamePathBespoke = "torrent-rename-path"

	methodTorrentSetJSONRPC = "torrent_set"
	methodTorrentSetBespoke = "torrent-set"

	methodQueueMoveTopJSONRPC = "queue_move_top"
	methodQueueMoveTopBespoke = "queue-move-top"

	methodQueueMoveUpJSONRPC = "queue_move_up"
	methodQueueMoveUpBespoke = "queue-move-up"

	methodQueueMoveDownJSONRPC = "queue_move_down"
	methodQueueMoveDownBespoke = "queue-move-down"

	methodQueueMoveBottomJSONRPC = "queue_move_bottom"
	methodQueueMoveBottomBespoke = "queue-move-bottom"

	methodPortTestJSONRPC = "port_test"
	methodPortTestBespoke = "port-test"

	methodFreeSpaceJSONRPC = "free_space"
	methodFreeSpaceBespoke = "free-space"
)

// ErrRPC signals that the server accepted the request at the transport level but
// returned an application error: a JSON-RPC error object, or a bespoke response
// whose result was not "success". The wrapped message carries the server text.
var ErrRPC = errors.New("rpc: server error")

// bespokeResultSuccess is the value the bespoke protocol's result field carries
// on success; any other value denotes an error.
const bespokeResultSuccess = "success"

// Session is the canonical, protocol-neutral result of a SessionGet call. Each
// Protocol implementation reads the correct per-scheme field names and fills
// this shared shape.
//
// The version/protocol fields are always populated. The session-settings fields
// are modeled as pointers so an absent field (a server that omits it) is
// distinguishable from a zero value; each Protocol decodes the per-scheme wire
// keys (snake_case for JSON-RPC 2.0, kebab-case for the bespoke scheme) into
// these shared fields. The settings carry snake_case JSON tags so the canonical
// shape is uniform regardless of the negotiated wire dialect.
type Session struct {
	// Version is the long Transmission version string, e.g. "4.0.6 (38c164933e)".
	Version string `json:"version"`

	// RPCVersionSemver is the semver-compatible RPC API version, e.g. "6.0.0".
	RPCVersionSemver string `json:"rpc_version_semver"`

	// Protocol is the negotiated protocol name (ProtocolJSONRPC or
	// ProtocolBespoke).
	Protocol string `json:"protocol"`

	// Download settings.

	// DownloadDir is the default download directory.
	DownloadDir *string `json:"download_dir,omitempty"`

	// StartAddedTorrents reports whether torrents start automatically when added.
	StartAddedTorrents *bool `json:"start_added_torrents,omitempty"`

	// RenamePartialFiles reports whether incomplete files get a ".part" suffix.
	RenamePartialFiles *bool `json:"rename_partial_files,omitempty"`

	// IncompleteDirEnabled reports whether incomplete torrents use a separate dir.
	IncompleteDirEnabled *bool `json:"incomplete_dir_enabled,omitempty"`

	// IncompleteDir is the directory for incomplete torrents.
	IncompleteDir *string `json:"incomplete_dir,omitempty"`

	// SeedRatioLimited reports whether the global seed-ratio limit is enforced.
	SeedRatioLimited *bool `json:"seed_ratio_limited,omitempty"`

	// SeedRatioLimit is the global seed-ratio limit.
	SeedRatioLimit *float64 `json:"seed_ratio_limit,omitempty"`

	// IdleSeedingLimitEnabled reports whether idle seeds are stopped after a limit.
	IdleSeedingLimitEnabled *bool `json:"idle_seeding_limit_enabled,omitempty"`

	// IdleSeedingLimit is the idle-seeding limit, in minutes.
	IdleSeedingLimit *int `json:"idle_seeding_limit,omitempty"`

	// CacheSizeMiB is the disk cache size, in MiB.
	CacheSizeMiB *int `json:"cache_size_mib,omitempty"`

	// Network settings.

	// PeerPort is the port for incoming peer connections.
	PeerPort *int `json:"peer_port,omitempty"`

	// PeerPortRandomOnStart reports whether the peer port is randomized at start.
	PeerPortRandomOnStart *bool `json:"peer_port_random_on_start,omitempty"`

	// PortForwardingEnabled reports whether UPnP/NAT-PMP port forwarding is on.
	PortForwardingEnabled *bool `json:"port_forwarding_enabled,omitempty"`

	// Encryption is the peer-encryption mode: "allowed", "preferred", or
	// "required" (canonical). The bespoke scheme spells "allowed" as "tolerated"
	// on the wire; the protocol normalizes it in both directions.
	Encryption *string `json:"encryption,omitempty"`

	// PeerLimitGlobal is the global limit on connected peers.
	PeerLimitGlobal *int `json:"peer_limit_global,omitempty"`

	// PeerLimitPerTorrent is the per-torrent limit on connected peers.
	PeerLimitPerTorrent *int `json:"peer_limit_per_torrent,omitempty"`

	// PexEnabled reports whether Peer Exchange is enabled.
	PexEnabled *bool `json:"pex_enabled,omitempty"`

	// DHTEnabled reports whether the Distributed Hash Table is enabled.
	DHTEnabled *bool `json:"dht_enabled,omitempty"`

	// LPDEnabled reports whether Local Peer Discovery is enabled.
	LPDEnabled *bool `json:"lpd_enabled,omitempty"`

	// UTPEnabled reports whether uTP (Micro Transport Protocol) is enabled.
	UTPEnabled *bool `json:"utp_enabled,omitempty"`

	// BlocklistEnabled reports whether the peer blocklist is enabled.
	BlocklistEnabled *bool `json:"blocklist_enabled,omitempty"`

	// BlocklistURL is the URL the blocklist is fetched from.
	BlocklistURL *string `json:"blocklist_url,omitempty"`

	// BlocklistSize is the number of rules in the blocklist (read-only).
	BlocklistSize *int `json:"blocklist_size,omitempty"`

	// Bandwidth settings.

	// SpeedLimitDownEnabled reports whether the global download limit is enforced.
	SpeedLimitDownEnabled *bool `json:"speed_limit_down_enabled,omitempty"`

	// SpeedLimitDown is the global download speed limit, in KB/s.
	SpeedLimitDown *int64 `json:"speed_limit_down,omitempty"`

	// SpeedLimitUpEnabled reports whether the global upload limit is enforced.
	SpeedLimitUpEnabled *bool `json:"speed_limit_up_enabled,omitempty"`

	// SpeedLimitUp is the global upload speed limit, in KB/s.
	SpeedLimitUp *int64 `json:"speed_limit_up,omitempty"`

	// AltSpeedEnabled reports whether the alternate speed limits are active.
	AltSpeedEnabled *bool `json:"alt_speed_enabled,omitempty"`

	// AltSpeedDown is the alternate download speed limit, in KB/s.
	AltSpeedDown *int64 `json:"alt_speed_down,omitempty"`

	// AltSpeedUp is the alternate upload speed limit, in KB/s.
	AltSpeedUp *int64 `json:"alt_speed_up,omitempty"`

	// AltSpeedTimeEnabled reports whether scheduled alt-speed times are used.
	AltSpeedTimeEnabled *bool `json:"alt_speed_time_enabled,omitempty"`

	// AltSpeedTimeBegin is when alt speeds turn on (minutes after midnight).
	AltSpeedTimeBegin *int `json:"alt_speed_time_begin,omitempty"`

	// AltSpeedTimeEnd is when alt speeds turn off (minutes after midnight).
	AltSpeedTimeEnd *int `json:"alt_speed_time_end,omitempty"`

	// AltSpeedTimeDay is the day bitmask for the alt-speed schedule.
	AltSpeedTimeDay *int `json:"alt_speed_time_day,omitempty"`

	// Queue settings.

	// DownloadQueueEnabled reports whether the download queue is enabled.
	DownloadQueueEnabled *bool `json:"download_queue_enabled,omitempty"`

	// DownloadQueueSize is the maximum number of simultaneously downloading torrents.
	DownloadQueueSize *int `json:"download_queue_size,omitempty"`

	// SeedQueueEnabled reports whether the seed queue is enabled.
	SeedQueueEnabled *bool `json:"seed_queue_enabled,omitempty"`

	// SeedQueueSize is the maximum number of simultaneously seeding torrents.
	SeedQueueSize *int `json:"seed_queue_size,omitempty"`

	// QueueStalledEnabled reports whether stalled torrents are ignored by the queue.
	QueueStalledEnabled *bool `json:"queue_stalled_enabled,omitempty"`

	// QueueStalledMinutes is the idle time before a torrent counts as stalled.
	QueueStalledMinutes *int `json:"queue_stalled_minutes,omitempty"`
}

// StatsSnapshot is the canonical, protocol-neutral shape of the cumulative and
// current stats objects nested in a SessionStats result. Each Protocol
// implementation reads the correct per-scheme field names and fills this shared
// shape.
type StatsSnapshot struct {
	// UploadedBytes is the total number of bytes uploaded in the snapshot.
	UploadedBytes int64 `json:"uploaded_bytes"`

	// DownloadedBytes is the total number of bytes downloaded in the snapshot.
	DownloadedBytes int64 `json:"downloaded_bytes"`

	// FilesAdded is the number of files added in the snapshot.
	FilesAdded int64 `json:"files_added"`

	// SessionCount is the number of sessions counted in the snapshot.
	SessionCount int64 `json:"session_count"`

	// SecondsActive is the number of seconds active in the snapshot.
	SecondsActive int64 `json:"seconds_active"`
}

// SessionStats is the canonical, protocol-neutral result of a SessionStats
// call. Each Protocol implementation reads the correct per-scheme field names
// and fills this shared shape.
type SessionStats struct {
	// ActiveTorrentCount is the number of active torrents.
	ActiveTorrentCount int64 `json:"active_torrent_count"`

	// PausedTorrentCount is the number of paused torrents.
	PausedTorrentCount int64 `json:"paused_torrent_count"`

	// TorrentCount is the total number of torrents.
	TorrentCount int64 `json:"torrent_count"`

	// DownloadSpeed is the current aggregate download speed in bytes/second.
	DownloadSpeed int64 `json:"download_speed"`

	// UploadSpeed is the current aggregate upload speed in bytes/second.
	UploadSpeed int64 `json:"upload_speed"`

	// CumulativeStats aggregates statistics across all sessions.
	CumulativeStats StatsSnapshot `json:"cumulative_stats"`

	// CurrentStats covers statistics for the current session only.
	CurrentStats StatsSnapshot `json:"current_stats"`
}

// PortTestResult is the canonical, protocol-neutral result of a PortTest call.
// Each Protocol implementation reads the correct per-scheme field names and
// fills this shared shape. IPProtocol is only populated by the JSON-RPC 2.0
// scheme (Transmission >= 4.1.0); the bespoke scheme returns only Open.
type PortTestResult struct {
	// Open reports whether the configured peer port is reachable from outside.
	Open bool `json:"open"`

	// IPProtocol is the IP protocol the test was carried out on ("ipv4" or
	// "ipv6"), or empty when the server did not report it (the bespoke scheme
	// never does, and JSON-RPC 2.0 omits it when it cannot be determined).
	IPProtocol string `json:"ip_protocol,omitempty"`
}

// FreeSpaceResult is the canonical, protocol-neutral result of a FreeSpace call.
// Each Protocol implementation reads the correct per-scheme field names and
// fills this shared shape. TotalBytes is only populated when the server reports
// it (Transmission >= 4.1.0); a server too old to return total_size leaves it 0.
type FreeSpaceResult struct {
	// Path is the directory the query ran on, echoed back by the server.
	Path string `json:"path"`

	// SizeBytes is the free space available in the directory, in bytes.
	SizeBytes int64 `json:"size_bytes"`

	// TotalBytes is the directory's total capacity, in bytes, or 0 (omitted) when
	// the server did not report it.
	TotalBytes int64 `json:"total_bytes,omitempty"`
}

// RenameResult is the server's echo of a completed torrent-rename-path call.
type RenameResult struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
	Name string `json:"name"`
}

// Torrent is the canonical, protocol-neutral result of one torrent in a
// TorrentGet call. Each Protocol implementation reads the correct per-scheme
// field names into a wire struct and converts it to this shape via toCanonical,
// which derives the Status enum from Transmission's raw status int and error
// fields. Its json tags are snatcher's stable output projection for
// --json/--jq, distinct from the per-protocol wire decode tags: the derived
// Status serializes as a string slug, and the raw status int and error code are
// not exposed (only the human-readable Error string is).
//
// Both the human-friendly numeric ID and the immutable hash are captured: the
// ID is the default displayed identifier, while the hash is available via
// --json and column selection for stable, server-independent identity.
type Torrent struct {
	// ID is the server's session-scoped numeric identifier. It is the default
	// displayed identifier.
	ID int64 `json:"id"`

	// Hash is the torrent's info-hash string, a stable identity independent of
	// the session-scoped ID.
	Hash string `json:"hash"`

	// Name is the torrent's display name.
	Name string `json:"name"`

	// Status is the derived lifecycle state.
	Status Status `json:"status"`

	// PercentDone is the download completion fraction in the range 0–1.
	PercentDone float64 `json:"percent_done"`

	// TotalSize is the total size of the torrent's contents, in bytes.
	TotalSize int64 `json:"total_size"`

	// SizeWhenDone is the number of bytes the selected files occupy when done.
	SizeWhenDone int64 `json:"size_when_done"`

	// LeftUntilDone is the number of bytes still to download.
	LeftUntilDone int64 `json:"left_until_done"`

	// RateDownload is the current download rate, in bytes/second.
	RateDownload int64 `json:"rate_download"`

	// RateUpload is the current upload rate, in bytes/second.
	RateUpload int64 `json:"rate_upload"`

	// UploadRatio is the ratio of bytes uploaded to bytes downloaded.
	UploadRatio float64 `json:"upload_ratio"`

	// ETA is the estimated seconds until download completes (negative = unknown).
	ETA int64 `json:"eta"`

	// PeersConnected is the number of peers currently connected.
	PeersConnected int64 `json:"peers_connected"`

	// PeersSendingToUs is the number of connected peers sending data to us.
	PeersSendingToUs int64 `json:"peers_sending_to_us"`

	// PeersGettingFromUs is the number of connected peers downloading from us.
	PeersGettingFromUs int64 `json:"peers_getting_from_us"`

	// AddedDate is the Unix timestamp (seconds) the torrent was added.
	AddedDate int64 `json:"added_date"`

	// DoneDate is the Unix timestamp (seconds) the torrent finished downloading.
	DoneDate int64 `json:"done_date"`

	// ActivityDate is the Unix timestamp (seconds) of the most recent activity.
	ActivityDate int64 `json:"activity_date"`

	// DownloadDir is the directory the torrent's data is downloaded into.
	DownloadDir string `json:"download_dir"`

	// Error is the human-readable error string, or empty when there is no error.
	Error string `json:"error"`

	// Labels are the user-assigned labels on the torrent.
	Labels []string `json:"labels"`

	// Trackers are the torrent's trackers, one per announce URL, reduced to
	// their hosts. The announce URL itself is never carried here: a private
	// tracker's URL embeds the user's passkey, and this struct is what
	// `list --json` emits.
	Trackers []TorrentTracker `json:"trackers"`

	// QueuePosition is the torrent's position in the server's queue.
	QueuePosition int64 `json:"queue_position"`

	// IsFinished reports whether seeding is complete.
	IsFinished bool `json:"is_finished"`

	// IsStalled reports whether the torrent has stalled.
	IsStalled bool `json:"is_stalled"`
}

// TorrentTracker is one of a torrent's trackers as the list read carries it:
// the announce URL's host, never the URL.
type TorrentTracker struct {
	// Host is the announce URL's lowercased hostname, without a port.
	Host string `json:"host"`

	// Sitename is the tracker's short site name, as the server derives it.
	Sitename string `json:"sitename"`

	// Tier is the tracker's tier, per BEP 12.
	Tier int `json:"tier"`
}

// wireTorrentTracker is one entry of torrent-get's "trackers" array. Both
// schemes spell it, and its keys, the same way.
type wireTorrentTracker struct {
	Announce string `json:"announce"`
	Sitename string `json:"sitename"`
	Tier     int    `json:"tier"`
}

// torrentTrackers reduces the wire trackers to their hosts. An announce URL
// that does not parse, or has no host, is dropped rather than carried whole,
// so nothing past the host can reach the canonical struct. The result is
// non-nil.
func torrentTrackers(wire []wireTorrentTracker) []TorrentTracker {
	out := make([]TorrentTracker, 0, len(wire))

	for _, w := range wire {
		u, err := url.Parse(w.Announce)
		if err != nil {
			continue
		}

		host := strings.ToLower(u.Hostname())
		if host == "" {
			continue
		}

		out = append(out, TorrentTracker{Host: host, Sitename: w.Sitename, Tier: w.Tier})
	}

	return out
}

// File is one file within a torrent, in a canonical TorrentDetail. Wanted and
// Priority come from the parallel torrent-get arrays (wanted, priorities); each
// Protocol normalizes the bespoke 0/1 wanted ints and JSON-RPC booleans to a
// Go bool.
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

// Peer is one connected peer for a torrent, in a canonical TorrentDetail.
type Peer struct {
	// Address is the peer's IP address.
	Address string `json:"address"`

	// Port is the peer's port.
	Port int `json:"port"`

	// Client is the peer's BitTorrent client name/version.
	Client string `json:"client"`

	// Progress is the peer's download completion fraction (0–1).
	Progress float64 `json:"progress"`

	// RateToClient is the rate we download from the peer, in bytes/second.
	RateToClient int64 `json:"rate_to_client"`

	// RateToPeer is the rate we upload to the peer, in bytes/second.
	RateToPeer int64 `json:"rate_to_peer"`

	// FlagStr is the compact per-peer status flag string.
	FlagStr string `json:"flag_str"`

	// IsEncrypted reports whether the peer connection is encrypted.
	IsEncrypted bool `json:"is_encrypted"`
}

// Tracker is one tracker for a torrent, in a canonical TorrentDetail. The fields
// come from Transmission's per-tracker trackerStats objects.
type Tracker struct {
	// Announce is the tracker's announce URL.
	Announce string `json:"announce"`

	// Host is the tracker's host (scheme + authority).
	Host string `json:"host"`

	// Sitename is the tracker's short site name.
	Sitename string `json:"sitename"`

	// Tier is the tracker's tier, per BEP 12: trackers in the same tier are
	// tried in random order, and a lower tier is tried before a higher one.
	Tier int `json:"tier"`

	// AnnounceState is the numeric announce state (Transmission's
	// tr_tracker_state). The package-level AnnounceState function maps it to a
	// human-readable name.
	AnnounceState int `json:"announce_state"`

	// SeederCount is the number of seeders the tracker reports (-1 = unknown).
	SeederCount int `json:"seeder_count"`

	// LeecherCount is the number of leechers the tracker reports (-1 = unknown).
	LeecherCount int `json:"leecher_count"`

	// DownloadCount is the completed-download count the tracker reports
	// (-1 = unknown).
	DownloadCount int `json:"download_count"`

	// LastAnnounceResult is the result of the last announce.
	LastAnnounceResult string `json:"last_announce_result"`

	// NextAnnounceTime is the Unix timestamp (seconds) of the next announce.
	NextAnnounceTime int64 `json:"next_announce_time"`
}

// TorrentDetail is the canonical detail view of a single torrent. It embeds the
// canonical Torrent (so the list fields are present) and adds the per-torrent
// collections and overview scalars, decoded identically from both wire
// protocols. Its json tags are snatcher's stable output projection, re-exported
// from internal/transmission as transmission.TorrentDetail.
type TorrentDetail struct {
	Torrent

	// Files are the torrent's files, in torrent order.
	Files []File `json:"files"`

	// Peers are the torrent's currently connected peers.
	Peers []Peer `json:"peers"`

	// Trackers are the torrent's trackers and their per-tracker stats.
	Trackers []Tracker `json:"trackers"`

	// Pieces is the have/have-not bitfield (bit i set means piece i is complete),
	// decoded from the base64 wire string.
	Pieces []bool `json:"pieces"`

	// MagnetLink is the torrent's magnet URI.
	MagnetLink string `json:"magnet_link"`

	// DownloadedEver is the total bytes ever downloaded.
	DownloadedEver int64 `json:"downloaded_ever"`

	// UploadedEver is the total bytes ever uploaded.
	UploadedEver int64 `json:"uploaded_ever"`

	// CorruptEver is the total bytes that failed verification.
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

	// IsPrivate reports whether the torrent is private.
	IsPrivate bool `json:"is_private"`

	// SecondsDownloading is the total time spent downloading, in seconds.
	SecondsDownloading int64 `json:"seconds_downloading"`

	// SecondsSeeding is the total time spent seeding, in seconds.
	SecondsSeeding int64 `json:"seconds_seeding"`

	// RecheckProgress is the verify (recheck) completion fraction (0–1).
	RecheckProgress float64 `json:"recheck_progress"`

	// MetadataPercentComplete is the metadata-download completion fraction (0–1).
	MetadataPercentComplete float64 `json:"metadata_percent_complete"`
}

// AddRequest carries the parameters for a TorrentAdd call. Each field maps to a
// torrent-add request parameter; empty fields are omitted on the wire
// (omitempty semantics) so the server applies its defaults. Either Metainfo or
// Filename must be set: Metainfo is base64-encoded .torrent content, Filename is
// a .torrent URL or magnet link.
type AddRequest struct {
	// Metainfo is the base64-encoded .torrent content (the server's metainfo
	// parameter).
	Metainfo string

	// Filename is a .torrent URL or magnet link (the server's filename
	// parameter).
	Filename string

	// DownloadDir is the server-side destination directory; empty uses the
	// server default.
	DownloadDir string

	// Paused, when true, adds the torrent without starting it.
	Paused bool

	// Labels are the user labels to assign to the torrent.
	Labels []string
}

// AddResult is the canonical, protocol-neutral result of a TorrentAdd call. Each
// Protocol implementation reads the correct per-scheme field names from the
// torrent-added/torrent-duplicate object and fills this shared shape.
type AddResult struct {
	// ID is the server's session-scoped numeric identifier for the torrent.
	ID int64 `json:"id"`

	// Name is the torrent's display name.
	Name string `json:"name"`

	// Hash is the torrent's info-hash string (hashString/hash_string).
	Hash string `json:"hash"`

	// Duplicate reports whether the torrent was already present: the server
	// returned a torrent-duplicate object rather than torrent-added.
	Duplicate bool `json:"duplicate"`
}

// RawResponse is the canonical, protocol-neutral envelope returned by Call. It
// carries both the exact server response bytes (for verbatim --raw output) and
// the normalized fields that are identical across the two wire dialects, so a
// generic caller renders one stable shape regardless of server version.
//
// OK distinguishes an RPC-level failure from success: an RPC-level failure is
// reported via OK == false (with Result/Code carrying the server's error) rather
// than as a Go error, so the caller can print the envelope and set its own exit
// code. Transport, auth, and decode failures still surface as Go errors from
// Call.
type RawResponse struct {
	// Raw is the exact response body the server sent, for verbatim --raw output.
	Raw json.RawMessage `json:"-"`

	// Result is the normalized status string: "success" on success, or the
	// server's error text on an RPC-level failure.
	Result string `json:"result"`

	// Arguments is the normalized result payload: the server's arguments/result
	// object on success, or null on an RPC-level failure.
	Arguments json.RawMessage `json:"arguments"`

	// Tag is the normalized request tag/id echoed by the server.
	Tag int `json:"tag"`

	// Code is the error code on an RPC-level failure (JSON-RPC error.code); it is
	// omitted from the normalized JSON when zero.
	Code int `json:"code,omitempty"`

	// OK reports whether the call succeeded at the RPC level (bespoke result ==
	// "success", or a JSON-RPC response without an error object).
	OK bool `json:"-"`
}

// transport is the subset of *Transport a Protocol needs to perform a request.
// Defining it as an interface keeps the protocol implementations testable in
// isolation from the HTTP layer.
type transport interface {
	Do(ctx context.Context, body []byte) ([]byte, error)
}

// Protocol abstracts the wire scheme used to talk to a Transmission server. Each
// implementation owns its request envelope, method naming, and response decoding.
type Protocol interface {
	// Name reports the protocol's canonical name (ProtocolJSONRPC or
	// ProtocolBespoke).
	Name() string

	// SessionGet calls the server's session-get/session_get method and returns a
	// canonical Session.
	SessionGet(ctx context.Context, t transport) (Session, error)

	// SessionSet calls the server's session-set/session_set method to write
	// session settings. settings is a snake_case-keyed map of canonical setting
	// keys to typed values; each implementation translates the keys to its wire
	// scheme (verbatim snake_case for JSON-RPC 2.0, snake→kebab for the bespoke
	// scheme) and normalizes the encryption enum. The method carries no response
	// payload, so the call only confirms success.
	SessionSet(ctx context.Context, t transport, settings map[string]any) error

	// SessionStats calls the server's session-stats/session_stats method and
	// returns canonical SessionStats.
	SessionStats(ctx context.Context, t transport) (SessionStats, error)

	// TorrentGet calls the server's torrent-get/torrent_get method and returns
	// the canonical Torrents. A nil or empty ids slice requests all torrents;
	// otherwise the mixed id/hash array selects torrents. fields is a list of
	// canonical snake_case field keys (see torrentGetCanonicalFields); each
	// implementation translates them to its wire scheme, and an empty fields
	// slice requests the full canonical set.
	TorrentGet(ctx context.Context, t transport, ids []any, fields []string) ([]Torrent, error)

	// TorrentGetDetail calls the server's torrent-get/torrent_get method
	// requesting the full detail field set (the list fields plus files, peers,
	// trackerStats, pieces, magnetLink, and the overview scalars) and returns the
	// canonical TorrentDetails. ids selects the torrents (mixed id/hash; a nil or
	// empty slice requests all). Each implementation decodes the per-scheme wire
	// names and the parity traps (the bespoke 0/1 wanted ints vs JSON-RPC
	// booleans, the base64 pieces bitfield) into the shared shape.
	TorrentGetDetail(ctx context.Context, t transport, ids []any) ([]TorrentDetail, error)

	// TorrentAdd calls the server's torrent-add/torrent_add method, sending only
	// the non-empty parameters in req, and returns the canonical AddResult. A
	// torrent that was already present is reported via AddResult.Duplicate rather
	// than as an error.
	TorrentAdd(ctx context.Context, t transport, req AddRequest) (AddResult, error)

	// TorrentStart calls the server's torrent-start/torrent_start method (or the
	// *-start-now variant when now is true, which disregards queue position). A
	// nil or empty ids slice targets all torrents; otherwise the mixed id/hash
	// array selects torrents. These methods carry no response payload, so the
	// call only confirms success.
	TorrentStart(ctx context.Context, t transport, ids []any, now bool) error

	// TorrentStop calls the server's torrent-stop/torrent_stop method. A nil or
	// empty ids slice targets all torrents; otherwise the mixed id/hash array
	// selects torrents. The method carries no response payload, so the call only
	// confirms success.
	TorrentStop(ctx context.Context, t transport, ids []any) error

	// TorrentVerify calls the server's torrent-verify/torrent_verify method,
	// queuing a re-hash of downloaded files against the torrent metadata. A nil or
	// empty ids slice targets all torrents; otherwise the mixed id/hash array
	// selects torrents. The method carries no response payload, so the call only
	// confirms success.
	TorrentVerify(ctx context.Context, t transport, ids []any) error

	// TorrentReannounce calls the server's torrent-reannounce/torrent_reannounce
	// method, forcing an immediate re-announce to the trackers. A nil or empty ids
	// slice targets all torrents; otherwise the mixed id/hash array selects
	// torrents. The method carries no response payload, so the call only confirms
	// success.
	TorrentReannounce(ctx context.Context, t transport, ids []any) error

	// QueueMoveTop calls the server's queue-move-top/queue_move_top method, moving
	// the selected torrents to the top of the queue. A nil or empty ids slice
	// targets all torrents; otherwise the mixed id/hash array selects torrents. The
	// method carries no response payload, so the call only confirms success.
	QueueMoveTop(ctx context.Context, t transport, ids []any) error

	// QueueMoveUp calls the server's queue-move-up/queue_move_up method, moving the
	// selected torrents up one position in the queue. A nil or empty ids slice
	// targets all torrents; otherwise the mixed id/hash array selects torrents. The
	// method carries no response payload, so the call only confirms success.
	QueueMoveUp(ctx context.Context, t transport, ids []any) error

	// QueueMoveDown calls the server's queue-move-down/queue_move_down method,
	// moving the selected torrents down one position in the queue. A nil or empty
	// ids slice targets all torrents; otherwise the mixed id/hash array selects
	// torrents. The method carries no response payload, so the call only confirms
	// success.
	QueueMoveDown(ctx context.Context, t transport, ids []any) error

	// QueueMoveBottom calls the server's queue-move-bottom/queue_move_bottom
	// method, moving the selected torrents to the bottom of the queue. A nil or
	// empty ids slice targets all torrents; otherwise the mixed id/hash array
	// selects torrents. The method carries no response payload, so the call only
	// confirms success.
	QueueMoveBottom(ctx context.Context, t transport, ids []any) error

	// TorrentRemove calls the server's torrent-remove/torrent_remove method. A nil
	// or empty ids slice targets all torrents; otherwise the mixed id/hash array
	// selects torrents. The delete-local-data/delete_local_data flag is always
	// sent: when deleteData is true the server also deletes the downloaded files.
	// The method carries no response payload, so the call only confirms success.
	TorrentRemove(ctx context.Context, t transport, ids []any, deleteData bool) error

	// TorrentSetLocation calls the server's torrent-set-location/torrent_set_location
	// method, changing the download location of the selected torrents. A nil or
	// empty ids slice targets all torrents; otherwise the mixed id/hash array
	// selects torrents. location is the new path (always sent), and move is always
	// sent: when move is true the server relocates the existing files, otherwise it
	// repoints to a path where the files already exist and searches there. The
	// method carries no response payload, so the call only confirms success.
	TorrentSetLocation(ctx context.Context, t transport, ids []any, location string, move bool) error

	// TorrentRenamePath calls the server's torrent-rename-path/torrent_rename_path
	// method, renaming the file or directory at the torrent-relative path to name.
	// Renaming the torrent's root path renames the torrent itself. Unlike every
	// other mutation here, ids is always sent even when empty: an omitted ids means
	// "all torrents", and this method accepts exactly one. The server echoes the
	// torrent id, the renamed path, and the new name in a RenameResult.
	TorrentRenamePath(ctx context.Context, t transport, ids []any, path, name string) (RenameResult, error)

	// TorrentSet calls the server's torrent-set/torrent_set method to write
	// per-torrent fields. A nil or empty ids slice targets all torrents;
	// otherwise the mixed id/hash array selects torrents. settings is a
	// snake_case-keyed map of canonical field keys to typed values; each
	// implementation translates the keys to its wire scheme (verbatim snake_case
	// for JSON-RPC 2.0, snake→kebab with documented camelCase exceptions for the
	// bespoke scheme). Only the keys present in settings are written. The method
	// carries no response payload, so the call only confirms success.
	TorrentSet(ctx context.Context, t transport, ids []any, settings map[string]any) error

	// Call issues an arbitrary RPC method with raw arguments and returns the
	// canonical RawResponse. method is the canonical kebab-case name (each
	// implementation translates it for its wire scheme); args is the raw
	// arguments/params object (nil for a zero-argument method). An RPC-level
	// failure is reported via RawResponse.OK == false, not a Go error; only
	// transport and JSON-decode failures return a Go error.
	Call(ctx context.Context, t transport, method string, args json.RawMessage) (RawResponse, error)

	// PortTest calls the server's port-test/port_test method, which checks
	// whether the configured peer port is reachable from outside, and returns
	// the canonical PortTestResult. The JSON-RPC 2.0 scheme also reports the IP
	// protocol the test ran on; the bespoke scheme returns only the open flag.
	PortTest(ctx context.Context, t transport) (PortTestResult, error)

	// FreeSpace calls the server's free-space/free_space method with the given
	// path and returns the canonical FreeSpaceResult. A server >= 4.1.0 also
	// reports the directory's total capacity; an older server returns only the
	// free size, leaving FreeSpaceResult.TotalBytes 0.
	FreeSpace(ctx context.Context, t transport, path string) (FreeSpaceResult, error)
}

// jsonrpcProtocol implements the JSON-RPC 2.0 scheme.
type jsonrpcProtocol struct{}

// jsonrpcRequest is the JSON-RPC 2.0 request envelope. Transmission requires
// params to be an Object (by-position params are unsupported).
type jsonrpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      int    `json:"id"`
}

// jsonrpcError is the JSON-RPC 2.0 Error object.
type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// jsonrpcResponse is the JSON-RPC 2.0 response envelope. result is captured raw
// so each method decodes its own result shape.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonrpcError   `json:"error"`
	ID      int             `json:"id"`
}

// jsonrpcSessionResult is the snake_case session_get result shape. The settings
// fields share the snake_case JSON tags of canonical Session, so the result
// unmarshals directly into the corresponding canonical fields.
type jsonrpcSessionResult struct {
	Version          string `json:"version"`
	RPCVersionSemver string `json:"rpc_version_semver"`

	DownloadDir             *string  `json:"download_dir"`
	StartAddedTorrents      *bool    `json:"start_added_torrents"`
	RenamePartialFiles      *bool    `json:"rename_partial_files"`
	IncompleteDirEnabled    *bool    `json:"incomplete_dir_enabled"`
	IncompleteDir           *string  `json:"incomplete_dir"`
	SeedRatioLimited        *bool    `json:"seed_ratio_limited"`
	SeedRatioLimit          *float64 `json:"seed_ratio_limit"`
	IdleSeedingLimitEnabled *bool    `json:"idle_seeding_limit_enabled"`
	IdleSeedingLimit        *int     `json:"idle_seeding_limit"`
	CacheSizeMiB            *int     `json:"cache_size_mb"`

	PeerPort              *int    `json:"peer_port"`
	PeerPortRandomOnStart *bool   `json:"peer_port_random_on_start"`
	PortForwardingEnabled *bool   `json:"port_forwarding_enabled"`
	Encryption            *string `json:"encryption"`
	PeerLimitGlobal       *int    `json:"peer_limit_global"`
	PeerLimitPerTorrent   *int    `json:"peer_limit_per_torrent"`
	PexEnabled            *bool   `json:"pex_enabled"`
	DHTEnabled            *bool   `json:"dht_enabled"`
	LPDEnabled            *bool   `json:"lpd_enabled"`
	UTPEnabled            *bool   `json:"utp_enabled"`
	BlocklistEnabled      *bool   `json:"blocklist_enabled"`
	BlocklistURL          *string `json:"blocklist_url"`
	BlocklistSize         *int    `json:"blocklist_size"`

	SpeedLimitDownEnabled *bool  `json:"speed_limit_down_enabled"`
	SpeedLimitDown        *int64 `json:"speed_limit_down"`
	SpeedLimitUpEnabled   *bool  `json:"speed_limit_up_enabled"`
	SpeedLimitUp          *int64 `json:"speed_limit_up"`
	AltSpeedEnabled       *bool  `json:"alt_speed_enabled"`
	AltSpeedDown          *int64 `json:"alt_speed_down"`
	AltSpeedUp            *int64 `json:"alt_speed_up"`
	AltSpeedTimeEnabled   *bool  `json:"alt_speed_time_enabled"`
	AltSpeedTimeBegin     *int   `json:"alt_speed_time_begin"`
	AltSpeedTimeEnd       *int   `json:"alt_speed_time_end"`
	AltSpeedTimeDay       *int   `json:"alt_speed_time_day"`

	DownloadQueueEnabled *bool `json:"download_queue_enabled"`
	DownloadQueueSize    *int  `json:"download_queue_size"`
	SeedQueueEnabled     *bool `json:"seed_queue_enabled"`
	SeedQueueSize        *int  `json:"seed_queue_size"`
	QueueStalledEnabled  *bool `json:"queue_stalled_enabled"`
	QueueStalledMinutes  *int  `json:"queue_stalled_minutes"`
}

// sessionGetFieldsJSONRPC lists the snake_case session_get fields snatcher
// requests: the two version/protocol fields plus every session-settings key.
var sessionGetFieldsJSONRPC = []string{
	"version", "rpc_version_semver",
	"download_dir", "start_added_torrents", "rename_partial_files",
	"incomplete_dir_enabled", "incomplete_dir", "seed_ratio_limited",
	"seed_ratio_limit", "idle_seeding_limit_enabled", "idle_seeding_limit",
	"cache_size_mb",
	"peer_port", "peer_port_random_on_start", "port_forwarding_enabled",
	"encryption", "peer_limit_global", "peer_limit_per_torrent", "pex_enabled",
	"dht_enabled", "lpd_enabled", "utp_enabled", "blocklist_enabled",
	"blocklist_url", "blocklist_size",
	"speed_limit_down_enabled", "speed_limit_down", "speed_limit_up_enabled",
	"speed_limit_up", "alt_speed_enabled", "alt_speed_down", "alt_speed_up",
	"alt_speed_time_enabled", "alt_speed_time_begin", "alt_speed_time_end",
	"alt_speed_time_day",
	"download_queue_enabled", "download_queue_size", "seed_queue_enabled",
	"seed_queue_size", "queue_stalled_enabled", "queue_stalled_minutes",
}

// jsonrpcStatsSnapshot is the snake_case nested stats object shape.
type jsonrpcStatsSnapshot struct {
	UploadedBytes   int64 `json:"uploaded_bytes"`
	DownloadedBytes int64 `json:"downloaded_bytes"`
	FilesAdded      int64 `json:"files_added"`
	SessionCount    int64 `json:"session_count"`
	SecondsActive   int64 `json:"seconds_active"`
}

// jsonrpcStatsResult is the snake_case session_stats result shape.
type jsonrpcStatsResult struct {
	ActiveTorrentCount int64                `json:"active_torrent_count"`
	PausedTorrentCount int64                `json:"paused_torrent_count"`
	TorrentCount       int64                `json:"torrent_count"`
	DownloadSpeed      int64                `json:"download_speed"`
	UploadSpeed        int64                `json:"upload_speed"`
	CumulativeStats    jsonrpcStatsSnapshot `json:"cumulative_stats"`
	CurrentStats       jsonrpcStatsSnapshot `json:"current_stats"`
}

// jsonrpcTorrent is the snake_case torrent_get torrent shape.
type jsonrpcTorrent struct {
	ID                 int64                `json:"id"`
	Hash               string               `json:"hash_string"`
	Name               string               `json:"name"`
	Status             int                  `json:"status"`
	PercentDone        float64              `json:"percent_done"`
	TotalSize          int64                `json:"total_size"`
	SizeWhenDone       int64                `json:"size_when_done"`
	LeftUntilDone      int64                `json:"left_until_done"`
	RateDownload       int64                `json:"rate_download"`
	RateUpload         int64                `json:"rate_upload"`
	UploadRatio        float64              `json:"upload_ratio"`
	ETA                int64                `json:"eta"`
	PeersConnected     int64                `json:"peers_connected"`
	PeersSendingToUs   int64                `json:"peers_sending_to_us"`
	PeersGettingFromUs int64                `json:"peers_getting_from_us"`
	AddedDate          int64                `json:"added_date"`
	DoneDate           int64                `json:"done_date"`
	ActivityDate       int64                `json:"activity_date"`
	DownloadDir        string               `json:"download_dir"`
	ErrorCode          int                  `json:"error"`
	Error              string               `json:"error_string"`
	Labels             []string             `json:"labels"`
	Trackers           []wireTorrentTracker `json:"trackers"`
	QueuePosition      int64                `json:"queue_position"`
	IsFinished         bool                 `json:"is_finished"`
	IsStalled          bool                 `json:"is_stalled"`
}

// toCanonical converts a decoded JSON-RPC torrent into the canonical Torrent,
// deriving the Status enum from the raw status int and error fields (a non-zero
// error code or non-empty error string promotes to StatusError).
func (t jsonrpcTorrent) toCanonical() Torrent {
	return Torrent{
		ID:                 t.ID,
		Hash:               t.Hash,
		Name:               t.Name,
		Status:             StatusFromTransmission(t.Status, t.ErrorCode, t.Error),
		PercentDone:        t.PercentDone,
		TotalSize:          t.TotalSize,
		SizeWhenDone:       t.SizeWhenDone,
		LeftUntilDone:      t.LeftUntilDone,
		RateDownload:       t.RateDownload,
		RateUpload:         t.RateUpload,
		UploadRatio:        t.UploadRatio,
		ETA:                t.ETA,
		PeersConnected:     t.PeersConnected,
		PeersSendingToUs:   t.PeersSendingToUs,
		PeersGettingFromUs: t.PeersGettingFromUs,
		AddedDate:          t.AddedDate,
		DoneDate:           t.DoneDate,
		ActivityDate:       t.ActivityDate,
		DownloadDir:        t.DownloadDir,
		Error:              t.Error,
		Labels:             t.Labels,
		Trackers:           torrentTrackers(t.Trackers),
		QueuePosition:      t.QueuePosition,
		IsFinished:         t.IsFinished,
		IsStalled:          t.IsStalled,
	}
}

// jsonrpcTorrentResult is the snake_case torrent_get result shape.
type jsonrpcTorrentResult struct {
	Torrents []jsonrpcTorrent `json:"torrents"`
}

// wantedFlag decodes a per-file "wanted" entry from either wire scheme: the
// JSON-RPC 2.0 scheme serializes it as a JSON boolean, while the bespoke scheme
// (for backwards compatibility) serializes it as a 0/1 integer. Both decode to a
// Go bool (non-zero / true means the file is wanted).
type wantedFlag bool

// UnmarshalJSON accepts either a JSON boolean (true/false) or a 0/1 number,
// normalizing both to a bool so the two protocols decode identically.
func (w *wantedFlag) UnmarshalJSON(data []byte) error {
	switch string(bytes.TrimSpace(data)) {
	case "true", "1":
		*w = true
	case "false", "0", "null":
		*w = false
	default:
		return fmt.Errorf("rpc: decoding wanted flag: unexpected value %q", data)
	}

	return nil
}

// jsonrpcDetailFields lists the snake_case torrent_get fields the JSON-RPC 2.0
// detail read requests: the full canonical list (translated to the wire names)
// plus the detail-only fields (files, peers, tracker_stats, pieces, magnet_link,
// and the overview scalars). It is computed once from the canonical list so the
// list and detail reads stay in sync.
var jsonrpcDetailFields = append(
	resolveTorrentGetFields(nil, torrentGetFieldsJSONRPCMap),
	"files", "file_stats", "wanted", "priorities", "peers", "tracker_stats",
	"pieces", "magnet_link", "downloaded_ever", "uploaded_ever", "corrupt_ever",
	"have_valid", "piece_count", "piece_size", "comment", "creator",
	"date_created", "is_private", "seconds_downloading", "seconds_seeding",
	"recheck_progress", "metadata_percent_complete",
)

// jsonrpcDetailFile is the snake_case files entry (constant per-file properties).
type jsonrpcDetailFile struct {
	Name           string `json:"name"`
	Length         int64  `json:"length"`
	BytesCompleted int64  `json:"bytes_completed"`
}

// jsonrpcDetailPeer is the snake_case peers entry.
type jsonrpcDetailPeer struct {
	Address      string  `json:"address"`
	Port         int     `json:"port"`
	Client       string  `json:"client_name"`
	Progress     float64 `json:"progress"`
	RateToClient int64   `json:"rate_to_client"`
	RateToPeer   int64   `json:"rate_to_peer"`
	FlagStr      string  `json:"flag_str"`
	IsEncrypted  bool    `json:"is_encrypted"`
}

// jsonrpcDetailTracker is the snake_case tracker_stats entry.
type jsonrpcDetailTracker struct {
	Announce           string `json:"announce"`
	Host               string `json:"host"`
	Sitename           string `json:"sitename"`
	Tier               int    `json:"tier"`
	AnnounceState      int    `json:"announce_state"`
	SeederCount        int    `json:"seeder_count"`
	LeecherCount       int    `json:"leecher_count"`
	DownloadCount      int    `json:"download_count"`
	LastAnnounceResult string `json:"last_announce_result"`
	NextAnnounceTime   int64  `json:"next_announce_time"`
}

// jsonrpcDetailTorrent is the snake_case torrent_get detail torrent shape: the
// base list fields plus the detail-only fields. The parallel wanted/priorities
// arrays are merged into Files by index.
type jsonrpcDetailTorrent struct {
	jsonrpcTorrent

	Files      []jsonrpcDetailFile    `json:"files"`
	FileStats  []jsonrpcDetailFile    `json:"file_stats"`
	Wanted     []wantedFlag           `json:"wanted"`
	Priorities []int                  `json:"priorities"`
	Peers      []jsonrpcDetailPeer    `json:"peers"`
	Trackers   []jsonrpcDetailTracker `json:"tracker_stats"`
	Pieces     string                 `json:"pieces"`

	MagnetLink              string  `json:"magnet_link"`
	DownloadedEver          int64   `json:"downloaded_ever"`
	UploadedEver            int64   `json:"uploaded_ever"`
	CorruptEver             int64   `json:"corrupt_ever"`
	HaveValid               int64   `json:"have_valid"`
	PieceCount              int64   `json:"piece_count"`
	PieceSize               int64   `json:"piece_size"`
	Comment                 string  `json:"comment"`
	Creator                 string  `json:"creator"`
	DateCreated             int64   `json:"date_created"`
	IsPrivate               bool    `json:"is_private"`
	SecondsDownloading      int64   `json:"seconds_downloading"`
	SecondsSeeding          int64   `json:"seconds_seeding"`
	RecheckProgress         float64 `json:"recheck_progress"`
	MetadataPercentComplete float64 `json:"metadata_percent_complete"`
}

// jsonrpcDetailResult is the snake_case torrent_get detail result shape.
type jsonrpcDetailResult struct {
	Torrents []jsonrpcDetailTorrent `json:"torrents"`
}

// jsonrpcFilesToCanonical converts the snake_case files/file_stats entries to
// canonical Files carrying only the constant per-file properties; the
// wanted/priority state is merged in separately by mergeFiles.
func jsonrpcFilesToCanonical(in []jsonrpcDetailFile) []File {
	out := make([]File, len(in))
	for i, f := range in {
		out[i] = File{Name: f.Name, Length: f.Length, BytesCompleted: f.BytesCompleted}
	}

	return out
}

// jsonrpcAddParams is the snake_case torrent_add request params shape. Empty
// fields are omitted so the server applies its defaults — except paused, which
// is always sent: omitting paused=false would defer to the server's
// start-added-torrents setting and silently add the torrent stopped.
type jsonrpcAddParams struct {
	Metainfo    string   `json:"metainfo,omitempty"`
	Filename    string   `json:"filename,omitempty"`
	DownloadDir string   `json:"download_dir,omitempty"`
	Paused      bool     `json:"paused"`
	Labels      []string `json:"labels,omitempty"`
}

// jsonrpcAddedTorrent is the snake_case torrent object returned in a
// torrent_added/torrent_duplicate result.
type jsonrpcAddedTorrent struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Hash string `json:"hash_string"`
}

// jsonrpcAddResult is the snake_case torrent_add result shape. Exactly one of
// the two objects is present: torrent_added on a new add, torrent_duplicate
// when the torrent was already present.
type jsonrpcAddResult struct {
	Added     *jsonrpcAddedTorrent `json:"torrent_added"`
	Duplicate *jsonrpcAddedTorrent `json:"torrent_duplicate"`
}

// torrentGetCanonicalFields lists every canonical torrent_get field snatcher can
// request, in a stable order. The names are the protocol-neutral snake_case keys
// callers pass in TorrentGet's fields argument; each protocol's
// torrentGetFields*Map translates them to its own wire spelling. An empty fields
// argument defaults to this full set.
var torrentGetCanonicalFields = []string{
	"id", "hash", "name", "status", "percent_done", "total_size",
	"size_when_done", "left_until_done", "rate_download", "rate_upload",
	"upload_ratio", "eta", "peers_connected", "peers_sending_to_us",
	"peers_getting_from_us", "added_date", "done_date", "activity_date",
	"download_dir", "error", "labels", "trackers", "queue_position",
	"is_finished", "is_stalled",
}

// torrentGetFieldsJSONRPCMap maps each canonical torrent_get field to the
// snake_case wire field(s) the JSON-RPC 2.0 scheme requests. The mapping is
// mostly verbatim; the canonical "hash" becomes "hash_string", and the canonical
// "error" expands to both the numeric "error" code and its "error_string"
// message.
var torrentGetFieldsJSONRPCMap = map[string][]string{
	"id":                    {"id"},
	"hash":                  {"hash_string"},
	"name":                  {"name"},
	"status":                {"status"},
	"percent_done":          {"percent_done"},
	"total_size":            {"total_size"},
	"size_when_done":        {"size_when_done"},
	"left_until_done":       {"left_until_done"},
	"rate_download":         {"rate_download"},
	"rate_upload":           {"rate_upload"},
	"upload_ratio":          {"upload_ratio"},
	"eta":                   {"eta"},
	"peers_connected":       {"peers_connected"},
	"peers_sending_to_us":   {"peers_sending_to_us"},
	"peers_getting_from_us": {"peers_getting_from_us"},
	"added_date":            {"added_date"},
	"done_date":             {"done_date"},
	"activity_date":         {"activity_date"},
	"download_dir":          {"download_dir"},
	"error":                 {"error", "error_string"},
	"labels":                {"labels"},
	"trackers":              {"trackers"},
	"queue_position":        {"queue_position"},
	"is_finished":           {"is_finished"},
	"is_stalled":            {"is_stalled"},
}

// resolveTorrentGetFields translates a list of canonical torrent_get fields to
// the wire field names for a protocol, using wireMap. An empty fields argument
// requests the full canonical set. Unknown canonical fields are skipped. The
// result preserves request order and is de-duplicated, so a field that expands to
// several wire names (or is listed twice) is emitted once.
func resolveTorrentGetFields(fields []string, wireMap map[string][]string) []string {
	if len(fields) == 0 {
		fields = torrentGetCanonicalFields
	}

	wire := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, canonical := range fields {
		for _, name := range wireMap[canonical] {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			wire = append(wire, name)
		}
	}

	return wire
}

// decodePiecesBitfield decodes the base64 "pieces" bitfield into a per-piece
// have/have-not slice: bit i (counting most-significant-bit first within each
// byte, matching Transmission's encoding) maps to element i. pieceCount bounds
// the result, since the final byte may carry padding bits past the last piece.
// An empty string, or a value that is not valid base64, yields a nil slice
// rather than an error — a malformed bitfield should not fail the whole detail
// read. The result length is bounded by the decoded payload (len(raw)*8), so a
// bogus pieceCount cannot drive an unbounded allocation.
func decodePiecesBitfield(encoded string, pieceCount int64) []bool {
	if encoded == "" || pieceCount <= 0 {
		return nil
	}

	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil
	}

	// Cap the preallocation to what the decoded bitfield can actually hold:
	// pieceCount is untrusted server input, so a malformed or malicious value
	// must not drive an allocation larger than the decoded payload (the loop
	// already stops at the end of raw). bits never grows past len(raw)*8.
	bits := make([]bool, 0, min(pieceCount, int64(len(raw))*8))
	for i := int64(0); i < pieceCount; i++ {
		byteIdx := i / 8
		if byteIdx >= int64(len(raw)) {
			break
		}

		bitMask := byte(1) << (7 - uint(i%8))
		bits = append(bits, raw[byteIdx]&bitMask != 0)
	}

	return bits
}

// mergeFiles combines the parallel torrent-get detail arrays into canonical
// Files. files holds the constant per-file properties (name, length); the
// completed-bytes value is taken from fileStats when present (it carries the
// non-constant properties) and otherwise from files. wanted and priorities are
// index-aligned with files. Entries are merged up to the length of files; any
// shorter parallel array leaves the remaining files at their zero wanted/priority.
func mergeFiles(files, fileStats []File, wanted []bool, priorities []int) []File {
	if len(files) == 0 {
		return nil
	}

	merged := make([]File, len(files))
	for i, f := range files {
		merged[i] = File{Name: f.Name, Length: f.Length, BytesCompleted: f.BytesCompleted}

		if i < len(fileStats) {
			merged[i].BytesCompleted = fileStats[i].BytesCompleted
		}
		if i < len(wanted) {
			merged[i].Wanted = wanted[i]
		}
		if i < len(priorities) {
			merged[i].Priority = priorities[i]
		}
	}

	return merged
}

func (jsonrpcProtocol) Name() string { return ProtocolJSONRPC }

func (p jsonrpcProtocol) SessionGet(ctx context.Context, t transport) (Session, error) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  methodSessionGetJSONRPC,
		Params:  map[string]any{"fields": sessionGetFieldsJSONRPC},
		ID:      1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return Session{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return Session{}, err
	}

	var resp jsonrpcResponse
	if err := decodeResponse("session_get", respBody, &resp); err != nil {
		return Session{}, err
	}

	if resp.Error != nil {
		return Session{}, fmt.Errorf("%w: %s", ErrRPC, resp.Error.Message)
	}

	var result jsonrpcSessionResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return Session{}, fmt.Errorf("rpc: decoding session_get result: %w", err)
	}

	return Session{
		Version:          result.Version,
		RPCVersionSemver: result.RPCVersionSemver,
		Protocol:         ProtocolJSONRPC,

		DownloadDir:             result.DownloadDir,
		StartAddedTorrents:      result.StartAddedTorrents,
		RenamePartialFiles:      result.RenamePartialFiles,
		IncompleteDirEnabled:    result.IncompleteDirEnabled,
		IncompleteDir:           result.IncompleteDir,
		SeedRatioLimited:        result.SeedRatioLimited,
		SeedRatioLimit:          result.SeedRatioLimit,
		IdleSeedingLimitEnabled: result.IdleSeedingLimitEnabled,
		IdleSeedingLimit:        result.IdleSeedingLimit,
		CacheSizeMiB:            result.CacheSizeMiB,

		PeerPort:              result.PeerPort,
		PeerPortRandomOnStart: result.PeerPortRandomOnStart,
		PortForwardingEnabled: result.PortForwardingEnabled,
		Encryption:            result.Encryption,
		PeerLimitGlobal:       result.PeerLimitGlobal,
		PeerLimitPerTorrent:   result.PeerLimitPerTorrent,
		PexEnabled:            result.PexEnabled,
		DHTEnabled:            result.DHTEnabled,
		LPDEnabled:            result.LPDEnabled,
		UTPEnabled:            result.UTPEnabled,
		BlocklistEnabled:      result.BlocklistEnabled,
		BlocklistURL:          result.BlocklistURL,
		BlocklistSize:         result.BlocklistSize,

		SpeedLimitDownEnabled: result.SpeedLimitDownEnabled,
		SpeedLimitDown:        result.SpeedLimitDown,
		SpeedLimitUpEnabled:   result.SpeedLimitUpEnabled,
		SpeedLimitUp:          result.SpeedLimitUp,
		AltSpeedEnabled:       result.AltSpeedEnabled,
		AltSpeedDown:          result.AltSpeedDown,
		AltSpeedUp:            result.AltSpeedUp,
		AltSpeedTimeEnabled:   result.AltSpeedTimeEnabled,
		AltSpeedTimeBegin:     result.AltSpeedTimeBegin,
		AltSpeedTimeEnd:       result.AltSpeedTimeEnd,
		AltSpeedTimeDay:       result.AltSpeedTimeDay,

		DownloadQueueEnabled: result.DownloadQueueEnabled,
		DownloadQueueSize:    result.DownloadQueueSize,
		SeedQueueEnabled:     result.SeedQueueEnabled,
		SeedQueueSize:        result.SeedQueueSize,
		QueueStalledEnabled:  result.QueueStalledEnabled,
		QueueStalledMinutes:  result.QueueStalledMinutes,
	}, nil
}

// SessionSet writes session settings via session_set. The snake_case canonical
// keys are sent as params verbatim except for the documented "mib"->"mb" cache
// size discrepancy, which is normalized to the real wire key. The method returns
// no result arguments, so only the error object is inspected.
func (jsonrpcProtocol) SessionSet(ctx context.Context, t transport, settings map[string]any) error {
	params := make(map[string]any, len(settings))
	for key, value := range settings {
		params[jsonrpcSettingKey(key)] = value
	}

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  methodSessionSetJSONRPC,
		Params:  params,
		ID:      1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return err
	}

	var resp jsonrpcResponse
	if err := decodeResponse(methodSessionSetJSONRPC, respBody, &resp); err != nil {
		return err
	}

	if resp.Error != nil {
		return fmt.Errorf("%w: %s", ErrRPC, resp.Error.Message)
	}

	return nil
}

func (p jsonrpcProtocol) SessionStats(ctx context.Context, t transport) (SessionStats, error) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  methodSessionStatsJSONRPC,
		ID:      1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return SessionStats{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return SessionStats{}, err
	}

	var resp jsonrpcResponse
	if err := decodeResponse("session_stats", respBody, &resp); err != nil {
		return SessionStats{}, err
	}

	if resp.Error != nil {
		return SessionStats{}, fmt.Errorf("%w: %s", ErrRPC, resp.Error.Message)
	}

	var result jsonrpcStatsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return SessionStats{}, fmt.Errorf("rpc: decoding session_stats result: %w", err)
	}

	return SessionStats{
		ActiveTorrentCount: result.ActiveTorrentCount,
		PausedTorrentCount: result.PausedTorrentCount,
		TorrentCount:       result.TorrentCount,
		DownloadSpeed:      result.DownloadSpeed,
		UploadSpeed:        result.UploadSpeed,
		CumulativeStats:    StatsSnapshot(result.CumulativeStats),
		CurrentStats:       StatsSnapshot(result.CurrentStats),
	}, nil
}

func (p jsonrpcProtocol) TorrentGet(ctx context.Context, t transport, ids []any, fields []string) ([]Torrent, error) {
	params := map[string]any{"fields": resolveTorrentGetFields(fields, torrentGetFieldsJSONRPCMap)}
	if len(ids) > 0 {
		params["ids"] = ids
	}

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  methodTorrentGetJSONRPC,
		Params:  params,
		ID:      1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return nil, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return nil, err
	}

	var resp jsonrpcResponse
	if err := decodeResponse("torrent_get", respBody, &resp); err != nil {
		return nil, err
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("%w: %s", ErrRPC, resp.Error.Message)
	}

	var result jsonrpcTorrentResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("rpc: decoding torrent_get result: %w", err)
	}

	torrents := make([]Torrent, len(result.Torrents))
	for i, raw := range result.Torrents {
		torrents[i] = raw.toCanonical()
	}

	return torrents, nil
}

func (p jsonrpcProtocol) TorrentGetDetail(ctx context.Context, t transport, ids []any) ([]TorrentDetail, error) {
	params := map[string]any{"fields": jsonrpcDetailFields}
	if len(ids) > 0 {
		params["ids"] = ids
	}

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  methodTorrentGetJSONRPC,
		Params:  params,
		ID:      1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return nil, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return nil, err
	}

	var resp jsonrpcResponse
	if err := decodeResponse("torrent_get", respBody, &resp); err != nil {
		return nil, err
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("%w: %s", ErrRPC, resp.Error.Message)
	}

	var result jsonrpcDetailResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("rpc: decoding torrent_get result: %w", err)
	}

	details := make([]TorrentDetail, len(result.Torrents))
	for i, raw := range result.Torrents {
		details[i] = raw.toDetail()
	}

	return details, nil
}

// toDetail converts a decoded JSON-RPC detail torrent into the canonical
// TorrentDetail, merging the parallel file arrays, normalizing the wanted flags,
// and decoding the base64 pieces bitfield.
func (d jsonrpcDetailTorrent) toDetail() TorrentDetail {
	wanted := make([]bool, len(d.Wanted))
	for i, w := range d.Wanted {
		wanted[i] = bool(w)
	}

	files := jsonrpcFilesToCanonical(d.Files)
	fileStats := jsonrpcFilesToCanonical(d.FileStats)

	peers := make([]Peer, len(d.Peers))
	for i, p := range d.Peers {
		peers[i] = Peer(p)
	}

	trackers := make([]Tracker, len(d.Trackers))
	for i, tr := range d.Trackers {
		trackers[i] = Tracker(tr)
	}

	return TorrentDetail{
		Torrent: d.toCanonical(),

		Files:    mergeFiles(files, fileStats, wanted, d.Priorities),
		Peers:    peers,
		Trackers: trackers,
		Pieces:   decodePiecesBitfield(d.Pieces, d.PieceCount),

		MagnetLink:              d.MagnetLink,
		DownloadedEver:          d.DownloadedEver,
		UploadedEver:            d.UploadedEver,
		CorruptEver:             d.CorruptEver,
		HaveValid:               d.HaveValid,
		PieceCount:              d.PieceCount,
		PieceSize:               d.PieceSize,
		Comment:                 d.Comment,
		Creator:                 d.Creator,
		DateCreated:             d.DateCreated,
		IsPrivate:               d.IsPrivate,
		SecondsDownloading:      d.SecondsDownloading,
		SecondsSeeding:          d.SecondsSeeding,
		RecheckProgress:         d.RecheckProgress,
		MetadataPercentComplete: d.MetadataPercentComplete,
	}
}

func (p jsonrpcProtocol) TorrentAdd(ctx context.Context, t transport, addReq AddRequest) (AddResult, error) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  methodTorrentAddJSONRPC,
		Params:  jsonrpcAddParams(addReq),
		ID:      1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return AddResult{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return AddResult{}, err
	}

	var resp jsonrpcResponse
	if err := decodeResponse(methodTorrentAddJSONRPC, respBody, &resp); err != nil {
		return AddResult{}, err
	}

	if resp.Error != nil {
		return AddResult{}, fmt.Errorf("%w: %s", ErrRPC, resp.Error.Message)
	}

	var result jsonrpcAddResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return AddResult{}, fmt.Errorf("rpc: decoding %s result: %w", methodTorrentAddJSONRPC, err)
	}

	if result.Duplicate != nil {
		return AddResult{
			ID:        result.Duplicate.ID,
			Name:      result.Duplicate.Name,
			Hash:      result.Duplicate.Hash,
			Duplicate: true,
		}, nil
	}

	if result.Added != nil {
		return AddResult{
			ID:   result.Added.ID,
			Name: result.Added.Name,
			Hash: result.Added.Hash,
		}, nil
	}

	return AddResult{}, nil
}

func (p jsonrpcProtocol) TorrentStart(ctx context.Context, t transport, ids []any, now bool) error {
	method := methodTorrentStartJSONRPC
	if now {
		method = methodTorrentStartNowJSONRPC
	}

	return p.action(ctx, t, method, ids)
}

func (p jsonrpcProtocol) TorrentStop(ctx context.Context, t transport, ids []any) error {
	return p.action(ctx, t, methodTorrentStopJSONRPC, ids)
}

func (p jsonrpcProtocol) TorrentVerify(ctx context.Context, t transport, ids []any) error {
	return p.action(ctx, t, methodTorrentVerifyJSONRPC, ids)
}

func (p jsonrpcProtocol) TorrentReannounce(ctx context.Context, t transport, ids []any) error {
	return p.action(ctx, t, methodTorrentReannounceJSONRPC, ids)
}

func (p jsonrpcProtocol) QueueMoveTop(ctx context.Context, t transport, ids []any) error {
	return p.action(ctx, t, methodQueueMoveTopJSONRPC, ids)
}

func (p jsonrpcProtocol) QueueMoveUp(ctx context.Context, t transport, ids []any) error {
	return p.action(ctx, t, methodQueueMoveUpJSONRPC, ids)
}

func (p jsonrpcProtocol) QueueMoveDown(ctx context.Context, t transport, ids []any) error {
	return p.action(ctx, t, methodQueueMoveDownJSONRPC, ids)
}

func (p jsonrpcProtocol) QueueMoveBottom(ctx context.Context, t transport, ids []any) error {
	return p.action(ctx, t, methodQueueMoveBottomJSONRPC, ids)
}

// TorrentRemove sends torrent_remove with the delete_local_data flag (always
// present) and an optional ids array (omitted when nil/empty = all torrents).
// Like the payload-free actions, the method returns no result arguments, so
// only the error object is inspected.
func (jsonrpcProtocol) TorrentRemove(ctx context.Context, t transport, ids []any, deleteData bool) error {
	params := map[string]any{"delete_local_data": deleteData}
	if len(ids) > 0 {
		params["ids"] = ids
	}

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  methodTorrentRemoveJSONRPC,
		Params:  params,
		ID:      1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return err
	}

	var resp jsonrpcResponse
	if err := decodeResponse(methodTorrentRemoveJSONRPC, respBody, &resp); err != nil {
		return err
	}

	if resp.Error != nil {
		return fmt.Errorf("%w: %s", ErrRPC, resp.Error.Message)
	}

	return nil
}

// TorrentSetLocation sends torrent_set_location with the location and move flags
// (always present) and an optional ids array (omitted when nil/empty = all
// torrents). Like the payload-free actions, the method returns no result
// arguments, so only the error object is inspected.
func (jsonrpcProtocol) TorrentSetLocation(ctx context.Context, t transport, ids []any, location string, move bool) error {
	params := map[string]any{"location": location, "move": move}
	if len(ids) > 0 {
		params["ids"] = ids
	}

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  methodTorrentSetLocationJSONRPC,
		Params:  params,
		ID:      1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return err
	}

	var resp jsonrpcResponse
	if err := decodeResponse(methodTorrentSetLocationJSONRPC, respBody, &resp); err != nil {
		return err
	}

	if resp.Error != nil {
		return fmt.Errorf("%w: %s", ErrRPC, resp.Error.Message)
	}

	return nil
}

// jsonrpcRenamePathResult is the torrent_rename_path result shape: the server
// echoes the torrent id, the path it renamed, and the new name. The spec spells
// these keys id/path/name in both schemes.
type jsonrpcRenamePathResult struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
	Name string `json:"name"`
}

// TorrentRenamePath sends torrent_rename_path with the torrent-relative path and
// the new name, and decodes the server's echo. ids is always sent, even when
// empty: unlike the other mutations here, an omitted ids would mean "every
// torrent", and this method accepts exactly one.
func (jsonrpcProtocol) TorrentRenamePath(ctx context.Context, t transport, ids []any, path, name string) (RenameResult, error) {
	// A nil slice marshals to null, and Transmission reads anything that is not a
	// list/int/string the same way it reads a missing key: every torrent. Send a
	// real empty list so the never-omit-ids rule holds on the wire, not just in
	// the params map.
	if ids == nil {
		ids = []any{}
	}

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  methodTorrentRenamePathJSONRPC,
		Params:  map[string]any{"ids": ids, "path": path, "name": name},
		ID:      1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return RenameResult{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return RenameResult{}, err
	}

	var resp jsonrpcResponse
	if err := decodeResponse(methodTorrentRenamePathJSONRPC, respBody, &resp); err != nil {
		return RenameResult{}, err
	}

	if resp.Error != nil {
		return RenameResult{}, fmt.Errorf("%w: %s", ErrRPC, resp.Error.Message)
	}

	var result jsonrpcRenamePathResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return RenameResult{}, fmt.Errorf("rpc: decoding %s result: %w", methodTorrentRenamePathJSONRPC, err)
	}

	return RenameResult(result), nil
}

// TorrentSet sends torrent_set with the per-torrent settings (translated from
// canonical snake_case to the JSON-RPC wire spelling) and an optional ids array
// (omitted when nil/empty = all torrents). Only the keys present in settings are
// written. Like the payload-free actions, the method returns no result
// arguments, so only the error object is inspected.
func (jsonrpcProtocol) TorrentSet(ctx context.Context, t transport, ids []any, settings map[string]any) error {
	params := make(map[string]any, len(settings)+1)
	for key, value := range settings {
		params[jsonrpcTorrentSetKey(key)] = value
	}
	if len(ids) > 0 {
		params["ids"] = ids
	}

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  methodTorrentSetJSONRPC,
		Params:  params,
		ID:      1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return err
	}

	var resp jsonrpcResponse
	if err := decodeResponse(methodTorrentSetJSONRPC, respBody, &resp); err != nil {
		return err
	}

	if resp.Error != nil {
		return fmt.Errorf("%w: %s", ErrRPC, resp.Error.Message)
	}

	return nil
}

// action performs a payload-free JSON-RPC mutation (start/start-now/stop): it
// sends method with an optional ids array (omitted when nil/empty = all
// torrents) and confirms success. These methods return no result arguments, so
// only the error object is inspected.
func (jsonrpcProtocol) action(ctx context.Context, t transport, method string, ids []any) error {
	var params any
	if len(ids) > 0 {
		params = map[string]any{"ids": ids}
	}

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return err
	}

	var resp jsonrpcResponse
	if err := decodeResponse(method, respBody, &resp); err != nil {
		return err
	}

	if resp.Error != nil {
		return fmt.Errorf("%w: %s", ErrRPC, resp.Error.Message)
	}

	return nil
}

// Call issues an arbitrary method against the JSON-RPC 2.0 scheme. The canonical
// kebab-case name is translated to snake_case, args become the params object,
// and the response is normalized: success synthesizes result "success" with the
// JSON-RPC result as Arguments and id mapped to Tag; an error object maps
// error.message to Result, error.code to Code, and null Arguments. An empty 2xx
// body is treated as an empty success envelope. Only transport and decode
// failures return a Go error.
func (jsonrpcProtocol) Call(ctx context.Context, t transport, method string, args json.RawMessage) (RawResponse, error) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  translateMethod(ProtocolJSONRPC, method),
		Params:  rawParams(args),
		ID:      1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return RawResponse{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return RawResponse{}, err
	}

	if len(bytes.TrimSpace(respBody)) == 0 {
		return RawResponse{
			Raw:       respBody,
			Result:    bespokeResultSuccess,
			Arguments: nil,
			Tag:       1,
			OK:        true,
		}, nil
	}

	var resp jsonrpcResponse
	if err := decodeResponse(method, respBody, &resp); err != nil {
		return RawResponse{}, err
	}

	if resp.Error != nil {
		return RawResponse{
			Raw:       respBody,
			Result:    resp.Error.Message,
			Arguments: nil,
			Tag:       resp.ID,
			Code:      resp.Error.Code,
			OK:        false,
		}, nil
	}

	return RawResponse{
		Raw:       respBody,
		Result:    bespokeResultSuccess,
		Arguments: resp.Result,
		Tag:       resp.ID,
		OK:        true,
	}, nil
}

// jsonrpcPortTestResult is the snake_case port_test result shape. ip_protocol is
// unset when the server cannot determine it.
type jsonrpcPortTestResult struct {
	PortIsOpen bool   `json:"port_is_open"`
	IPProtocol string `json:"ip_protocol"`
}

// PortTest calls port_test and decodes the snake_case result, surfacing both the
// open flag and the IP protocol the test ran on. The default behavior (no
// ip_protocol param) is used, so the server picks the protocol.
func (jsonrpcProtocol) PortTest(ctx context.Context, t transport) (PortTestResult, error) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  methodPortTestJSONRPC,
		ID:      1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return PortTestResult{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return PortTestResult{}, err
	}

	var resp jsonrpcResponse
	if err := decodeResponse(methodPortTestJSONRPC, respBody, &resp); err != nil {
		return PortTestResult{}, err
	}

	if resp.Error != nil {
		return PortTestResult{}, fmt.Errorf("%w: %s", ErrRPC, resp.Error.Message)
	}

	var result jsonrpcPortTestResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return PortTestResult{}, fmt.Errorf("rpc: decoding %s result: %w", methodPortTestJSONRPC, err)
	}

	return PortTestResult{
		Open:       result.PortIsOpen,
		IPProtocol: result.IPProtocol,
	}, nil
}

// jsonrpcFreeSpaceResult is the snake_case free_space result shape. total_size is
// unset on a server too old to report it (TotalBytes then stays 0).
type jsonrpcFreeSpaceResult struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	TotalSize int64  `json:"total_size"`
}

// FreeSpace calls free_space with the queried path and decodes the snake_case
// result, surfacing the free size and (when reported) the total capacity.
func (jsonrpcProtocol) FreeSpace(ctx context.Context, t transport, path string) (FreeSpaceResult, error) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  methodFreeSpaceJSONRPC,
		Params:  map[string]any{"path": path},
		ID:      1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return FreeSpaceResult{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return FreeSpaceResult{}, err
	}

	var resp jsonrpcResponse
	if err := decodeResponse(methodFreeSpaceJSONRPC, respBody, &resp); err != nil {
		return FreeSpaceResult{}, err
	}

	if resp.Error != nil {
		return FreeSpaceResult{}, fmt.Errorf("%w: %s", ErrRPC, resp.Error.Message)
	}

	var result jsonrpcFreeSpaceResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return FreeSpaceResult{}, fmt.Errorf("rpc: decoding %s result: %w", methodFreeSpaceJSONRPC, err)
	}

	return FreeSpaceResult{
		Path:       result.Path,
		SizeBytes:  result.SizeBytes,
		TotalBytes: result.TotalSize,
	}, nil
}

// bespokeProtocol implements the legacy bespoke scheme.
type bespokeProtocol struct{}

// bespokeRequest is the bespoke request envelope.
type bespokeRequest struct {
	Method    string `json:"method"`
	Arguments any    `json:"arguments,omitempty"`
	Tag       int    `json:"tag"`
}

// bespokeResponse is the bespoke response envelope. arguments is captured raw so
// each method decodes its own shape.
type bespokeResponse struct {
	Result    string          `json:"result"`
	Arguments json.RawMessage `json:"arguments"`
	Tag       int             `json:"tag"`
}

// bespokeSessionArguments is the kebab-case session-get arguments shape. The
// settings keys are kebab-case on the wire (e.g. download-dir) and decode into
// the canonical Session's pointer fields.
type bespokeSessionArguments struct {
	Version          string `json:"version"`
	RPCVersionSemver string `json:"rpc-version-semver"`

	DownloadDir             *string  `json:"download-dir"`
	StartAddedTorrents      *bool    `json:"start-added-torrents"`
	RenamePartialFiles      *bool    `json:"rename-partial-files"`
	IncompleteDirEnabled    *bool    `json:"incomplete-dir-enabled"`
	IncompleteDir           *string  `json:"incomplete-dir"`
	SeedRatioLimited        *bool    `json:"seedRatioLimited"`
	SeedRatioLimit          *float64 `json:"seedRatioLimit"`
	IdleSeedingLimitEnabled *bool    `json:"idle-seeding-limit-enabled"`
	IdleSeedingLimit        *int     `json:"idle-seeding-limit"`
	CacheSizeMiB            *int     `json:"cache-size-mb"`

	PeerPort              *int    `json:"peer-port"`
	PeerPortRandomOnStart *bool   `json:"peer-port-random-on-start"`
	PortForwardingEnabled *bool   `json:"port-forwarding-enabled"`
	Encryption            *string `json:"encryption"`
	PeerLimitGlobal       *int    `json:"peer-limit-global"`
	PeerLimitPerTorrent   *int    `json:"peer-limit-per-torrent"`
	PexEnabled            *bool   `json:"pex-enabled"`
	DHTEnabled            *bool   `json:"dht-enabled"`
	LPDEnabled            *bool   `json:"lpd-enabled"`
	UTPEnabled            *bool   `json:"utp-enabled"`
	BlocklistEnabled      *bool   `json:"blocklist-enabled"`
	BlocklistURL          *string `json:"blocklist-url"`
	BlocklistSize         *int    `json:"blocklist-size"`

	SpeedLimitDownEnabled *bool  `json:"speed-limit-down-enabled"`
	SpeedLimitDown        *int64 `json:"speed-limit-down"`
	SpeedLimitUpEnabled   *bool  `json:"speed-limit-up-enabled"`
	SpeedLimitUp          *int64 `json:"speed-limit-up"`
	AltSpeedEnabled       *bool  `json:"alt-speed-enabled"`
	AltSpeedDown          *int64 `json:"alt-speed-down"`
	AltSpeedUp            *int64 `json:"alt-speed-up"`
	AltSpeedTimeEnabled   *bool  `json:"alt-speed-time-enabled"`
	AltSpeedTimeBegin     *int   `json:"alt-speed-time-begin"`
	AltSpeedTimeEnd       *int   `json:"alt-speed-time-end"`
	AltSpeedTimeDay       *int   `json:"alt-speed-time-day"`

	DownloadQueueEnabled *bool `json:"download-queue-enabled"`
	DownloadQueueSize    *int  `json:"download-queue-size"`
	SeedQueueEnabled     *bool `json:"seed-queue-enabled"`
	SeedQueueSize        *int  `json:"seed-queue-size"`
	QueueStalledEnabled  *bool `json:"queue-stalled-enabled"`
	QueueStalledMinutes  *int  `json:"queue-stalled-minutes"`
}

// sessionGetFieldsBespoke lists the kebab-case session-get fields snatcher
// requests: the two version/protocol fields plus every session-settings key.
var sessionGetFieldsBespoke = []string{
	"version", "rpc-version-semver",
	"download-dir", "start-added-torrents", "rename-partial-files",
	"incomplete-dir-enabled", "incomplete-dir", "seedRatioLimited",
	"seedRatioLimit", "idle-seeding-limit-enabled", "idle-seeding-limit",
	"cache-size-mb",
	"peer-port", "peer-port-random-on-start", "port-forwarding-enabled",
	"encryption", "peer-limit-global", "peer-limit-per-torrent", "pex-enabled",
	"dht-enabled", "lpd-enabled", "utp-enabled", "blocklist-enabled",
	"blocklist-url", "blocklist-size",
	"speed-limit-down-enabled", "speed-limit-down", "speed-limit-up-enabled",
	"speed-limit-up", "alt-speed-enabled", "alt-speed-down", "alt-speed-up",
	"alt-speed-time-enabled", "alt-speed-time-begin", "alt-speed-time-end",
	"alt-speed-time-day",
	"download-queue-enabled", "download-queue-size", "seed-queue-enabled",
	"seed-queue-size", "queue-stalled-enabled", "queue-stalled-minutes",
}

// bespokeStatsSnapshot is the camelCase nested stats object shape.
type bespokeStatsSnapshot struct {
	UploadedBytes   int64 `json:"uploadedBytes"`
	DownloadedBytes int64 `json:"downloadedBytes"`
	FilesAdded      int64 `json:"filesAdded"`
	SessionCount    int64 `json:"sessionCount"`
	SecondsActive   int64 `json:"secondsActive"`
}

// bespokeStatsArguments is the camelCase/kebab-case session-stats arguments
// shape. The top-level counts and speeds are camelCase; the nested snapshot
// objects are kebab-case keys.
type bespokeStatsArguments struct {
	ActiveTorrentCount int64                `json:"activeTorrentCount"`
	PausedTorrentCount int64                `json:"pausedTorrentCount"`
	TorrentCount       int64                `json:"torrentCount"`
	DownloadSpeed      int64                `json:"downloadSpeed"`
	UploadSpeed        int64                `json:"uploadSpeed"`
	CumulativeStats    bespokeStatsSnapshot `json:"cumulative-stats"`
	CurrentStats       bespokeStatsSnapshot `json:"current-stats"`
}

// bespokeTorrent is the camelCase torrent-get torrent shape.
type bespokeTorrent struct {
	ID                 int64                `json:"id"`
	Hash               string               `json:"hashString"`
	Name               string               `json:"name"`
	Status             int                  `json:"status"`
	PercentDone        float64              `json:"percentDone"`
	TotalSize          int64                `json:"totalSize"`
	SizeWhenDone       int64                `json:"sizeWhenDone"`
	LeftUntilDone      int64                `json:"leftUntilDone"`
	RateDownload       int64                `json:"rateDownload"`
	RateUpload         int64                `json:"rateUpload"`
	UploadRatio        float64              `json:"uploadRatio"`
	ETA                int64                `json:"eta"`
	PeersConnected     int64                `json:"peersConnected"`
	PeersSendingToUs   int64                `json:"peersSendingToUs"`
	PeersGettingFromUs int64                `json:"peersGettingFromUs"`
	AddedDate          int64                `json:"addedDate"`
	DoneDate           int64                `json:"doneDate"`
	ActivityDate       int64                `json:"activityDate"`
	DownloadDir        string               `json:"downloadDir"`
	ErrorCode          int                  `json:"error"`
	Error              string               `json:"errorString"`
	Labels             []string             `json:"labels"`
	Trackers           []wireTorrentTracker `json:"trackers"`
	QueuePosition      int64                `json:"queuePosition"`
	IsFinished         bool                 `json:"isFinished"`
	IsStalled          bool                 `json:"isStalled"`
}

// toCanonical converts a decoded bespoke torrent into the canonical Torrent,
// deriving the Status enum from the raw status int and error fields (a non-zero
// error code or non-empty error string promotes to StatusError).
func (t bespokeTorrent) toCanonical() Torrent {
	return Torrent{
		ID:                 t.ID,
		Hash:               t.Hash,
		Name:               t.Name,
		Status:             StatusFromTransmission(t.Status, t.ErrorCode, t.Error),
		PercentDone:        t.PercentDone,
		TotalSize:          t.TotalSize,
		SizeWhenDone:       t.SizeWhenDone,
		LeftUntilDone:      t.LeftUntilDone,
		RateDownload:       t.RateDownload,
		RateUpload:         t.RateUpload,
		UploadRatio:        t.UploadRatio,
		ETA:                t.ETA,
		PeersConnected:     t.PeersConnected,
		PeersSendingToUs:   t.PeersSendingToUs,
		PeersGettingFromUs: t.PeersGettingFromUs,
		AddedDate:          t.AddedDate,
		DoneDate:           t.DoneDate,
		ActivityDate:       t.ActivityDate,
		DownloadDir:        t.DownloadDir,
		Error:              t.Error,
		Labels:             t.Labels,
		Trackers:           torrentTrackers(t.Trackers),
		QueuePosition:      t.QueuePosition,
		IsFinished:         t.IsFinished,
		IsStalled:          t.IsStalled,
	}
}

// bespokeTorrentArguments is the kebab-case torrent-get arguments shape.
type bespokeTorrentArguments struct {
	Torrents []bespokeTorrent `json:"torrents"`
}

// bespokeDetailFields lists the camelCase torrent-get fields the bespoke detail
// read requests: the full canonical list (translated to the wire names) plus the
// detail-only fields. It is computed once from the canonical list so the list and
// detail reads stay in sync.
var bespokeDetailFields = append(
	resolveTorrentGetFields(nil, torrentGetFieldsBespokeMap),
	"files", "fileStats", "wanted", "priorities", "peers", "trackerStats",
	"pieces", "magnetLink", "downloadedEver", "uploadedEver", "corruptEver",
	"haveValid", "pieceCount", "pieceSize", "comment", "creator", "dateCreated",
	"isPrivate", "secondsDownloading", "secondsSeeding", "recheckProgress",
	"metadataPercentComplete",
)

// bespokeDetailFile is the camelCase files entry (constant per-file properties).
type bespokeDetailFile struct {
	Name           string `json:"name"`
	Length         int64  `json:"length"`
	BytesCompleted int64  `json:"bytesCompleted"`
}

// bespokeDetailPeer is the camelCase peers entry.
type bespokeDetailPeer struct {
	Address      string  `json:"address"`
	Port         int     `json:"port"`
	Client       string  `json:"clientName"`
	Progress     float64 `json:"progress"`
	RateToClient int64   `json:"rateToClient"`
	RateToPeer   int64   `json:"rateToPeer"`
	FlagStr      string  `json:"flagStr"`
	IsEncrypted  bool    `json:"isEncrypted"`
}

// bespokeDetailTracker is the camelCase trackerStats entry.
type bespokeDetailTracker struct {
	Announce           string `json:"announce"`
	Host               string `json:"host"`
	Sitename           string `json:"sitename"`
	Tier               int    `json:"tier"`
	AnnounceState      int    `json:"announceState"`
	SeederCount        int    `json:"seederCount"`
	LeecherCount       int    `json:"leecherCount"`
	DownloadCount      int    `json:"downloadCount"`
	LastAnnounceResult string `json:"lastAnnounceResult"`
	NextAnnounceTime   int64  `json:"nextAnnounceTime"`
}

// bespokeDetailTorrent is the camelCase torrent-get detail torrent shape: the
// base list fields plus the detail-only fields. The parallel wanted/priorities
// arrays are merged into Files by index.
type bespokeDetailTorrent struct {
	bespokeTorrent

	Files      []bespokeDetailFile    `json:"files"`
	FileStats  []bespokeDetailFile    `json:"fileStats"`
	Wanted     []wantedFlag           `json:"wanted"`
	Priorities []int                  `json:"priorities"`
	Peers      []bespokeDetailPeer    `json:"peers"`
	Trackers   []bespokeDetailTracker `json:"trackerStats"`
	Pieces     string                 `json:"pieces"`

	MagnetLink              string  `json:"magnetLink"`
	DownloadedEver          int64   `json:"downloadedEver"`
	UploadedEver            int64   `json:"uploadedEver"`
	CorruptEver             int64   `json:"corruptEver"`
	HaveValid               int64   `json:"haveValid"`
	PieceCount              int64   `json:"pieceCount"`
	PieceSize               int64   `json:"pieceSize"`
	Comment                 string  `json:"comment"`
	Creator                 string  `json:"creator"`
	DateCreated             int64   `json:"dateCreated"`
	IsPrivate               bool    `json:"isPrivate"`
	SecondsDownloading      int64   `json:"secondsDownloading"`
	SecondsSeeding          int64   `json:"secondsSeeding"`
	RecheckProgress         float64 `json:"recheckProgress"`
	MetadataPercentComplete float64 `json:"metadataPercentComplete"`
}

// bespokeDetailArguments is the kebab-case torrent-get detail arguments shape.
type bespokeDetailArguments struct {
	Torrents []bespokeDetailTorrent `json:"torrents"`
}

// bespokeFilesToCanonical converts the camelCase files/fileStats entries to
// canonical Files carrying only the constant per-file properties; the
// wanted/priority state is merged in separately by mergeFiles.
func bespokeFilesToCanonical(in []bespokeDetailFile) []File {
	out := make([]File, len(in))
	for i, f := range in {
		out[i] = File{Name: f.Name, Length: f.Length, BytesCompleted: f.BytesCompleted}
	}

	return out
}

// toDetail converts a decoded bespoke detail torrent into the canonical
// TorrentDetail, merging the parallel file arrays, normalizing the wanted flags
// (0/1 ints on this scheme), and decoding the base64 pieces bitfield.
func (d bespokeDetailTorrent) toDetail() TorrentDetail {
	wanted := make([]bool, len(d.Wanted))
	for i, w := range d.Wanted {
		wanted[i] = bool(w)
	}

	files := bespokeFilesToCanonical(d.Files)
	fileStats := bespokeFilesToCanonical(d.FileStats)

	peers := make([]Peer, len(d.Peers))
	for i, p := range d.Peers {
		peers[i] = Peer(p)
	}

	trackers := make([]Tracker, len(d.Trackers))
	for i, tr := range d.Trackers {
		trackers[i] = Tracker(tr)
	}

	return TorrentDetail{
		Torrent: d.toCanonical(),

		Files:    mergeFiles(files, fileStats, wanted, d.Priorities),
		Peers:    peers,
		Trackers: trackers,
		Pieces:   decodePiecesBitfield(d.Pieces, d.PieceCount),

		MagnetLink:              d.MagnetLink,
		DownloadedEver:          d.DownloadedEver,
		UploadedEver:            d.UploadedEver,
		CorruptEver:             d.CorruptEver,
		HaveValid:               d.HaveValid,
		PieceCount:              d.PieceCount,
		PieceSize:               d.PieceSize,
		Comment:                 d.Comment,
		Creator:                 d.Creator,
		DateCreated:             d.DateCreated,
		IsPrivate:               d.IsPrivate,
		SecondsDownloading:      d.SecondsDownloading,
		SecondsSeeding:          d.SecondsSeeding,
		RecheckProgress:         d.RecheckProgress,
		MetadataPercentComplete: d.MetadataPercentComplete,
	}
}

// bespokeAddArguments is the torrent-add request arguments shape (kebab-case
// download-dir, lowercase everything else). Empty fields are omitted so the
// server applies its defaults — except paused, which is always sent (see
// jsonrpcAddParams).
type bespokeAddArguments struct {
	Metainfo    string   `json:"metainfo,omitempty"`
	Filename    string   `json:"filename,omitempty"`
	DownloadDir string   `json:"download-dir,omitempty"`
	Paused      bool     `json:"paused"`
	Labels      []string `json:"labels,omitempty"`
}

// bespokeAddedTorrent is the camelCase torrent object returned in a
// torrent-added/torrent-duplicate result.
type bespokeAddedTorrent struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Hash string `json:"hashString"`
}

// bespokeAddArgumentsResult is the torrent-add result arguments shape. Exactly
// one of the two objects is present: torrent-added on a new add,
// torrent-duplicate when the torrent was already present.
type bespokeAddArgumentsResult struct {
	Added     *bespokeAddedTorrent `json:"torrent-added"`
	Duplicate *bespokeAddedTorrent `json:"torrent-duplicate"`
}

// torrentGetFieldsBespokeMap maps each canonical torrent_get field to the
// camelCase wire field(s) the bespoke scheme requests. The canonical "hash"
// becomes "hashString", and the canonical "error" expands to both the numeric
// "error" code and its "errorString" message.
var torrentGetFieldsBespokeMap = map[string][]string{
	"id":                    {"id"},
	"hash":                  {"hashString"},
	"name":                  {"name"},
	"status":                {"status"},
	"percent_done":          {"percentDone"},
	"total_size":            {"totalSize"},
	"size_when_done":        {"sizeWhenDone"},
	"left_until_done":       {"leftUntilDone"},
	"rate_download":         {"rateDownload"},
	"rate_upload":           {"rateUpload"},
	"upload_ratio":          {"uploadRatio"},
	"eta":                   {"eta"},
	"peers_connected":       {"peersConnected"},
	"peers_sending_to_us":   {"peersSendingToUs"},
	"peers_getting_from_us": {"peersGettingFromUs"},
	"added_date":            {"addedDate"},
	"done_date":             {"doneDate"},
	"activity_date":         {"activityDate"},
	"download_dir":          {"downloadDir"},
	"error":                 {"error", "errorString"},
	"labels":                {"labels"},
	"trackers":              {"trackers"},
	"queue_position":        {"queuePosition"},
	"is_finished":           {"isFinished"},
	"is_stalled":            {"isStalled"},
}

func (bespokeProtocol) Name() string { return ProtocolBespoke }

func (p bespokeProtocol) SessionGet(ctx context.Context, t transport) (Session, error) {
	req := bespokeRequest{
		Method:    methodSessionGetBespoke,
		Arguments: map[string]any{"fields": sessionGetFieldsBespoke},
		Tag:       1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return Session{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return Session{}, err
	}

	var resp bespokeResponse
	if err := decodeResponse("session-get", respBody, &resp); err != nil {
		return Session{}, err
	}

	if resp.Result != bespokeResultSuccess {
		return Session{}, fmt.Errorf("%w: %s", ErrRPC, resp.Result)
	}

	var args bespokeSessionArguments
	if err := json.Unmarshal(resp.Arguments, &args); err != nil {
		return Session{}, fmt.Errorf("rpc: decoding session-get arguments: %w", err)
	}

	// The bespoke wire spells canonical "allowed" encryption as "tolerated";
	// normalize it to the canonical value on the way in.
	encryption := args.Encryption
	if encryption != nil {
		normalized := normalizeEncryptionFromWire(*encryption)
		encryption = &normalized
	}

	return Session{
		Version:          args.Version,
		RPCVersionSemver: args.RPCVersionSemver,
		Protocol:         ProtocolBespoke,

		DownloadDir:             args.DownloadDir,
		StartAddedTorrents:      args.StartAddedTorrents,
		RenamePartialFiles:      args.RenamePartialFiles,
		IncompleteDirEnabled:    args.IncompleteDirEnabled,
		IncompleteDir:           args.IncompleteDir,
		SeedRatioLimited:        args.SeedRatioLimited,
		SeedRatioLimit:          args.SeedRatioLimit,
		IdleSeedingLimitEnabled: args.IdleSeedingLimitEnabled,
		IdleSeedingLimit:        args.IdleSeedingLimit,
		CacheSizeMiB:            args.CacheSizeMiB,

		PeerPort:              args.PeerPort,
		PeerPortRandomOnStart: args.PeerPortRandomOnStart,
		PortForwardingEnabled: args.PortForwardingEnabled,
		Encryption:            encryption,
		PeerLimitGlobal:       args.PeerLimitGlobal,
		PeerLimitPerTorrent:   args.PeerLimitPerTorrent,
		PexEnabled:            args.PexEnabled,
		DHTEnabled:            args.DHTEnabled,
		LPDEnabled:            args.LPDEnabled,
		UTPEnabled:            args.UTPEnabled,
		BlocklistEnabled:      args.BlocklistEnabled,
		BlocklistURL:          args.BlocklistURL,
		BlocklistSize:         args.BlocklistSize,

		SpeedLimitDownEnabled: args.SpeedLimitDownEnabled,
		SpeedLimitDown:        args.SpeedLimitDown,
		SpeedLimitUpEnabled:   args.SpeedLimitUpEnabled,
		SpeedLimitUp:          args.SpeedLimitUp,
		AltSpeedEnabled:       args.AltSpeedEnabled,
		AltSpeedDown:          args.AltSpeedDown,
		AltSpeedUp:            args.AltSpeedUp,
		AltSpeedTimeEnabled:   args.AltSpeedTimeEnabled,
		AltSpeedTimeBegin:     args.AltSpeedTimeBegin,
		AltSpeedTimeEnd:       args.AltSpeedTimeEnd,
		AltSpeedTimeDay:       args.AltSpeedTimeDay,

		DownloadQueueEnabled: args.DownloadQueueEnabled,
		DownloadQueueSize:    args.DownloadQueueSize,
		SeedQueueEnabled:     args.SeedQueueEnabled,
		SeedQueueSize:        args.SeedQueueSize,
		QueueStalledEnabled:  args.QueueStalledEnabled,
		QueueStalledMinutes:  args.QueueStalledMinutes,
	}, nil
}

// SessionSet writes session settings via session-set. The snake_case canonical
// keys in settings are remapped to the bespoke wire's kebab-case spellings (with
// the documented camelCase exceptions for the seed-ratio keys), and the
// encryption enum's canonical "allowed" is mapped to the wire's "tolerated". The
// method returns no arguments, so only the result string is inspected.
func (bespokeProtocol) SessionSet(ctx context.Context, t transport, settings map[string]any) error {
	arguments := make(map[string]any, len(settings))
	for key, value := range settings {
		wireKey := bespokeSettingKey(key)
		if wireKey == bespokeEncryptionKey {
			value = normalizeEncryptionToWire(value)
		}

		arguments[wireKey] = value
	}

	req := bespokeRequest{
		Method:    methodSessionSetBespoke,
		Arguments: arguments,
		Tag:       1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return err
	}

	var resp bespokeResponse
	if err := decodeResponse(methodSessionSetBespoke, respBody, &resp); err != nil {
		return err
	}

	if resp.Result != bespokeResultSuccess {
		return fmt.Errorf("%w: %s", ErrRPC, resp.Result)
	}

	return nil
}

// decodeResponse unmarshals an RPC response body into v, labeling failures with
// method. When the body is not JSON at all — most often an HTML error or login
// page from a reverse proxy, or a non-RPC URL (missing the /transmission/rpc
// path) — it returns an actionable hint instead of the raw json syntax error.
func decodeResponse(method string, body []byte, v any) error {
	if err := json.Unmarshal(body, v); err != nil {
		if looksLikeHTML(body) {
			return fmt.Errorf("rpc: %s: server returned an HTML page, not JSON — check the URL includes the RPC path (e.g. /transmission/rpc) and that any reverse-proxy auth is configured: %w", method, err)
		}
		return fmt.Errorf("rpc: decoding %s response: %w", method, err)
	}

	return nil
}

// looksLikeHTML reports whether body appears to be HTML (or XML) rather than a
// JSON document: its first non-whitespace byte is '<'.
func looksLikeHTML(body []byte) bool {
	trimmed := bytes.TrimSpace(body)

	return len(trimmed) > 0 && trimmed[0] == '<'
}

func (p bespokeProtocol) SessionStats(ctx context.Context, t transport) (SessionStats, error) {
	req := bespokeRequest{
		Method: methodSessionStatsBespoke,
		Tag:    1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return SessionStats{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return SessionStats{}, err
	}

	var resp bespokeResponse
	if err := decodeResponse("session-stats", respBody, &resp); err != nil {
		return SessionStats{}, err
	}

	if resp.Result != bespokeResultSuccess {
		return SessionStats{}, fmt.Errorf("%w: %s", ErrRPC, resp.Result)
	}

	var args bespokeStatsArguments
	if err := json.Unmarshal(resp.Arguments, &args); err != nil {
		return SessionStats{}, fmt.Errorf("rpc: decoding session-stats arguments: %w", err)
	}

	return SessionStats{
		ActiveTorrentCount: args.ActiveTorrentCount,
		PausedTorrentCount: args.PausedTorrentCount,
		TorrentCount:       args.TorrentCount,
		DownloadSpeed:      args.DownloadSpeed,
		UploadSpeed:        args.UploadSpeed,
		CumulativeStats:    StatsSnapshot(args.CumulativeStats),
		CurrentStats:       StatsSnapshot(args.CurrentStats),
	}, nil
}

func (p bespokeProtocol) TorrentGet(ctx context.Context, t transport, ids []any, fields []string) ([]Torrent, error) {
	arguments := map[string]any{"fields": resolveTorrentGetFields(fields, torrentGetFieldsBespokeMap)}
	if len(ids) > 0 {
		arguments["ids"] = ids
	}

	req := bespokeRequest{
		Method:    methodTorrentGetBespoke,
		Arguments: arguments,
		Tag:       1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return nil, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return nil, err
	}

	var resp bespokeResponse
	if err := decodeResponse("torrent-get", respBody, &resp); err != nil {
		return nil, err
	}

	if resp.Result != bespokeResultSuccess {
		return nil, fmt.Errorf("%w: %s", ErrRPC, resp.Result)
	}

	var args bespokeTorrentArguments
	if err := json.Unmarshal(resp.Arguments, &args); err != nil {
		return nil, fmt.Errorf("rpc: decoding torrent-get arguments: %w", err)
	}

	torrents := make([]Torrent, len(args.Torrents))
	for i, raw := range args.Torrents {
		torrents[i] = raw.toCanonical()
	}

	return torrents, nil
}

func (p bespokeProtocol) TorrentGetDetail(ctx context.Context, t transport, ids []any) ([]TorrentDetail, error) {
	arguments := map[string]any{"fields": bespokeDetailFields}
	if len(ids) > 0 {
		arguments["ids"] = ids
	}

	req := bespokeRequest{
		Method:    methodTorrentGetBespoke,
		Arguments: arguments,
		Tag:       1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return nil, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return nil, err
	}

	var resp bespokeResponse
	if err := decodeResponse("torrent-get", respBody, &resp); err != nil {
		return nil, err
	}

	if resp.Result != bespokeResultSuccess {
		return nil, fmt.Errorf("%w: %s", ErrRPC, resp.Result)
	}

	var args bespokeDetailArguments
	if err := json.Unmarshal(resp.Arguments, &args); err != nil {
		return nil, fmt.Errorf("rpc: decoding torrent-get arguments: %w", err)
	}

	details := make([]TorrentDetail, len(args.Torrents))
	for i, raw := range args.Torrents {
		details[i] = raw.toDetail()
	}

	return details, nil
}

func (p bespokeProtocol) TorrentAdd(ctx context.Context, t transport, addReq AddRequest) (AddResult, error) {
	req := bespokeRequest{
		Method:    methodTorrentAddBespoke,
		Arguments: bespokeAddArguments(addReq),
		Tag:       1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return AddResult{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return AddResult{}, err
	}

	var resp bespokeResponse
	if err := decodeResponse(methodTorrentAddBespoke, respBody, &resp); err != nil {
		return AddResult{}, err
	}

	if resp.Result != bespokeResultSuccess {
		return AddResult{}, fmt.Errorf("%w: %s", ErrRPC, resp.Result)
	}

	var args bespokeAddArgumentsResult
	if err := json.Unmarshal(resp.Arguments, &args); err != nil {
		return AddResult{}, fmt.Errorf("rpc: decoding %s arguments: %w", methodTorrentAddBespoke, err)
	}

	if args.Duplicate != nil {
		return AddResult{
			ID:        args.Duplicate.ID,
			Name:      args.Duplicate.Name,
			Hash:      args.Duplicate.Hash,
			Duplicate: true,
		}, nil
	}

	if args.Added != nil {
		return AddResult{
			ID:   args.Added.ID,
			Name: args.Added.Name,
			Hash: args.Added.Hash,
		}, nil
	}

	return AddResult{}, nil
}

func (p bespokeProtocol) TorrentStart(ctx context.Context, t transport, ids []any, now bool) error {
	method := methodTorrentStartBespoke
	if now {
		method = methodTorrentStartNowBespoke
	}

	return p.action(ctx, t, method, ids)
}

func (p bespokeProtocol) TorrentStop(ctx context.Context, t transport, ids []any) error {
	return p.action(ctx, t, methodTorrentStopBespoke, ids)
}

func (p bespokeProtocol) TorrentVerify(ctx context.Context, t transport, ids []any) error {
	return p.action(ctx, t, methodTorrentVerifyBespoke, ids)
}

func (p bespokeProtocol) TorrentReannounce(ctx context.Context, t transport, ids []any) error {
	return p.action(ctx, t, methodTorrentReannounceBespoke, ids)
}

func (p bespokeProtocol) QueueMoveTop(ctx context.Context, t transport, ids []any) error {
	return p.action(ctx, t, methodQueueMoveTopBespoke, ids)
}

func (p bespokeProtocol) QueueMoveUp(ctx context.Context, t transport, ids []any) error {
	return p.action(ctx, t, methodQueueMoveUpBespoke, ids)
}

func (p bespokeProtocol) QueueMoveDown(ctx context.Context, t transport, ids []any) error {
	return p.action(ctx, t, methodQueueMoveDownBespoke, ids)
}

func (p bespokeProtocol) QueueMoveBottom(ctx context.Context, t transport, ids []any) error {
	return p.action(ctx, t, methodQueueMoveBottomBespoke, ids)
}

// TorrentRemove sends torrent-remove with the delete-local-data flag (always
// present) and an optional ids array (omitted when nil/empty = all torrents).
// Like the payload-free actions, the method returns no arguments, so only the
// result string is inspected.
func (bespokeProtocol) TorrentRemove(ctx context.Context, t transport, ids []any, deleteData bool) error {
	arguments := map[string]any{"delete-local-data": deleteData}
	if len(ids) > 0 {
		arguments["ids"] = ids
	}

	req := bespokeRequest{
		Method:    methodTorrentRemoveBespoke,
		Arguments: arguments,
		Tag:       1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return err
	}

	var resp bespokeResponse
	if err := decodeResponse(methodTorrentRemoveBespoke, respBody, &resp); err != nil {
		return err
	}

	if resp.Result != bespokeResultSuccess {
		return fmt.Errorf("%w: %s", ErrRPC, resp.Result)
	}

	return nil
}

// TorrentSetLocation sends torrent-set-location with the location and move flags
// (always present) and an optional ids array (omitted when nil/empty = all
// torrents). Like the payload-free actions, the method returns no arguments, so
// only the result string is inspected.
func (bespokeProtocol) TorrentSetLocation(ctx context.Context, t transport, ids []any, location string, move bool) error {
	arguments := map[string]any{"location": location, "move": move}
	if len(ids) > 0 {
		arguments["ids"] = ids
	}

	req := bespokeRequest{
		Method:    methodTorrentSetLocationBespoke,
		Arguments: arguments,
		Tag:       1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return err
	}

	var resp bespokeResponse
	if err := decodeResponse(methodTorrentSetLocationBespoke, respBody, &resp); err != nil {
		return err
	}

	if resp.Result != bespokeResultSuccess {
		return fmt.Errorf("%w: %s", ErrRPC, resp.Result)
	}

	return nil
}

// bespokeRenamePathArguments is the torrent-rename-path arguments shape: the
// server echoes the torrent id, the path it renamed, and the new name. The spec
// spells these keys id/path/name in both schemes.
type bespokeRenamePathArguments struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
	Name string `json:"name"`
}

// TorrentRenamePath sends torrent-rename-path with the torrent-relative path and
// the new name, and decodes the server's echo. ids is always sent, even when
// empty: unlike the other mutations here, an omitted ids would mean "every
// torrent", and this method accepts exactly one.
func (bespokeProtocol) TorrentRenamePath(ctx context.Context, t transport, ids []any, path, name string) (RenameResult, error) {
	// A nil slice marshals to null, and Transmission reads anything that is not a
	// list/int/string the same way it reads a missing key: every torrent. Send a
	// real empty list so the never-omit-ids rule holds on the wire, not just in
	// the arguments map.
	if ids == nil {
		ids = []any{}
	}

	req := bespokeRequest{
		Method:    methodTorrentRenamePathBespoke,
		Arguments: map[string]any{"ids": ids, "path": path, "name": name},
		Tag:       1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return RenameResult{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return RenameResult{}, err
	}

	var resp bespokeResponse
	if err := decodeResponse(methodTorrentRenamePathBespoke, respBody, &resp); err != nil {
		return RenameResult{}, err
	}

	if resp.Result != bespokeResultSuccess {
		return RenameResult{}, fmt.Errorf("%w: %s", ErrRPC, resp.Result)
	}

	var args bespokeRenamePathArguments
	if err := json.Unmarshal(resp.Arguments, &args); err != nil {
		return RenameResult{}, fmt.Errorf("rpc: decoding %s arguments: %w", methodTorrentRenamePathBespoke, err)
	}

	return RenameResult(args), nil
}

// TorrentSet sends torrent-set with the per-torrent settings (translated from
// canonical snake_case to the bespoke wire spelling, with the documented
// camelCase exceptions) and an optional ids array (omitted when nil/empty = all
// torrents). Only the keys present in settings are written. Like the
// payload-free actions, the method returns no arguments, so only the result
// string is inspected.
func (bespokeProtocol) TorrentSet(ctx context.Context, t transport, ids []any, settings map[string]any) error {
	arguments := make(map[string]any, len(settings)+1)
	for key, value := range settings {
		arguments[bespokeTorrentSetKey(key)] = value
	}
	if len(ids) > 0 {
		arguments["ids"] = ids
	}

	req := bespokeRequest{
		Method:    methodTorrentSetBespoke,
		Arguments: arguments,
		Tag:       1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return err
	}

	var resp bespokeResponse
	if err := decodeResponse(methodTorrentSetBespoke, respBody, &resp); err != nil {
		return err
	}

	if resp.Result != bespokeResultSuccess {
		return fmt.Errorf("%w: %s", ErrRPC, resp.Result)
	}

	return nil
}

// action performs a payload-free bespoke mutation (start/start-now/stop): it
// sends method with an optional ids array (omitted when nil/empty = all
// torrents) and confirms success via the result field. These methods return no
// arguments, so only the result string is inspected.
func (bespokeProtocol) action(ctx context.Context, t transport, method string, ids []any) error {
	var arguments any
	if len(ids) > 0 {
		arguments = map[string]any{"ids": ids}
	}

	req := bespokeRequest{
		Method:    method,
		Arguments: arguments,
		Tag:       1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return err
	}

	var resp bespokeResponse
	if err := decodeResponse(method, respBody, &resp); err != nil {
		return err
	}

	if resp.Result != bespokeResultSuccess {
		return fmt.Errorf("%w: %s", ErrRPC, resp.Result)
	}

	return nil
}

// Call issues an arbitrary method against the bespoke scheme. The canonical
// kebab-case name is sent verbatim, args become the arguments object, and the
// response is passed through: result/arguments/tag map straight onto the
// normalized fields, with OK reporting whether result == "success". An empty 2xx
// body is treated as an empty success envelope. Only transport and decode
// failures return a Go error.
func (bespokeProtocol) Call(ctx context.Context, t transport, method string, args json.RawMessage) (RawResponse, error) {
	req := bespokeRequest{
		Method:    translateMethod(ProtocolBespoke, method),
		Arguments: rawParams(args),
		Tag:       1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return RawResponse{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return RawResponse{}, err
	}

	if len(bytes.TrimSpace(respBody)) == 0 {
		return RawResponse{
			Raw:       respBody,
			Result:    bespokeResultSuccess,
			Arguments: nil,
			Tag:       1,
			OK:        true,
		}, nil
	}

	var resp bespokeResponse
	if err := decodeResponse(method, respBody, &resp); err != nil {
		return RawResponse{}, err
	}

	return RawResponse{
		Raw:       respBody,
		Result:    resp.Result,
		Arguments: resp.Arguments,
		Tag:       resp.Tag,
		OK:        resp.Result == bespokeResultSuccess,
	}, nil
}

// bespokePortTestArguments is the kebab-case port-test arguments shape. The
// bespoke scheme reports only port-is-open; there is no ip-protocol field.
type bespokePortTestArguments struct {
	PortIsOpen bool `json:"port-is-open"`
}

// PortTest calls port-test and decodes the kebab-case result. The bespoke scheme
// reports only whether the port is open, so PortTestResult.IPProtocol is left
// empty.
func (bespokeProtocol) PortTest(ctx context.Context, t transport) (PortTestResult, error) {
	req := bespokeRequest{
		Method: methodPortTestBespoke,
		Tag:    1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return PortTestResult{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return PortTestResult{}, err
	}

	var resp bespokeResponse
	if err := decodeResponse(methodPortTestBespoke, respBody, &resp); err != nil {
		return PortTestResult{}, err
	}

	if resp.Result != bespokeResultSuccess {
		return PortTestResult{}, fmt.Errorf("%w: %s", ErrRPC, resp.Result)
	}

	var args bespokePortTestArguments
	if err := json.Unmarshal(resp.Arguments, &args); err != nil {
		return PortTestResult{}, fmt.Errorf("rpc: decoding %s arguments: %w", methodPortTestBespoke, err)
	}

	return PortTestResult{Open: args.PortIsOpen}, nil
}

// bespokeFreeSpaceArguments is the free-space arguments shape. The free size is
// kebab-case (size-bytes), but the total capacity is spelled total_size on the
// wire even in the bespoke scheme (see docs/transmission/rpc-spec-406.md); a
// server too old to return it leaves TotalSize 0.
type bespokeFreeSpaceArguments struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size-bytes"`
	TotalSize int64  `json:"total_size"`
}

// FreeSpace calls free-space with the queried path and decodes the result. The
// free size is always reported; the total capacity (total_size) is reported only
// by a server new enough to include it, otherwise FreeSpaceResult.TotalBytes
// stays 0.
func (bespokeProtocol) FreeSpace(ctx context.Context, t transport, path string) (FreeSpaceResult, error) {
	req := bespokeRequest{
		Method:    methodFreeSpaceBespoke,
		Arguments: map[string]any{"path": path},
		Tag:       1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return FreeSpaceResult{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return FreeSpaceResult{}, err
	}

	var resp bespokeResponse
	if err := decodeResponse(methodFreeSpaceBespoke, respBody, &resp); err != nil {
		return FreeSpaceResult{}, err
	}

	if resp.Result != bespokeResultSuccess {
		return FreeSpaceResult{}, fmt.Errorf("%w: %s", ErrRPC, resp.Result)
	}

	var args bespokeFreeSpaceArguments
	if err := json.Unmarshal(resp.Arguments, &args); err != nil {
		return FreeSpaceResult{}, fmt.Errorf("rpc: decoding %s arguments: %w", methodFreeSpaceBespoke, err)
	}

	return FreeSpaceResult{
		Path:       args.Path,
		SizeBytes:  args.SizeBytes,
		TotalBytes: args.TotalSize,
	}, nil
}

// Encryption enum values. The canonical "allowed" is spelled "tolerated" on the
// bespoke wire; "preferred"/"required" are identical across schemes.
const (
	encryptionAllowed   = "allowed"
	encryptionTolerated = "tolerated"
)

// bespokeEncryptionKey is the bespoke wire key for the encryption setting,
// derived from the canonical snake_case key.
const bespokeEncryptionKey = "encryption"

// bespokeSettingExceptions maps the canonical snake_case session-set keys whose
// bespoke wire spelling is not a plain "_"->"-" transform. Transmission's legacy
// scheme spells the seed-ratio keys in camelCase, and the cache size key uses
// "mb" rather than "mib".
var bespokeSettingExceptions = map[string]string{
	"seed_ratio_limited": "seedRatioLimited",
	"seed_ratio_limit":   "seedRatioLimit",
	"cache_size_mib":     "cache-size-mb",
}

// jsonrpcSettingExceptions maps the canonical snake_case session-set keys whose
// JSON-RPC 2.0 wire spelling differs: only the cache size key, which uses "mb"
// rather than "mib" on the wire.
var jsonrpcSettingExceptions = map[string]string{
	"cache_size_mib": "cache_size_mb",
}

// jsonrpcSettingKey maps a canonical snake_case session-set key to the JSON-RPC
// 2.0 wire spelling: a known exception when listed, otherwise the key verbatim.
func jsonrpcSettingKey(key string) string {
	if wireKey, ok := jsonrpcSettingExceptions[key]; ok {
		return wireKey
	}

	return key
}

// bespokeSettingKey maps a canonical snake_case session-set key to the bespoke
// wire spelling: a known exception when listed, otherwise the mechanical
// "_"->"-" transform.
func bespokeSettingKey(key string) string {
	if wireKey, ok := bespokeSettingExceptions[key]; ok {
		return wireKey
	}

	return strings.ReplaceAll(key, "_", "-")
}

// bespokeTorrentSetExceptions maps the canonical snake_case torrent-set keys
// whose bespoke wire spelling is not a plain "_"->"-" transform. Transmission's
// legacy 4.0.x torrent-set arguments are camelCase for these fields; all other
// keys (e.g. peer_limit -> peer-limit, labels, group, location) fall back to the
// mechanical transform.
var bespokeTorrentSetExceptions = map[string]string{
	"bandwidth_priority":    "bandwidthPriority",
	"download_limit":        "downloadLimit",
	"download_limited":      "downloadLimited",
	"upload_limit":          "uploadLimit",
	"upload_limited":        "uploadLimited",
	"honors_session_limits": "honorsSessionLimits",
	"queue_position":        "queuePosition",
	"seed_ratio_limit":      "seedRatioLimit",
	"seed_ratio_mode":       "seedRatioMode",
	"seed_idle_limit":       "seedIdleLimit",
	"seed_idle_mode":        "seedIdleMode",
	"sequential_download":   "sequentialDownload",
	"tracker_list":          "trackerList",
}

// jsonrpcTorrentSetKey maps a canonical snake_case torrent-set key to the
// JSON-RPC 2.0 wire spelling. The in-scope fields are all sent verbatim, so the
// key passes through unchanged.
func jsonrpcTorrentSetKey(key string) string {
	return key
}

// bespokeTorrentSetKey maps a canonical snake_case torrent-set key to the
// bespoke wire spelling: a known camelCase exception when listed, otherwise the
// mechanical "_"->"-" transform.
func bespokeTorrentSetKey(key string) string {
	if wireKey, ok := bespokeTorrentSetExceptions[key]; ok {
		return wireKey
	}

	return strings.ReplaceAll(key, "_", "-")
}

// normalizeEncryptionFromWire maps the bespoke wire encryption value to the
// canonical value: "tolerated" becomes "allowed"; other values pass through.
func normalizeEncryptionFromWire(value string) string {
	if value == encryptionTolerated {
		return encryptionAllowed
	}

	return value
}

// normalizeEncryptionToWire maps a canonical encryption value to the bespoke
// wire value: the string "allowed" becomes "tolerated". Non-string or other
// values pass through unchanged.
func normalizeEncryptionToWire(value any) any {
	if s, ok := value.(string); ok && s == encryptionAllowed {
		return encryptionTolerated
	}

	return value
}

// translateMethod maps a canonical kebab-case RPC method name to the spelling
// the given protocol expects: the JSON-RPC 2.0 scheme uses snake_case (so each
// "-" becomes "_"), while the bespoke scheme uses the kebab-case name verbatim.
func translateMethod(protocol, method string) string {
	if protocol == ProtocolJSONRPC {
		return strings.ReplaceAll(method, "-", "_")
	}

	return method
}

// rawParams converts a raw arguments fragment into the value placed in a request
// envelope's params/arguments field. A nil fragment yields nil, so the
// "omitempty" envelope tag drops the field entirely for a zero-argument method.
func rawParams(args json.RawMessage) any {
	if len(args) == 0 {
		return nil
	}

	return args
}

// marshalRequest encodes a request envelope, wrapping any error in a clear,
// protocol-neutral message.
func marshalRequest(req any) ([]byte, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("rpc: encoding request: %w", err)
	}

	return body, nil
}
