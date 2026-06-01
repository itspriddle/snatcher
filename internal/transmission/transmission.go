// Package transmission adapts the typed Transmission RPC client
// (internal/transmission/rpc) to the vendor-neutral torrent.Backend interface.
// It is the only package that imports the rpc package; commands depend solely
// on torrent.Backend, so a future client backend slots in without rewriting
// commands.
package transmission

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/itspriddle/snatcher/internal/torrent"
	"github.com/itspriddle/snatcher/internal/transmission/rpc"
)

// Backend implements torrent.Backend by wrapping an rpc.Client, converting the
// rpc package's typed results into neutral torrent types and mapping rpc errors
// to the neutral sentinels. A zero Backend is not usable; build one with New.
type Backend struct {
	client *rpc.Client
}

// Compile-time assertion that *Backend satisfies torrent.Backend.
var _ torrent.Backend = (*Backend)(nil)

// Compile-time assertion that *Backend satisfies APICaller.
var _ APICaller = (*Backend)(nil)

// APICaller is the Transmission-specific capability for issuing arbitrary RPC
// calls. It is deliberately kept off the vendor-neutral torrent.Backend
// interface: a generic "call any method" escape hatch is a Transmission feature,
// so the api command type-asserts the backend to this capability rather than
// assuming every backend supports it.
type APICaller interface {
	// CallAPI issues an arbitrary RPC method with raw arguments and returns the
	// normalized APIResponse. method is the canonical kebab-case name; args is the
	// raw arguments object (nil for a zero-argument method). An RPC-level failure
	// is reported via APIResponse.OK == false, not a Go error; only transport and
	// auth failures (mapped to the neutral sentinels) return a Go error.
	CallAPI(ctx context.Context, method string, args json.RawMessage) (APIResponse, error)
}

// APIResponse is the result of a CallAPI: the normalized response envelope plus
// the exact server bytes and an OK flag. It is the Transmission-layer mirror of
// rpc.RawResponse, so commands render a stable shape (and can output the raw
// bytes verbatim) without importing the rpc package.
type APIResponse struct {
	// Raw is the exact response body the server sent, for verbatim --raw output.
	Raw json.RawMessage

	// Result is the normalized status string: "success" on success, or the
	// server's error text on an RPC-level failure.
	Result string

	// Arguments is the normalized result payload: the server's result object on
	// success, or null on an RPC-level failure.
	Arguments json.RawMessage

	// Tag is the normalized request tag/id echoed by the server.
	Tag int

	// Code is the error code on an RPC-level failure (JSON-RPC error.code).
	Code int

	// OK reports whether the call succeeded at the RPC level.
	OK bool
}

// Option configures a Backend built by New. It is a neutral wrapper over the
// rpc package's options, so callers (commands) never need to import the rpc
// package to configure the backend. Each Option contributes the rpc.Option it
// wraps via the unexported rpcOption accessor.
type Option interface {
	rpcOption() rpc.Option
}

// optionFunc adapts an rpc.Option into a transmission.Option.
type optionFunc func() rpc.Option

func (f optionFunc) rpcOption() rpc.Option { return f() }

// WithBasicAuth enables HTTP Basic Auth with the given credentials, wrapping
// rpc.WithBasicAuth. When username is empty, requests are sent without an
// Authorization header.
func WithBasicAuth(username, password string) Option {
	return optionFunc(func() rpc.Option {
		return rpc.WithBasicAuth(username, password)
	})
}

// WithHTTPClient overrides the HTTP client used for requests, wrapping
// rpc.WithHTTPClient. Primarily useful in tests.
func WithHTTPClient(client rpc.HTTPDoer) Option {
	return optionFunc(func() rpc.Option {
		return rpc.WithHTTPClient(client)
	})
}

// New returns a Backend that talks to the Transmission server at rpcURL,
// applying the supplied neutral options. Callers configure the backend through
// transmission.Option (e.g. transmission.WithBasicAuth) and never need to touch
// the rpc package directly.
func New(rpcURL string, opts ...Option) *Backend {
	rpcOpts := make([]rpc.Option, len(opts))
	for i, opt := range opts {
		rpcOpts[i] = opt.rpcOption()
	}

	return &Backend{client: rpc.NewClient(rpcURL, rpcOpts...)}
}

