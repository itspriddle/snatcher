package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/prowlarr"
)

// searchFixtureAPIKey is the API key the fixture bodies carry inside their
// downloadUrl and that the never-prints-the-key guards configure. Prowlarr
// really does hand the key back that way — a downloadUrl is a proxy link, and a
// torrent client fetching it has nowhere to put a header — so a fixture whose
// URLs carry no query string would let those guards pass whether or not any
// redaction exists.
const searchFixtureAPIKey = "super-secret-api-key"

// searchResultsBody is a Prowlarr /api/v1/search payload (camelCase wire fields)
// covering a torrent release that reports every count and a usenet release that
// reports none, so the null-vs-zero rendering is exercised end to end. The
// unmodeled sortTitle key stands in for whatever a newer Prowlarr adds, and both
// downloadUrls are proxy links carrying searchFixtureAPIKey.
const searchResultsBody = `[
	{
		"guid": "https://tracker.example/1",
		"title": "Ubuntu 24.04 LTS",
		"indexer": "Example Tracker",
		"indexerId": 3,
		"age": 12,
		"publishDate": "2026-08-01T00:00:00Z",
		"size": 2147483648,
		"files": 1,
		"grabs": 42,
		"seeders": 120,
		"leechers": 7,
		"protocol": "torrent",
		"infoHash": "aaa111",
		"downloadUrl": "http://prowlarr.example/3/download?apikey=super-secret-api-key&link=abc",
		"magnetUrl": "magnet:?xt=urn:btih:aaa111",
		"infoUrl": "https://tracker.example/details/1",
		"commentUrl": "https://tracker.example/details/1#comments",
		"sortTitle": "ubuntu 24.04 lts"
	},
	{
		"guid": "https://usenet.example/2",
		"title": "Debian 13",
		"indexer": "Example Usenet",
		"indexerId": 5,
		"age": 3,
		"size": 1073741824,
		"grabs": null,
		"seeders": null,
		"leechers": null,
		"protocol": "usenet",
		"downloadUrl": "http://prowlarr.example/5/download?apikey=super-secret-api-key&link=def"
	}
]`

// prowlarrSearchStub emulates a Prowlarr instance's search endpoint. Each
// successive request is answered with the next fixture body, so a --pages test
// describes a paged conversation as a list of pages; once the list runs out the
// last page repeats, so a test that only cares about the answer supplies one
// body and may run the command more than once. Every request's query is
// recorded, in order, so a test can assert exactly what went on the wire.
type prowlarrSearchStub struct {
	pages    []string
	requests *[]url.Values
}

