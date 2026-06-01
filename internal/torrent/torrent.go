// Package torrent is a temporary re-export shim over internal/transmission. The
// domain model and the concrete client moved to internal/transmission; this
// package re-exports every symbol the rest of the codebase still references
// under the torrent.* path so commands and their tests compile unchanged while
// the type collapse lands. It is scaffolding: a later step repoints all
// consumers at internal/transmission directly and deletes this package.
package torrent

import (
	"context"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// Error sentinels, re-exported from internal/transmission.
var (
	ErrAuth        = transmission.ErrAuth
	ErrUnreachable = transmission.ErrUnreachable
	ErrServer      = transmission.ErrServer
	ErrUnsupported = transmission.ErrUnsupported
)

// Domain types, re-exported as aliases from internal/transmission.
type (
	ServerInfo      = transmission.ServerInfo
	Stats           = transmission.Stats
	StatsSnapshot   = transmission.StatsSnapshot
	PortTest        = transmission.PortTest
	FreeSpace       = transmission.FreeSpace
	SessionSettings = transmission.SessionSettings
	Torrent         = transmission.Torrent
	TorrentDetail   = transmission.TorrentDetail
	File            = transmission.File
	Peer            = transmission.Peer
	Tracker         = transmission.Tracker
	FileChanges     = transmission.FileChanges
	AddRequest      = transmission.AddRequest
	AddResult       = transmission.AddResult
	Field           = transmission.Field
	Status          = transmission.Status
	Source          = transmission.Source
	Selector        = transmission.Selector
	Filter          = transmission.Filter
	SortField       = transmission.SortField
)

// Status constants, re-exported from internal/transmission.
const (
	StatusStopped          = transmission.StatusStopped
	StatusQueuedToVerify   = transmission.StatusQueuedToVerify
	StatusVerifying        = transmission.StatusVerifying
	StatusQueuedToDownload = transmission.StatusQueuedToDownload
	StatusDownloading      = transmission.StatusDownloading
	StatusQueuedToSeed     = transmission.StatusQueuedToSeed
	StatusSeeding          = transmission.StatusSeeding
	StatusError            = transmission.StatusError
)

// Source constants, re-exported from internal/transmission.
const (
	SourceFile   = transmission.SourceFile
	SourceURL    = transmission.SourceURL
	SourceMagnet = transmission.SourceMagnet
)

// Field constants, re-exported from internal/transmission.
const (
	FieldID                 = transmission.FieldID
	FieldHash               = transmission.FieldHash
	FieldName               = transmission.FieldName
	FieldStatus             = transmission.FieldStatus
	FieldPercentDone        = transmission.FieldPercentDone
	FieldTotalSize          = transmission.FieldTotalSize
	FieldSizeWhenDone       = transmission.FieldSizeWhenDone
	FieldLeftUntilDone      = transmission.FieldLeftUntilDone
	FieldRateDownload       = transmission.FieldRateDownload
	FieldRateUpload         = transmission.FieldRateUpload
	FieldUploadRatio        = transmission.FieldUploadRatio
	FieldETA                = transmission.FieldETA
	FieldPeersConnected     = transmission.FieldPeersConnected
	FieldPeersSendingToUs   = transmission.FieldPeersSendingToUs
	FieldPeersGettingFromUs = transmission.FieldPeersGettingFromUs
	FieldAddedDate          = transmission.FieldAddedDate
	FieldDoneDate           = transmission.FieldDoneDate
	FieldActivityDate       = transmission.FieldActivityDate
	FieldDownloadDir        = transmission.FieldDownloadDir
	FieldError              = transmission.FieldError
	FieldLabels             = transmission.FieldLabels
	FieldQueuePosition      = transmission.FieldQueuePosition
	FieldIsFinished         = transmission.FieldIsFinished
	FieldIsStalled          = transmission.FieldIsStalled
)

// Filter constants, re-exported from internal/transmission.
const (
	FilterAll              = transmission.FilterAll
	FilterActive           = transmission.FilterActive
	FilterDownloading      = transmission.FilterDownloading
	FilterSeeding          = transmission.FilterSeeding
	FilterStopped          = transmission.FilterStopped
	FilterVerifying        = transmission.FilterVerifying
	FilterQueuedToVerify   = transmission.FilterQueuedToVerify
	FilterQueuedToDownload = transmission.FilterQueuedToDownload
	FilterQueuedToSeed     = transmission.FilterQueuedToSeed
	FilterError            = transmission.FilterError
)

// SortField constants, re-exported from internal/transmission.
const (
	SortQueue    = transmission.SortQueue
	SortName     = transmission.SortName
	SortAdded    = transmission.SortAdded
	SortSize     = transmission.SortSize
	SortProgress = transmission.SortProgress
	SortRatio    = transmission.SortRatio
	SortSpeed    = transmission.SortSpeed
)

// Field projections, re-exported from internal/transmission.
var (
	AllFields  = transmission.AllFields
	PollFields = transmission.PollFields
)

// Domain functions, re-exported from internal/transmission.
var (
	StatusFromTransmission = transmission.StatusFromTransmission
	FieldKeys              = transmission.FieldKeys
	ParseSelector          = transmission.ParseSelector
	ParseFilter            = transmission.ParseFilter
	ParseSortField         = transmission.ParseSortField
	ClassifySource         = transmission.ClassifySource
	FilterByStatus         = transmission.FilterByStatus
	SearchByName           = transmission.SearchByName
	SortTorrents           = transmission.SortTorrents
)

// Reader is the read-only half of Backend.
type Reader interface {
	ServerInfo(ctx context.Context) (ServerInfo, error)
	Stats(ctx context.Context) (Stats, error)
	ListTorrents(ctx context.Context, sel Selector, fields ...Field) ([]Torrent, error)
	TorrentDetail(ctx context.Context, id int64) (TorrentDetail, error)
	GetSession(ctx context.Context) (SessionSettings, error)
	TestPort(ctx context.Context) (PortTest, error)
	FreeSpace(ctx context.Context, path string) (FreeSpace, error)
}

// Mutator is the write half of Backend.
type Mutator interface {
	StartTorrents(ctx context.Context, sel Selector, now bool) error
	StopTorrents(ctx context.Context, sel Selector) error
	VerifyTorrents(ctx context.Context, sel Selector) error
	ReannounceTorrents(ctx context.Context, sel Selector) error
	QueueMoveTop(ctx context.Context, sel Selector) error
	QueueMoveUp(ctx context.Context, sel Selector) error
	QueueMoveDown(ctx context.Context, sel Selector) error
	QueueMoveBottom(ctx context.Context, sel Selector) error
	RemoveTorrents(ctx context.Context, sel Selector, deleteData bool) error
	MoveTorrents(ctx context.Context, sel Selector, location string, move bool) error
	SetTorrents(ctx context.Context, sel Selector, changes map[string]any) error
	SetFiles(ctx context.Context, sel Selector, changes FileChanges) error
	AddTorrent(ctx context.Context, req AddRequest) (AddResult, error)
	SetSession(ctx context.Context, changes map[string]any) error
}

// Backend is the interface commands depend on. It composes Reader and Mutator
// and adds Close. The concrete implementation is *transmission.Backend.
type Backend interface {
	Reader
	Mutator
	Close() error
}

// Compile-time assertions that *transmission.Backend satisfies the interfaces.
var (
	_ Reader  = (*transmission.Backend)(nil)
	_ Mutator = (*transmission.Backend)(nil)
	_ Backend = (*transmission.Backend)(nil)
)
