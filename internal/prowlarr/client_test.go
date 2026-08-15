package prowlarr

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// systemStatusBody is a trimmed GET /api/v1/system/status response, carrying
// fields snatcher does not model so the decode is proven to tolerate them.
const systemStatusBody = `{
  "appName": "Prowlarr",
  "instanceName": "Basement",
  "version": "1.37.0.5076",
  "isProduction": true,
  "runtimeVersion": "6.0.36",
  "urlBase": ""
}`

// newTestClient builds a Client for baseURL, failing the test if the URL is
// rejected.
func newTestClient(t *testing.T, baseURL string, opts ...Option) *Client {
	t.Helper()

	client, err := NewClient(baseURL, opts...)
	require.NoError(t, err)

	return client
}

func TestSystemStatusDecodesTheInstanceDescription(t *testing.T) {
	var (
		gotPath   string
		gotMethod string
		gotKey    string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotKey = r.Header.Get(apiKeyHeader)
		_, _ = w.Write([]byte(systemStatusBody))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	status, err := client.SystemStatus(t.Context())
	require.NoError(t, err)

	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Equal(t, "/api/v1/system/status", gotPath)
	assert.Equal(t, testAPIKey, gotKey)

	assert.Equal(t, SystemStatus{
		AppName:      "Prowlarr",
		InstanceName: "Basement",
		Version:      "1.37.0.5076",
	}, status)
}

func TestSystemStatusUsesThePathPrefixOfTheBaseURL(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(systemStatusBody))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL+"/prowlarr/", WithAPIKey(testAPIKey))

	_, err := client.SystemStatus(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "/prowlarr/api/v1/system/status", gotPath)
}

func TestSystemStatusMapsUnauthorizedToAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.SystemStatus(t.Context())
	require.ErrorIs(t, err, ErrAuth)
	assert.NotContains(t, err.Error(), testAPIKey)
}

