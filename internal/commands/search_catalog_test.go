package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/prowlarr"
)

// searchIndexersBody is a Prowlarr /api/v1/indexer payload (camelCase wire
// fields) covering an enabled private torrent tracker and a disabled public
// usenet indexer, so the yes/no rendering is exercised both ways. The first
// carries the credential-bearing fields array a real IndexerResource has, which
// no render mode may echo.
const searchIndexersBody = `[
	{
		"id": 3,
		"name": "Example Tracker",
		"protocol": "torrent",
		"privacy": "private",
		"enable": true,
		"priority": 25,
		"supportsSearch": true,
		"supportsPagination": true,
		"language": "en-US",
		"fields": [{"name": "apiKey", "value": "indexer-passkey"}]
	},
	{
		"id": 5,
		"name": "Example Usenet",
		"protocol": "usenet",
		"privacy": "public",
		"enable": false,
		"priority": 50,
		"supportsSearch": true,
		"supportsPagination": false,
		"language": "en-US"
	}
]`

// searchCategoriesBody is a Prowlarr /api/v1/indexer/categories payload: two
// root categories, the first with two children and the second with one, so both
// the flatten order and the parent linkage are exercised.
const searchCategoriesBody = `[
	{
		"id": 2000,
		"name": "Movies",
		"description": "Movie releases",
		"subCategories": [
			{"id": 2010, "name": "Movies/Foreign", "subCategories": []},
			{"id": 2040, "name": "Movies/HD", "subCategories": []}
		]
	},
	{
		"id": 5000,
		"name": "TV",
		"subCategories": [
			{"id": 5040, "name": "TV/HD", "subCategories": []}
		]
	}
]`

// prowlarrCatalogStub answers the two catalog endpoints from fixture bodies and
// records the path of every request, so a test can assert both what was rendered
// and which endpoint produced it. An unknown path answers with an empty array,
// which is what makes a wrong-path request show up as an empty rendering rather
// than as a decode error.
type prowlarrCatalogStub struct {
	indexers   string
	categories string
	paths      *[]string
}