// ServerInfo performs a SessionGet round-trip and returns the neutral
// ServerInfo. RPC failures are mapped to the neutral sentinels.
func (b *Backend) ServerInfo(ctx context.Context) (torrent.ServerInfo, error) {
	session, err := b.client.SessionGet(ctx)
	if err != nil {
		return torrent.ServerInfo{}, mapError(err)
	}

	return torrent.ServerInfo{
		Version:          session.Version,
		RPCVersionSemver: session.RPCVersionSemver,
		Protocol:         session.Protocol,
	}, nil
}

// Stats performs a SessionStats round-trip and returns the neutral Stats. RPC
// failures are mapped to the neutral sentinels.
func (b *Backend) Stats(ctx context.Context) (torrent.Stats, error) {
	stats, err := b.client.SessionStats(ctx)
	if err != nil {
		return torrent.Stats{}, mapError(err)
	}

	return torrent.Stats{
		ActiveTorrentCount: stats.ActiveTorrentCount,
		PausedTorrentCount: stats.PausedTorrentCount,
		TorrentCount:       stats.TorrentCount,
		DownloadSpeed:      stats.DownloadSpeed,
		UploadSpeed:        stats.UploadSpeed,
		CumulativeStats:    torrent.StatsSnapshot(stats.CumulativeStats),
		CurrentStats:       torrent.StatsSnapshot(stats.CurrentStats),
	}, nil
}

// ListTorrents performs a TorrentGet round-trip and returns the neutral
// Torrents matched by sel. An All selector (or one whose RPCIDs is empty)
// returns every torrent; otherwise the server filters to the selected
// ids/hashes. The fields projection is translated to the rpc client's canonical
// snake_case keys; an empty projection requests the full set. RPC failures are
// mapped to the neutral sentinels. The returned slice is non-nil on success,
// empty when there are no matching torrents.
func (b *Backend) ListTorrents(ctx context.Context, sel torrent.Selector, fields ...torrent.Field) ([]torrent.Torrent, error) {
	raw, err := b.client.TorrentGet(ctx, sel.RPCIDs(), torrent.FieldKeys(fields))
	if err != nil {
		return nil, mapError(err)
	}

	torrents := make([]torrent.Torrent, len(raw))
	for i, rt := range raw {
		torrents[i] = mapTorrent(rt)
	}

	return torrents, nil
}

// TorrentDetail performs a single detail TorrentGet round-trip for the torrent
// with the given numeric id and maps the canonical rpc.TorrentDetail to the
// neutral torrent.TorrentDetail. An id that matches no torrent returns an error.
// RPC failures are mapped to the neutral sentinels.
func (b *Backend) TorrentDetail(ctx context.Context, id int64) (torrent.TorrentDetail, error) {
	raw, err := b.client.TorrentGetDetail(ctx, []any{id})
	if err != nil {
		return torrent.TorrentDetail{}, mapError(err)
	}

	if len(raw) == 0 {
		return torrent.TorrentDetail{}, fmt.Errorf("no torrent matched id %d", id)
	}

	return mapTorrentDetail(raw[0]), nil
}

// StartTorrents resumes the torrents matched by sel, dispatching to
// torrent_start_now when now is true (disregarding queue position) and
// torrent_start otherwise. An All selector omits the ids argument so the server
// targets every torrent. RPC failures are mapped to the neutral sentinels.
func (b *Backend) StartTorrents(ctx context.Context, sel torrent.Selector, now bool) error {
	return mapError(b.client.TorrentStart(ctx, sel.RPCIDs(), now))
}

// StopTorrents pauses the torrents matched by sel. An All selector omits the ids
// argument so the server targets every torrent. RPC failures are mapped to the
// neutral sentinels.
func (b *Backend) StopTorrents(ctx context.Context, sel torrent.Selector) error {
	return mapError(b.client.TorrentStop(ctx, sel.RPCIDs()))
}

