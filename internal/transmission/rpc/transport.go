// Package rpc is a typed, low-level client for the Transmission RPC interface.
//
// This file contains the transport: the layer that POSTs a JSON body to the
// configured Transmission RPC endpoint, applies optional HTTP Basic Auth, and
// transparently manages the X-Transmission-Session-Id CSRF token. On HTTP 409
// the server returns the correct session id in its own headers; the transport
// captures it, caches it (race-free), and retries the request once, so higher
// layers never deal with HTTP details.
package rpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// defaultTimeout bounds an individual RPC request when the caller supplies no
// HTTP client of its own. Without it a black-holed connection would hang a
// long-lived polling client forever; the context still governs per-call
// deadlines on top of this ceiling.
const defaultTimeout = 30 * time.Second

// sessionIDHeader is the CSRF header Transmission requires on every request and
// returns (with the correct value) alongside an HTTP 409 response.
const sessionIDHeader = "X-Transmission-Session-Id"

// rpcVersionHeader is the header Transmission >= 4.1.0 returns (carrying its
// rpc-version-semver) in the CSRF HTTP 409 response, letting clients pick the
// RPC scheme without an extra request. It rides the same 409 as the session id.
const rpcVersionHeader = "X-Transmission-Rpc-Version"

// contentType is the request body media type for the RPC endpoint.
const contentType = "application/json"

// ErrAuth signals that the server rejected the request's credentials (HTTP 401).
var ErrAuth = errors.New("rpc: authentication failed")

// ErrTransport signals a transport-level failure: a non-2xx response other than
// the 401 mapped to ErrAuth, or the inability to complete the HTTP exchange.
var ErrTransport = errors.New("rpc: transport error")

// HTTPDoer is the subset of *http.Client the transport depends on, so callers
// (and tests) can supply their own implementation.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Transport POSTs JSON request bodies to a Transmission RPC endpoint and manages
// the X-Transmission-Session-Id CSRF token. A zero Transport is not usable; build
// one with NewTransport.
type Transport struct {
	url        string
	httpClient HTTPDoer
	username   string
	password   string

	mu         sync.Mutex
	sessionID  string
	rpcVersion string
}

// Option configures a Transport built by NewTransport.
type Option func(*Transport)

// WithHTTPClient overrides the HTTP client used to perform requests. The default
// is a private *http.Client with a defaultTimeout request ceiling.
func WithHTTPClient(c HTTPDoer) Option {
	return func(t *Transport) {
		if c != nil {
			t.httpClient = c
		}
	}
}

// WithBasicAuth enables HTTP Basic Auth with the given credentials. When unset
// (or username is empty) requests are sent without an Authorization header.
func WithBasicAuth(username, password string) Option {
	return func(t *Transport) {
		t.username = username
		t.password = password
	}
}

// NewTransport returns a Transport that POSTs to rpcURL, applying the supplied
// options.
func NewTransport(rpcURL string, opts ...Option) *Transport {
	t := &Transport{
		url:        rpcURL,
		httpClient: &http.Client{Timeout: defaultTimeout},
	}

	for _, opt := range opts {
		opt(t)
	}

	return t
}

// Do POSTs body to the RPC endpoint and returns the decoded response bytes. On
// an HTTP 409 it captures the X-Transmission-Session-Id from the response,
// caches it for later requests, and retries the request once. HTTP 401 maps to
// ErrAuth; other non-2xx responses map to ErrTransport. The supplied context
// governs cancellation and deadlines.
func (t *Transport) Do(ctx context.Context, body []byte) ([]byte, error) {
	respBody, _, err := t.do(ctx, body, true)
	return respBody, err
}

// Probe POSTs body to the RPC endpoint like Do, but also returns the response
// headers so higher layers can read X-Transmission-Rpc-Version for protocol
// detection. The CSRF 409 retry still applies.
func (t *Transport) Probe(ctx context.Context, body []byte) ([]byte, http.Header, error) {
	return t.do(ctx, body, true)
}

// do performs a single request and, when retryOn409 is true and the server
// answers 409 with a fresh session id, caches that id and retries exactly once.
func (t *Transport) do(ctx context.Context, body []byte, retryOn409 bool) ([]byte, http.Header, error) {
	req, err := t.newRequest(ctx, body)
	if err != nil {
		return nil, nil, err
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrTransport, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusConflict && retryOn409 {
		if id := resp.Header.Get(sessionIDHeader); id != "" {
			// The 409 also carries X-Transmission-Rpc-Version on servers >= 4.1.0;
			// capture it now since it does not ride later (non-409) responses.
			t.captureCSRF(id, resp.Header.Get(rpcVersionHeader))
			// Drain and close the 409 body before retrying so the connection can
			// be reused.
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()

			return t.do(ctx, body, false)
		}
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: reading response body: %w", ErrTransport, err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, nil, fmt.Errorf("%w (status %d)", ErrAuth, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, nil, fmt.Errorf("%w: unexpected status %d", ErrTransport, resp.StatusCode)
	}

	return respBody, resp.Header, nil
}

// newRequest builds the POST request for body, attaching the JSON content type,
// the cached CSRF session id (if any), and Basic Auth when configured.
func (t *Transport) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: building request: %w", ErrTransport, err)
	}

	req.Header.Set("Content-Type", contentType)

	if id := t.getSessionID(); id != "" {
		req.Header.Set(sessionIDHeader, id)
	}

	if t.username != "" {
		req.SetBasicAuth(t.username, t.password)
	}

	return req, nil
}

// getSessionID returns the cached CSRF session id under the mutex.
func (t *Transport) getSessionID() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.sessionID
}

// captureCSRF caches the CSRF session id and the rpc-version header seen on the
// 409 response under the mutex. The version may be empty (older servers).
func (t *Transport) captureCSRF(sessionID, rpcVersion string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.sessionID = sessionID
	t.rpcVersion = rpcVersion
}

// RPCVersion returns the rpc-version-semver captured from the CSRF 409 response,
// or "" if none was seen yet (or the server did not send the header). Callers
// must perform a request first so the CSRF handshake has run.
func (t *Transport) RPCVersion() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.rpcVersion
}
