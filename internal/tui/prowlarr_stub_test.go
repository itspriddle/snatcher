package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/prowlarr"
)

// prowlarrTestKey is the API key the stub requires, and the value no frame,
// yank, or opened URL may carry.
const prowlarrTestKey = "sekrit-api-key-0123"

// torrentFixture is the body the stub serves for a release download.
const torrentFixture = "d8:announce31:http://tracker.example/announcee"

// releaseFixtures are the stub's search results, in Prowlarr's order: a release
// with every URL, each proxied through Prowlarr with the key in its query; a
// magnet-only one; and one whose title carries an escape sequence and whose
// seeders are unreported. %[1]s is the stub's URL and %[2]s the key.
const releaseFixtures = `[
	{
		"guid": "g-ubuntu", "title": "Ubuntu 24.04 Desktop", "indexer": "PublicTracker", "indexerId": 1,
		"age": 3, "publishDate": "2026-09-13T10:00:00Z", "size": 5368709120, "files": 1,
		"grabs": 10, "seeders": 120, "leechers": 5, "protocol": "torrent",
		"categories": [{"id": 4000, "name": "PC"}],
		"infoHash": "aaa111",
		"downloadUrl": "%[1]s/1/download?apikey=%[2]s&link=abc",
		"magnetUrl": "magnet:?xt=urn:btih:aaa111&dn=ubuntu",
		"infoUrl": "%[1]s/details/1?apikey=%[2]s",
		"commentUrl": "%[1]s/comments/1",
		"extraField": {"kept": true}
	},
	{
		"guid": "g-debian", "title": "Debian 12 netinst", "indexer": "OtherTracker", "indexerId": 2,
		"age": 30, "size": 700000000, "grabs": 400, "seeders": 50, "leechers": 1, "protocol": "torrent",
		"magnetUrl": "magnet:?xt=urn:btih:bbb222&dn=debian",
		"infoUrl": "https://other.example/t/2"
	},
	{
		"guid": "g-hostile", "title": "\u001b[31mhostile\u001b[0m release", "indexer": "PublicTracker", "indexerId": 1,
		"age": 1, "size": 1024, "grabs": null, "seeders": null, "leechers": null, "protocol": "torrent",
		"downloadUrl": "%[1]s/3/download?apikey=%[2]s",
		"commentUrl": "https://tracker.example/comments/3"
	}
]`

// indexerFixtures are two indexers, one disabled.
const indexerFixtures = `[
	{"id": 1, "name": "PublicTracker", "protocol": "torrent", "privacy": "public", "enable": true},
	{"id": 2, "name": "OtherTracker", "protocol": "torrent", "privacy": "private", "enable": false}
]`

// categoryFixtures is a two-level category tree.
const categoryFixtures = `[
	{"id": 2000, "name": "Movies", "subCategories": [{"id": 2040, "name": "Movies/HD"}]},
	{"id": 4000, "name": "PC", "subCategories": []}
]`

// prowlarrStub is a Prowlarr server: every API request must carry the key in
// X-Api-Key, searches page through the fixtures, and every request is recorded.
type prowlarrStub struct {
	url string

	mu       sync.Mutex
	releases []json.RawMessage
	queries  []url.Values
	posts    [][]byte
	paths    []string

	// status, when non-zero, answers every API request with that status.
	status atomic.Int32
}

// newProwlarrStub starts a stub server for the test.
func newProwlarrStub(t *testing.T) *prowlarrStub {
	t.Helper()

	stub := &prowlarrStub{}
	srv := httptest.NewServer(http.HandlerFunc(stub.serve))
	t.Cleanup(srv.Close)

	stub.url = srv.URL

	var raw []json.RawMessage
	require.NoError(t, json.Unmarshal(fmt.Appendf(nil, releaseFixtures, srv.URL, prowlarrTestKey), &raw))

	stub.releases = raw

	return stub
}