func (s prowlarrSearchStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*s.requests = append(*s.requests, r.URL.Query())

		body := "[]"
		if len(s.pages) > 0 {
			page := min(len(*s.requests)-1, len(s.pages)-1)
			body = s.pages[page]
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// searchConfig starts a Prowlarr search stub and writes a config registering it
// as the default Prowlarr server. It returns the config path and a pointer to
// the recorded requests.
func searchConfig(t *testing.T, pages ...string) (string, *[]url.Values) {
	t.Helper()

	keyring.MockInit()

	requests := new([]url.Values)
	srv := httptest.NewServer(prowlarrSearchStub{pages: pages, requests: requests}.handler())
	t.Cleanup(srv.Close)

	cfgPath := writeConfigFile(t, fmt.Sprintf(
		`{"servers": {"indexers": {"url": %q, "type": "prowlarr"}}, "default_prowlarr_server": "indexers"}`,
		srv.URL))

	return cfgPath, requests
}

// runSearch executes the search command with args, returning stdout, stderr, and
// the Execute error. The command's output is a *bytes.Buffer, so isTTY reports
// false and the piped (tab-separated) plaintext contract is exercised.
func runSearch(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewSearchCmd()
	AddGlobalFlags(cmd)

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// searchPageBody renders a fixture body of n releases whose guids and titles
// carry label, so a paging test can tell which page a rendered row came from.
func searchPageBody(label string, n int) string {
	releases := make([]string, n)
	for i := range n {
		releases[i] = fmt.Sprintf(
			`{"guid":%q,"title":%q,"indexer":"Example","indexerId":1,"size":1024,"age":1}`,
			fmt.Sprintf("%s-%d", label, i), fmt.Sprintf("%s %d", label, i))
	}

	return "[" + strings.Join(releases, ",") + "]"
}

func TestSearchPipedTabSeparated(t *testing.T) {
	cfgPath, _ := searchConfig(t, searchResultsBody)

	out, _, err := runSearch(t, "--config", cfgPath, "ubuntu")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2, "piped output is header-less, one row per release")

	assert.Equal(t, strings.Join([]string{
		"1", "12d", "Ubuntu 24.04 LTS", "Example Tracker", "2.0 GB", "42", "120", "7",
	}, "\t"), lines[0])

	assert.Equal(t, strings.Join([]string{
		"2", "3d", "Debian 13", "Example Usenet", "1.0 GB", "-", "-", "-",
	}, "\t"), lines[1], "counts the indexer does not publish render as a dash")
}

func TestSearchPipedEmptyWritesNothing(t *testing.T) {
	cfgPath, _ := searchConfig(t, "[]")

	out, _, err := runSearch(t, "--config", cfgPath, "nothing")
	require.NoError(t, err)
	assert.Empty(t, out, "piped output for an empty result set is empty")
}

func TestSearchTableAligned(t *testing.T) {
	_, releases := searchReleases(t, searchResultsBody)
	results := numberSearchResults(releases)

	var buf bytes.Buffer
	// Width 0 disables truncation so the full titles show.
	writeListTable(&buf, results, searchColumns, 0, "No results.")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3, "header row plus one row per release")

	assert.True(t, strings.HasPrefix(strings.TrimSpace(lines[0]), "#"))
	for _, header := range []string{"AGE", "TITLE", "INDEXER", "SIZE", "GRABS", "SEEDERS", "LEECHERS"} {
		assert.Contains(t, lines[0], header)
	}

	assert.Contains(t, lines[1], "Ubuntu 24.04 LTS")
	assert.Contains(t, lines[2], "Debian 13")
}

func TestSearchTableEmptyPrintsNoResults(t *testing.T) {
	var buf bytes.Buffer
	writeListTable(&buf, []searchResult(nil), searchColumns, 80, "No results.")

	assert.Equal(t, "No results.", strings.TrimSpace(buf.String()))
}

func TestSearchJSON(t *testing.T) {
	cfgPath, _ := searchConfig(t, searchResultsBody)

	out, _, err := runSearch(t, "--config", cfgPath, "--json", "ubuntu")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 2)

	assert.InDelta(t, 1.0, got[0]["index"], 0, "the # column is present as a 1-based index field")
	assert.InDelta(t, 2.0, got[1]["index"], 0)

	assert.Equal(t, "https://tracker.example/1", got[0]["guid"])
	assert.Equal(t, "Ubuntu 24.04 LTS", got[0]["title"])
	assert.InDelta(t, 3.0, got[0]["indexer_id"], 0)
	assert.Nil(t, got[1]["seeders"], "an unreported count stays null rather than becoming zero")

	assert.NotContains(t, got[0], "Raw", "the retained ReleaseResource bytes are not doubled into output")
	assert.NotContains(t, got[0], "raw")
	assert.NotContains(t, out, "sortTitle", "unmodeled wire fields are not echoed")
}

func TestSearchJSONEmptyIsArrayNotNull(t *testing.T) {
	cfgPath, _ := searchConfig(t, "[]")

	out, _, err := runSearch(t, "--config", cfgPath, "--json", "nothing")
	require.NoError(t, err)
	assert.Equal(t, "[]", strings.TrimSpace(out))
}

func TestSearchJQ(t *testing.T) {
	cfgPath, _ := searchConfig(t, searchResultsBody)

	out, _, err := runSearch(t, "--config", cfgPath, "--jq", ".[].guid", "ubuntu")
	require.NoError(t, err)

	assert.Equal(t,
		`"https://tracker.example/1"`+"\n"+`"https://usenet.example/2"`,
		strings.TrimSpace(out))
}

// TestSearchJQTakesPrecedenceOverJSON pins the fixed --jq -> --json -> plaintext
// order.
func TestSearchJQTakesPrecedenceOverJSON(t *testing.T) {
	cfgPath, _ := searchConfig(t, searchResultsBody)

	out, _, err := runSearch(t, "--config", cfgPath, "--json", "--jq", ".[].index", "ubuntu")
	require.NoError(t, err)

	assert.Equal(t, "1\n2", strings.TrimSpace(out))
}