// VerifyTorrents queues local-data verification for the torrents matched by sel.
// An All selector omits the ids argument so the server targets every torrent.
// RPC failures are mapped to the neutral sentinels.
func (b *Backend) VerifyTorrents(ctx context.Context, sel torrent.Selector) error {
	return mapError(b.client.TorrentVerify(ctx, sel.RPCIDs()))
}

// ReannounceTorrents forces the torrents matched by sel to re-announce to their
// trackers immediately. An All selector omits the ids argument so the server
// targets every torrent. RPC failures are mapped to the neutral sentinels.
func (b *Backend) ReannounceTorrents(ctx context.Context, sel torrent.Selector) error {
	return mapError(b.client.TorrentReannounce(ctx, sel.RPCIDs()))
}

// QueueMoveTop moves the torrents matched by sel to the top of the queue. An All
// selector omits the ids argument so the server targets every torrent. RPC
// failures are mapped to the neutral sentinels.
func (b *Backend) QueueMoveTop(ctx context.Context, sel torrent.Selector) error {
	return mapError(b.client.QueueMoveTop(ctx, sel.RPCIDs()))
}

// QueueMoveUp moves the torrents matched by sel up one position in the queue. An
// All selector omits the ids argument so the server targets every torrent. RPC
// failures are mapped to the neutral sentinels.
func (b *Backend) QueueMoveUp(ctx context.Context, sel torrent.Selector) error {
	return mapError(b.client.QueueMoveUp(ctx, sel.RPCIDs()))
}

// QueueMoveDown moves the torrents matched by sel down one position in the queue.
// An All selector omits the ids argument so the server targets every torrent. RPC
// failures are mapped to the neutral sentinels.
func (b *Backend) QueueMoveDown(ctx context.Context, sel torrent.Selector) error {
	return mapError(b.client.QueueMoveDown(ctx, sel.RPCIDs()))
}

// QueueMoveBottom moves the torrents matched by sel to the bottom of the queue.
// An All selector omits the ids argument so the server targets every torrent. RPC
// failures are mapped to the neutral sentinels.
func (b *Backend) QueueMoveBottom(ctx context.Context, sel torrent.Selector) error {
	return mapError(b.client.QueueMoveBottom(ctx, sel.RPCIDs()))
}

// RemoveTorrents removes the torrents matched by sel, also deleting their
// downloaded local files when deleteData is true. An All selector omits the ids
// argument so the server targets every torrent. RPC failures are mapped to the
// neutral sentinels.
func (b *Backend) RemoveTorrents(ctx context.Context, sel torrent.Selector, deleteData bool) error {
	return mapError(b.client.TorrentRemove(ctx, sel.RPCIDs(), deleteData))
}

// MoveTorrents changes the download location of the torrents matched by sel to
// location, relocating the existing files when move is true and repointing
// without relocating (searching location for already-present files) when false.
// An All selector omits the ids argument so the server targets every torrent.
// RPC failures are mapped to the neutral sentinels.
func (b *Backend) MoveTorrents(ctx context.Context, sel torrent.Selector, location string, move bool) error {
	return mapError(b.client.TorrentSetLocation(ctx, sel.RPCIDs(), location, move))
}

// SetTorrents writes per-torrent fields on the torrents matched by sel. The
// changes map is keyed by neutral kebab-case field key with typed values; each
// key is translated to the snake_case rpc key (a mechanical "-"->"_"
// replacement, reusing rpcSettingKey) before forwarding to rpc.Client.TorrentSet,
// which owns the per-wire-scheme translation (including the bespoke camelCase
// names). Only the keys present in changes are written. An All selector omits
// the ids argument so the server targets every torrent. RPC failures are mapped
// to the neutral sentinels.
func (b *Backend) SetTorrents(ctx context.Context, sel torrent.Selector, changes map[string]any) error {
	settings := make(map[string]any, len(changes))
	for key, value := range changes {
		settings[rpcSettingKey(key)] = value
	}

	return mapError(b.client.TorrentSet(ctx, sel.RPCIDs(), settings))
}