func TestSystemStatusMapsAnUndecodableBodyToServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html>not json</html>`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.SystemStatus(t.Context())
	require.ErrorIs(t, err, ErrServer)
	assert.Contains(t, err.Error(), "decoding system status")
}

func TestNewClientRejectsAnUnusableBaseURL(t *testing.T) {
	_, err := NewClient("")
	require.ErrorIs(t, err, ErrUnreachable)
}

// searchResultsBody is a two-element GET /api/v1/search response. The first
// release is fully populated and carries two keys snatcher does not model
// (sortTitle, indexerFlags) so the raw-retention round trip has something to
// prove; the second exercises null counts, a null category list, and the empty
// strings a usenet release arrives with.
const searchResultsBody = `[
  {
    "guid": "https://tracker.invalid/torrent/1",
    "title": "Ubuntu 24.04 LTS Desktop amd64",
    "indexer": "Linux Tracker",
    "indexerId": 3,
    "age": 12,
    "publishDate": "2026-08-01T09:15:00Z",
    "size": 6203355136,
    "files": 1,
    "grabs": 4213,
    "seeders": 812,
    "leechers": 17,
    "protocol": "torrent",
    "categories": [
      {
        "id": 4000,
        "name": "PC",
        "description": "",
        "subCategories": [
          {"id": 4050, "name": "PC/ISO", "description": "Disc images", "subCategories": []}
        ]
      }
    ],
    "infoHash": "0123456789abcdef0123456789abcdef01234567",
    "downloadUrl": "https://tracker.invalid/download/1",
    "magnetUrl": "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
    "infoUrl": "https://tracker.invalid/details/1",
    "commentUrl": "https://tracker.invalid/details/1#comments",
    "sortTitle": "ubuntu 24 04 lts desktop amd64",
    "indexerFlags": ["freeleech"]
  },
  {
    "guid": "https://tracker.invalid/torrent/2",
    "title": "Debian 13 netinst amd64",
    "indexer": "Usenet Provider",
    "indexerId": 7,
    "age": 3,
    "publishDate": "2026-08-10T22:00:00Z",
    "size": 702545920,
    "files": null,
    "grabs": 0,
    "seeders": null,
    "leechers": null,
    "protocol": "usenet",
    "categories": null,
    "infoHash": "",
    "downloadUrl": "https://usenet.invalid/download/2",
    "magnetUrl": "",
    "infoUrl": "",
    "commentUrl": ""
  }
]`

// indexersBody is a GET /api/v1/indexer response carrying fields snatcher does
// not model, so the decode is proven to tolerate them.
const indexersBody = `[
  {
    "id": 3,
    "name": "Linux Tracker",
    "protocol": "torrent",
    "privacy": "public",
    "enable": true,
    "priority": 25,
    "supportsSearch": true,
    "supportsPagination": true,
    "language": "en-US",
    "implementation": "Cardigann",
    "fields": [{"name": "baseUrl", "value": "https://tracker.invalid"}]
  },
  {
    "id": 7,
    "name": "Usenet Provider",
    "protocol": "usenet",
    "privacy": "private",
    "enable": false,
    "priority": 50,
    "supportsSearch": true,
    "supportsPagination": false,
    "language": "en-US"
  }
]`

// categoriesBody is a GET /api/v1/indexer/categories response with one nested
// level, matching Prowlarr's default tree.
const categoriesBody = `[
  {
    "id": 2000,
    "name": "Movies",
    "description": "",
    "subCategories": [
      {"id": 2040, "name": "Movies/HD", "description": "High definition", "subCategories": []}
    ]
  },
  {
    "id": 5000,
    "name": "TV",
    "description": "",
    "subCategories": []
  }
]`

// recordedRequest is the part of a request the fixture servers keep, so an
// assertion can check what the client actually asked for.
type recordedRequest struct {
	Method string
	URL    *url.URL
}

// jsonServer starts a test server that records the request line into got and
// answers every request with body.
func jsonServer(t *testing.T, body string, got *recordedRequest) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = recordedRequest{Method: r.Method, URL: r.URL}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return srv
}

// statusServer starts a test server that answers every request with status and
// a JSON error body.
func statusServer(t *testing.T, status int) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"message":"nope"}`))
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestSearchSendsEveryParameterItIsGiven(t *testing.T) {
	var got recordedRequest

	srv := jsonServer(t, `[]`, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.Search(t.Context(), SearchParams{
		Query:      "ubuntu 24.04",
		Type:       "search",
		IndexerIDs: []int{3, 7},
		Categories: []int{4000, 4050},
		Limit:      100,
		Offset:     200,
	})
	require.NoError(t, err)

	assert.Equal(t, http.MethodGet, got.Method)
	assert.Equal(t, "/api/v1/search", got.URL.Path)

	query := got.URL.Query()
	assert.Equal(t, "ubuntu 24.04", query.Get("query"))
	assert.Equal(t, "search", query.Get("type"))
	assert.Equal(t, []string{"3", "7"}, query["indexerIds"])
	assert.Equal(t, []string{"4000", "4050"}, query["categories"])
	assert.Equal(t, "100", query.Get("limit"))
	assert.Equal(t, "200", query.Get("offset"))
}

func TestSearchOmitsParametersItWasNotGiven(t *testing.T) {
	var got recordedRequest

	srv := jsonServer(t, `[]`, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.Search(t.Context(), SearchParams{})
	require.NoError(t, err)

	assert.Empty(t, got.URL.RawQuery)
}

func TestSearchDecodesTheReleaseFields(t *testing.T) {
	var got recordedRequest

	srv := jsonServer(t, searchResultsBody, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	releases, err := client.Search(t.Context(), SearchParams{Query: "ubuntu"})
	require.NoError(t, err)
	require.Len(t, releases, 2)

	first := releases[0]
	assert.Equal(t, "https://tracker.invalid/torrent/1", first.GUID)
	assert.Equal(t, "Ubuntu 24.04 LTS Desktop amd64", first.Title)
	assert.Equal(t, "Linux Tracker", first.Indexer)
	assert.Equal(t, 3, first.IndexerID)
	assert.Equal(t, 12, first.Age)
	assert.Equal(t, "2026-08-01T09:15:00Z", first.PublishDate)
	assert.Equal(t, int64(6203355136), first.Size)
	assert.Equal(t, ptr(1), first.Files)
	assert.Equal(t, ptr(4213), first.Grabs)
	assert.Equal(t, ptr(812), first.Seeders)
	assert.Equal(t, ptr(17), first.Leechers)
	assert.Equal(t, "torrent", first.Protocol)
	assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", first.InfoHash)
	assert.Equal(t, "https://tracker.invalid/download/1", first.DownloadURL)
	assert.Equal(t, "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", first.MagnetURL)
	assert.Equal(t, "https://tracker.invalid/details/1", first.InfoURL)
	assert.Equal(t, "https://tracker.invalid/details/1#comments", first.CommentURL)

	assert.Equal(t, []Category{{
		ID:   4000,
		Name: "PC",
		SubCategories: []Category{
			{ID: 4050, Name: "PC/ISO", Description: "Disc images"},
		},
	}}, first.Categories)
}

func TestSearchDistinguishesANullCountFromZero(t *testing.T) {
	var got recordedRequest

	srv := jsonServer(t, searchResultsBody, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	releases, err := client.Search(t.Context(), SearchParams{})
	require.NoError(t, err)
	require.Len(t, releases, 2)

	second := releases[1]
	assert.Nil(t, second.Files)
	assert.Nil(t, second.Seeders)
	assert.Nil(t, second.Leechers)
	assert.Equal(t, ptr(0), second.Grabs)
	assert.Nil(t, second.Categories)
}

func TestSearchRetainsTheRawReleaseSoAGrabCanRoundTripIt(t *testing.T) {
	var got recordedRequest

	srv := jsonServer(t, searchResultsBody, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	releases, err := client.Search(t.Context(), SearchParams{})
	require.NoError(t, err)
	require.Len(t, releases, 2)

	var resource map[string]any
	require.NoError(t, json.Unmarshal(releases[0].Raw, &resource))

	assert.Equal(t, "ubuntu 24 04 lts desktop amd64", resource["sortTitle"])
	assert.Equal(t, []any{"freeleech"}, resource["indexerFlags"])
	assert.Equal(t, "Ubuntu 24.04 LTS Desktop amd64", resource["title"])
}

func TestSearchExcludesTheRawReleaseFromSnatcherJSON(t *testing.T) {
	var got recordedRequest

	srv := jsonServer(t, searchResultsBody, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	releases, err := client.Search(t.Context(), SearchParams{})
	require.NoError(t, err)

	encoded, err := json.Marshal(releases[0])
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(encoded, &out))

	assert.NotContains(t, out, "raw")
	assert.NotContains(t, out, "sortTitle")
	assert.Contains(t, out, "info_hash")
	assert.Contains(t, out, "indexer_id")
}

func TestSearchPreservesServerOrder(t *testing.T) {
	var got recordedRequest

	srv := jsonServer(t, searchResultsBody, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	releases, err := client.Search(t.Context(), SearchParams{})
	require.NoError(t, err)

	assert.Equal(t,
		[]string{"Ubuntu 24.04 LTS Desktop amd64", "Debian 13 netinst amd64"},
		[]string{releases[0].Title, releases[1].Title},
	)
}

func TestSearchReturnsAnEmptySliceWhenNothingMatches(t *testing.T) {
	var got recordedRequest

	srv := jsonServer(t, `[]`, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	releases, err := client.Search(t.Context(), SearchParams{Query: "nothing"})
	require.NoError(t, err)
	assert.Empty(t, releases)
	assert.NotNil(t, releases)
}

func TestSearchMapsUnauthorizedToAuthError(t *testing.T) {
	srv := statusServer(t, http.StatusUnauthorized)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.Search(t.Context(), SearchParams{Query: "ubuntu"})
	require.ErrorIs(t, err, ErrAuth)
	assert.NotContains(t, err.Error(), testAPIKey)
}

func TestSearchMapsAnUndecodableBodyToServerError(t *testing.T) {
	var got recordedRequest

	srv := jsonServer(t, `<html>not json</html>`, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.Search(t.Context(), SearchParams{})
	require.ErrorIs(t, err, ErrServer)
	assert.Contains(t, err.Error(), "decoding search results")
}

func TestIndexersDecodesTheConfiguredIndexers(t *testing.T) {
	var got recordedRequest

	srv := jsonServer(t, indexersBody, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	indexers, err := client.Indexers(t.Context())
	require.NoError(t, err)

	assert.Equal(t, http.MethodGet, got.Method)
	assert.Equal(t, "/api/v1/indexer", got.URL.Path)

	assert.Equal(t, []Indexer{
		{
			ID:                 3,
			Name:               "Linux Tracker",
			Protocol:           "torrent",
			Privacy:            "public",
			Enable:             true,
			Priority:           25,
			SupportsSearch:     true,
			SupportsPagination: true,
			Language:           "en-US",
		},
		{
			ID:                 7,
			Name:               "Usenet Provider",
			Protocol:           "usenet",
			Privacy:            "private",
			Enable:             false,
			Priority:           50,
			SupportsSearch:     true,
			SupportsPagination: false,
			Language:           "en-US",
		},
	}, indexers)
}

func TestIndexersReturnsAnEmptySliceWhenNoneAreConfigured(t *testing.T) {
	var got recordedRequest

	srv := jsonServer(t, `[]`, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	indexers, err := client.Indexers(t.Context())
	require.NoError(t, err)
	assert.Empty(t, indexers)
	assert.NotNil(t, indexers)
}

func TestIndexersMapsUnauthorizedToAuthError(t *testing.T) {
	srv := statusServer(t, http.StatusUnauthorized)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.Indexers(t.Context())
	require.ErrorIs(t, err, ErrAuth)
	assert.NotContains(t, err.Error(), testAPIKey)
}

func TestIndexersMapsAnUndecodableBodyToServerError(t *testing.T) {
	var got recordedRequest

	srv := jsonServer(t, `{"not":"an array"}`, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.Indexers(t.Context())
	require.ErrorIs(t, err, ErrServer)
	assert.Contains(t, err.Error(), "decoding indexers")
}

func TestCategoriesDecodesTheNestedTree(t *testing.T) {
	var got recordedRequest

	srv := jsonServer(t, categoriesBody, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	cats, err := client.Categories(t.Context())
	require.NoError(t, err)

	assert.Equal(t, http.MethodGet, got.Method)
	assert.Equal(t, "/api/v1/indexer/categories", got.URL.Path)

	assert.Equal(t, []Category{
		{
			ID:   2000,
			Name: "Movies",
			SubCategories: []Category{
				{ID: 2040, Name: "Movies/HD", Description: "High definition"},
			},
		},
		{ID: 5000, Name: "TV"},
	}, cats)
}

func TestCategoriesReturnsAnEmptySliceWhenTheTreeIsEmpty(t *testing.T) {
	var got recordedRequest

	srv := jsonServer(t, `[]`, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	cats, err := client.Categories(t.Context())
	require.NoError(t, err)
	assert.Empty(t, cats)
	assert.NotNil(t, cats)
}

func TestCategoriesMapsUnauthorizedToAuthError(t *testing.T) {
	srv := statusServer(t, http.StatusUnauthorized)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.Categories(t.Context())
	require.ErrorIs(t, err, ErrAuth)
	assert.NotContains(t, err.Error(), testAPIKey)
}

func TestCategoriesMapsAnUndecodableBodyToServerError(t *testing.T) {
	var got recordedRequest

	srv := jsonServer(t, `{"not":"an array"}`, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.Categories(t.Context())
	require.ErrorIs(t, err, ErrServer)
	assert.Contains(t, err.Error(), "decoding categories")
}

func TestSearchIndexersAndCategoriesUseThePathPrefixOfTheBaseURL(t *testing.T) {
	var got recordedRequest

	srv := jsonServer(t, `[]`, &got)
	client := newTestClient(t, srv.URL+"/prowlarr", WithAPIKey(testAPIKey))

	_, err := client.Search(t.Context(), SearchParams{})
	require.NoError(t, err)
	assert.Equal(t, "/prowlarr/api/v1/search", got.URL.Path)

	_, err = client.Indexers(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "/prowlarr/api/v1/indexer", got.URL.Path)

	_, err = client.Categories(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "/prowlarr/api/v1/indexer/categories", got.URL.Path)
}

// grabbedReleaseBody is the ReleaseResource Prowlarr echoes back from a
// POST /api/v1/search. It carries an unmodeled key so the echoed release is
// proven to retain its own raw bytes.
const grabbedReleaseBody = `{
  "guid": "https://tracker.invalid/torrent/1",
  "title": "Ubuntu 24.04 LTS Desktop amd64",
  "indexer": "Linux Tracker",
  "indexerId": 3,
  "protocol": "torrent",
  "downloadUrl": "https://tracker.invalid/download/1",
  "sortTitle": "ubuntu 24 04 lts desktop amd64"
}`

// recordedBody is what the grab fixture server keeps about the request it saw.
type recordedBody struct {
	Method string
	Path   string
	Body   []byte
}

// grabServer starts a test server that records the request it was POSTed and
// answers with body.
func grabServer(t *testing.T, body string, got *recordedBody) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		*got = recordedBody{Method: r.Method, Path: r.URL.Path, Body: payload}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestGrabPostsTheRetainedReleaseResourceVerbatim(t *testing.T) {
	var got recordedBody

	srv := grabServer(t, grabbedReleaseBody, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.Grab(t.Context(), Release{Raw: json.RawMessage(grabbedReleaseBody)})
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, got.Method)
	assert.Equal(t, "/api/v1/search", got.Path)

	// The whole resource goes back, including the fields snatcher does not model.
	assert.JSONEq(t, grabbedReleaseBody, string(got.Body))
}

func TestGrabUsesThePathPrefixOfTheBaseURL(t *testing.T) {
	var got recordedBody

	srv := grabServer(t, grabbedReleaseBody, &got)
	client := newTestClient(t, srv.URL+"/prowlarr", WithAPIKey(testAPIKey))

	_, err := client.Grab(t.Context(), Release{Raw: json.RawMessage(grabbedReleaseBody)})
	require.NoError(t, err)
	assert.Equal(t, "/prowlarr/api/v1/search", got.Path)
}

func TestGrabDecodesTheEchoedRelease(t *testing.T) {
	var got recordedBody

	srv := grabServer(t, grabbedReleaseBody, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	grabbed, err := client.Grab(t.Context(), Release{Raw: json.RawMessage(`{"guid":"stale"}`)})
	require.NoError(t, err)

	assert.Equal(t, "https://tracker.invalid/torrent/1", grabbed.GUID)
	assert.Equal(t, "Ubuntu 24.04 LTS Desktop amd64", grabbed.Title)
	assert.Equal(t, "Linux Tracker", grabbed.Indexer)
	assert.Equal(t, 3, grabbed.IndexerID)
	assert.JSONEq(t, grabbedReleaseBody, string(grabbed.Raw))
}

func TestGrabEchoesTheReleaseWhenTheServerAnswersWithNoBody(t *testing.T) {
	var got recordedBody

	// A 2xx means the grab was accepted. An answer carrying no resource must not
	// turn a completed grab into an error.
	srv := grabServer(t, "", &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	sent := Release{Title: "Ubuntu 24.04 LTS Desktop amd64", Raw: json.RawMessage(grabbedReleaseBody)}

	grabbed, err := client.Grab(t.Context(), sent)
	require.NoError(t, err)
	assert.Equal(t, sent.Title, grabbed.Title)
}

func TestGrabRefusesAReleaseCarryingNoRetainedPayload(t *testing.T) {
	var got recordedBody

	srv := grabServer(t, grabbedReleaseBody, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.Grab(t.Context(), Release{Title: "Ubuntu 24.04 LTS Desktop amd64"})
	require.ErrorIs(t, err, ErrServer)
	assert.Contains(t, err.Error(), "no server payload")
	assert.Empty(t, got.Method, "nothing should be sent for a release with no payload")
}

func TestGrabMapsUnauthorizedToAuthError(t *testing.T) {
	srv := statusServer(t, http.StatusUnauthorized)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.Grab(t.Context(), Release{Raw: json.RawMessage(grabbedReleaseBody)})
	require.ErrorIs(t, err, ErrAuth)
	assert.NotContains(t, err.Error(), testAPIKey)
}

func TestGrabMapsAnUndecodableBodyToServerError(t *testing.T) {
	var got recordedBody

	srv := grabServer(t, `<html>not json</html>`, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.Grab(t.Context(), Release{Raw: json.RawMessage(grabbedReleaseBody)})
	require.ErrorIs(t, err, ErrServer)
	assert.Contains(t, err.Error(), "decoding grabbed release")
}

// torrentBytes stands in for the .torrent file a download returns.
var torrentBytes = []byte("d8:announce20:http://tracker.teste")

// recordedFetch is what a download fixture server keeps about the request it
// saw, including whether the API key header reached it at all.
type recordedFetch struct {
	Path   string
	APIKey string
	HadKey bool
}

// fileServer starts a test server that records the request it was sent and
// answers with torrentBytes.
func fileServer(t *testing.T, got *recordedFetch) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values, had := r.Header[apiKeyHeader]

		*got = recordedFetch{Path: r.URL.Path, HadKey: had}
		if had {
			got.APIKey = values[0]
		}

		_, _ = w.Write(torrentBytes)
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestDownloadSendsTheAPIKeyToTheProwlarrOrigin(t *testing.T) {
	var got recordedFetch

	srv := fileServer(t, &got)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	body, err := client.Download(t.Context(), Release{
		Title:       "Ubuntu 24.04 LTS Desktop amd64",
		DownloadURL: srv.URL + "/api/v1/indexer/3/download?link=abc",
	})
	require.NoError(t, err)

	assert.Equal(t, torrentBytes, body)
	assert.Equal(t, "/api/v1/indexer/3/download", got.Path)
	assert.True(t, got.HadKey, "the Prowlarr instance itself must receive the API key")
	assert.Equal(t, testAPIKey, got.APIKey)
}

func TestDownloadWithholdsTheAPIKeyFromAForeignHost(t *testing.T) {
	var (
		gotProwlarr recordedFetch
		gotIndexer  recordedFetch
	)

	prowlarr := fileServer(t, &gotProwlarr)
	indexer := fileServer(t, &gotIndexer)

	client := newTestClient(t, prowlarr.URL, WithAPIKey(testAPIKey))

	body, err := client.Download(t.Context(), Release{
		Title:       "Ubuntu 24.04 LTS Desktop amd64",
		DownloadURL: indexer.URL + "/download/1.torrent",
	})
	require.NoError(t, err)

	assert.Equal(t, torrentBytes, body)
	assert.Equal(t, "/download/1.torrent", gotIndexer.Path)
	assert.False(t, gotIndexer.HadKey, "an indexer must never be handed the Prowlarr API key")
	assert.Empty(t, gotProwlarr.Path, "the Prowlarr instance is not involved in a third-party download")
}

func TestDownloadDropsTheAPIKeyWhenARedirectLeavesTheProwlarrOrigin(t *testing.T) {
	var (
		gotProwlarr recordedFetch
		gotIndexer  recordedFetch
	)

	indexer := fileServer(t, &gotIndexer)

	prowlarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values, had := r.Header[apiKeyHeader]

		gotProwlarr = recordedFetch{Path: r.URL.Path, HadKey: had}
		if had {
			gotProwlarr.APIKey = values[0]
		}

		http.Redirect(w, r, indexer.URL+"/download/1.torrent", http.StatusFound)
	}))
	t.Cleanup(prowlarr.Close)

	client := newTestClient(t, prowlarr.URL, WithAPIKey(testAPIKey))

	body, err := client.Download(t.Context(), Release{
		Title:       "Ubuntu 24.04 LTS Desktop amd64",
		DownloadURL: prowlarr.URL + "/api/v1/indexer/3/download",
	})
	require.NoError(t, err)

	assert.Equal(t, torrentBytes, body)
	assert.True(t, gotProwlarr.HadKey, "the first hop is the Prowlarr instance itself")
	assert.False(t, gotIndexer.HadKey, "the key must be dropped the moment a redirect leaves Prowlarr")
}

func TestDownloadKeepsTheAPIKeyAcrossASameOriginRedirect(t *testing.T) {
	var got recordedFetch

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/files/1.torrent" {
			http.Redirect(w, r, "/files/1.torrent", http.StatusFound)

			return
		}

		values, had := r.Header[apiKeyHeader]

		got = recordedFetch{Path: r.URL.Path, HadKey: had}
		if had {
			got.APIKey = values[0]
		}

		_, _ = w.Write(torrentBytes)
	}))
	t.Cleanup(srv.Close)

	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	body, err := client.Download(t.Context(), Release{DownloadURL: srv.URL + "/api/v1/indexer/3/download"})
	require.NoError(t, err)

	assert.Equal(t, torrentBytes, body)
	assert.True(t, got.HadKey, "a redirect within the Prowlarr origin keeps the key")
	assert.Equal(t, testAPIKey, got.APIKey)
}

func TestDownloadBoundsTheRedirectChain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.Download(t.Context(), Release{DownloadURL: srv.URL + "/loop"})
	require.ErrorIs(t, err, ErrUnreachable)
	assert.Contains(t, err.Error(), "redirects")
	assert.NotContains(t, err.Error(), testAPIKey)
}

func TestDownloadRefusesAReleaseWithNoDownloadURL(t *testing.T) {
	client := newTestClient(t, "http://127.0.0.1:9696", WithAPIKey(testAPIKey))

	_, err := client.Download(t.Context(), Release{
		Title:     "Ubuntu 24.04 LTS Desktop amd64",
		MagnetURL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
	})
	require.ErrorIs(t, err, ErrServer)
	assert.Contains(t, err.Error(), "no download_url")
	assert.Contains(t, err.Error(), "magnet_url", "the magnet must be named as the alternative")
}

func TestDownloadRefusesAReleaseWithNeitherURL(t *testing.T) {
	client := newTestClient(t, "http://127.0.0.1:9696", WithAPIKey(testAPIKey))

	_, err := client.Download(t.Context(), Release{Title: "Ubuntu 24.04 LTS Desktop amd64"})
	require.ErrorIs(t, err, ErrServer)
	assert.Contains(t, err.Error(), "no download_url")
}

func TestDownloadRefusesANonHTTPScheme(t *testing.T) {
	client := newTestClient(t, "http://127.0.0.1:9696", WithAPIKey(testAPIKey))

	// Some indexers publish the magnet link as the download URL. It is not
	// something to GET, and no scheme other than http(s) is fetchable.
	_, err := client.Download(t.Context(), Release{
		DownloadURL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
	})
	require.ErrorIs(t, err, ErrServer)
	assert.Contains(t, err.Error(), `"magnet"`, "the refused scheme must be named")
}

func TestDownloadCapsTheResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.CopyN(w, zeroReader{}, maxDownloadBytes+1)
	}))
	t.Cleanup(srv.Close)

	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.Download(t.Context(), Release{DownloadURL: srv.URL + "/huge.torrent"})
	require.ErrorIs(t, err, ErrServer)
	assert.Contains(t, err.Error(), "more than")
}

func TestDownloadMapsUnauthorizedToAuthError(t *testing.T) {
	srv := statusServer(t, http.StatusUnauthorized)
	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.Download(t.Context(), Release{DownloadURL: srv.URL + "/1.torrent"})
	require.ErrorIs(t, err, ErrAuth)
	assert.NotContains(t, err.Error(), testAPIKey)
}

func TestDownloadMapsAnErrorStatusToServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("no such release"))
	}))
	t.Cleanup(srv.Close)

	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.Download(t.Context(), Release{DownloadURL: srv.URL + "/1.torrent"})
	require.ErrorIs(t, err, ErrServer)
	assert.Contains(t, err.Error(), "404")
	assert.Contains(t, err.Error(), "no such release")
}

func TestDownloadMapsAnUnreachableHostToUnreachable(t *testing.T) {
	client := newTestClient(t, "http://127.0.0.1:9696",
		WithAPIKey(testAPIKey),
		WithHTTPClient(failingDoer{}),
	)

	_, err := client.Download(t.Context(), Release{
		DownloadURL: "http://127.0.0.1:9696/1.torrent?apikey=" + testAPIKey,
	})
	require.ErrorIs(t, err, ErrUnreachable)
	assert.NotContains(t, err.Error(), testAPIKey)
}

// zeroReader is an endless stream of zero bytes, so a fixture can answer with a
// body larger than the download cap without building one.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)

	return len(p), nil
}

// ptr returns a pointer to v. The nullable release counts are pointers so a
// count the indexer did not report stays distinguishable from a reported zero.
func ptr[T any](v T) *T {
	return &v
}
