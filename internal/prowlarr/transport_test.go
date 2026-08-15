package prowlarr

import (
	"context"
	"encoding/json"
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

// TestDoScrubsTheAPIKeyStraddlingTheErrorBodyTruncation is the boundary case the
// whole-key assertion above cannot see: a key that begins inside the last bytes
// of an oversized body. Bound the text before scrubbing it and the cut splits
// the key, so the replacement no longer matches and the surviving prefix is
// printed. Truncation only ever keeps a prefix, so every prefix long enough to
// be a fragment of the credential is asserted absent.
func TestDoScrubsTheAPIKeyStraddlingTheErrorBodyTruncation(t *testing.T) {
	const keptKeyChars = 4

	body := strings.Repeat("x", maxErrorBody-keptKeyChars) + testAPIKey

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	tr := newTestTransport(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := tr.Do(t.Context(), Request{Path: pathSystemStatus})
	require.ErrorIs(t, err, ErrServer)

	for n := keptKeyChars; n <= len(testAPIKey); n++ {
		assert.NotContainsf(t, err.Error(), testAPIKey[:n],
			"a %d-character fragment of the API key survived the truncation", n)
	}

	assert.Contains(t, err.Error(), redactedValue[:keptKeyChars],
		"what the cut lands in the middle of is the placeholder, not the credential")
	assert.Less(t, len(err.Error()), maxErrorBody+200, "the quoted body is still bounded")
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

func TestDoRefusesARedirectThatLeavesTheProwlarrOrigin(t *testing.T) {
	var foreignSeen recordedFetch

	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values, had := r.Header[apiKeyHeader]

		foreignSeen = recordedFetch{Path: r.URL.Path, HadKey: had}
		if had {
			foreignSeen.APIKey = values[0]
		}

		_, _ = w.Write([]byte(`{}`))
	}))
	defer foreign.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreign.URL+pathSystemStatus, http.StatusFound)
	}))
	defer srv.Close()

	tr := newTestTransport(t, srv.URL, WithAPIKey(testAPIKey))

	// net/http copies X-Api-Key across a cross-host redirect, so an API call that
	// followed one would hand the key to whatever host answered.
	_, err := tr.Do(t.Context(), Request{Path: pathSystemStatus})
	require.ErrorIs(t, err, ErrUnreachable)

	assert.Contains(t, err.Error(), "refusing to follow a redirect")
	assert.NotContains(t, err.Error(), testAPIKey)
	assert.Empty(t, foreignSeen.Path, "a foreign origin must never be sent the request")
	assert.False(t, foreignSeen.HadKey, "a foreign origin must never receive the API key")
}

func TestDoFollowsARedirectWithinTheProwlarrOrigin(t *testing.T) {
	var got recordedFetch

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/system/status" {
			http.Redirect(w, r, "/api/v1/system/status", http.StatusFound)

			return
		}

		values, had := r.Header[apiKeyHeader]

		got = recordedFetch{Path: r.URL.Path, HadKey: had}
		if had {
			got.APIKey = values[0]
		}

		_, _ = w.Write([]byte(`{"appName":"Prowlarr"}`))
	}))
	defer srv.Close()

	// A reverse proxy normalizing a path is an ordinary same-origin redirect and
	// must still be followed, key intact.
	tr := newTestTransport(t, srv.URL+"/prowlarr", WithAPIKey(testAPIKey))

	body, err := tr.Do(t.Context(), Request{Path: pathSystemStatus})
	require.NoError(t, err)

	assert.JSONEq(t, `{"appName":"Prowlarr"}`, string(body))
	assert.True(t, got.HadKey, "a redirect within the Prowlarr origin keeps the key")
	assert.Equal(t, testAPIKey, got.APIKey)
}

func TestDoBoundsTheRedirectChain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/v1/again", http.StatusFound)
	}))
	defer srv.Close()

	tr := newTestTransport(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := tr.Do(t.Context(), Request{Path: pathSystemStatus})
	require.ErrorIs(t, err, ErrUnreachable)

	assert.Contains(t, err.Error(), "redirects")
	assert.NotContains(t, err.Error(), testAPIKey)
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

// TestRedactURLStringOnlyRewritesAKeyBearingURL pins the render-boundary strip:
// it touches the apikey parameter and nothing else, so a URL that holds no key
// survives byte-for-byte rather than being re-encoded.
func TestRedactURLStringOnlyRewritesAKeyBearingURL(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{
			name: "proxy link",
			raw:  "http://prowlarr.test/3/download?apikey=" + testAPIKey + "&link=abc",
			want: "http://prowlarr.test/3/download?apikey=" + redactedValue + "&link=abc",
		},
		{
			name: "parameter name is matched case-insensitively",
			raw:  "http://prowlarr.test/3/details?ApiKey=" + testAPIKey,
			want: "http://prowlarr.test/3/details?ApiKey=" + redactedValue,
		},
		{
			name: "fragment survives",
			raw:  "https://tracker.test/1?apikey=" + testAPIKey + "#comments",
			want: "https://tracker.test/1?apikey=" + redactedValue + "#comments",
		},
		{
			name: "no query",
			raw:  "https://tracker.test/details/1",
			want: "https://tracker.test/details/1",
		},
		{
			name: "query without the key is untouched",
			raw:  "https://tracker.test/dl?id=7&x=%2Fa+b",
			want: "https://tracker.test/dl?id=7&x=%2Fa+b",
		},
		{
			name: "magnet",
			raw:  "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Ubuntu",
			want: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Ubuntu",
		},
		{
			name: "empty",
			raw:  "",
			want: "",
		},
		{
			name: "unparseable loses its whole query",
			raw:  "http://%zz.test/x?apikey=" + testAPIKey,
			want: "http://%zz.test/x?" + redactedValue,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, redactURLString(tc.raw))
		})
	}
}

