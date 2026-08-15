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
	"net/url"
	"strconv"
)

// pathSystemStatus is the endpoint describing the Prowlarr instance itself.
const pathSystemStatus = apiPrefix + "/system/status"

// pathSearch is the endpoint that searches the configured indexers (GET) and
// hands a release back to Prowlarr to grab (POST).
const pathSearch = apiPrefix + "/search"

// pathIndexers is the endpoint listing the configured indexers.
const pathIndexers = apiPrefix + "/indexer"

// pathCategories is the endpoint listing Prowlarr's default torznab category
// tree.
const pathCategories = apiPrefix + "/indexer/categories"

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

// SearchParams describes a single search request. A zero-valued field is an
// omitted parameter and is not sent, leaving Prowlarr's own default in place.
type SearchParams struct {
	// Query is the search term. An empty Query asks the indexers for their
	// current listing rather than a term match.
	Query string

	// Type is the torznab search type, e.g. "search", "tvsearch", "movie",
	// "music", or "book".
	Type string

	// IndexerIDs restricts the search to these indexer ids. Empty searches every
	// enabled indexer.
	IndexerIDs []int

	// Categories restricts the search to these torznab category ids. Empty
	// searches every category.
	Categories []int

	// Limit is the maximum number of results to return.
	Limit int

	// Offset is how many results to skip, which is what paging steps.
	Offset int
}

// query renders the params as the search endpoint's query string. Repeated
// parameters are sent as repeated keys, which is how Prowlarr binds an array.
func (p SearchParams) query() url.Values {
	query := url.Values{}

	if p.Query != "" {
		query.Set("query", p.Query)
	}

	if p.Type != "" {
		query.Set("type", p.Type)
	}

	for _, id := range p.IndexerIDs {
		query.Add("indexerIds", strconv.Itoa(id))
	}

	for _, id := range p.Categories {
		query.Add("categories", strconv.Itoa(id))
	}

	if p.Limit != 0 {
		query.Set("limit", strconv.Itoa(p.Limit))
	}

	if p.Offset != 0 {
		query.Set("offset", strconv.Itoa(p.Offset))
	}

	return query
}

// Search performs a GET /api/v1/search round trip and returns the releases the
// server reported, in the server's own order: the client does no sorting,
// filtering, or de-duplication. Failures are the transport's sentinels.
func (c *Client) Search(ctx context.Context, params SearchParams) ([]Release, error) {
	body, err := c.transport.Do(ctx, Request{Path: pathSearch, Query: params.query()})
	if err != nil {
		return nil, err
	}

	return decodeReleases(body)
}

// Indexers performs a GET /api/v1/indexer round trip and returns the configured
// indexers in the server's own order. Failures are the transport's sentinels.
func (c *Client) Indexers(ctx context.Context) ([]Indexer, error) {
	body, err := c.transport.Do(ctx, Request{Path: pathIndexers})
	if err != nil {
		return nil, err
	}

	var wires []indexerWire
	if err := json.Unmarshal(body, &wires); err != nil {
		return nil, fmt.Errorf("%w: decoding indexers: %w", ErrServer, err)
	}

	indexers := make([]Indexer, 0, len(wires))
	for _, wire := range wires {
		indexers = append(indexers, wire.indexer())
	}

	return indexers, nil
}

// Categories performs a GET /api/v1/indexer/categories round trip and returns
// Prowlarr's default torznab category tree in the server's own order. Failures
// are the transport's sentinels.
func (c *Client) Categories(ctx context.Context) ([]Category, error) {
	body, err := c.transport.Do(ctx, Request{Path: pathCategories})
	if err != nil {
		return nil, err
	}

	var wires []categoryWire
	if err := json.Unmarshal(body, &wires); err != nil {
		return nil, fmt.Errorf("%w: decoding categories: %w", ErrServer, err)
	}

	cats := make([]Category, 0, len(wires))
	for _, wire := range wires {
		cats = append(cats, wire.category())
	}

	return cats, nil
}

// decodeReleases decodes a ReleaseResource array, retaining each element's exact
// bytes on its Release so a later grab can hand the release back verbatim. The
// array is decoded element by element rather than in one pass for exactly that
// reason.
func decodeReleases(body []byte) ([]Release, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(body, &raws); err != nil {
		return nil, fmt.Errorf("%w: decoding search results: %w", ErrServer, err)
	}

	releases := make([]Release, 0, len(raws))
	for i, raw := range raws {
		release, err := decodeRelease(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: decoding search result %d: %w", ErrServer, i, err)
		}

		releases = append(releases, release)
	}

	return releases, nil
}

// decodeRelease decodes one ReleaseResource object into a Release, keeping the
// original bytes on Raw. json.Unmarshal already copies into a RawMessage, so raw
// does not alias the response buffer.
func decodeRelease(raw json.RawMessage) (Release, error) {
	var wire releaseWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Release{}, err
	}

	release := wire.release()
	release.Raw = raw

	return release, nil
}
