// This file contains the Client: the typed entry point commands depend on. It
// wraps the Transport, issuing REST calls against Prowlarr's v1 API and
// decoding the responses into the domain types in types.go. Transport failures
// reach the caller as the ErrAuth / ErrUnreachable / ErrServer sentinels
// unchanged.
package prowlarr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

// APIResponse is the result of a CallAPI: the exact bytes the instance answered
// with, the HTTP status it answered them under, and whether that status was a
// success. There is no envelope to normalize — Prowlarr's REST API answers with
// the resource itself — so the body is carried through untouched.
type APIResponse struct {
	// Raw is the exact response body the server sent.
	Raw []byte

	// StatusCode is the HTTP status the server answered with.
	StatusCode int

	// OK reports whether the request succeeded (a 2xx status).
	OK bool
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

// Grab hands a release back to Prowlarr with a POST /api/v1/search round trip
// and returns the release as the server echoed it.
//
// What is POSTed is the exact ReleaseResource the search returned, retained on
// Release.Raw, so fields snatcher does not model reach Prowlarr intact rather
// than being silently dropped by a re-encode of the typed fields.
//
// A grab hands the release to *Prowlarr's* configured download client, which is
// not necessarily the Transmission server snatcher is pointed at.
func (c *Client) Grab(ctx context.Context, release Release) (Release, error) {
	if len(bytes.TrimSpace(release.Raw)) == 0 {
		return Release{}, fmt.Errorf("%w: release %q carries no server payload to grab", ErrServer, release.Title)
	}

	body, err := c.transport.Do(ctx, Request{
		Method: http.MethodPost,
		Path:   pathSearch,
		Body:   release.Raw,
	})
	if err != nil {
		return Release{}, err
	}

	// A 2xx is the grab being accepted. Prowlarr answers with the accepted
	// ReleaseResource, but an empty answer is still a success, so echo the
	// release back rather than failing after the server has already acted.
	if isBlankJSON(body) {
		return release, nil
	}

	grabbed, err := decodeRelease(body)
	if err != nil {
		return Release{}, fmt.Errorf("%w: decoding grabbed release: %w", ErrServer, err)
	}

	return grabbed, nil
}

// Download fetches the release's .torrent (or .nzb) bytes from its download URL.
//
// That URL may point at the Prowlarr instance or straight at the indexer, so the
// API key is attached only for the Prowlarr origin and is dropped at any
// redirect that leaves it. The response is capped at 32 MiB.
func (c *Client) Download(ctx context.Context, release Release) ([]byte, error) {
	if strings.TrimSpace(release.DownloadURL) == "" {
		if release.MagnetURL != "" {
			return nil, fmt.Errorf(
				"%w: release %q has no download_url; it publishes only a magnet_url",
				ErrServer, release.Title)
		}

		return nil, fmt.Errorf("%w: release %q has no download_url", ErrServer, release.Title)
	}

	return c.transport.fetch(ctx, release.DownloadURL)
}

// CallAPI issues an arbitrary REST request against the instance and returns the
// server's exact answer. It is the escape hatch behind "snatcher api
// --prowlarr", for the endpoints the typed calls above do not cover.
//
// Unlike every typed call, a non-2xx status is not an error here: it is reported
// through APIResponse.OK, so the command can print Prowlarr's own error body
// before exiting non-zero rather than replacing it with a bounded excerpt inside
// ErrServer. Only a failure to complete the exchange returns an error.
func (c *Client) CallAPI(ctx context.Context, req Request) (APIResponse, error) {
	status, body, err := c.transport.call(ctx, req)
	if err != nil {
		return APIResponse{}, err
	}

	return APIResponse{
		Raw:        body,
		StatusCode: status,
		OK:         status >= 200 && status < 300,
	}, nil
}

// APIPath expands a path written on a command line into a Request path. A path
// that already starts with "/" is used verbatim against the configured base URL,
// so an endpoint outside the versioned API stays reachable; a bare one
// ("indexer") is expanded to the v1 API ("/api/v1/indexer"), which is where
// every documented endpoint lives.
func APIPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if strings.HasPrefix(trimmed, "/") {
		return trimmed
	}

	return apiPrefix + "/" + trimmed
}

// isBlankJSON reports whether body carries no JSON value at all: an empty body,
// whitespace, or a literal null.
func isBlankJSON(body []byte) bool {
	trimmed := bytes.TrimSpace(body)

	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
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
