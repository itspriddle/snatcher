// Package torrent defines the vendor-neutral seam between snatcher's commands
// and a concrete BitTorrent client. Commands depend only on the Backend
// interface and the neutral types here, so a future qbittorrent backend can
// slot in without rewriting commands. Today the only implementation is the
// Transmission adapter in internal/transmission.
package torrent

import (
	"context"
	"errors"
)

// ErrAuth signals that the backend rejected the supplied credentials. It is the
// neutral equivalent of a client-specific authentication failure and is
// matchable with errors.Is.
var ErrAuth = errors.New("torrent: authentication failed")

// ErrUnreachable signals a transport-level failure: the backend could not be
// reached, the HTTP exchange could not complete, or the server returned an
// unexpected non-2xx status. A request the server accepted but rejected at the
// application level is ErrServer, not this. It is matchable with errors.Is.
var ErrUnreachable = errors.New("torrent: server unreachable")

// ErrServer signals that the backend accepted the request at the transport level
// but reported a server-side RPC application error (e.g. an invalid argument or
// a rejected operation). It is distinct from ErrUnreachable so a status bar can
// tell "can't reach host" from "server rejected the request". It is matchable
// with errors.Is.
var ErrServer = errors.New("torrent: server error")

// ErrUnsupported signals that the backend server is older than the minimum
// version snatcher supports. It is matchable with errors.Is.
var ErrUnsupported = errors.New("torrent: unsupported server version")

// ServerInfo is the vendor-neutral description of a connected server, returned
// by Backend.ServerInfo.
type ServerInfo struct {
	// Version is the server's long version string, e.g. "4.0.6 (38c164933e)".
	Version string `json:"version"`

	// RPCVersionSemver is the server's semver-compatible RPC API version, e.g.
	// "6.0.0".
	RPCVersionSemver string `json:"rpc_version_semver"`

	// Protocol is the negotiated wire protocol name (backend-specific, e.g.
	// "jsonrpc-2.0" or "bespoke" for Transmission).
	Protocol string `json:"protocol"`
}

// StatsSnapshot is the vendor-neutral shape of an aggregate stats snapshot
// (cumulative across all sessions, or the current session only).
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

// PortTest is the vendor-neutral result of a port-reachability check, returned
// by Backend.TestPort. IPProtocol is only populated when the backend reports it
// (Transmission's JSON-RPC 2.0 scheme); it is omitted otherwise.
type PortTest struct {
	// Open reports whether the configured peer port is reachable from outside.
	Open bool `json:"open"`

	// IPProtocol is the IP protocol the test was carried out on ("ipv4" or
	// "ipv6"), or empty when the backend did not report it.
	IPProtocol string `json:"ip_protocol,omitempty"`
}

