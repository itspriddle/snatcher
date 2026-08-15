package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// grabbedReleaseBody is what Prowlarr echoes back from a successful grab: the
// accepted ReleaseResource, which for the exact form is the first description of
// the release snatcher has seen at all. Its downloadUrl is a proxy link carrying
// searchFixtureAPIKey, as Prowlarr's really is, so the never-prints-the-key
// guard below has something to catch.
const grabbedReleaseBody = `{
	"guid": "https://tracker.example/1",
	"title": "Ubuntu 24.04 LTS",
	"indexer": "Example Tracker",
	"indexerId": 3,
	"protocol": "torrent",
	"downloadUrl": "http://prowlarr.example/3/download?apikey=super-secret-api-key&link=abc"
}`

// prowlarrGrabStub answers both halves of a grab on /api/v1/search: a GET is the
// re-run the positional form resolves its index from, and a POST is the grab
// itself. Both are recorded — the searches by query, the grabs by body — so a
// test can assert that the exact form issued no search at all, and that the
// positional form handed back the release it resolved.
type prowlarrGrabStub struct {
	pages    []string
	grabbed  string
	searches *[]url.Values
	posted   *[]json.RawMessage
}

func (s prowlarrGrabStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			*s.posted = append(*s.posted, json.RawMessage(body))
			_, _ = w.Write([]byte(s.grabbed))

			return
		}

		*s.searches = append(*s.searches, r.URL.Query())

		body := "[]"
		if len(s.pages) > 0 {
			body = s.pages[min(len(*s.searches)-1, len(s.pages)-1)]
		}

		_, _ = w.Write([]byte(body))
	}
}

// grabConfig starts a grab stub and writes a config registering it as the
// default Prowlarr server. It returns the config path, the recorded searches,
// and the recorded grab bodies.
func grabConfig(t *testing.T, pages ...string) (string, *[]url.Values, *[]json.RawMessage) {
	t.Helper()

	keyring.MockInit()

	searches := new([]url.Values)
	posted := new([]json.RawMessage)

	stub := prowlarrGrabStub{pages: pages, grabbed: grabbedReleaseBody, searches: searches, posted: posted}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	cfgPath := writeConfigFile(t, fmt.Sprintf(
		`{"servers": {"indexers": {"url": %q, "type": "prowlarr"}}, "default_prowlarr_server": "indexers"}`,
		srv.URL))

	return cfgPath, searches, posted
}

func TestSearchGrabResolvesThePositionalIndex(t *testing.T) {
	cfgPath, searches, posted := grabConfig(t, searchResultsBody)

	_, _, err := runSearch(t, "--config", cfgPath, "grab", "ubuntu", "2")
	require.NoError(t, err)

	require.Len(t, *searches, 1, "the query is re-run exactly once")
	assert.Equal(t, "ubuntu", (*searches)[0].Get("query"))

	require.Len(t, *posted, 1)

	var sent map[string]any
	require.NoError(t, json.Unmarshal((*posted)[0], &sent))
	assert.Equal(t, "https://usenet.example/2", sent["guid"], "the 1-based number picks the second result")
}

// TestSearchGrabPostsTheRetainedResourceVerbatim pins that a positional grab
// hands back the exact ReleaseResource the search returned, unmodeled fields
// included, rather than a re-encode of the fields snatcher renders.
func TestSearchGrabPostsTheRetainedResourceVerbatim(t *testing.T) {
	cfgPath, _, posted := grabConfig(t, searchResultsBody)

	_, _, err := runSearch(t, "--config", cfgPath, "grab", "ubuntu", "1")
	require.NoError(t, err)

	require.Len(t, *posted, 1)

	var sent map[string]any
	require.NoError(t, json.Unmarshal((*posted)[0], &sent))
	assert.Equal(t, "ubuntu 24.04 lts", sent["sortTitle"], "a field snatcher does not model still reaches Prowlarr")
	assert.Equal(t, "http://prowlarr.example/3/download?apikey="+searchFixtureAPIKey+"&link=abc", sent["downloadUrl"],
		"the retained bytes are re-POSTed unredacted; the strip is a render-boundary rewrite only")
}