func TestSearchSendsEveryFilter(t *testing.T) {
	cfgPath, requests := searchConfig(t, searchResultsBody)

	_, _, err := runSearch(t, "--config", cfgPath,
		"--indexer", "3", "--indexer", "5",
		"--category", "5000", "--category", "5040",
		"--limit", "25",
		"--offset", "50",
		"--search-type", "tvsearch",
		"ubuntu",
	)
	require.NoError(t, err)

	require.Len(t, *requests, 1)
	query := (*requests)[0]

	assert.Equal(t, "ubuntu", query.Get("query"))
	assert.Equal(t, "tvsearch", query.Get("type"))
	assert.Equal(t, []string{"3", "5"}, query["indexerIds"])
	assert.Equal(t, []string{"5000", "5040"}, query["categories"])
	assert.Equal(t, "25", query.Get("limit"))
	assert.Equal(t, "50", query.Get("offset"))
}

// TestSearchAlwaysSendsTheLimit pins the paging contract: the limit is on the
// wire even when the caller never passed --limit, so an offset step is
// deterministic rather than tied to the server's own default page size.
func TestSearchAlwaysSendsTheLimit(t *testing.T) {
	cfgPath, requests := searchConfig(t, "[]")

	_, _, err := runSearch(t, "--config", cfgPath, "ubuntu")
	require.NoError(t, err)

	require.Len(t, *requests, 1)
	assert.Equal(t, "100", (*requests)[0].Get("limit"))
	assert.Equal(t, "search", (*requests)[0].Get("type"))
	assert.Empty(t, (*requests)[0]["indexerIds"], "an unset filter is not sent")
	assert.Empty(t, (*requests)[0]["categories"])
}

func TestSearchQueryFlag(t *testing.T) {
	cfgPath, requests := searchConfig(t, "[]")

	_, _, err := runSearch(t, "--config", cfgPath, "--query", "indexers")
	require.NoError(t, err)

	require.Len(t, *requests, 1)
	assert.Equal(t, "indexers", (*requests)[0].Get("query"))
}

func TestSearchRejectsBothQueryForms(t *testing.T) {
	cfgPath, requests := searchConfig(t, "[]")

	_, _, err := runSearch(t, "--config", cfgPath, "--query", "debian", "ubuntu")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query given twice")
	assert.Empty(t, *requests, "the query is settled before dialing")
}

func TestSearchRequiresAQuery(t *testing.T) {
	cfgPath, requests := searchConfig(t, "[]")

	_, _, err := runSearch(t, "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no query given")
	assert.Contains(t, err.Error(), "--query", "the error names both forms")
	assert.Contains(t, err.Error(), "snatcher search")
	assert.Empty(t, *requests)
}

func TestSearchTypeIsCaseInsensitive(t *testing.T) {
	cfgPath, requests := searchConfig(t, "[]")

	_, _, err := runSearch(t, "--config", cfgPath, "--search-type", "  MOVIE ", "dune")
	require.NoError(t, err)

	require.Len(t, *requests, 1)
	assert.Equal(t, "movie", (*requests)[0].Get("type"))
}

