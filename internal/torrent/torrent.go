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

// ErrUnreachable signals that the backend could not be reached or returned an
// error that is not an authentication failure (a transport problem, an
// unexpected HTTP status, or a server-side RPC error). It is matchable with
// errors.Is.
var ErrUnreachable = errors.New("torrent: server unreachable")

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
}
