// This file contains the Client: the typed entry point that detects the
// server's RPC scheme on first use and then dispatches typed calls through the
// negotiated Protocol, so callers never deal with version differences.
package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// jsonrpcMajorMin is the semver major version (of rpc-version-semver) at which
// Transmission supports JSON-RPC 2.0. 4.1.0 ships rpc-version-semver 6.0.0.
const jsonrpcMajorMin = 6

// minRPCVersion is the lowest integer rpc-version snatcher operates against:
// Transmission 4.0.0 reports rpc-version 17. Older servers are refused with
// ErrUnsupported. The integer rpc-version (present since Transmission 1.50) is
// the dependable cross-version signal; rpc-version-semver only exists from 4.0.0
// and the CSRF version header only from 4.1.0.
const minRPCVersion = 17

// ErrUnsupported signals that the server is older than the minimum Transmission
// version snatcher supports (see minRPCVersion). It is matchable with errors.Is.
var ErrUnsupported = errors.New("rpc: unsupported server version")

// Client is the typed Transmission RPC client. It probes the server once to
// detect the RPC scheme, then dispatches typed calls through the negotiated
// Protocol. A zero Client is not usable; build one with NewClient.
type Client struct {
	transport *Transport

	detectMu sync.Mutex
	protocol Protocol // non-nil once detection has succeeded
}

// NewClient returns a Client that talks to rpcURL, forwarding opts to the
// underlying Transport (e.g. WithBasicAuth, WithHTTPClient).
func NewClient(rpcURL string, opts ...Option) *Client {
	return &Client{transport: NewTransport(rpcURL, opts...)}
}

// SessionGet detects the protocol (once) and returns the server's canonical
// Session.
func (c *Client) SessionGet(ctx context.Context) (Session, error) {
	proto, err := c.detect(ctx)
	if err != nil {
		return Session{}, err
	}

	return proto.SessionGet(ctx, c.transport)
}

// SessionSet detects the protocol (once) and writes session settings. settings
// is a snake_case-keyed map of canonical setting keys to typed values; the
// negotiated protocol translates the keys to its wire scheme and normalizes the
// encryption enum. The method confirms success and carries no response payload.
func (c *Client) SessionSet(ctx context.Context, settings map[string]any) error {
	proto, err := c.detect(ctx)
	if err != nil {
		return err
	}

	return proto.SessionSet(ctx, c.transport, settings)
}

// SessionStats detects the protocol (once) and returns the server's canonical
// SessionStats.
func (c *Client) SessionStats(ctx context.Context) (SessionStats, error) {
	proto, err := c.detect(ctx)
	if err != nil {
		return SessionStats{}, err
	}

	return proto.SessionStats(ctx, c.transport)
}

// TorrentGet detects the protocol (once) and returns the server's torrents as
// canonical Torrents. A nil or empty ids slice requests all torrents; otherwise
// the mixed id/hash array selects them (matching the other Client methods).
// fields is a list of canonical snake_case field keys; an empty fields slice
// requests the full canonical set. The negotiated protocol translates the
// canonical fields to its wire scheme.
func (c *Client) TorrentGet(ctx context.Context, ids []any, fields []string) ([]Torrent, error) {
	proto, err := c.detect(ctx)
	if err != nil {
		return nil, err
	}

	return proto.TorrentGet(ctx, c.transport, ids, fields)
}

// TorrentGetDetail detects the protocol (once) and returns the full detail view
// (files, peers, trackers, the pieces bitfield, the magnet link, and the overview
// scalars on top of the embedded torrent) for the selected torrents as canonical
// TorrentDetails. A nil or empty ids slice requests all torrents; otherwise the
// mixed id/hash array selects them. The negotiated protocol decodes the
// per-scheme detail fields and parity traps into the shared shape.
func (c *Client) TorrentGetDetail(ctx context.Context, ids []any) ([]TorrentDetail, error) {
	proto, err := c.detect(ctx)
	if err != nil {
		return nil, err
	}

	return proto.TorrentGetDetail(ctx, c.transport, ids)
}

// TorrentAdd detects the protocol (once) and adds a torrent from the parameters
// in req, returning the canonical AddResult. A torrent that was already present
// is reported via AddResult.Duplicate rather than as an error.
func (c *Client) TorrentAdd(ctx context.Context, req AddRequest) (AddResult, error) {
	proto, err := c.detect(ctx)
	if err != nil {
		return AddResult{}, err
	}

	return proto.TorrentAdd(ctx, c.transport, req)
}

// TorrentStart detects the protocol (once) and starts the selected torrents
// (start-now when now is true, disregarding queue position). A nil or empty ids
// slice targets all torrents; otherwise the mixed id/hash array selects them.
func (c *Client) TorrentStart(ctx context.Context, ids []any, now bool) error {
	proto, err := c.detect(ctx)
	if err != nil {
		return err
	}

	return proto.TorrentStart(ctx, c.transport, ids, now)
}