// SetFiles applies per-file selection and priority changes to the torrents
// matched by sel, routing them through rpc.Client.TorrentSet as the canonical
// snake_case torrent-set arguments files_wanted / files_unwanted / priority_high
// / priority_normal / priority_low. The rpc layer translates each to its wire
// spelling — verbatim snake_case for JSON-RPC 2.0, the mechanical "_"->"-" kebab
// transform for bespoke (none of these keys is a camelCase exception). Only the
// non-empty FileChanges fields are sent; an empty FileChanges issues no
// torrent-set arguments (a no-op). An All selector omits the ids argument so the
// server targets every torrent. RPC failures are mapped to the neutral sentinels.
func (b *Backend) SetFiles(ctx context.Context, sel torrent.Selector, changes torrent.FileChanges) error {
	settings := make(map[string]any, 5)
	if len(changes.Wanted) > 0 {
		settings["files_wanted"] = changes.Wanted
	}
	if len(changes.Unwanted) > 0 {
		settings["files_unwanted"] = changes.Unwanted
	}
	if len(changes.PriorityHigh) > 0 {
		settings["priority_high"] = changes.PriorityHigh
	}
	if len(changes.PriorityNormal) > 0 {
		settings["priority_normal"] = changes.PriorityNormal
	}
	if len(changes.PriorityLow) > 0 {
		settings["priority_low"] = changes.PriorityLow
	}

	return mapError(b.client.TorrentSet(ctx, sel.RPCIDs(), settings))
}

// AddTorrent adds a single torrent via the rpc client's TorrentAdd. It
// base64-encodes raw Metainfo into the RPC metainfo field, maps URL/Magnet to
// the RPC filename field, and passes DownloadDir/Paused/Labels through. The
// canonical rpc.AddResult is mapped to the neutral AddResult; a duplicate is
// reported via AddResult.Duplicate (not an error). RPC failures are mapped to
// the neutral sentinels.
func (b *Backend) AddTorrent(ctx context.Context, req torrent.AddRequest) (torrent.AddResult, error) {
	rpcReq := rpc.AddRequest{
		DownloadDir: req.DownloadDir,
		Paused:      req.Paused,
		Labels:      req.Labels,
	}

	switch {
	case len(req.Metainfo) > 0:
		rpcReq.Metainfo = base64.StdEncoding.EncodeToString(req.Metainfo)
	case req.URL != "":
		rpcReq.Filename = req.URL
	case req.Magnet != "":
		rpcReq.Filename = req.Magnet
	}

	res, err := b.client.TorrentAdd(ctx, rpcReq)
	if err != nil {
		return torrent.AddResult{}, mapError(err)
	}

	return torrent.AddResult{
		ID:        res.ID,
		Hash:      res.Hash,
		Name:      res.Name,
		Duplicate: res.Duplicate,
	}, nil
}

// GetSession performs a SessionGet round-trip and maps the canonical rpc.Session
// to the neutral torrent.SessionSettings. RPC failures are mapped to the neutral
// sentinels.
func (b *Backend) GetSession(ctx context.Context) (torrent.SessionSettings, error) {
	session, err := b.client.SessionGet(ctx)
	if err != nil {
		return torrent.SessionSettings{}, mapError(err)
	}

	return mapSession(session), nil
}

// SetSession writes session settings. The changes map is keyed by neutral
// kebab-case setting key with typed values; each key is translated to the
// snake_case rpc key (a mechanical "-"->"_" replacement) before forwarding to
// rpc.Client.SessionSet, which owns the per-wire-scheme translation. RPC
// failures are mapped to the neutral sentinels.
func (b *Backend) SetSession(ctx context.Context, changes map[string]any) error {
	settings := make(map[string]any, len(changes))
	for key, value := range changes {
		settings[rpcSettingKey(key)] = value
	}

	return mapError(b.client.SessionSet(ctx, settings))
}

// TestPort performs a PortTest round-trip and maps the canonical
// rpc.PortTestResult to the neutral torrent.PortTest. A reachable-but-closed
// port is reported via PortTest.Open == false (not a Go error); RPC failures are
// mapped to the neutral sentinels.
func (b *Backend) TestPort(ctx context.Context) (torrent.PortTest, error) {
	result, err := b.client.PortTest(ctx)
	if err != nil {
		return torrent.PortTest{}, mapError(err)
	}

	return torrent.PortTest{
		Open:       result.Open,
		IPProtocol: result.IPProtocol,
	}, nil
}

