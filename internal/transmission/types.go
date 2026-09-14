package transmission

import (
	"errors"

	"github.com/itspriddle/snatcher/internal/transmission/rpc"
)

// ErrAuth signals that the server rejected the supplied credentials. It is
// matchable with errors.Is.
var ErrAuth = errors.New("transmission: authentication failed")

// ErrUnreachable signals a transport-level failure: the server could not be
// reached, the HTTP exchange could not complete, or the server returned an
// unexpected non-2xx status. A request the server accepted but rejected at the
// application level is ErrServer, not this. It is matchable with errors.Is.
var ErrUnreachable = errors.New("transmission: server unreachable")

// ErrServer signals that the server accepted the request at the transport level
// but reported a server-side RPC application error (e.g. an invalid argument or
// a rejected operation). It is distinct from ErrUnreachable so a status bar can
// tell "can't reach host" from "server rejected the request". It is matchable
// with errors.Is.
var ErrServer = errors.New("transmission: server error")

// ErrUnsupported signals that the server is older than the minimum Transmission
// version snatcher supports. It is matchable with errors.Is.
var ErrUnsupported = errors.New("transmission: unsupported server version")

// The domain types below are the single canonical set. The wire-decoded shapes
// (Torrent, TorrentDetail, File, Peer, Tracker, Stats, StatsSnapshot,
// SessionSettings, ServerInfo, PortTest, FreeSpace, RenameResult) are owned by
// the rpc client package (it decodes them off the wire and carries the curated
// output projection); they are aliased here so consumers see one domain model
// under transmission.* with no conversion. Status is the derived lifecycle enum,
// also owned by rpc since the canonical Torrent carries it.

// ServerInfo is the description of a connected server.
type ServerInfo = rpc.ServerInfo

// Stats is the server's session statistics.
type Stats = rpc.SessionStats

// StatsSnapshot is an aggregate stats snapshot (cumulative or current session).
type StatsSnapshot = rpc.StatsSnapshot

// PortTest is the result of a port-reachability check.
type PortTest = rpc.PortTestResult

// FreeSpace is the result of a free-space query on a download path.
type FreeSpace = rpc.FreeSpaceResult

// RenameResult is the server's echo of a completed rename: the torrent id, the
// path it renamed, and the new name.
type RenameResult = rpc.RenameResult

// SessionSettings is the snapshot of a server's configurable session settings.
type SessionSettings = rpc.SessionSettings

// Torrent is the description of a single torrent, returned by ListTorrents.
type Torrent = rpc.Torrent

// TorrentDetail is the detail view of a single torrent, returned by
// TorrentDetail.
type TorrentDetail = rpc.TorrentDetail

// File is one file within a torrent.
type File = rpc.File

// Peer is one connected peer for a torrent.
type Peer = rpc.Peer

// Tracker is one tracker for a torrent.
type Tracker = rpc.Tracker

// Status is the derived lifecycle state of a torrent.
type Status = rpc.Status

// The Status constants, re-exported from rpc so consumers reference
// transmission.StatusDownloading rather than reaching into the rpc package.
const (
	StatusStopped          = rpc.StatusStopped
	StatusQueuedToVerify   = rpc.StatusQueuedToVerify
	StatusVerifying        = rpc.StatusVerifying
	StatusQueuedToDownload = rpc.StatusQueuedToDownload
	StatusDownloading      = rpc.StatusDownloading
	StatusQueuedToSeed     = rpc.StatusQueuedToSeed
	StatusSeeding          = rpc.StatusSeeding
	StatusError            = rpc.StatusError
)

// StatusFromTransmission maps a raw Transmission torrent-get "status" int (0–6)
// plus the torrent's error fields to a Status.
func StatusFromTransmission(status, errorCode int, errorString string) Status {
	return rpc.StatusFromTransmission(status, errorCode, errorString)
}

// AnnounceState maps a Tracker's raw AnnounceState int to a lowercase name
// ("inactive", "waiting", "queued", "active"). An unrecognized value renders as
// its decimal integer.
func AnnounceState(state int) string {
	return rpc.AnnounceState(state)
}

// FileChanges is the set of per-file selection and priority changes applied by
// SetFiles. Each field is a list of file indices (matching the order of
// TorrentDetail.Files); only the non-empty fields are written, and an empty
// FileChanges is a no-op. Wanted and Unwanted set whether the listed files are
// downloaded; PriorityHigh, PriorityNormal, and PriorityLow set the listed
// files' download priority. The indices in different fields are independent
// (e.g. a file may appear in both Wanted and PriorityHigh).
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

// Field is a projectable Torrent field. Its string value is the canonical
// snake_case key matching the Torrent struct's json tag, which the rpc client
// translates to each wire protocol's spelling. Callers pass a projection of
// Fields to ListTorrents so a polling client can request only the cheap subset
// it needs (see PollFields) instead of the full record. An empty projection
// means the full set (AllFields).
type Field string

// The full set of projectable Torrent fields, one per Torrent struct field. The
// string values are the canonical snake_case keys (matching the json tags), so
// FieldKeys can hand them straight to the rpc client.
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
// frequent poll stays light. The TUI's poll projects AllFields instead, because
// its list, sidebar, and detail pane need nearly every field. Its exact
// membership is not contract-locked the way the json tags are.
var PollFields = []Field{
	FieldID, FieldHash, FieldStatus, FieldPercentDone, FieldRateDownload,
	FieldRateUpload, FieldLeftUntilDone, FieldSizeWhenDone,
}

// FieldKeys converts a Field projection to the canonical snake_case key slice
// the rpc client consumes. An empty projection (nil or len 0) expands to
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