func (s prowlarrCatalogStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*s.paths = append(*s.paths, r.URL.Path)

		body := "[]"
		switch r.URL.Path {
		case "/api/v1/indexer":
			body = s.indexers
		case "/api/v1/indexer/categories":
			body = s.categories
		}

		if body == "" {
			body = "[]"
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// catalogConfig starts a Prowlarr catalog stub and writes a config registering
// it as the default Prowlarr server. It returns the config path and a pointer to
// the recorded request paths.
func catalogConfig(t *testing.T, indexers, categories string) (string, *[]string) {
	t.Helper()

	keyring.MockInit()

	paths := new([]string)
	srv := httptest.NewServer(prowlarrCatalogStub{indexers: indexers, categories: categories, paths: paths}.handler())
	t.Cleanup(srv.Close)

	cfgPath := writeConfigFile(t, fmt.Sprintf(
		`{"servers": {"indexers": {"url": %q, "type": "prowlarr"}}, "default_prowlarr_server": "indexers"}`,
		srv.URL))

	return cfgPath, paths
}

// TestSearchGroupLongNamesEverySubcommand mirrors the torrent group's guard: the
// parenthesized subcommand list in Long is prose neither check-surface nor
// check-man looks at, and it is also the FR-14 note telling a user how to search
// for a word a subcommand shadows. A subcommand added without updating it would
// silently leave that note wrong.
func TestSearchGroupLongNamesEverySubcommand(t *testing.T) {
	cmd := NewSearchCmd()

	open := strings.Index(cmd.Long, "(")
	closing := strings.Index(cmd.Long, ")")
	require.Greater(t, closing, open, "Long carries a parenthesized subcommand list")

	named := strings.Split(cmd.Long[open+1:closing], ", ")

	registered := make([]string, 0, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		registered = append(registered, sub.Name())
	}

	assert.ElementsMatch(t, registered, named,
		"the Long enumeration names exactly the registered subcommands")
}

// TestSearchLongDocumentsTheQueryEscape pins the other half of FR-14: the note
// must name the escape at the point of confusion, not only in the man page.
func TestSearchLongDocumentsTheQueryEscape(t *testing.T) {
	assert.Contains(t, NewSearchCmd().Long, `"snatcher search --query indexers"`)
}

func TestSearchIndexersPipedTabSeparated(t *testing.T) {
	cfgPath, paths := catalogConfig(t, searchIndexersBody, "[]")

	out, _, err := runSearch(t, "--config", cfgPath, "indexers")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2, "piped output is header-less, one row per indexer")

	assert.Equal(t, strings.Join([]string{
		"3", "Example Tracker", "torrent", "private", "yes", "25",
	}, "\t"), lines[0])

	assert.Equal(t, strings.Join([]string{
		"5", "Example Usenet", "usenet", "public", "no", "50",
	}, "\t"), lines[1])

	require.Len(t, *paths, 1)
	assert.Equal(t, "/api/v1/indexer", (*paths)[0])
}

func TestSearchIndexersNeverEchoesIndexerCredentials(t *testing.T) {
	cfgPath, _ := catalogConfig(t, searchIndexersBody, "[]")

	for _, args := range [][]string{
		{"indexers"},
		{"indexers", "--json"},
		{"indexers", "--jq", "."},
	} {
		out, errOut, err := runSearch(t, append([]string{"--config", cfgPath}, args...)...)
		require.NoErrorf(t, err, "%v", args)
		assert.NotContainsf(t, out+errOut, "indexer-passkey", "%v", args)
	}
}

func TestSearchIndexersTableAligned(t *testing.T) {
	cfgPath, _ := catalogConfig(t, searchIndexersBody, "[]")

	client, _, err := dialProwlarr(dialCmd(t, cfgPath), "")
	require.NoError(t, err)

	indexers, err := client.Indexers(t.Context())
	require.NoError(t, err)

	var buf bytes.Buffer
	// Width 0 disables truncation so the full names show.
	writeListTable(&buf, indexers, searchIndexerColumns, 0, "No indexers configured.")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3, "header row plus one row per indexer")

	assert.True(t, strings.HasPrefix(strings.TrimSpace(lines[0]), "ID"))
	for _, header := range []string{"NAME", "PROTOCOL", "PRIVACY", "ENABLED", "PRIORITY"} {
		assert.Contains(t, lines[0], header)
	}

	assert.Contains(t, lines[1], "Example Tracker")
	assert.Contains(t, lines[2], "Example Usenet")
}

func TestSearchIndexersTableEmptyPrintsAFriendlyLine(t *testing.T) {
	var buf bytes.Buffer
	writeListTable(&buf, []prowlarr.Indexer(nil), searchIndexerColumns, 80, "No indexers configured.")

	assert.Equal(t, "No indexers configured.", strings.TrimSpace(buf.String()))
}

func TestSearchIndexersJSON(t *testing.T) {
	cfgPath, _ := catalogConfig(t, searchIndexersBody, "[]")

	out, _, err := runSearch(t, "--config", cfgPath, "indexers", "--json")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 2)

	assert.InDelta(t, 3.0, got[0]["id"], 0)
	assert.Equal(t, "Example Tracker", got[0]["name"])
	assert.Equal(t, "torrent", got[0]["protocol"])
	assert.Equal(t, "private", got[0]["privacy"])
	assert.Equal(t, true, got[0]["enable"])
	assert.InDelta(t, 25.0, got[0]["priority"], 0)
	assert.Equal(t, true, got[0]["supports_search"])
	assert.Equal(t, true, got[0]["supports_pagination"])
	assert.Equal(t, "en-US", got[0]["language"])

	assert.Equal(t, false, got[1]["enable"])
	assert.Equal(t, false, got[1]["supports_pagination"])
}