// FreeSpace performs a FreeSpace round-trip for path and maps the canonical
// rpc.FreeSpaceResult to the neutral torrent.FreeSpace. TotalBytes is carried
// through as reported (0 when the server is too old to return total capacity, in
// which case the neutral json tag omits it). RPC failures are mapped to the
// neutral sentinels.
func (b *Backend) FreeSpace(ctx context.Context, path string) (torrent.FreeSpace, error) {
	result, err := b.client.FreeSpace(ctx, path)
	if err != nil {
		return torrent.FreeSpace{}, mapError(err)
	}

	return torrent.FreeSpace{
		Path:       result.Path,
		SizeBytes:  result.SizeBytes,
		TotalBytes: result.TotalBytes,
	}, nil
}

// CallAPI issues an arbitrary RPC method with raw arguments and returns the
// normalized APIResponse. Transport and auth failures are mapped to the neutral
// sentinels via mapError; an RPC-level non-success is *not* mapped to a Go error
// — it is reported via APIResponse.OK == false so the api command can print the
// envelope and set its own exit code.
func (b *Backend) CallAPI(ctx context.Context, method string, args json.RawMessage) (APIResponse, error) {
	resp, err := b.client.Call(ctx, method, args)
	if err != nil {
		return APIResponse{}, mapError(err)
	}

	return APIResponse{
		Raw:       resp.Raw,
		Result:    resp.Result,
		Arguments: resp.Arguments,
		Tag:       resp.Tag,
		Code:      resp.Code,
		OK:        resp.OK,
	}, nil
}

// mapTorrent converts a canonical rpc.Torrent into the neutral torrent.Torrent,
// translating Transmission's raw status int and error fields into the neutral
// Status (a non-zero error code or non-empty error string promotes to
// StatusError).
func mapTorrent(t rpc.Torrent) torrent.Torrent {
	return torrent.Torrent{
		ID:                 t.ID,
		Hash:               t.Hash,
		Name:               t.Name,
		Status:             torrent.StatusFromTransmission(t.Status, t.ErrorCode, t.Error),
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
		QueuePosition:      t.QueuePosition,
		IsFinished:         t.IsFinished,
		IsStalled:          t.IsStalled,
	}
}

