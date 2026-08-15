// Package prowlarr is snatcher's Prowlarr backend: a typed client for a
// Prowlarr instance's v1 REST API, used by the search commands. It mirrors the
// layering of internal/transmission/rpc (transport → typed client → domain
// types) but gets none of its protocol negotiation: Prowlarr's v1 API is
// single-dialect. It never imports the Transmission packages, and declares its
// own error sentinels rather than sharing theirs.
//
// This file contains the transport: the layer that issues HTTP requests against
// the configured base URL (which may carry a reverse-proxy path prefix),
// attaches the API key as an X-Api-Key header, and classifies every failure
// into ErrAuth, ErrServer, or ErrUnreachable so higher layers can tell "can't
// reach it" from "it said no".
package prowlarr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultTimeout bounds an individual request when the caller supplies no HTTP
// client of its own, matching the Transmission transport's ceiling. Without it
// a black-holed connection would hang forever; the caller's context still
// governs per-call deadlines on top of this ceiling.
const defaultTimeout = 30 * time.Second

// apiKeyHeader is the header Prowlarr authenticates with. The equivalent
// ?apikey= query form is never used: a secret in a URL lands in proxy logs,
// browser history, and process listings.
const apiKeyHeader = "X-Api-Key" //nolint:gosec // G101 false positive: a header name, not a credential

// apiKeyParam is the query parameter form of the API key. snatcher never sends
// it, but a caller-supplied query (or a redirect) may carry one, so any URL
// quoted in an error has this parameter's value redacted.
const apiKeyParam = "apikey"

// apiPrefix is the path every v1 API endpoint hangs off. It is joined onto the
// configured base URL, so a client built for http://host/prowlarr requests
// http://host/prowlarr/api/v1/….
const apiPrefix = "/api/v1"

// contentTypeJSON is the media type Prowlarr's REST API speaks.
const contentTypeJSON = "application/json"

// redactedValue replaces a secret removed from an error message.
const redactedValue = "REDACTED"

// maxErrorBody bounds how much of a failing response body is quoted in an
// ErrServer message, so a server answering with an HTML error page cannot flood
// the terminal with it.
const maxErrorBody = 512

// maxDownloadBytes bounds how large a fetched file may be. A .torrent is
// kilobytes and a .nzb is at worst a few megabytes; anything past this ceiling
// is a misdirected URL or a hostile server, and must not be buffered into memory
// unbounded.
const maxDownloadBytes = 32 << 20 // 32 MiB

// maxRedirects bounds the redirect chain a request may follow, matching
// net/http's own default. Installing a redirect policy replaces that default
// outright, so both policies re-impose the bound themselves.
const maxRedirects = 10

// ErrAuth signals that the server rejected the supplied API key (HTTP 401 or
// 403). It is matchable with errors.Is.
var ErrAuth = errors.New("prowlarr: authentication failed")

// ErrUnreachable signals a transport-level failure: the server could not be
// reached, the request timed out, or the HTTP exchange could not complete. A
// request the server answered but rejected is ErrServer, not this. It is
// matchable with errors.Is.
var ErrUnreachable = errors.New("prowlarr: server unreachable")

// ErrServer signals that the server answered but reported a failure: any non-2xx
// status other than the 401/403 mapped to ErrAuth, or a response body that could
// not be decoded. It is distinct from ErrUnreachable so a status bar can tell
// "can't reach host" from "server rejected the request". It is matchable with
// errors.Is.
var ErrServer = errors.New("prowlarr: server error")

// HTTPDoer is the subset of *http.Client the transport depends on, so callers
// (and tests) can supply their own implementation.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Request describes a single REST call: the HTTP method (GET when empty), the
// path relative to the base URL, optional query parameters, and an optional
// request body (sent as JSON).
type Request struct {
	// Method is the HTTP verb. An empty Method means GET.
	Method string

	// Path is the request path relative to the base URL, e.g.
	// "/api/v1/system/status". It is joined onto any path prefix the base URL
	// carries.
	Path string

	// Query is the set of query parameters to send. Empty values are not sent.
	Query url.Values

	// Body is the request body. An empty Body sends no body and no Content-Type.
	Body []byte
}

