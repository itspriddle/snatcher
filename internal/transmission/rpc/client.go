// This file contains the Client: the typed entry point that detects the
// server's RPC scheme on first use and then dispatches typed calls through the
// negotiated Protocol, so callers never deal with version differences.
package rpc

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
)

// jsonrpcMajorMin is the semver major version (of rpc-version-semver) at which
// Transmission supports JSON-RPC 2.0. 4.1.0 ships rpc-version-semver 6.0.0.
const jsonrpcMajorMin = 6

// Client is the typed Transmission RPC client. It probes the server once to
// detect the RPC scheme, then dispatches typed calls through the negotiated
// Protocol. A zero Client is not usable; build one with NewClient.
type Client struct {
	transport *Transport

	detectOnce sync.Once
	detectErr  error
	protocol   Protocol
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
// the server filters to the given numeric ids.
func (c *Client) TorrentGet(ctx context.Context, ids []int64) ([]Torrent, error) {
	proto, err := c.detect(ctx)
	if err != nil {
		return nil, err
	}

	return proto.TorrentGet(ctx, c.transport, ids)
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

// Protocol returns the negotiated protocol name, detecting it if necessary. It
// is primarily useful for diagnostics; typed calls detect on their own.
func (c *Client) Protocol(ctx context.Context) (string, error) {
	proto, err := c.detect(ctx)
	if err != nil {
		return "", err
	}

	return proto.Name(), nil
}

// detect resolves the protocol exactly once per client. It probes the server to
// run the CSRF handshake (which surfaces the X-Transmission-Rpc-Version header on
// the 409), then selects JSON-RPC 2.0 when that header's semver major is >=
// jsonrpcMajorMin, falling back to the bespoke scheme when the header is absent
// or older.
func (c *Client) detect(ctx context.Context) (Protocol, error) {
	c.detectOnce.Do(func() {
		// A minimal session-get probe doubles as the CSRF handshake. Use the
		// bespoke envelope: a 4.1.x server still answers it, and the captured
		// header (not the body) drives the decision. The body is discarded — only
		// the handshake matters here.
		body, err := marshalRequest(bespokeRequest{Method: methodSessionGetBespoke, Tag: 1})
		if err != nil {
			c.detectErr = err
			return
		}

		if _, _, err := c.transport.Probe(ctx, body); err != nil {
			c.detectErr = err
			return
		}

		c.protocol = selectProtocol(c.transport.RPCVersion())
	})

	if c.detectErr != nil {
		return nil, c.detectErr
	}

	return c.protocol, nil
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