func TestSearchIndexersJSONEmptyIsArrayNotNull(t *testing.T) {
	cfgPath, _ := catalogConfig(t, "[]", "[]")

	out, _, err := runSearch(t, "--config", cfgPath, "indexers", "--json")
	require.NoError(t, err)
	assert.Equal(t, "[]", strings.TrimSpace(out))
}

func TestSearchIndexersPipedEmptyWritesNothing(t *testing.T) {
	cfgPath, _ := catalogConfig(t, "[]", "[]")

	out, _, err := runSearch(t, "--config", cfgPath, "indexers")
	require.NoError(t, err)
	assert.Empty(t, out)
}

// TestSearchIndexersJQTakesPrecedenceOverJSON pins the fixed --jq -> --json ->
// plaintext order for the subcommand.
func TestSearchIndexersJQTakesPrecedenceOverJSON(t *testing.T) {
	cfgPath, _ := catalogConfig(t, searchIndexersBody, "[]")

	out, _, err := runSearch(t, "--config", cfgPath, "indexers", "--json", "--jq", ".[].id")
	require.NoError(t, err)

	assert.Equal(t, "3\n5", strings.TrimSpace(out))
}

func TestSearchCategoriesPipedFlattensDepthFirst(t *testing.T) {
	cfgPath, paths := catalogConfig(t, "[]", searchCategoriesBody)

	out, _, err := runSearch(t, "--config", cfgPath, "categories")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 5, "every category is a row, parents included")

	assert.Equal(t, []string{
		"2000\tMovies",
		"2010\tMovies/Foreign",
		"2040\tMovies/HD",
		"5000\tTV",
		"5040\tTV/HD",
	}, lines, "a parent is the row above its first child, and the piped name is not indented")

	require.Len(t, *paths, 1)
	assert.Equal(t, "/api/v1/indexer/categories", (*paths)[0])
}

func TestSearchCategoriesTableIndentsSubCategories(t *testing.T) {
	rows := flattenCategories([]prowlarr.Category{
		{ID: 2000, Name: "Movies", SubCategories: []prowlarr.Category{
			{ID: 2040, Name: "Movies/HD"},
		}},
	})

	var buf bytes.Buffer
	// Width 0 disables truncation so the full, indented names show.
	writeListTable(&buf, rows, searchCategoryTableColumns, 0, "No categories.")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3, "header row plus one row per category")

	assert.Contains(t, lines[0], "ID")
	assert.Contains(t, lines[0], "NAME")

	assert.Contains(t, lines[1], "Movies")
	assert.Contains(t, lines[2], categoryIndent+"Movies/HD",
		"a sub-category is indented under its parent on the TTY path")
}

func TestSearchCategoriesTableEmptyPrintsAFriendlyLine(t *testing.T) {
	var buf bytes.Buffer
	writeListTable(&buf, []searchCategory(nil), searchCategoryTableColumns, 80, "No categories.")

	assert.Equal(t, "No categories.", strings.TrimSpace(buf.String()))
}