func TestSearchRejectsUnknownSearchType(t *testing.T) {
	cfgPath, requests := searchConfig(t, "[]")

	_, _, err := runSearch(t, "--config", cfgPath, "--search-type", "anime", "naruto")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid --search-type "anime"`)
	assert.Contains(t, err.Error(), `"search", "tvsearch", "movie", "music", "book"`)
	assert.Empty(t, *requests)
}

func TestSearchRejectsInvalidPaging(t *testing.T) {
	cfgPath, requests := searchConfig(t, "[]")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"--pages", "0"}, want: "invalid --pages 0"},
		{args: []string{"--pages", "-1"}, want: "invalid --pages -1"},
		{args: []string{"--limit", "0"}, want: "invalid --limit 0"},
		{args: []string{"--offset", "-1"}, want: "invalid --offset -1"},
	} {
		args := append([]string{"--config", cfgPath}, tc.args...)
		_, _, err := runSearch(t, append(args, "ubuntu")...)
		require.Errorf(t, err, "%v", tc.args)
		assert.Contains(t, err.Error(), tc.want)
	}

	assert.Empty(t, *requests, "paging is validated before dialing")
}

func TestSearchPagesConcatenatesInOrder(t *testing.T) {
	cfgPath, requests := searchConfig(t,
		searchPageBody("first", 2),
		searchPageBody("second", 2),
		searchPageBody("third", 2),
	)

	out, _, err := runSearch(t, "--config", cfgPath, "--pages", "3", "--limit", "2", "ubuntu")
	require.NoError(t, err)

	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, rows, 6)

	// The rendered order is the order the pages arrived in, numbered 1..6 over
	// the whole concatenated set rather than restarting on each page.
	for i, want := range []string{"first 0", "first 1", "second 0", "second 1", "third 0", "third 1"} {
		cells := strings.Split(rows[i], "\t")
		require.Len(t, cells, 8)
		assert.Equal(t, fmt.Sprint(i+1), cells[0])
		assert.Equal(t, want, cells[2])
	}

	require.Len(t, *requests, 3)
	assert.Empty(t, (*requests)[0].Get("offset"), "a zero offset is not sent")
	assert.Equal(t, "2", (*requests)[1].Get("offset"))
	assert.Equal(t, "4", (*requests)[2].Get("offset"))
}

func TestSearchPagesStopsOnAShortPage(t *testing.T) {
	cfgPath, requests := searchConfig(t,
		searchPageBody("first", 2),
		searchPageBody("second", 1),
		searchPageBody("third", 2),
	)

	out, _, err := runSearch(t, "--config", cfgPath, "--pages", "3", "--limit", "2", "ubuntu")
	require.NoError(t, err)

	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, rows, 3, "the short second page ends the walk")
	assert.Len(t, *requests, 2, "the third page is never requested")
}

// TestSearchPagesStepFromTheGivenOffset pins that --pages walks from --offset
// rather than from zero.
func TestSearchPagesStepFromTheGivenOffset(t *testing.T) {
	cfgPath, requests := searchConfig(t, searchPageBody("first", 2), searchPageBody("second", 2))

	_, _, err := runSearch(t, "--config", cfgPath,
		"--pages", "2", "--limit", "2", "--offset", "10", "ubuntu")
	require.NoError(t, err)

	require.Len(t, *requests, 2)
	assert.Equal(t, "10", (*requests)[0].Get("offset"))
	assert.Equal(t, "12", (*requests)[1].Get("offset"))
}

// TestSearchToleratesAnEnormousLimit pins that --limit sizes the *request* and
// nothing else. It is a user-supplied count of results to ask for, not a promise
// of what arrives, so it must never reach the allocator as a capacity hint: a
// fat-fingered limit has to travel to the wire and come back empty like any
// other, rather than abort the process before the first round trip.
//
// The value is the largest one pflag will accept, which makes the guard
// deterministic — a merely large limit (say 100000000) reserves tens of
// gigabytes of address space that an overcommitting kernel hands over without
// complaint, so it would not fail reliably.
func TestSearchToleratesAnEnormousLimit(t *testing.T) {
	cfgPath, requests := searchConfig(t, "[]")

	limit := strconv.Itoa(math.MaxInt)

	out, _, err := runSearch(t, "--config", cfgPath, "--limit", limit, "ubuntu")
	require.NoError(t, err)
	assert.Empty(t, out)

	require.Len(t, *requests, 1)
	assert.Equal(t, limit, (*requests)[0].Get("limit"))
}

// TestSearchSanitizesPlaintextOnly pins the sanitizer boundary: a crafted title
// or indexer name cannot inject terminal escapes into the human/plaintext paths,
// and is never altered on the structured --json path.
func TestSearchSanitizesPlaintextOnly(t *testing.T) {
	esc := string(rune(0o33))

	body, err := json.Marshal([]map[string]any{{
		"guid":      "g1",
		"title":     esc + "[2Jmalicious" + string(rune(0o07)) + ".iso",
		"indexer":   "Ex" + esc + "[31mploit",
		"indexerId": 1,
		"size":      1024,
	}})
	require.NoError(t, err)

	cfgPath, _ := searchConfig(t, string(body))

	plain, _, err := runSearch(t, "--config", cfgPath, "ubuntu")
	require.NoError(t, err)
	assert.NotContains(t, plain, esc)
	assert.Contains(t, plain, "[2Jmalicious.iso")
	assert.Contains(t, plain, "Ex[31mploit")

	structured, _, err := runSearch(t, "--config", cfgPath, "--json", "ubuntu")
	require.NoError(t, err)

	var got []struct {
		Title   string `json:"title"`
		Indexer string `json:"indexer"`
	}
	require.NoError(t, json.Unmarshal([]byte(structured), &got))
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Title, esc, "--json is a structured contract and is never sanitized")
	assert.Contains(t, got[0].Indexer, esc)
}

// TestSearchNeverPrintsTheAPIKey covers every render mode: the key reaches the
// wire and nothing else.
//
// The guard is only worth anything if the fixture can actually leak, so it first
// pins that searchResultsBody hands back a key-bearing downloadUrl the way
// Prowlarr does. Delete the render-boundary redaction and this test fails.
func TestSearchNeverPrintsTheAPIKey(t *testing.T) {
	require.Contains(t, searchResultsBody, "apikey="+searchFixtureAPIKey,
		"the fixture must carry the key in a URL, or this guard passes vacuously")

	cfgPath, _ := searchConfig(t, searchResultsBody)
	t.Setenv(envAPIKey, searchFixtureAPIKey)

	for _, args := range [][]string{
		{"ubuntu"},
		{"--json", "ubuntu"},
		{"--jq", ".", "ubuntu"},
	} {
		out, errOut, err := runSearch(t, append([]string{"--config", cfgPath}, args...)...)
		require.NoErrorf(t, err, "%v", args)
		assert.NotContainsf(t, out+errOut, searchFixtureAPIKey, "%v", args)
	}
}

// TestSearchRedactsTheAPIKeyFromResultURLs pins the credential strip itself: the
// three URL fields Prowlarr or an indexer may build as a key-bearing proxy link
// are rewritten for --json/--jq, and a URL that holds no key is emitted exactly
// as it arrived.
func TestSearchRedactsTheAPIKeyFromResultURLs(t *testing.T) {
	body := `[{
		"guid": "https://tracker.example/1",
		"title": "Ubuntu 24.04 LTS",
		"indexer": "Example Tracker",
		"indexerId": 3,
		"downloadUrl": "http://prowlarr.example/3/download?apikey=` + searchFixtureAPIKey + `&link=abc",
		"infoUrl": "http://prowlarr.example/3/details?ApiKey=` + searchFixtureAPIKey + `",
		"commentUrl": "https://tracker.example/details/1?apikey=` + searchFixtureAPIKey + `#comments",
		"magnetUrl": "magnet:?xt=urn:btih:aaa111"
	}]`

	cfgPath, _ := searchConfig(t, body)

	out, _, err := runSearch(t, "--config", cfgPath, "--json", "ubuntu")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 1)

	assert.Equal(t, "http://prowlarr.example/3/download?apikey=REDACTED&link=abc", got[0]["download_url"])
	assert.Equal(t, "http://prowlarr.example/3/details?ApiKey=REDACTED", got[0]["info_url"],
		"the parameter name is matched case-insensitively")
	assert.Equal(t, "https://tracker.example/details/1?apikey=REDACTED#comments", got[0]["comment_url"])

	assert.Equal(t, "magnet:?xt=urn:btih:aaa111", got[0]["magnet_url"],
		"a URL carrying no key is emitted byte-for-byte as it arrived")
	assert.Equal(t, "https://tracker.example/1", got[0]["guid"])
}

// TestSearchRedactsTheAPIKeyWhereverItAppears is the value-based half of the
// strip, and the reason it exists: Prowlarr proxies downloadUrl with an apikey=
// parameter today, so a redactor that only looks for that name on those three
// URL fields would start leaking the moment the response shape moved. Here the
// key arrives under a differently spelled parameter and on two fields the URL
// rewrite never visits.
func TestSearchRedactsTheAPIKeyWhereverItAppears(t *testing.T) {
	body := `[{
		"guid": "https://tracker.example/1?apikey=` + searchFixtureAPIKey + `",
		"title": "Ubuntu 24.04 LTS",
		"indexer": "Example Tracker",
		"indexerId": 3,
		"downloadUrl": "http://prowlarr.example/3/download?api_key=` + searchFixtureAPIKey + `&link=abc",
		"magnetUrl": "magnet:?xt=urn:btih:aaa111&tr=http://tracker.example/announce?passkey=` + searchFixtureAPIKey + `"
	}]`

	require.Contains(t, body, `"guid": "https://tracker.example/1?apikey=`+searchFixtureAPIKey,
		"the fixture must carry the key on a field Redacted does not touch, or this guard passes vacuously")
	require.Contains(t, body, "api_key="+searchFixtureAPIKey,
		"and under a parameter name the URL rewrite does not match")

	cfgPath, _ := searchConfig(t, body)
	t.Setenv(envAPIKey, searchFixtureAPIKey)

	for _, args := range [][]string{
		{"--json", "ubuntu"},
		{"--jq", ".", "ubuntu"},
	} {
		out, errOut, err := runSearch(t, append([]string{"--config", cfgPath}, args...)...)
		require.NoErrorf(t, err, "%v", args)
		assert.NotContainsf(t, out+errOut, searchFixtureAPIKey, "%v", args)
	}

	out, _, err := runSearch(t, "--config", cfgPath, "--json", "ubuntu")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 1)

	assert.Equal(t, "https://tracker.example/1?apikey=REDACTED", got[0]["guid"])
	assert.Equal(t, "http://prowlarr.example/3/download?api_key=REDACTED&link=abc", got[0]["download_url"],
		"the value is matched, not the parameter name")
	assert.Equal(t, "magnet:?xt=urn:btih:aaa111&tr=http://tracker.example/announce?passkey=REDACTED",
		got[0]["magnet_url"])
}

