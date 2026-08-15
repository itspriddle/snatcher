// This file contains the Client: the typed entry point commands depend on. It
// wraps the Transport, issuing REST calls against Prowlarr's v1 API and
// decoding the responses into the domain types in types.go. Transport failures
// reach the caller as the ErrAuth / ErrUnreachable / ErrServer sentinels
// unchanged.
package prowlarr

import (
	"context"
	"encoding/json"
	"fmt"
)

// pathSystemStatus is the endpoint describing the Prowlarr instance itself.
const pathSystemStatus = apiPrefix + "/system/status"

// Client is the typed Prowlarr client. A zero Client is not usable; build one
// with NewClient.
type Client struct {
	transport *Transport
}

// NewClient returns a Client that talks to the Prowlarr instance at baseURL,
// forwarding opts to the underlying Transport (e.g. WithAPIKey,
// WithHTTPClient). It returns an error when baseURL is empty, unparseable, or
// not an http(s) URL.
func NewClient(baseURL string, opts ...Option) (*Client, error) {
	transport, err := NewTransport(baseURL, opts...)
	if err != nil {
		return nil, err
	}

	return &Client{transport: transport}, nil
}

// SystemStatus performs a GET /api/v1/system/status round trip and returns the
// instance's self-description. Failures are the transport's sentinels.
func (c *Client) SystemStatus(ctx context.Context) (SystemStatus, error) {
	body, err := c.transport.Do(ctx, Request{Path: pathSystemStatus})
	if err != nil {
		return SystemStatus{}, err
	}

	var wire systemStatusWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return SystemStatus{}, fmt.Errorf("%w: decoding system status: %w", ErrServer, err)
	}

	return wire.status(), nil
}