// Stats is the vendor-neutral session statistics returned by Backend.Stats.
type Stats struct {
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

// Backend is the vendor-neutral interface every BitTorrent client adapter
// implements. Commands depend on this interface rather than any concrete
// client. Methods honor the supplied context for cancellation and deadlines,
// and map client-specific failures to the neutral sentinels (ErrAuth,
// ErrUnreachable).
type Backend interface {
	// ServerInfo performs a round-trip to the server and returns its neutral
	// ServerInfo.
	ServerInfo(ctx context.Context) (ServerInfo, error)

	// Stats returns the server's neutral session statistics.
	Stats(ctx context.Context) (Stats, error)

	// ListTorrents returns the torrents matched by sel as neutral Torrents. An
	// All selector (or one whose RPCIDs is empty) returns every torrent;
	// otherwise the server filters to the selected ids/hashes. The optional
	// fields projection limits which Torrent fields the server returns and
	// snatcher decodes (see PollFields for the cheap polling subset); an empty
	// projection requests the full record (AllFields). Fields not requested are
	// left at their zero value. The returned slice is never nil on success (it is
	// empty when there are no matching torrents).
	ListTorrents(ctx context.Context, sel Selector, fields ...Field) ([]Torrent, error)

	// TorrentDetail returns the full detail view (files, peers, trackers, the
	// pieces bitfield, the magnet link, and the overview scalars, on top of the
	// embedded Torrent record) for the single torrent with the given numeric id,
	// in one round-trip. An id that matches no torrent returns an error.
	TorrentDetail(ctx context.Context, id int64) (TorrentDetail, error)

	// StartTorrents resumes the torrents matched by sel (start-now when now is
	// true, disregarding queue position). An All selector targets every torrent.
	// Starting an already-started torrent is a no-op.
	StartTorrents(ctx context.Context, sel Selector, now bool) error

	// StopTorrents pauses the torrents matched by sel. An All selector targets
	// every torrent. Stopping an already-stopped torrent is a no-op.
	StopTorrents(ctx context.Context, sel Selector) error

	// VerifyTorrents queues local-data verification (a re-hash of downloaded
	// files against the torrent metadata) for the torrents matched by sel. An All
	// selector targets every torrent. Verifying an already-verifying torrent is a
	// no-op.
	VerifyTorrents(ctx context.Context, sel Selector) error

	// ReannounceTorrents forces the torrents matched by sel to re-announce to
	// their trackers immediately, rather than waiting for the next scheduled
	// announce. An All selector targets every torrent. Reannouncing is a no-op-safe
	// action.
	ReannounceTorrents(ctx context.Context, sel Selector) error

	// QueueMoveTop moves the torrents matched by sel to the top of the server's
	// queue. An All selector targets every torrent.
	QueueMoveTop(ctx context.Context, sel Selector) error

	// QueueMoveUp moves the torrents matched by sel up one position in the
	// server's queue. An All selector targets every torrent.
	QueueMoveUp(ctx context.Context, sel Selector) error

	// QueueMoveDown moves the torrents matched by sel down one position in the
	// server's queue. An All selector targets every torrent.
	QueueMoveDown(ctx context.Context, sel Selector) error

	// QueueMoveBottom moves the torrents matched by sel to the bottom of the
	// server's queue. An All selector targets every torrent.
	QueueMoveBottom(ctx context.Context, sel Selector) error

	// RemoveTorrents removes the torrents matched by sel from the session, also
	// deleting their downloaded local files when deleteData is true. An All
	// selector targets every torrent. Removal is immediate and irreversible.
	RemoveTorrents(ctx context.Context, sel Selector, deleteData bool) error

	// MoveTorrents changes the download location of the torrents matched by sel to
	// location. When move is true the backend relocates the existing files to
	// location; when false it repoints at location and searches there for files
	// that are already present (e.g. after a manual move). An All selector targets
	// every torrent. The location is passed through as given; the backend does not
	// validate that it exists or is writable.
	MoveTorrents(ctx context.Context, sel Selector, location string, move bool) error

	// SetTorrents writes per-torrent fields on the torrents matched by sel. The
	// changes map is keyed by neutral kebab-case field key (e.g. "bandwidth-
	// priority", "seed-ratio-mode") with the parsed typed value; the backend
	// translates each key to its own wire scheme. Only the keys present in changes
	// are written; unspecified fields are left untouched. An All selector targets
	// every torrent. The call confirms success and carries no response payload.
	SetTorrents(ctx context.Context, sel Selector, changes map[string]any) error

	// SetFiles applies per-file selection and priority changes to the torrents
	// matched by sel. The changes' index lists select which files within each
	// torrent to mark wanted/unwanted or set to high/normal/low priority; only the
	// non-empty FileChanges fields are written, and an empty FileChanges is a
	// no-op. An All selector targets every torrent. The call confirms success and
	// carries no response payload.
	SetFiles(ctx context.Context, sel Selector, changes FileChanges) error

	// AddTorrent adds a single torrent described by req (exactly one of Metainfo,
	// URL, or Magnet identifies the source) and returns the neutral AddResult. A
	// torrent that was already present is reported via AddResult.Duplicate rather
	// than as an error.
	AddTorrent(ctx context.Context, req AddRequest) (AddResult, error)

	// GetSession performs a round-trip to the server and returns its neutral
	// SessionSettings (the server's current configurable session settings).
	GetSession(ctx context.Context) (SessionSettings, error)

	// SetSession writes the supplied session settings. The changes map is keyed by
	// neutral kebab-case setting key (e.g. "peer-port", "dht-enabled") with the
	// parsed typed value; the backend translates each key to its own wire scheme.
	// Only the keys present in changes are written. The call confirms success and
	// carries no response payload.
	SetSession(ctx context.Context, changes map[string]any) error

	// TestPort checks whether the configured peer port is reachable from outside
	// and returns the neutral PortTest. The result's Open flag is a probe value
	// (a reachable-but-closed port is not an error); a transport or auth failure
	// maps to the neutral sentinels (ErrAuth, ErrUnreachable).
	TestPort(ctx context.Context) (PortTest, error)
}
