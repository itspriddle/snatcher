// Package transmission is snatcher's Transmission domain layer: it owns the
// domain model (the torrent/session types, the Selector, the list transforms,
// the source classification, and the error sentinels) and the concrete Client
// commands depend on. It wraps the typed Transmission RPC client
// (internal/transmission/rpc), which decodes the wire schemes and owns the
// canonical response types this package re-exports; this package adds the
// pure-domain helpers and maps rpc errors to the sentinels.
package transmission

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/itspriddle/snatcher/internal/transmission/rpc"
)

// Client wraps an rpc.Client, returning the canonical domain types directly and
// mapping rpc errors to the sentinels. It is the concrete Transmission client
// commands depend on. A zero Client is not usable; build one with New.
type Client struct {
	client *rpc.Client
}

// APIResponse is the result of a CallAPI: the normalized response envelope plus
// the exact server bytes and an OK flag. It mirrors rpc.RawResponse, so commands
// render a stable shape (and can output the raw bytes verbatim) without
// importing the rpc package.
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

// Option configures a Client built by New. It is a wrapper over the rpc
// package's options, so callers (commands) never need to import the rpc package
// to configure the client. Each Option contributes the rpc.Option it wraps via
// the unexported rpcOption accessor.
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

// New returns a Client that talks to the Transmission server at rpcURL,
// applying the supplied options. Callers configure the client through
// transmission.Option (e.g. transmission.WithBasicAuth) and never need to touch
// the rpc package directly.
func New(rpcURL string, opts ...Option) *Client {
	rpcOpts := make([]rpc.Option, len(opts))
	for i, opt := range opts {
		rpcOpts[i] = opt.rpcOption()
	}

	return &Client{client: rpc.NewClient(rpcURL, rpcOpts...)}
}

// ServerInfo performs a SessionGet round-trip and returns the server's
// ServerInfo. RPC failures are mapped to the sentinels.
func (b *Client) ServerInfo(ctx context.Context) (ServerInfo, error) {
	session, err := b.client.SessionGet(ctx)
	if err != nil {
		return ServerInfo{}, mapError(err)
	}

	return session.ServerInfo(), nil
}

// Stats performs a SessionStats round-trip and returns the server's Stats. RPC
// failures are mapped to the sentinels.
func (b *Client) Stats(ctx context.Context) (Stats, error) {
	stats, err := b.client.SessionStats(ctx)
	if err != nil {
		return Stats{}, mapError(err)
	}

	return stats, nil
}

// ListTorrents performs a TorrentGet round-trip and returns the Torrents
// matched by sel. An All selector (or one whose RPCIDs is empty) returns every
// torrent; otherwise the server filters to the selected ids/hashes. The fields
// projection is translated to the rpc client's canonical snake_case keys; an
// empty projection requests the full set. RPC failures are mapped to the
// sentinels. The returned slice is non-nil on success, empty when there are no
// matching torrents.
func (b *Client) ListTorrents(ctx context.Context, sel Selector, fields ...Field) ([]Torrent, error) {
	torrents, err := b.client.TorrentGet(ctx, sel.RPCIDs(), FieldKeys(fields))
	if err != nil {
		return nil, mapError(err)
	}

	return torrents, nil
}

// TorrentDetail performs a single detail TorrentGet round-trip for the torrent
// with the given numeric id. An id that matches no torrent returns an error.
// RPC failures are mapped to the sentinels.
func (b *Client) TorrentDetail(ctx context.Context, id int64) (TorrentDetail, error) {
	details, err := b.client.TorrentGetDetail(ctx, []any{id})
	if err != nil {
		return TorrentDetail{}, mapError(err)
	}

	if len(details) == 0 {
		return TorrentDetail{}, fmt.Errorf("no torrent matched id %d", id)
	}

	return details[0], nil
}

// StartTorrents resumes the torrents matched by sel, dispatching to
// torrent_start_now when now is true (disregarding queue position) and
// torrent_start otherwise. An All selector omits the ids argument so the server
// targets every torrent. RPC failures are mapped to the sentinels.
func (b *Client) StartTorrents(ctx context.Context, sel Selector, now bool) error {
	return mapError(b.client.TorrentStart(ctx, sel.RPCIDs(), now))
}

