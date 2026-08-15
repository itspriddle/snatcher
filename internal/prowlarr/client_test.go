package prowlarr

import (
	"encoding/json"
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

// ptr returns a pointer to v. The nullable release counts are pointers so a
// count the indexer did not report stays distinguishable from a reported zero.
func ptr[T any](v T) *T {
	return &v
}