// TestSearchLeavesTheReleaseItActsOnUnredacted is the other half of the strip:
// the rewrite happens on a copy at the render boundary, so the release the rest
// of the command holds — the URL search download fetches, the bytes search grab
// re-POSTs — is still the one Prowlarr sent.
func TestSearchLeavesTheReleaseItActsOnUnredacted(t *testing.T) {
	// The key has to be configured for the value-based half of the strip to have
	// anything to match, so this covers both halves of Redact at once.
	t.Setenv(envAPIKey, searchFixtureAPIKey)

	client, releases := searchReleases(t, searchResultsBody)
	before := releases[0].DownloadURL
	raw := string(releases[0].Raw)

	results := numberSearchResults(releases)
	redacted := redactSearchResults(client, results)

	assert.Contains(t, redacted[0].DownloadURL, "apikey=REDACTED")
	assert.NotContains(t, redacted[0].DownloadURL, searchFixtureAPIKey)
	assert.Equal(t, before, results[0].DownloadURL, "the rendered copy does not mutate the release")
	assert.Contains(t, before, "apikey="+searchFixtureAPIKey)
	assert.Equal(t, raw, string(results[0].Raw), "the retained ReleaseResource bytes stay byte-exact")
}

func TestSearchAttachesTheAPIKeyHeader(t *testing.T) {
	keyring.MockInit()

	sent := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*sent = r.Header.Get("X-Api-Key")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(srv.Close)

	cfgPath := writeConfigFile(t, fmt.Sprintf(
		`{"servers": {"indexers": {"url": %q, "type": "prowlarr"}}, "default_prowlarr_server": "indexers"}`,
		srv.URL))

	t.Setenv(envAPIKey, "wire-key")

	_, _, err := runSearch(t, "--config", cfgPath, "ubuntu")
	require.NoError(t, err)
	assert.Equal(t, "wire-key", *sent)
}