// Transport issues HTTP requests against a Prowlarr instance and classifies the
// failures. A zero Transport is not usable; build one with NewTransport.
type Transport struct {
	base   *url.URL
	apiKey string

	// httpClient performs API calls against the configured Prowlarr instance.
	// It carries a redirect policy that refuses to leave that instance's
	// origin; see apiRedirect.
	httpClient HTTPDoer

	// fetchClient performs fetch requests against URLs that did not come from
	// snatcher. It carries a redirect policy that re-applies the same-origin
	// rule at every hop; see fetchRedirect.
	fetchClient HTTPDoer
}

// Option configures a Transport built by NewTransport (and, through it, a
// Client built by NewClient).
type Option func(*Transport)

// WithAPIKey sets the API key sent as the X-Api-Key header on every request.
// When unset (or empty) requests are sent without the header.
func WithAPIKey(key string) Option {
	return func(t *Transport) {
		t.apiKey = key
	}
}

// WithHTTPClient overrides the HTTP client used to perform requests. The default
// is a private *http.Client with a defaultTimeout request ceiling.
//
// A supplied *http.Client is cloned so its redirect policy can keep the API key
// from leaving the Prowlarr origin (see withRedirectPolicy). A custom HTTPDoer
// cannot be given that policy, so one must not follow redirects transparently.
func WithHTTPClient(c HTTPDoer) Option {
	return func(t *Transport) {
		if c != nil {
			t.httpClient = c
		}
	}
}

// NewTransport returns a Transport that issues requests against baseURL,
// applying the supplied options. baseURL may carry a path prefix
// (http://host/prowlarr); every request path is joined onto it. It returns an
// error when baseURL is empty, unparseable, or not an http(s) URL.
func NewTransport(baseURL string, opts ...Option) (*Transport, error) {
	base, err := parseBaseURL(baseURL)
	if err != nil {
		return nil, err
	}

	t := &Transport{
		base:       base,
		httpClient: &http.Client{Timeout: defaultTimeout},
	}

	for _, opt := range opts {
		opt(t)
	}

	// Both clients clone the configured doer, so derive them from the same
	// source before either replaces it.
	doer := t.httpClient
	t.httpClient = t.withRedirectPolicy(doer, t.apiRedirect)
	t.fetchClient = t.withRedirectPolicy(doer, t.fetchRedirect)

	return t, nil
}

// BaseURL returns a copy of the transport's base URL. Callers use it to compare
// a third-party URL's origin against the configured Prowlarr instance.
func (t *Transport) BaseURL() *url.URL {
	base := *t.base
	return &base
}

// Do performs req and returns the response body. HTTP 401 and 403 map to
// ErrAuth; any other non-2xx status maps to ErrServer, carrying a bounded
// excerpt of the server's response text; a failure to complete the HTTP
// exchange maps to ErrUnreachable. The supplied context governs cancellation
// and deadlines on top of the client's own timeout.
func (t *Transport) Do(ctx context.Context, req Request) ([]byte, error) {
	status, body, err := t.call(ctx, req)
	if err != nil {
		return nil, err
	}

	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return nil, fmt.Errorf("%w (status %d)", ErrAuth, status)
	case status < 200 || status >= 300:
		return nil, fmt.Errorf("%w: unexpected status %d%s", ErrServer, status, t.detail(body))
	}

	return body, nil
}

