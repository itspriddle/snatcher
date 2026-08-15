package prowlarr

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testAPIKey is the key every test configures, so assertions can prove it never
// escapes into an error message.
const testAPIKey = "s3cr3t-api-key"

// newTestTransport builds a Transport for baseURL, failing the test if the URL
// is rejected.
func newTestTransport(t *testing.T, baseURL string, opts ...Option) *Transport {
	t.Helper()

	transport, err := NewTransport(baseURL, opts...)
	require.NoError(t, err)

	return transport
}

func TestDoSendsAPIKeyAsHeaderNeverAsQueryParameter(t *testing.T) {
	var (
		gotHeader string
		gotQuery  string
		gotAccept string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(apiKeyHeader)
		gotQuery = r.URL.Query().Get(apiKeyParam)
		gotAccept = r.Header.Get("Accept")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	tr := newTestTransport(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := tr.Do(t.Context(), Request{Path: pathSystemStatus})
	require.NoError(t, err)

	assert.Equal(t, testAPIKey, gotHeader)
	assert.Empty(t, gotQuery, "the ?apikey= query form must never be used")
	assert.Equal(t, "application/json", gotAccept)
}

func TestDoWithoutAPIKeySendsNoAPIKeyHeader(t *testing.T) {
	var hadHeader bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadHeader = r.Header[apiKeyHeader]
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	tr := newTestTransport(t, srv.URL)

	_, err := tr.Do(t.Context(), Request{Path: pathSystemStatus})
	require.NoError(t, err)
	assert.False(t, hadHeader)
}

func TestDoJoinsPathOntoBareHostBaseURL(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	tr := newTestTransport(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := tr.Do(t.Context(), Request{Path: pathSystemStatus})
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/system/status", gotPath)
}

func TestDoJoinsPathOntoPrefixedBaseURL(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	// A reverse-proxied Prowlarr lives under a path prefix; every API path must
	// be joined onto it rather than replacing it.
	tr := newTestTransport(t, srv.URL+"/prowlarr", WithAPIKey(testAPIKey))

	_, err := tr.Do(t.Context(), Request{Path: pathSystemStatus})
	require.NoError(t, err)
	assert.Equal(t, "/prowlarr/api/v1/system/status", gotPath)
}

func TestDoSendsQueryParameters(t *testing.T) {
	var gotQuery url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	tr := newTestTransport(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := tr.Do(t.Context(), Request{
		Path:  "/api/v1/search",
		Query: url.Values{"query": {"ubuntu 24.04"}, "indexerIds": {"1", "2"}},
	})
	require.NoError(t, err)

	assert.Equal(t, "ubuntu 24.04", gotQuery.Get("query"))
	assert.Equal(t, []string{"1", "2"}, gotQuery["indexerIds"])
}

func TestDoSendsBodyAsJSONWithMethod(t *testing.T) {
	var (
		gotMethod      string
		gotContentType string
		gotBody        []byte
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	tr := newTestTransport(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := tr.Do(t.Context(), Request{
		Method: http.MethodPost,
		Path:   "/api/v1/search",
		Body:   []byte(`{"guid":"abc"}`),
	})
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "application/json", gotContentType)
	assert.JSONEq(t, `{"guid":"abc"}`, string(gotBody))
}

func TestDoDefaultsToGETAndSendsNoContentType(t *testing.T) {
	var (
		gotMethod         string
		hadContentTypeSet bool
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_, hadContentTypeSet = r.Header["Content-Type"]
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	tr := newTestTransport(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := tr.Do(t.Context(), Request{Path: pathSystemStatus})
	require.NoError(t, err)

	assert.Equal(t, http.MethodGet, gotMethod)
	assert.False(t, hadContentTypeSet)
}

func TestDoMapsUnauthorizedToAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	tr := newTestTransport(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := tr.Do(t.Context(), Request{Path: pathSystemStatus})
	require.ErrorIs(t, err, ErrAuth)
	require.NotErrorIs(t, err, ErrServer)
	require.NotErrorIs(t, err, ErrUnreachable)
	assert.NotContains(t, err.Error(), testAPIKey)
}

func TestDoMapsForbiddenToAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	tr := newTestTransport(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := tr.Do(t.Context(), Request{Path: pathSystemStatus})
	require.ErrorIs(t, err, ErrAuth)
	assert.NotErrorIs(t, err, ErrServer)
}

func TestDoMapsOtherNon2xxToServerErrorCarryingResponseText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("indexer exploded"))
	}))
	defer srv.Close()

	tr := newTestTransport(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := tr.Do(t.Context(), Request{Path: pathSystemStatus})
	require.ErrorIs(t, err, ErrServer)
	require.NotErrorIs(t, err, ErrAuth)
	assert.Contains(t, err.Error(), "500")
	assert.Contains(t, err.Error(), "indexer exploded")
}

func TestDoBoundsTheQuotedServerErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(strings.Repeat("x", 100_000)))
	}))
	defer srv.Close()

	tr := newTestTransport(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := tr.Do(t.Context(), Request{Path: pathSystemStatus})
	require.ErrorIs(t, err, ErrServer)
	assert.Less(t, len(err.Error()), maxErrorBody+200, "the quoted body must be bounded")
	assert.Contains(t, err.Error(), "...")
}