// StopTorrents pauses the torrents matched by sel. An All selector omits the ids
// argument so the server targets every torrent. RPC failures are mapped to the
// sentinels.
func (b *Client) StopTorrents(ctx context.Context, sel Selector) error {
	return mapError(b.client.TorrentStop(ctx, sel.RPCIDs()))
}

// VerifyTorrents queues local-data verification for the torrents matched by sel.
// An All selector omits the ids argument so the server targets every torrent.
// RPC failures are mapped to the sentinels.
func (b *Client) VerifyTorrents(ctx context.Context, sel Selector) error {
	return mapError(b.client.TorrentVerify(ctx, sel.RPCIDs()))
}

// ReannounceTorrents forces the torrents matched by sel to re-announce to their
// trackers immediately. An All selector omits the ids argument so the server
// targets every torrent. RPC failures are mapped to the sentinels.
func (b *Client) ReannounceTorrents(ctx context.Context, sel Selector) error {
	return mapError(b.client.TorrentReannounce(ctx, sel.RPCIDs()))
}

// QueueMoveTop moves the torrents matched by sel to the top of the queue. An All
// selector omits the ids argument so the server targets every torrent. RPC
// failures are mapped to the sentinels.
func (b *Client) QueueMoveTop(ctx context.Context, sel Selector) error {
	return mapError(b.client.QueueMoveTop(ctx, sel.RPCIDs()))
}

// QueueMoveUp moves the torrents matched by sel up one position in the queue. An
// All selector omits the ids argument so the server targets every torrent. RPC
// failures are mapped to the sentinels.
func (b *Client) QueueMoveUp(ctx context.Context, sel Selector) error {
	return mapError(b.client.QueueMoveUp(ctx, sel.RPCIDs()))
}

// QueueMoveDown moves the torrents matched by sel down one position in the queue.
// An All selector omits the ids argument so the server targets every torrent. RPC
// failures are mapped to the sentinels.
func (b *Client) QueueMoveDown(ctx context.Context, sel Selector) error {
	return mapError(b.client.QueueMoveDown(ctx, sel.RPCIDs()))
}

// QueueMoveBottom moves the torrents matched by sel to the bottom of the queue.
// An All selector omits the ids argument so the server targets every torrent. RPC
// failures are mapped to the sentinels.
func (b *Client) QueueMoveBottom(ctx context.Context, sel Selector) error {
	return mapError(b.client.QueueMoveBottom(ctx, sel.RPCIDs()))
}

// RemoveTorrents removes the torrents matched by sel, also deleting their
// downloaded local files when deleteData is true. An All selector omits the ids
// argument so the server targets every torrent. RPC failures are mapped to the
// sentinels.
func (b *Client) RemoveTorrents(ctx context.Context, sel Selector, deleteData bool) error {
	return mapError(b.client.TorrentRemove(ctx, sel.RPCIDs(), deleteData))
}

// MoveTorrents changes the download location of the torrents matched by sel to
// location, relocating the existing files when move is true and repointing
// without relocating (searching location for already-present files) when false.
// An All selector omits the ids argument so the server targets every torrent.
// RPC failures are mapped to the sentinels.
func (b *Client) MoveTorrents(ctx context.Context, sel Selector, location string, move bool) error {
	return mapError(b.client.TorrentSetLocation(ctx, sel.RPCIDs(), location, move))
}