// mapTorrentDetail converts a canonical rpc.TorrentDetail into the neutral
// torrent.TorrentDetail: the embedded Torrent is mapped via mapTorrent (so the
// status promotion applies), and the detail collections and scalars are copied
// across. The rpc and neutral File/Peer/Tracker types share identical layouts,
// so each slice converts element-wise.
func mapTorrentDetail(d rpc.TorrentDetail) torrent.TorrentDetail {
	files := make([]torrent.File, len(d.Files))
	for i, f := range d.Files {
		files[i] = torrent.File(f)
	}

	peers := make([]torrent.Peer, len(d.Peers))
	for i, p := range d.Peers {
		peers[i] = torrent.Peer(p)
	}

	trackers := make([]torrent.Tracker, len(d.Trackers))
	for i, tr := range d.Trackers {
		trackers[i] = torrent.Tracker(tr)
	}

	return torrent.TorrentDetail{
		Torrent: mapTorrent(d.Torrent),

		Files:    files,
		Peers:    peers,
		Trackers: trackers,
		Pieces:   d.Pieces,

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

// mapSession converts a canonical rpc.Session into the neutral
// torrent.SessionSettings. The rpc settings fields are pointers (an absent field
// is distinguishable from a zero value); the neutral type flattens them,
// substituting the zero value when the server omitted a field.
func mapSession(s rpc.Session) torrent.SessionSettings {
	return torrent.SessionSettings{
		DownloadDir:             deref(s.DownloadDir),
		StartAddedTorrents:      deref(s.StartAddedTorrents),
		RenamePartialFiles:      deref(s.RenamePartialFiles),
		IncompleteDirEnabled:    deref(s.IncompleteDirEnabled),
		IncompleteDir:           deref(s.IncompleteDir),
		SeedRatioLimited:        deref(s.SeedRatioLimited),
		SeedRatioLimit:          deref(s.SeedRatioLimit),
		IdleSeedingLimitEnabled: deref(s.IdleSeedingLimitEnabled),
		IdleSeedingLimit:        deref(s.IdleSeedingLimit),
		CacheSizeMiB:            deref(s.CacheSizeMiB),

		PeerPort:              deref(s.PeerPort),
		PeerPortRandomOnStart: deref(s.PeerPortRandomOnStart),
		PortForwardingEnabled: deref(s.PortForwardingEnabled),
		Encryption:            deref(s.Encryption),
		PeerLimitGlobal:       deref(s.PeerLimitGlobal),
		PeerLimitPerTorrent:   deref(s.PeerLimitPerTorrent),
		PexEnabled:            deref(s.PexEnabled),
		DHTEnabled:            deref(s.DHTEnabled),
		LPDEnabled:            deref(s.LPDEnabled),
		UTPEnabled:            deref(s.UTPEnabled),
		BlocklistEnabled:      deref(s.BlocklistEnabled),
		BlocklistURL:          deref(s.BlocklistURL),
		BlocklistSize:         deref(s.BlocklistSize),

		SpeedLimitDownEnabled: deref(s.SpeedLimitDownEnabled),
		SpeedLimitDown:        deref(s.SpeedLimitDown),
		SpeedLimitUpEnabled:   deref(s.SpeedLimitUpEnabled),
		SpeedLimitUp:          deref(s.SpeedLimitUp),
		AltSpeedEnabled:       deref(s.AltSpeedEnabled),
		AltSpeedDown:          deref(s.AltSpeedDown),
		AltSpeedUp:            deref(s.AltSpeedUp),
		AltSpeedTimeEnabled:   deref(s.AltSpeedTimeEnabled),
		AltSpeedTimeBegin:     deref(s.AltSpeedTimeBegin),
		AltSpeedTimeEnd:       deref(s.AltSpeedTimeEnd),
		AltSpeedTimeDay:       deref(s.AltSpeedTimeDay),

		DownloadQueueEnabled: deref(s.DownloadQueueEnabled),
		DownloadQueueSize:    deref(s.DownloadQueueSize),
		SeedQueueEnabled:     deref(s.SeedQueueEnabled),
		SeedQueueSize:        deref(s.SeedQueueSize),
		QueueStalledEnabled:  deref(s.QueueStalledEnabled),
		QueueStalledMinutes:  deref(s.QueueStalledMinutes),
	}
}

// deref returns the value pointed to by p, or the zero value of T when p is nil.
// It flattens the rpc.Session's pointer settings fields into the neutral
// SessionSettings' plain typed fields.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}

	return *p
}

// rpcSettingKey translates a neutral kebab-case setting key into the snake_case
// key the rpc client expects (its canonical input contract). The translation is
// a mechanical "-"->"_" replacement; the rpc layer owns any further per-wire-
// scheme key remapping (e.g. cache-size-mib's wire spelling) and the encryption
// enum normalization.
func rpcSettingKey(key string) string {
	return strings.ReplaceAll(key, "-", "_")
}

// mapError translates an rpc-layer error into a neutral sentinel while
// preserving the original error in the wrap chain (so errors.Is against the rpc
// sentinels still works and the server message is retained). An rpc.ErrAuth
// becomes torrent.ErrAuth; rpc.ErrUnsupported becomes torrent.ErrUnsupported;
// rpc.ErrRPC (a server-side application error) becomes torrent.ErrServer; any
// other failure — including rpc.ErrTransport — becomes torrent.ErrUnreachable.
// A nil error is returned unchanged.
func mapError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, rpc.ErrAuth) {
		return fmt.Errorf("%w: %w", torrent.ErrAuth, err)
	}

	if errors.Is(err, rpc.ErrUnsupported) {
		return fmt.Errorf("%w: %w", torrent.ErrUnsupported, err)
	}

	if errors.Is(err, rpc.ErrRPC) {
		return fmt.Errorf("%w: %w", torrent.ErrServer, err)
	}

	return fmt.Errorf("%w: %w", torrent.ErrUnreachable, err)
}