func TestDoScrubsTheAPIKeyEchoedBackInAServerErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad api key: " + testAPIKey))
	}))
	defer srv.Close()

	tr := newTestTransport(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := tr.Do(t.Context(), Request{Path: pathSystemStatus})
	require.ErrorIs(t, err, ErrServer)
	assert.NotContains(t, err.Error(), testAPIKey)
	assert.Contains(t, err.Error(), redactedValue)
}

func TestDoMapsTransportFailureToUnreachable(t *testing.T) {
	tr := newTestTransport(t, "http://127.0.0.1:9696", WithAPIKey(testAPIKey), WithHTTPClient(failingDoer{}))

	_, err := tr.Do(t.Context(), Request{Path: pathSystemStatus})
	require.ErrorIs(t, err, ErrUnreachable)
	require.ErrorIs(t, err, errDoer)
	require.NotErrorIs(t, err, ErrAuth)
	require.NotErrorIs(t, err, ErrServer)
}

func TestDoMapsTimeoutToUnreachable(t *testing.T) {
	release := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	tr := newTestTransport(t, srv.URL,
		WithAPIKey(testAPIKey),
		WithHTTPClient(&http.Client{Timeout: 10 * time.Millisecond}),
	)

	_, err := tr.Do(t.Context(), Request{Path: pathSystemStatus})
	require.ErrorIs(t, err, ErrUnreachable)
	assert.NotContains(t, err.Error(), testAPIKey)
}

func TestDoHonorsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	tr := newTestTransport(t, srv.URL, WithAPIKey(testAPIKey))

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel before the request runs

	_, err := tr.Do(ctx, Request{Path: pathSystemStatus})
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, ErrUnreachable)
}

func TestDoRedactsAnAPIKeyQueryParameterQuotedInAnError(t *testing.T) {
	tr := newTestTransport(t, "http://127.0.0.1:9696", WithAPIKey(testAPIKey), WithHTTPClient(failingDoer{}))

	// snatcher never sends the key this way, but the raw `api --prowlarr` escape
	// hatch lets a caller build any query it likes.
	_, err := tr.Do(t.Context(), Request{
		Path:  pathSystemStatus,
		Query: url.Values{apiKeyParam: {testAPIKey}},
	})
	require.ErrorIs(t, err, ErrUnreachable)

	assert.NotContains(t, err.Error(), testAPIKey)
	assert.Contains(t, err.Error(), apiKeyParam+"="+redactedValue)
}

func TestNewTransportDefaultsToTimeoutClient(t *testing.T) {
	tr := newTestTransport(t, "http://127.0.0.1:9696")

	// The default client must bound a request so a black-holed connection cannot
	// hang the CLI forever.
	client, ok := tr.httpClient.(*http.Client)
	require.True(t, ok, "default httpClient should be an *http.Client")
	assert.Equal(t, defaultTimeout, client.Timeout)
}

func TestWithHTTPClientOverridesDefaultTimeoutClient(t *testing.T) {
	custom := failingDoer{}
	tr := newTestTransport(t, "http://127.0.0.1:9696", WithHTTPClient(custom))

	assert.Equal(t, custom, tr.httpClient)
}

func TestNewTransportRejectsAnEmptyURL(t *testing.T) {
	_, err := NewTransport("   ")
	require.ErrorIs(t, err, ErrUnreachable)
	assert.Contains(t, err.Error(), "no server URL")
}

func TestNewTransportRejectsANonHTTPScheme(t *testing.T) {
	_, err := NewTransport("ftp://example.test/prowlarr")
	require.ErrorIs(t, err, ErrUnreachable)
	assert.Contains(t, err.Error(), "ftp")
}

func TestNewTransportRejectsAURLWithoutAHost(t *testing.T) {
	_, err := NewTransport("http:///api")
	require.ErrorIs(t, err, ErrUnreachable)
	assert.Contains(t, err.Error(), "no host")
}

func TestNewTransportNormalizesTheBaseURL(t *testing.T) {
	tr := newTestTransport(t, "http://example.test/prowlarr/?apikey=leaked#frag")

	base := tr.BaseURL()
	assert.Equal(t, "/prowlarr", base.Path)
	assert.Empty(t, base.RawQuery)
	assert.Empty(t, base.Fragment)
}

func TestBaseURLReturnsACopy(t *testing.T) {
	tr := newTestTransport(t, "http://example.test/prowlarr")

	base := tr.BaseURL()
	base.Path = "/mutated"

	assert.Equal(t, "/prowlarr", tr.BaseURL().Path)
}

func TestRedactURLMasksUserinfoAndAPIKeyParameter(t *testing.T) {
	u, err := url.Parse("https://alice:hunter2@example.test/prowlarr?ApiKey=" + testAPIKey + "&query=ubuntu")
	require.NoError(t, err)

	got := redactURL(u)
	assert.NotContains(t, got, testAPIKey)
	assert.NotContains(t, got, "hunter2")
	assert.Contains(t, got, "query=ubuntu")
}

func TestRedactRawURLDropsTheQueryOfAnUnparseableURL(t *testing.T) {
	assert.Equal(t, "http://example.test/x?"+redactedValue,
		redactRawURL("http://example.test/x?apikey="+testAPIKey))
	assert.Equal(t, "http://example.test/x", redactRawURL("http://example.test/x"))
}

var errDoer = errors.New("boom")

type failingDoer struct{}

func (failingDoer) Do(*http.Request) (*http.Response, error) {
	return nil, errDoer
}