// newRequest builds the HTTP request for req, joining its path onto the base
// URL and attaching the API key header, the JSON Accept header, and (for a
// request carrying a body) the JSON Content-Type.
func (t *Transport) newRequest(ctx context.Context, req Request) (*http.Request, error) {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}

	endpoint := t.base.JoinPath(req.Path)
	if len(req.Query) > 0 {
		endpoint.RawQuery = req.Query.Encode()
	}

	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, fmt.Errorf("%w: building request for %s: %w",
			ErrUnreachable, t.safeURL(endpoint), unwrapURLError(err))
	}

	httpReq.Header.Set("Accept", contentTypeJSON)

	if len(req.Body) > 0 {
		httpReq.Header.Set("Content-Type", contentTypeJSON)
	}

	t.authorize(httpReq)

	return httpReq, nil
}

// authorize attaches the configured API key to req. It is a no-op when no key
// is configured.
func (t *Transport) authorize(req *http.Request) {
	if t.apiKey != "" {
		req.Header.Set(apiKeyHeader, t.apiKey)
	}
}

// call performs req and returns the status the server answered with alongside
// the response body. Only a failure to complete the HTTP exchange is an error
// (ErrUnreachable); classifying the status is left to the caller.
//
// Do maps it onto the sentinels, which is what every typed call wants. The raw
// api escape hatch instead prints whatever the instance said and exits non-zero,
// so it needs the status and the untouched body rather than a bounded excerpt
// wrapped in ErrServer.
func (t *Transport) call(ctx context.Context, req Request) (int, []byte, error) {
	httpReq, err := t.newRequest(ctx, req)
	if err != nil {
		return 0, nil, err
	}

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %s %s: %w",
			ErrUnreachable, httpReq.Method, t.safeURL(httpReq.URL), unwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: reading response body: %w", ErrUnreachable, unwrapURLError(err))
	}

	return resp.StatusCode, body, nil
}

// fetch performs a bare GET against an absolute URL snatcher did not construct —
// a release's download URL, which may point at the Prowlarr instance itself or
// straight at the indexer that published the release — and returns the response
// body.
//
// The API key travels only when the URL's origin (scheme plus host and port)
// matches the configured Prowlarr instance, and is dropped again the moment a
// redirect leaves that origin: an indexer must never be handed the key that
// controls Prowlarr. The body is capped at maxDownloadBytes.
func (t *Transport) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	target, err := parseFetchURL(rawURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: building request for %s: %w",
			ErrUnreachable, t.safeURL(target), unwrapURLError(err))
	}

	if t.sameOrigin(target) {
		t.authorize(req)
	}

	resp, err := t.fetchClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: GET %s: %w",
			ErrUnreachable, t.safeURL(target), unwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w (status %d) fetching %s",
			ErrAuth, resp.StatusCode, t.safeURL(target))
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		excerpt, _, _ := readCapped(resp.Body, maxErrorBody)

		return nil, fmt.Errorf("%w: unexpected status %d fetching %s%s",
			ErrServer, resp.StatusCode, t.safeURL(target), t.detail(excerpt))
	}

	body, capped, err := readCapped(resp.Body, maxDownloadBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: reading %s: %w",
			ErrUnreachable, t.safeURL(target), unwrapURLError(err))
	}

	if capped {
		return nil, fmt.Errorf("%w: %s returned more than %d bytes",
			ErrServer, t.safeURL(target), maxDownloadBytes)
	}

	return body, nil
}

// withRedirectPolicy returns doer with check installed as its redirect policy.
// net/http strips only Authorization, WWW-Authenticate, Cookie, and Cookie2
// across a cross-host redirect — every other header, X-Api-Key included, is
// copied — so without a policy of its own a single off-origin hop would hand the
// API key to a third-party host.
//
// A policy lives on *http.Client, which is what NewTransport builds and what a
// caller realistically supplies. Any other HTTPDoer is returned unchanged and
// owns its own redirect handling.
func (t *Transport) withRedirectPolicy(doer HTTPDoer, check func(*http.Request, []*http.Request) error) HTTPDoer {
	client, ok := doer.(*http.Client)
	if !ok {
		return doer
	}

	clone := *client
	clone.CheckRedirect = check

	return &clone
}