// SetTorrents writes per-torrent fields on the torrents matched by sel. The
// changes map is keyed by kebab-case field key with typed values; each key is
// translated to the snake_case rpc key (a mechanical "-"->"_" replacement,
// reusing rpcSettingKey) before forwarding to rpc.Client.TorrentSet, which owns
// the per-wire-scheme translation (including the bespoke camelCase names). Only
// the keys present in changes are written. An All selector omits the ids
// argument so the server targets every torrent. RPC failures are mapped to the
// sentinels.
func (b *Client) SetTorrents(ctx context.Context, sel Selector, changes map[string]any) error {
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
// server targets every torrent. RPC failures are mapped to the sentinels.
func (b *Client) SetFiles(ctx context.Context, sel Selector, changes FileChanges) error {
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
// the RPC filename field, and passes DownloadDir/Paused/Labels through. A
// duplicate is reported via AddResult.Duplicate (not an error). RPC failures are
// mapped to the sentinels.
func (b *Client) AddTorrent(ctx context.Context, req AddRequest) (AddResult, error) {
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
		return AddResult{}, mapError(err)
	}

	return res, nil
}

// GetSession performs a SessionGet round-trip and returns the server's flattened
// SessionSettings. RPC failures are mapped to the sentinels.
func (b *Client) GetSession(ctx context.Context) (SessionSettings, error) {
	session, err := b.client.SessionGet(ctx)
	if err != nil {
		return SessionSettings{}, mapError(err)
	}

	return session.Settings(), nil
}

// SetSession writes session settings. The changes map is keyed by kebab-case
// setting key with typed values; each key is translated to the snake_case rpc
// key (a mechanical "-"->"_" replacement) before forwarding to
// rpc.Client.SessionSet, which owns the per-wire-scheme translation. RPC
// failures are mapped to the sentinels.
func (b *Client) SetSession(ctx context.Context, changes map[string]any) error {
	settings := make(map[string]any, len(changes))
	for key, value := range changes {
		settings[rpcSettingKey(key)] = value
	}

	return mapError(b.client.SessionSet(ctx, settings))
}

// TestPort performs a PortTest round-trip and returns the PortTest. A
// reachable-but-closed port is reported via PortTest.Open == false (not a Go
// error); RPC failures are mapped to the sentinels.
func (b *Client) TestPort(ctx context.Context) (PortTest, error) {
	result, err := b.client.PortTest(ctx)
	if err != nil {
		return PortTest{}, mapError(err)
	}

	return result, nil
}

// FreeSpace performs a FreeSpace round-trip for path and returns the FreeSpace.
// TotalBytes is carried through as reported (0 when the server is too old to
// return total capacity, in which case the json tag omits it). RPC failures are
// mapped to the sentinels.
func (b *Client) FreeSpace(ctx context.Context, path string) (FreeSpace, error) {
	result, err := b.client.FreeSpace(ctx, path)
	if err != nil {
		return FreeSpace{}, mapError(err)
	}

	return result, nil
}

// Close releases any resources held by the backend. The Transmission adapter is
// stateless (it holds only an rpc.Client over the shared HTTP transport and
// caches nothing on disk), so Close is a no-op that always returns nil.
func (b *Client) Close() error {
	return nil
}

// CallAPI issues an arbitrary RPC method with raw arguments and returns the
// normalized APIResponse. Transport and auth failures are mapped to the
// sentinels via mapError; an RPC-level non-success is *not* mapped to a Go error
// — it is reported via APIResponse.OK == false so the api command can print the
// envelope and set its own exit code.
func (b *Client) CallAPI(ctx context.Context, method string, args json.RawMessage) (APIResponse, error) {
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

// rpcSettingKey translates a kebab-case setting key into the snake_case key the
// rpc client expects (its canonical input contract). The translation is a
// mechanical "-"->"_" replacement; the rpc layer owns any further per-wire-
// scheme key remapping (e.g. cache-size-mib's wire spelling) and the encryption
// enum normalization.
func rpcSettingKey(key string) string {
	return strings.ReplaceAll(key, "-", "_")
}

// mapError translates an rpc-layer error into a sentinel while preserving the
// original error in the wrap chain (so errors.Is against the rpc sentinels still
// works and the server message is retained). An rpc.ErrAuth becomes ErrAuth;
// rpc.ErrUnsupported becomes ErrUnsupported; rpc.ErrRPC (a server-side
// application error) becomes ErrServer; any other failure — including
// rpc.ErrTransport — becomes ErrUnreachable. A nil error is returned unchanged.
func mapError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, rpc.ErrAuth) {
		return fmt.Errorf("%w: %w", ErrAuth, err)
	}

	if errors.Is(err, rpc.ErrUnsupported) {
		return fmt.Errorf("%w: %w", ErrUnsupported, err)
	}

	if errors.Is(err, rpc.ErrRPC) {
		return fmt.Errorf("%w: %w", ErrServer, err)
	}

	return fmt.Errorf("%w: %w", ErrUnreachable, err)
}