// serve answers one request.
func (s *prowlarrStub) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.paths = append(s.paths, r.URL.Path)
	s.mu.Unlock()

	if strings.HasSuffix(r.URL.Path, "/download") {
		if r.URL.Query().Get("apikey") != prowlarrTestKey {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		_, _ = io.WriteString(w, torrentFixture)

		return
	}

	if r.Header.Get("X-Api-Key") != prowlarrTestKey {
		w.WriteHeader(http.StatusUnauthorized)

		return
	}

	if code := s.status.Load(); code != 0 {
		w.WriteHeader(int(code))

		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch r.URL.Path {
	case "/api/v1/system/status":
		_, _ = io.WriteString(w, `{"appName": "Prowlarr", "instanceName": "Prowlarr", "version": "1.37.0.5076"}`)
	case "/api/v1/indexer":
		_, _ = io.WriteString(w, indexerFixtures)
	case "/api/v1/indexer/categories":
		_, _ = io.WriteString(w, categoryFixtures)
	case "/api/v1/search":
		s.search(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// search answers a GET with a page of the fixtures, and records a POST's body.
func (s *prowlarrStub) search(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if r.Method == http.MethodPost {
		body, _ := io.ReadAll(r.Body)
		s.posts = append(s.posts, body)
		_, _ = w.Write(body)

		return
	}

	q := r.URL.Query()
	s.queries = append(s.queries, q)

	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, err := strconv.Atoi(q.Get("limit"))

	if err != nil || limit <= 0 {
		limit = len(s.releases)
	}

	page := []json.RawMessage{}
	if offset < len(s.releases) {
		page = s.releases[offset:min(len(s.releases), offset+limit)]
	}

	body, _ := json.Marshal(page)
	_, _ = w.Write(body)
}

// lastQuery is the last search's query parameters.
func (s *prowlarrStub) lastQuery(t *testing.T) url.Values {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	require.NotEmpty(t, s.queries, "no search reached the stub")

	return s.queries[len(s.queries)-1]
}

// searches is how many searches the stub answered.
func (s *prowlarrStub) searches() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.queries)
}

// grabs is every grab body the stub received.
func (s *prowlarrStub) grabs() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([][]byte{}, s.posts...)
}

// requested reports whether any request was made for a path.
func (s *prowlarrStub) requested(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range s.paths {
		if p == path {
			return true
		}
	}

	return false
}

// withProwlarr gives a model a Prowlarr dialer onto the stub and a server list.
// The URL the dial reports is the stub's, since P opens it; goldens that show
// it are not written.
func withProwlarr(m Model, stub *prowlarrStub, servers []ServerEntry, dialed *[]string) Model {
	m.deps.ProwlarrServers = func() ([]ServerEntry, error) { return servers, nil }
	m.deps.DialProwlarr = func(name string) (ProwlarrDialResult, error) {
		if dialed != nil {
			*dialed = append(*dialed, name)
		}

		client, err := prowlarr.NewClient(stub.url, prowlarr.WithAPIKey(prowlarrTestKey))

		return ProwlarrDialResult{Client: client, Name: name, URL: stub.url + "/?apikey=" + prowlarrTestKey}, err
	}

	return m
}

// prowlarrServers is the stub's server list: one Prowlarr alias, the default.
func prowlarrServers() []ServerEntry {
	return []ServerEntry{{Name: "indexer", URL: "http://prowlarr:9696", Default: true}}
}

// prowlarrModel is a model in the Prowlarr window, dialed onto the stub. The
// window is marked opened first, so ctrl+p returns the dial alone: the query
// field's focus command is a cursor blink that sleeps.
func prowlarrModel(t *testing.T, w, h int) (*prowlarrStub, Model) {
	t.Helper()

	stub := newProwlarrStub(t)

	m := withProwlarr(New(testDeps()).applyTheme(true).resize(w, h), stub, prowlarrServers(), nil)
	m.ctx = t.Context()
	m.prowlarr.opened = true

	m, cmd := pressCmd(m, "ctrl+p")
	m = settle(t, m, cmd)

	require.NotNil(t, m.prowlarr.client, "the dial did not land: %s", m.toast)

	return stub, m
}

// searchModel is a Prowlarr model that has searched for "ubuntu" and has the
// three fixtures on screen.
func searchModel(t *testing.T, w, h int) (*prowlarrStub, Model) {
	t.Helper()

	stub, m := prowlarrModel(t, w, h)

	return stub, runSearchFor(t, m, "ubuntu")
}

// runSearchFor types a query into the search pane and submits it, settling the
// reply.
func runSearchFor(t *testing.T, m Model, query string) Model {
	t.Helper()

	m = typeText(press(m, "/"), query)
	m, cmd := pressCmd(m, "enter")

	return settle(t, m, cmd)
}

// runKey presses a key and settles the command it returns.
func runKey(t *testing.T, m Model, k string) Model {
	t.Helper()

	m, cmd := pressCmd(m, k)

	return settle(t, m, cmd)
}

// cmdMsgs runs a command and returns its messages, one level of batch
// flattened, without feeding them back.
func cmdMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}

	msg := cmd()

	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}

	var out []tea.Msg

	for _, c := range batch {
		if c != nil {
			out = append(out, c())
		}
	}

	return out
}

// apiKeyOf is the apikey query parameter of a URL.
func apiKeyOf(t *testing.T, raw string) string {
	t.Helper()

	u, err := url.Parse(raw)
	require.NoError(t, err)

	return u.Query().Get("apikey")
}