// apiRedirect is the redirect policy for calls against the configured Prowlarr
// instance: every typed method, and the raw api --prowlarr escape hatch. A REST
// endpoint has no legitimate reason to send the client to another origin, so the
// hop is refused outright rather than followed with the key stripped — which
// would only surface later as a puzzling authentication failure. net/http never
// issues a request its CheckRedirect rejected, so the foreign origin sees
// nothing at all.
func (t *Transport) apiRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}

	if !t.sameOrigin(req.URL) {
		return fmt.Errorf("refusing to follow a redirect to %s: it leaves the configured server",
			t.safeURL(req.URL))
	}

	return nil
}

// fetchRedirect is the redirect policy for fetches of a URL that arrived on
// server data rather than being built by snatcher. Unlike an API call, such a
// fetch legitimately ends up off-origin — a release's download URL often points
// straight at the indexer — so the hop is followed with the API key dropped the
// moment it leaves the Prowlarr origin.
func (t *Transport) fetchRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}

	if !t.sameOrigin(req.URL) {
		req.Header.Del(apiKeyHeader)
	}

	return nil
}

// sameOrigin reports whether u names the same origin — scheme, host, and port —
// as the configured Prowlarr instance. It is the test that decides whether the
// API key is attached to a fetch; the request path is deliberately not part of
// it, since Prowlarr serves downloads from its own API paths.
func (t *Transport) sameOrigin(u *url.URL) bool {
	if u == nil {
		return false
	}

	return strings.EqualFold(u.Scheme, t.base.Scheme) &&
		strings.EqualFold(originHost(u), originHost(t.base))
}

// originHost renders a URL's authority with its scheme's default port made
// explicit, so http://host and http://host:80 compare equal — they name the same
// origin.
func originHost(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}

	return net.JoinHostPort(u.Hostname(), port)
}

// parseFetchURL validates a URL that arrived on a release rather than being
// built by snatcher. Only absolute http(s) URLs are fetchable: a magnet link is
// not something to GET, and no other scheme has any business here.
//
// A URL snatcher cannot use is classified as ErrServer — the Prowlarr instance
// answered, it just handed back a release that cannot be downloaded.
func parseFetchURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: no URL to fetch", ErrServer)
	}

	target, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid URL %q: %w",
			ErrServer, redactRawURL(trimmed), unwrapURLError(err))
	}

	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("%w: cannot fetch a %q URL: only http and https are supported",
			ErrServer, target.Scheme)
	}

	if target.Host == "" {
		return nil, fmt.Errorf("%w: URL %q has no host", ErrServer, redactURL(target))
	}

	return target, nil
}

// readCapped reads at most limit bytes from r, reporting whether the reader had
// more to give. It reads one byte past the limit to tell a body that exactly
// fills it from one that overruns it.
func readCapped(r io.Reader, limit int64) (data []byte, capped bool, err error) {
	data, err = io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, false, err
	}

	if int64(len(data)) > limit {
		return data[:limit], true, nil
	}

	return data, false, nil
}

// detail renders a bounded, single-line excerpt of a failing response body for
// an ErrServer message, or "" when the body is empty. The body is untrusted
// server output, so the configured API key is scrubbed from it.
func (t *Transport) detail(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if text == "" {
		return ""
	}

	if len(text) > maxErrorBody {
		text = strings.ToValidUTF8(text[:maxErrorBody], "") + "..."
	}

	return ": " + t.scrub(text)
}

// safeURL renders u for an error message with its userinfo and any apikey query
// parameter redacted, and the configured API key scrubbed for good measure.
func (t *Transport) safeURL(u *url.URL) string {
	return t.scrub(redactURL(u))
}