func TestSearchGrabEchoesTheResolvedReleaseBeforeActing(t *testing.T) {
	cfgPath, _, _ := grabConfig(t, searchResultsBody)

	out, errOut, err := runSearch(t, "--config", cfgPath, "grab", "ubuntu", "1")
	require.NoError(t, err)

	assert.Equal(t, "Grabbing: Ubuntu 24.04 LTS [Example Tracker]\n", errOut,
		"what is acted on is named on stderr before the grab")
	assert.Equal(t, "Grabbed: Ubuntu 24.04 LTS [Example Tracker]\n", out)
}

// TestSearchGrabExactFormIssuesNoSearch is the whole point of --guid: no re-run,
// so no result set can shift underneath the caller.
func TestSearchGrabExactFormIssuesNoSearch(t *testing.T) {
	cfgPath, searches, posted := grabConfig(t, searchResultsBody)

	out, errOut, err := runSearch(t, "--config", cfgPath,
		"grab", "--guid", "https://tracker.example/1", "--indexer", "3")
	require.NoError(t, err)

	assert.Empty(t, *searches, "the exact form re-runs nothing")

	require.Len(t, *posted, 1)
	assert.JSONEq(t, `{"guid":"https://tracker.example/1","indexerId":3}`, string((*posted)[0]))

	assert.Equal(t, "Grabbing: https://tracker.example/1 [indexer 3]\n", errOut,
		"a release known only by guid is named by guid")
	assert.Equal(t, "Grabbed: Ubuntu 24.04 LTS [Example Tracker]\n", out,
		"the echoed resource names the release Prowlarr accepted")
}