// TestSearchRefusesATransmissionServer is the type-mismatch half of the dial
// seam, seen from the command a user actually runs.
func TestSearchRefusesATransmissionServer(t *testing.T) {
	keyring.MockInit()

	cfgPath := writeConfigFile(t,
		`{"servers": {"local": {"url": "http://local:9091/transmission/rpc"}}, "default_server": "local"}`)

	_, _, err := runSearch(t, "--config", cfgPath, "--server", "local", "ubuntu")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `server "local" is a Transmission server`)
}

func TestSearchReportsAServerError(t *testing.T) {
	keyring.MockInit()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	cfgPath := writeConfigFile(t, fmt.Sprintf(
		`{"servers": {"indexers": {"url": %q, "type": "prowlarr"}}, "default_prowlarr_server": "indexers"}`,
		srv.URL))

	_, _, err := runSearch(t, "--config", cfgPath, "ubuntu")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "searching "+srv.URL)
}

func TestSearchAgeAndCountRendering(t *testing.T) {
	assert.Equal(t, "0d", searchAge(0))
	assert.Equal(t, "12d", searchAge(12))
	assert.Equal(t, "-", searchAge(-1), "a future publish date renders as a dash, not a negative age")

	zero, grabs := 0, 42
	assert.Equal(t, "-", searchCount(nil))
	assert.Equal(t, "0", searchCount(&zero), "a published zero is distinct from an unpublished count")
	assert.Equal(t, "42", searchCount(&grabs))
}

// searchReleases decodes a fixture search body through the same client path the
// command uses, so a rendering test starts from real decoded releases rather
// than hand-built ones. The client is returned alongside them: it is what owns
// the render-boundary redaction, so a test rendering these releases needs it.
func searchReleases(t *testing.T, body string) (*prowlarr.Client, []prowlarr.Release) {
	t.Helper()

	cfgPath, _ := searchConfig(t, body)

	client, _, err := dialProwlarr(dialCmd(t, cfgPath), "")
	require.NoError(t, err)

	releases, err := client.Search(t.Context(), prowlarr.SearchParams{Limit: defaultSearchLimit})
	require.NoError(t, err)

	return client, releases
}