// TorrentStop detects the protocol (once) and stops the selected torrents. A nil
// or empty ids slice targets all torrents; otherwise the mixed id/hash array
// selects them.
func (c *Client) TorrentStop(ctx context.Context, ids []any) error {
	proto, err := c.detect(ctx)
	if err != nil {
		return err
	}

	return proto.TorrentStop(ctx, c.transport, ids)
}

// TorrentVerify detects the protocol (once) and queues local-data verification
// for the selected torrents. A nil or empty ids slice targets all torrents;
// otherwise the mixed id/hash array selects them.
func (c *Client) TorrentVerify(ctx context.Context, ids []any) error {
	proto, err := c.detect(ctx)
	if err != nil {
		return err
	}

	return proto.TorrentVerify(ctx, c.transport, ids)
}

// TorrentReannounce detects the protocol (once) and forces the selected torrents
// to re-announce to their trackers immediately. A nil or empty ids slice targets
// all torrents; otherwise the mixed id/hash array selects them.
func (c *Client) TorrentReannounce(ctx context.Context, ids []any) error {
	proto, err := c.detect(ctx)
	if err != nil {
		return err
	}

	return proto.TorrentReannounce(ctx, c.transport, ids)
}

// QueueMoveTop detects the protocol (once) and moves the selected torrents to the
// top of the queue. A nil or empty ids slice targets all torrents; otherwise the
// mixed id/hash array selects them.
func (c *Client) QueueMoveTop(ctx context.Context, ids []any) error {
	proto, err := c.detect(ctx)
	if err != nil {
		return err
	}

	return proto.QueueMoveTop(ctx, c.transport, ids)
}

// QueueMoveUp detects the protocol (once) and moves the selected torrents up one
// position in the queue. A nil or empty ids slice targets all torrents; otherwise
// the mixed id/hash array selects them.
func (c *Client) QueueMoveUp(ctx context.Context, ids []any) error {
	proto, err := c.detect(ctx)
	if err != nil {
		return err
	}

	return proto.QueueMoveUp(ctx, c.transport, ids)
}

// QueueMoveDown detects the protocol (once) and moves the selected torrents down
// one position in the queue. A nil or empty ids slice targets all torrents;
// otherwise the mixed id/hash array selects them.
func (c *Client) QueueMoveDown(ctx context.Context, ids []any) error {
	proto, err := c.detect(ctx)
	if err != nil {
		return err
	}

	return proto.QueueMoveDown(ctx, c.transport, ids)
}

// QueueMoveBottom detects the protocol (once) and moves the selected torrents to
// the bottom of the queue. A nil or empty ids slice targets all torrents;
// otherwise the mixed id/hash array selects them.
func (c *Client) QueueMoveBottom(ctx context.Context, ids []any) error {
	proto, err := c.detect(ctx)
	if err != nil {
		return err
	}

	return proto.QueueMoveBottom(ctx, c.transport, ids)
}

// TorrentRemove detects the protocol (once) and removes the selected torrents,
// also deleting the downloaded local files when deleteData is true. A nil or
// empty ids slice targets all torrents; otherwise the mixed id/hash array
// selects them.
func (c *Client) TorrentRemove(ctx context.Context, ids []any, deleteData bool) error {
	proto, err := c.detect(ctx)
	if err != nil {
		return err
	}

	return proto.TorrentRemove(ctx, c.transport, ids, deleteData)
}

// TorrentSetLocation detects the protocol (once) and changes the download
// location of the selected torrents to location. When move is true the server
// relocates the existing files; otherwise it repoints to a path where the files
// already exist and searches there. A nil or empty ids slice targets all
// torrents; otherwise the mixed id/hash array selects them.
func (c *Client) TorrentSetLocation(ctx context.Context, ids []any, location string, move bool) error {
	proto, err := c.detect(ctx)
	if err != nil {
		return err
	}

	return proto.TorrentSetLocation(ctx, c.transport, ids, location, move)
}

// TorrentSet detects the protocol (once) and writes per-torrent fields on the
// selected torrents. settings is a snake_case-keyed map of canonical field keys
// to typed values; the negotiated protocol translates the keys to its wire
// scheme. Only the keys present in settings are written. A nil or empty ids
// slice targets all torrents; otherwise the mixed id/hash array selects them.
func (c *Client) TorrentSet(ctx context.Context, ids []any, settings map[string]any) error {
	proto, err := c.detect(ctx)
	if err != nil {
		return err
	}

	return proto.TorrentSet(ctx, c.transport, ids, settings)
}

// Call detects the protocol (once) and issues an arbitrary RPC method with raw
// arguments, returning the canonical RawResponse. method is the canonical
// kebab-case name (the protocol translates it for its wire scheme); args is the
// raw arguments object (nil for a zero-argument method). An RPC-level failure is
// reported via RawResponse.OK == false, not a Go error; only transport and
// JSON-decode failures return a Go error.
func (c *Client) Call(ctx context.Context, method string, args json.RawMessage) (RawResponse, error) {
	proto, err := c.detect(ctx)
	if err != nil {
		return RawResponse{}, err
	}

	return proto.Call(ctx, c.transport, method, args)
}

