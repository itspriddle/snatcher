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
	base       *url.URL
	apiKey     string
	httpClient HTTPDoer
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
	httpReq, err := t.newRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	return t.do(httpReq)
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

// do performs the request and maps the outcome onto the sentinels.
func (t *Transport) do(req *http.Request) ([]byte, error) {
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s %s: %w",
			ErrUnreachable, req.Method, t.safeURL(req.URL), unwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: reading response body: %w", ErrUnreachable, unwrapURLError(err))
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w (status %d)", ErrAuth, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("%w: unexpected status %d%s", ErrServer, resp.StatusCode, t.detail(body))
	}

	return body, nil
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

	if clone.RawQuery != "" {
		query := clone.Query()

		found := false
		for key := range query {
			if strings.EqualFold(key, apiKeyParam) {
				query.Set(key, redactedValue)
				found = true
			}
		}

		if found {
			clone.RawQuery = query.Encode()
		}
	}

	return clone.Redacted()
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