func TestSearchGrabRejectsGuidWithPositionalArgs(t *testing.T) {
	cfgPath, searches, posted := grabConfig(t, searchResultsBody)

	_, _, err := runSearch(t, "--config", cfgPath,
		"grab", "--guid", "https://tracker.example/1", "--indexer", "3", "ubuntu", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--guid names a release exactly and takes no positional arguments")

	assert.Empty(t, *searches, "a contradictory invocation reaches no endpoint")
	assert.Empty(t, *posted)
}

func TestSearchGrabRequiresExactlyOneIndexerWithGuid(t *testing.T) {
	cfgPath, _, posted := grabConfig(t, searchResultsBody)

	_, _, err := runSearch(t, "--config", cfgPath, "grab", "--guid", "https://tracker.example/1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--guid needs the indexer that issued it")
	assert.Contains(t, err.Error(), "snatcher search indexers")

	_, _, err = runSearch(t, "--config", cfgPath,
		"grab", "--guid", "https://tracker.example/1", "--indexer", "3", "--indexer", "5")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--guid needs exactly one --indexer, got 2")

	assert.Empty(t, *posted)
}

func TestSearchGrabRequiresAQueryAndANumber(t *testing.T) {
	cfgPath, searches, _ := grabConfig(t, searchResultsBody)

	for _, args := range [][]string{{"grab"}, {"grab", "ubuntu"}, {"grab", "ubuntu", "1", "2"}} {
		_, _, err := runSearch(t, append([]string{"--config", cfgPath}, args...)...)
		require.Errorf(t, err, "%v", args)
		assert.Containsf(t, err.Error(), "name a release by position with a query and a result number", "%v", args)
		assert.Containsf(t, err.Error(), "--guid", "%v", args)
	}

	assert.Empty(t, *searches, "the selection is settled before dialing")
}

func TestSearchGrabRejectsANonPositiveOrUnparseableNumber(t *testing.T) {
	cfgPath, searches, _ := grabConfig(t, searchResultsBody)

	for _, tc := range []struct{ arg, want string }{
		{arg: "0", want: "invalid result number 0: results are numbered from 1"},
		{arg: "three", want: `invalid result number "three"`},
	} {
		_, _, err := runSearch(t, "--config", cfgPath, "grab", "ubuntu", tc.arg)
		require.Errorf(t, err, "%v", tc.arg)
		assert.Contains(t, err.Error(), tc.want)
	}

	// A leading dash is pflag's to claim, so a negative number reaches the
	// validator only after the "--" terminator. Both paths refuse it.
	_, _, err := runSearch(t, "--config", cfgPath, "grab", "ubuntu", "--", "-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid result number -1: results are numbered from 1")

	assert.Empty(t, *searches)
}

func TestSearchGrabRejectsAnOutOfRangeNumber(t *testing.T) {
	cfgPath, _, posted := grabConfig(t, searchResultsBody)

	_, _, err := runSearch(t, "--config", cfgPath, "grab", "ubuntu", "5")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no result 5: the search returned 2 results")
	assert.Empty(t, *posted, "nothing is grabbed when the number names no result")
}

func TestSearchGrabRejectsAnOutOfRangeNumberOnAnEmptySearch(t *testing.T) {
	cfgPath, _, posted := grabConfig(t, "[]")

	_, _, err := runSearch(t, "--config", cfgPath, "grab", "ubuntu", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no result 1: the search returned 0 results")
	assert.Empty(t, *posted)
}

// TestSearchGrabResolvesAnIndexAcrossPages pins that the number is a position in
// the whole concatenated result set, exactly as the # column numbers it.
func TestSearchGrabResolvesAnIndexAcrossPages(t *testing.T) {
	cfgPath, searches, posted := grabConfig(t, searchPageBody("first", 2), searchPageBody("second", 2))

	_, _, err := runSearch(t, "--config", cfgPath,
		"grab", "--pages", "2", "--limit", "2", "ubuntu", "3")
	require.NoError(t, err)

	require.Len(t, *searches, 2)
	assert.Equal(t, "2", (*searches)[1].Get("offset"))

	require.Len(t, *posted, 1)

	var sent map[string]any
	require.NoError(t, json.Unmarshal((*posted)[0], &sent))
	assert.Equal(t, "second-0", sent["guid"], "result 3 is the first row of the second page")
}

func TestSearchGrabPassesEveryFilterToTheReRun(t *testing.T) {
	cfgPath, searches, _ := grabConfig(t, searchResultsBody)

	_, _, err := runSearch(t, "--config", cfgPath, "grab",
		"--indexer", "3", "--indexer", "5",
		"--category", "5000",
		"--limit", "25",
		"--offset", "50",
		"--search-type", "tvsearch",
		"ubuntu", "1",
	)
	require.NoError(t, err)

	require.Len(t, *searches, 1)
	query := (*searches)[0]

	assert.Equal(t, "ubuntu", query.Get("query"))
	assert.Equal(t, "tvsearch", query.Get("type"))
	assert.Equal(t, []string{"3", "5"}, query["indexerIds"])
	assert.Equal(t, []string{"5000"}, query["categories"])
	assert.Equal(t, "25", query.Get("limit"))
	assert.Equal(t, "50", query.Get("offset"))
}

func TestSearchGrabRejectsInvalidPagingAndSearchType(t *testing.T) {
	cfgPath, searches, _ := grabConfig(t, searchResultsBody)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"--pages", "0"}, want: "invalid --pages 0"},
		{args: []string{"--limit", "0"}, want: "invalid --limit 0"},
		{args: []string{"--offset", "-1"}, want: "invalid --offset -1"},
		{args: []string{"--search-type", "anime"}, want: `invalid --search-type "anime"`},
	} {
		args := append([]string{"--config", cfgPath, "grab"}, tc.args...)
		_, _, err := runSearch(t, append(args, "ubuntu", "1")...)
		require.Errorf(t, err, "%v", tc.args)
		assert.Contains(t, err.Error(), tc.want)
	}

	assert.Empty(t, *searches)
}

func TestSearchGrabJSONEmitsTheGrabbedRelease(t *testing.T) {
	cfgPath, _, _ := grabConfig(t, searchResultsBody)

	out, _, err := runSearch(t, "--config", cfgPath, "--json", "grab", "ubuntu", "1")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	assert.Equal(t, "https://tracker.example/1", got["guid"])
	assert.Equal(t, "Ubuntu 24.04 LTS", got["title"])
	assert.InDelta(t, 3.0, got["indexer_id"], 0)
	assert.NotContains(t, got, "index", "a grab acts on one release; there is no rendered order to number")
	assert.NotContains(t, got, "raw")

	assert.Equal(t, "http://prowlarr.example/3/download?apikey=REDACTED&link=abc", got["download_url"],
		"the echoed proxy link carries the API key and is stripped at the render boundary")
}