// PortTest detects the protocol (once) and checks whether the configured peer
// port is reachable from outside, returning the canonical PortTestResult. The
// JSON-RPC 2.0 scheme also reports the IP protocol the test ran on; the bespoke
// scheme returns only the open flag.
func (c *Client) PortTest(ctx context.Context) (PortTestResult, error) {
	proto, err := c.detect(ctx)
	if err != nil {
		return PortTestResult{}, err
	}

	return proto.PortTest(ctx, c.transport)
}

// Protocol returns the negotiated protocol name, detecting it if necessary. It
// is primarily useful for diagnostics; typed calls detect on their own.
func (c *Client) Protocol(ctx context.Context) (string, error) {
	proto, err := c.detect(ctx)
	if err != nil {
		return "", err
	}

	return proto.Name(), nil
}

// detect resolves the protocol, caching only a successful detection so a
// transient probe failure does not brick a long-lived client: when the probe
// fails the protocol stays nil and the next call re-probes. It runs the CSRF
// handshake (which surfaces the X-Transmission-Rpc-Version header on the 409),
// then selects JSON-RPC 2.0 when that header's semver major is >= jsonrpcMajorMin,
// falling back to the bespoke scheme when the header is absent or older. It also
// enforces the Transmission 4.0+ floor: a JSON-RPC server is necessarily >= 4.1.0
// and clears it for free, while a bespoke server is confirmed against
// minRPCVersion using the probe's own session-get body — no extra round-trip.
func (c *Client) detect(ctx context.Context) (Protocol, error) {
	c.detectMu.Lock()
	defer c.detectMu.Unlock()

	// A previous call already detected the protocol; reuse it.
	if c.protocol != nil {
		return c.protocol, nil
	}

	// A minimal session-get probe doubles as the CSRF handshake. Use the bespoke
	// envelope: a 4.1.x server still answers it, and the captured header drives
	// the protocol choice. The body is reused only to enforce the version floor
	// on the bespoke path below.
	body, err := marshalRequest(bespokeRequest{Method: methodSessionGetBespoke, Tag: 1})
	if err != nil {
		return nil, err
	}

	probeBody, _, err := c.transport.Probe(ctx, body)
	if err != nil {
		return nil, err
	}

	proto := selectProtocol(c.transport.RPCVersion())

	// Only the bespoke path needs verification: a JSON-RPC server is >= 4.1.0
	// (the CSRF version header starts at semver 6.0.0), so it already clears the
	// floor.
	if proto.Name() == ProtocolBespoke {
		if err := enforceMinVersion(probeBody); err != nil {
			return nil, err
		}
	}

	// Cache only on success: an errored probe above returns without setting
	// c.protocol, leaving the client undetected so the next call re-probes.
	c.protocol = proto

	return c.protocol, nil
}

// enforceMinVersion reads the integer rpc-version from a bespoke session-get
// response body and reports ErrUnsupported when the server positively reports a
// version below minRPCVersion. Every Transmission 2.x/3.x server reports
// rpc-version (it has existed since 1.50), so the realistic "too old" case is
// caught here. An absent rpc-version — a pre-1.50 server, or a non-Transmission
// endpoint that happens to answer — is left to fail naturally downstream rather
// than be mislabeled as a version problem. The wrapped message names the
// detected version so callers (e.g. "server ping") can explain the refusal.
func enforceMinVersion(probeBody []byte) error {
	var resp struct {
		Arguments struct {
			Version    string `json:"version"`
			RPCVersion *int   `json:"rpc-version"`
		} `json:"arguments"`
	}
	_ = json.Unmarshal(probeBody, &resp)

	if resp.Arguments.RPCVersion == nil || *resp.Arguments.RPCVersion >= minRPCVersion {
		return nil
	}

	versionDesc := strings.TrimSpace(resp.Arguments.Version)
	if versionDesc == "" {
		versionDesc = "unknown"
	}

	return fmt.Errorf("%w: server reports rpc-version %d (Transmission %s); snatcher requires Transmission 4.0+ (rpc-version %d)",
		ErrUnsupported, *resp.Arguments.RPCVersion, versionDesc, minRPCVersion)
}

// selectProtocol picks a Protocol from the rpc-version-semver captured during
// the CSRF handshake: JSON-RPC 2.0 when its semver major is >= jsonrpcMajorMin,
// otherwise the bespoke scheme (including when the version is absent).
func selectProtocol(rpcVersionSemver string) Protocol {
	if major, ok := semverMajor(rpcVersionSemver); ok && major >= jsonrpcMajorMin {
		return jsonrpcProtocol{}
	}

	return bespokeProtocol{}
}

// semverMajor parses the major version from a semver-compatible string (e.g.
// "6.0.0" -> 6). It reports false when the value is empty or the major segment
// is not a number.
func semverMajor(semver string) (int, bool) {
	if semver == "" {
		return 0, false
	}

	majorField, _, _ := strings.Cut(semver, ".")

	major, err := strconv.Atoi(majorField)
	if err != nil {
		return 0, false
	}

	return major, true
}
