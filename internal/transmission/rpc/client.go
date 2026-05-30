// This file contains the Client: the typed entry point that detects the
// server's RPC scheme on first use and then dispatches typed calls through the
// negotiated Protocol, so callers never deal with version differences.
package rpc

import (
	"context"
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