func TestSearchGrabJQTakesPrecedenceOverJSON(t *testing.T) {
	cfgPath, _, _ := grabConfig(t, searchResultsBody)

	out, _, err := runSearch(t, "--config", cfgPath, "--json", "--jq", ".title", "grab", "ubuntu", "1")
	require.NoError(t, err)

	assert.Equal(t, `"Ubuntu 24.04 LTS"`, strings.TrimSpace(out))
}

// TestSearchGrabSanitizesPlaintextOnly pins the sanitizer boundary on both the
// pre-grab echo and the confirmation line, and that --json is left verbatim.
func TestSearchGrabSanitizesPlaintextOnly(t *testing.T) {
	esc := string(rune(0o33))

	search, err := json.Marshal([]map[string]any{{
		"guid":      "g1",
		"title":     esc + "[2Jmalicious.iso",
		"indexer":   "Ex" + esc + "[31mploit",
		"indexerId": 1,
	}})
	require.NoError(t, err)

	keyring.MockInit()

	searches := new([]url.Values)
	posted := new([]json.RawMessage)
	stub := prowlarrGrabStub{
		pages:    []string{string(search)},
		grabbed:  string(search[1 : len(search)-1]),
		searches: searches,
		posted:   posted,
	}

	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	cfgPath := writeConfigFile(t, fmt.Sprintf(
		`{"servers": {"indexers": {"url": %q, "type": "prowlarr"}}, "default_prowlarr_server": "indexers"}`,
		srv.URL))

	out, errOut, err := runSearch(t, "--config", cfgPath, "grab", "ubuntu", "1")
	require.NoError(t, err)

	assert.NotContains(t, errOut, esc)
	assert.NotContains(t, out, esc)
	assert.Contains(t, errOut, "Grabbing: [2Jmalicious.iso [Ex[31mploit]")
	assert.Contains(t, out, "Grabbed: [2Jmalicious.iso [Ex[31mploit]")

	structured, _, err := runSearch(t, "--config", cfgPath, "--json", "grab", "ubuntu", "1")
	require.NoError(t, err)

	var got struct {
		Title string `json:"title"`
	}
	require.NoError(t, json.Unmarshal([]byte(structured), &got))
	assert.Contains(t, got.Title, esc, "--json is a structured contract and is never sanitized")
}

func TestSearchGrabNeverPrintsTheAPIKey(t *testing.T) {
	require.Contains(t, grabbedReleaseBody, "apikey="+searchFixtureAPIKey,
		"the echoed resource must carry the key in a URL, or this guard passes vacuously")

	cfgPath, _, _ := grabConfig(t, searchResultsBody)
	t.Setenv(envAPIKey, searchFixtureAPIKey)

	for _, args := range [][]string{
		{"grab", "ubuntu", "1"},
		{"--json", "grab", "ubuntu", "1"},
		{"--jq", ".", "grab", "ubuntu", "1"},
		{"grab", "--guid", "https://tracker.example/1", "--indexer", "3"},
		{"--json", "grab", "--guid", "https://tracker.example/1", "--indexer", "3"},
	} {
		out, errOut, err := runSearch(t, append([]string{"--config", cfgPath}, args...)...)
		require.NoErrorf(t, err, "%v", args)
		assert.NotContainsf(t, out+errOut, searchFixtureAPIKey, "%v", args)
	}
}