// TestReleaseRedactedLeavesTheReceiverAlone pins that the strip is a copy: the
// release the caller keeps still carries the URL Download must fetch and the raw
// bytes Grab must re-POST.
func TestReleaseRedactedLeavesTheReceiverAlone(t *testing.T) {
	release := Release{
		DownloadURL: "http://prowlarr.test/3/download?apikey=" + testAPIKey,
		InfoURL:     "http://prowlarr.test/3/details?apikey=" + testAPIKey,
		CommentURL:  "https://tracker.test/1?apikey=" + testAPIKey,
		MagnetURL:   "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		Raw:         json.RawMessage(`{"downloadUrl":"http://prowlarr.test/3/download?apikey=` + testAPIKey + `"}`),
	}

	redacted := release.Redacted()

	for _, got := range []string{redacted.DownloadURL, redacted.InfoURL, redacted.CommentURL} {
		assert.NotContains(t, got, testAPIKey)
		assert.Contains(t, got, "apikey="+redactedValue)
	}

	assert.Equal(t, release.MagnetURL, redacted.MagnetURL)
	assert.Equal(t, string(release.Raw), string(redacted.Raw), "the retained resource is never rewritten")

	assert.Contains(t, release.DownloadURL, testAPIKey, "the receiver is untouched")
	assert.Contains(t, release.InfoURL, testAPIKey)
	assert.Contains(t, release.CommentURL, testAPIKey)
}

func TestRedactRawURLDropsTheQueryOfAnUnparseableURL(t *testing.T) {
	assert.Equal(t, "http://example.test/x?"+redactedValue,
		redactRawURL("http://example.test/x?apikey="+testAPIKey))
	assert.Equal(t, "http://example.test/x", redactRawURL("http://example.test/x"))
}

func TestSameOriginIgnoresPathAndDefaultPorts(t *testing.T) {
	tr := newTestTransport(t, "http://example.test/prowlarr")

	sameOrigin := func(raw string) bool {
		t.Helper()

		u, err := url.Parse(raw)
		require.NoError(t, err)

		return tr.sameOrigin(u)
	}

	assert.True(t, sameOrigin("http://example.test/api/v1/indexer/3/download"))
	assert.True(t, sameOrigin("http://EXAMPLE.TEST/whatever"))
	assert.True(t, sameOrigin("http://example.test:80/whatever"), "80 is http's own port")

	assert.False(t, sameOrigin("https://example.test/whatever"), "a different scheme is a different origin")
	assert.False(t, sameOrigin("http://example.test:8080/whatever"), "a different port is a different origin")
	assert.False(t, sameOrigin("http://evil.test/whatever"))
	assert.False(t, sameOrigin("http://example.test.evil.test/whatever"), "a suffix match is not an origin match")
}

func TestParseFetchURLRejectsWhatCannotBeFetched(t *testing.T) {
	_, err := parseFetchURL("   ")
	require.ErrorIs(t, err, ErrServer)
	assert.Contains(t, err.Error(), "no URL")

	_, err = parseFetchURL("magnet:?xt=urn:btih:0123456789abcdef")
	require.ErrorIs(t, err, ErrServer)
	assert.Contains(t, err.Error(), `"magnet"`)

	_, err = parseFetchURL("file:///etc/passwd")
	require.ErrorIs(t, err, ErrServer)
	assert.Contains(t, err.Error(), `"file"`)

	_, err = parseFetchURL("http:///1.torrent")
	require.ErrorIs(t, err, ErrServer)
	assert.Contains(t, err.Error(), "no host")

	target, err := parseFetchURL("  https://tracker.test/download/1.torrent  ")
	require.NoError(t, err)
	assert.Equal(t, "https://tracker.test/download/1.torrent", target.String())
}

func TestReadCappedReportsAnOverrun(t *testing.T) {
	data, capped, err := readCapped(strings.NewReader("abcdef"), 10)
	require.NoError(t, err)
	assert.Equal(t, "abcdef", string(data))
	assert.False(t, capped)

	data, capped, err = readCapped(strings.NewReader("abcdef"), 6)
	require.NoError(t, err)
	assert.Equal(t, "abcdef", string(data))
	assert.False(t, capped, "a body that exactly fills the limit is not an overrun")

	data, capped, err = readCapped(strings.NewReader("abcdefg"), 6)
	require.NoError(t, err)
	assert.Equal(t, "abcdef", string(data))
	assert.True(t, capped)
}

var errDoer = errors.New("boom")

type failingDoer struct{}

func (failingDoer) Do(*http.Request) (*http.Response, error) {
	return nil, errDoer
}