func TestSearchCategoriesJSONNamesTheParent(t *testing.T) {
	cfgPath, _ := catalogConfig(t, "[]", searchCategoriesBody)

	out, _, err := runSearch(t, "--config", cfgPath, "categories", "--json")
	require.NoError(t, err)

	var got []struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		ParentID    *int   `json:"parent_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 5)

	assert.Equal(t, 2000, got[0].ID)
	assert.Equal(t, "Movies", got[0].Name)
	assert.Equal(t, "Movie releases", got[0].Description)
	assert.Nil(t, got[0].ParentID, "a root category has a null parent_id, not 0")

	require.NotNil(t, got[1].ParentID)
	assert.Equal(t, 2000, *got[1].ParentID)
	require.NotNil(t, got[2].ParentID)
	assert.Equal(t, 2000, *got[2].ParentID)

	assert.Nil(t, got[3].ParentID)
	require.NotNil(t, got[4].ParentID)
	assert.Equal(t, 5000, *got[4].ParentID)

	assert.NotContains(t, out, "sub_categories",
		"the flattened rendering does not also carry the tree")
	assert.NotContains(t, out, "depth", "the indent level is not part of the JSON contract")
}

func TestSearchCategoriesJSONEmptyIsArrayNotNull(t *testing.T) {
	cfgPath, _ := catalogConfig(t, "[]", "[]")

	out, _, err := runSearch(t, "--config", cfgPath, "categories", "--json")
	require.NoError(t, err)
	assert.Equal(t, "[]", strings.TrimSpace(out))
}

func TestSearchCategoriesJQ(t *testing.T) {
	cfgPath, _ := catalogConfig(t, "[]", searchCategoriesBody)

	out, _, err := runSearch(t, "--config", cfgPath, "categories", "--jq", `.[] | select(.parent_id == 2000) | .id`)
	require.NoError(t, err)

	assert.Equal(t, "2010\n2040", strings.TrimSpace(out))
}

// TestSearchCategoriesJQTakesPrecedenceOverJSON pins the fixed --jq -> --json ->
// plaintext order for this subcommand. The precedence block is written once per
// subcommand rather than shared, so it has to be covered once per subcommand.
func TestSearchCategoriesJQTakesPrecedenceOverJSON(t *testing.T) {
	cfgPath, _ := catalogConfig(t, "[]", searchCategoriesBody)

	out, _, err := runSearch(t, "--config", cfgPath, "categories", "--json", "--jq", ".[].id")
	require.NoError(t, err)

	assert.Equal(t, "2000\n2010\n2040\n5000\n5040", strings.TrimSpace(out))
}

// TestFlattenCategoriesRecursesBeyondTwoLevels covers a tree deeper than
// Prowlarr's own default two, since the wire shape is recursive and nothing
// caps it.
func TestFlattenCategoriesRecursesBeyondTwoLevels(t *testing.T) {
	rows := flattenCategories([]prowlarr.Category{
		{ID: 1, Name: "a", SubCategories: []prowlarr.Category{
			{ID: 2, Name: "b", SubCategories: []prowlarr.Category{
				{ID: 3, Name: "c"},
			}},
			{ID: 4, Name: "d"},
		}},
	})

	require.Len(t, rows, 4)

	assert.Equal(t, []int{1, 2, 3, 4}, []int{rows[0].ID, rows[1].ID, rows[2].ID, rows[3].ID})
	assert.Equal(t, []int{0, 1, 2, 1}, []int{rows[0].depth, rows[1].depth, rows[2].depth, rows[3].depth})

	assert.Nil(t, rows[0].ParentID)
	require.NotNil(t, rows[2].ParentID)
	assert.Equal(t, 2, *rows[2].ParentID, "a grandchild names its own parent, not the root")
	require.NotNil(t, rows[3].ParentID)
	assert.Equal(t, 1, *rows[3].ParentID)
}

func TestFlattenCategoriesEmptyIsEmptyNotNil(t *testing.T) {
	assert.NotNil(t, flattenCategories(nil), "an empty tree still renders as [] under --json")
	assert.Empty(t, flattenCategories(nil))
}

// TestSearchCatalogSanitizesPlaintextOnly pins the sanitizer boundary for both
// listings: a crafted indexer or category name cannot inject terminal escapes
// into the plaintext paths, and is never altered on the structured --json path.
func TestSearchCatalogSanitizesPlaintextOnly(t *testing.T) {
	esc := string(rune(0o33))

	indexers, err := json.Marshal([]map[string]any{{
		"id":       1,
		"name":     esc + "[2Jmalicious",
		"protocol": "tor" + esc + "[31mrent",
		"privacy":  "pri" + esc + "[0mvate",
		"enable":   true,
		"priority": 25,
	}})
	require.NoError(t, err)

	categories, err := json.Marshal([]map[string]any{{
		"id":   2000,
		"name": esc + "[2JMovies",
		"subCategories": []map[string]any{{
			"id":   2040,
			"name": "Movies/" + esc + "[31mHD",
		}},
	}})
	require.NoError(t, err)

	cfgPath, _ := catalogConfig(t, string(indexers), string(categories))

	for _, sub := range []string{"indexers", "categories"} {
		plain, _, err := runSearch(t, "--config", cfgPath, sub)
		require.NoErrorf(t, err, "%s", sub)
		assert.NotContainsf(t, plain, esc, "%s plaintext is neutralized", sub)
		assert.Containsf(t, plain, "[2J", "%s keeps everything but the control byte", sub)

		structured, _, err := runSearch(t, "--config", cfgPath, sub, "--json")
		require.NoErrorf(t, err, "%s", sub)

		var got []struct {
			Name string `json:"name"`
		}
		require.NoErrorf(t, json.Unmarshal([]byte(structured), &got), "%s", sub)
		require.NotEmptyf(t, got, "%s", sub)
		assert.Containsf(t, got[0].Name, esc,
			"%s --json is a structured contract and is never sanitized", sub)
	}
}

// TestSearchCatalogNeverPrintsTheAPIKey covers every render mode of both
// listings: the key reaches the wire and nothing else.
func TestSearchCatalogNeverPrintsTheAPIKey(t *testing.T) {
	const apiKey = "super-secret-api-key"

	cfgPath, _ := catalogConfig(t, searchIndexersBody, searchCategoriesBody)
	t.Setenv(envAPIKey, apiKey)

	for _, args := range [][]string{
		{"indexers"},
		{"indexers", "--json"},
		{"indexers", "--jq", "."},
		{"categories"},
		{"categories", "--json"},
		{"categories", "--jq", "."},
	} {
		out, errOut, err := runSearch(t, append([]string{"--config", cfgPath}, args...)...)
		require.NoErrorf(t, err, "%v", args)
		assert.NotContainsf(t, out+errOut, apiKey, "%v", args)
	}
}

// TestSearchCatalogRefusesATransmissionServer proves both listings go through
// the same typed dial seam the search command does.
func TestSearchCatalogRefusesATransmissionServer(t *testing.T) {
	keyring.MockInit()

	cfgPath := writeConfigFile(t,
		`{"servers": {"local": {"url": "http://local:9091/transmission/rpc"}}, "default_server": "local"}`)

	for _, sub := range []string{"indexers", "categories"} {
		_, _, err := runSearch(t, "--config", cfgPath, sub, "--server", "local")
		require.Errorf(t, err, "%s", sub)
		assert.Contains(t, err.Error(), `server "local" is a Transmission server`)
	}
}

func TestSearchCatalogReportsAServerError(t *testing.T) {
	keyring.MockInit()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	cfgPath := writeConfigFile(t, fmt.Sprintf(
		`{"servers": {"indexers": {"url": %q, "type": "prowlarr"}}, "default_prowlarr_server": "indexers"}`,
		srv.URL))

	for sub, want := range map[string]string{
		"indexers":   "listing indexers on " + srv.URL,
		"categories": "listing categories on " + srv.URL,
	} {
		_, _, err := runSearch(t, "--config", cfgPath, sub)
		require.Errorf(t, err, "%s", sub)
		assert.Contains(t, err.Error(), want)
	}
}

// TestSearchSubcommandsShadowABareQuery is the FR-14 trade-off, seen from the
// command line: the subcommand wins, and --query is what searches for the word.
func TestSearchSubcommandsShadowABareQuery(t *testing.T) {
	cfgPath, paths := catalogConfig(t, searchIndexersBody, "[]")

	out, _, err := runSearch(t, "--config", cfgPath, "indexers")
	require.NoError(t, err)
	assert.Contains(t, out, "Example Tracker", "the bare word runs the subcommand")

	require.Len(t, *paths, 1)
	assert.Equal(t, "/api/v1/indexer", (*paths)[0])

	_, _, err = runSearch(t, "--config", cfgPath, "--query", "indexers")
	require.NoError(t, err)

	require.Len(t, *paths, 2)
	assert.Equal(t, "/api/v1/search", (*paths)[1], "--query searches for the literal word")
}