// TestSearchGrabRedactsTheAPIKeyWhereverItAppears is the grab half of the
// value-based strip. The echoed resource carries the key on its guid and under a
// parameter spelled api_key=, neither of which the URL rewrite visits, so a
// redactor matching only on the name Prowlarr happens to use today would print
// the key twice. The bytes handed back to Prowlarr are unaffected: the strip is a
// render-boundary rewrite, and a grab must re-POST what it was sent.
func TestSearchGrabRedactsTheAPIKeyWhereverItAppears(t *testing.T) {
	page := `[{
		"guid": "https://tracker.example/1?apikey=` + searchFixtureAPIKey + `",
		"title": "Ubuntu 24.04 LTS",
		"indexer": "Example Tracker",
		"indexerId": 3,
		"downloadUrl": "http://prowlarr.example/3/download?api_key=` + searchFixtureAPIKey + `&link=abc"
	}]`

	require.Contains(t, page, `"guid": "https://tracker.example/1?apikey=`+searchFixtureAPIKey,
		"the fixture must carry the key on a field Redacted does not touch, or this guard passes vacuously")
	require.Contains(t, page, "api_key="+searchFixtureAPIKey,
		"and under a parameter name the URL rewrite does not match")

	keyring.MockInit()

	searches := new([]url.Values)
	posted := new([]json.RawMessage)
	stub := prowlarrGrabStub{
		pages: []string{page},
		// The echoed grab is the same resource without its enclosing array, which
		// is exactly the shape Prowlarr answers a grab with.
		grabbed:  page[1 : len(page)-1],
		searches: searches,
		posted:   posted,
	}

	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	cfgPath := writeConfigFile(t, fmt.Sprintf(
		`{"servers": {"indexers": {"url": %q, "type": "prowlarr"}}, "default_prowlarr_server": "indexers"}`,
		srv.URL))

	t.Setenv(envAPIKey, searchFixtureAPIKey)

	out, errOut, err := runSearch(t, "--config", cfgPath, "--json", "grab", "ubuntu", "1")
	require.NoError(t, err)
	assert.NotContains(t, out+errOut, searchFixtureAPIKey)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	assert.Equal(t, "https://tracker.example/1?apikey=REDACTED", got["guid"])
	assert.Equal(t, "http://prowlarr.example/3/download?api_key=REDACTED&link=abc", got["download_url"],
		"the value is matched, not the parameter name")

	require.Len(t, *posted, 1)
	assert.Contains(t, string((*posted)[0]), "api_key="+searchFixtureAPIKey,
		"the retained bytes are re-POSTed unredacted; the strip is a render-boundary rewrite only")
}

// TestSearchGrabReportsAServerError pins that the POST half of a grab is not
// swallowed: the re-run succeeds, Prowlarr refuses the grab, and the error says
// which instance refused it.
func TestSearchGrabReportsAServerError(t *testing.T) {
	keyring.MockInit()

	searches := new([]url.Values)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message": "no download client configured"}`))

			return
		}

		*searches = append(*searches, r.URL.Query())

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(searchResultsBody))
	}))
	t.Cleanup(srv.Close)

	cfgPath := writeConfigFile(t, fmt.Sprintf(
		`{"servers": {"indexers": {"url": %q, "type": "prowlarr"}}, "default_prowlarr_server": "indexers"}`,
		srv.URL))

	_, _, err := runSearch(t, "--config", cfgPath, "grab", "ubuntu", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "grabbing on "+srv.URL)
	assert.Contains(t, err.Error(), "no download client configured",
		"the instance's own explanation survives the wrap")

	assert.Len(t, *searches, 1, "the re-run succeeded; it is the grab that failed")
}

func TestSearchGrabRefusesATransmissionServer(t *testing.T) {
	keyring.MockInit()

	cfgPath := writeConfigFile(t,
		`{"servers": {"local": {"url": "http://local:9091/transmission/rpc"}}, "default_server": "local"}`)

	_, _, err := runSearch(t, "--config", cfgPath, "--server", "local", "grab", "ubuntu", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `server "local" is a Transmission server`)
}

// TestSearchGrabDocumentsTheDelegation pins the one thing a user must not get
// wrong: a grab goes to Prowlarr's download client, not to the server snatcher
// is pointed at. The man page carries the same note; check-man only proves the
// command is mentioned, not what it says.
func TestSearchGrabDocumentsTheDelegation(t *testing.T) {
	var grab string
	for _, sub := range NewSearchCmd().Commands() {
		if sub.Name() == "grab" {
			grab = sub.Long
		}
	}

	require.NotEmpty(t, grab)
	assert.Contains(t, grab, "Prowlarr's own configured download client")
	assert.Contains(t, grab, "not necessarily the Transmission server snatcher is pointed at")
	assert.Contains(t, grab, "--search-type", "the help states which flags must match for a number to mean the same release")
}