// scrub replaces every occurrence of the configured API key in s, so a key a
// server echoes back (or a caller embeds in a query) cannot reach an error
// message.
func (t *Transport) scrub(s string) string {
	if t.apiKey == "" {
		return s
	}

	return strings.ReplaceAll(s, t.apiKey, redactedValue)
}

// scrubBytes is scrub over a response body, for the raw escape hatch: whatever
// the instance answered with is about to be written to a terminal, and a
// Prowlarr body echoes the key back in more than one shape — inside a release's
// proxy downloadUrl, and as config/host's apiKey field. Matching on the key's
// own value covers both, where hunting for a parameter spelled "apikey" would
// only cover the first.
func (t *Transport) scrubBytes(body []byte) []byte {
	if t.apiKey == "" || len(body) == 0 {
		return body
	}

	return bytes.ReplaceAll(body, []byte(t.apiKey), []byte(redactedValue))
}

// parseBaseURL validates and normalizes a Prowlarr base URL, stripping any
// query, fragment, and trailing slash so request paths join onto it cleanly.
func parseBaseURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: no server URL configured", ErrUnreachable)
	}

	base, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid server URL %q: %w",
			ErrUnreachable, redactRawURL(trimmed), unwrapURLError(err))
	}

	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("%w: server URL scheme %q is not http or https", ErrUnreachable, base.Scheme)
	}

	if base.Host == "" {
		return nil, fmt.Errorf("%w: server URL has no host", ErrUnreachable)
	}

	base.RawQuery = ""
	base.ForceQuery = false
	base.Fragment = ""
	base.RawFragment = ""
	base.Path = strings.TrimSuffix(base.Path, "/")
	base.RawPath = ""

	return base, nil
}

// redactRawURL renders a URL string that could not be parsed, dropping its
// query string wholesale rather than guessing at its structure — an
// unparseable URL is exactly the case where an apikey parameter cannot be
// located reliably.
func redactRawURL(raw string) string {
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		return raw[:i] + "?" + redactedValue
	}

	return raw
}

// redactURL renders u for display with its userinfo password masked and the
// value of any apikey query parameter replaced, so a URL quoted in an error
// message cannot carry a secret.
func redactURL(u *url.URL) string {
	if u == nil {
		return ""
	}

	clone := *u
	redactQuery(&clone)

	return clone.Redacted()
}

// redactURLString renders a URL that arrived as server data with the value of
// any apikey query parameter replaced. Unlike redactURL it masks nothing else
// and re-encodes nothing: a URL carrying no apikey parameter is returned
// byte-for-byte as it arrived, so the only URL this rewrites is one that
// actually holds a secret.
//
// Prowlarr rewrites a release's downloadUrl into a proxy link of the form
// http://prowlarr/<indexerId>/download?apikey=<KEY>&link=…, since a torrent
// client fetching that URL cannot send a header. An indexer may build an info or
// comment URL the same way. Those URLs are the one place a server-derived string
// must be rewritten before snatcher emits it.
func redactURLString(raw string) string {
	if raw == "" {
		return raw
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		// An unparseable URL cannot have its parameters located reliably, so the
		// whole query goes.
		return redactRawURL(raw)
	}

	if !redactQuery(parsed) {
		return raw
	}

	return parsed.String()
}

// redactQuery replaces the value of every apikey query parameter on u in place,
// reporting whether it found one. A URL with no such parameter is left untouched
// rather than re-encoded.
func redactQuery(u *url.URL) bool {
	if u.RawQuery == "" {
		return false
	}

	query := u.Query()

	found := false
	for key := range query {
		if strings.EqualFold(key, apiKeyParam) {
			query.Set(key, redactedValue)
			found = true
		}
	}

	if found {
		u.RawQuery = query.Encode()
	}

	return found
}

// unwrapURLError strips the *url.Error wrapper net/url and net/http add, whose
// URL field would otherwise carry a caller-supplied apikey query parameter into
// an error message. The inner error is returned; a non-url.Error is returned
// unchanged.
func unwrapURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}

	return err
}
