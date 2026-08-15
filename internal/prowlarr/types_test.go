package prowlarr

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReleaseJSONContract locks the stable scripting surface of a search result:
// the json key every script reads a release by.
//
// The decode tests assert Go fields projected from the camelCase wire struct, so
// none of them would notice an *output* tag being renamed — a rename would only
// surface as a user's --jq filter quietly matching nothing. This pins the names
// themselves. The 1-based "index" a search prints is a command-layer addition and
// belongs to the search tests, not here.
func TestReleaseJSONContract(t *testing.T) {
	files, grabs, seeders, leechers := 1, 42, 120, 7

	release := Release{
		Raw:         json.RawMessage(`{"guid":"https://tracker.example/1","sortTitle":"ubuntu 24.04 lts"}`),
		GUID:        "https://tracker.example/1",
		Title:       "Ubuntu 24.04 LTS",
		Indexer:     "Example Tracker",
		IndexerID:   3,
		Age:         12,
		PublishDate: "2026-08-01T00:00:00Z",
		Size:        2147483648,
		Files:       &files,
		Grabs:       &grabs,
		Seeders:     &seeders,
		Leechers:    &leechers,
		Protocol:    "torrent",
		Categories: []Category{{
			ID:            2000,
			Name:          "Movies",
			Description:   "Movie releases",
			SubCategories: []Category{{ID: 2040, Name: "Movies/HD"}},
		}},
		InfoHash:    "aaa111",
		DownloadURL: "http://prowlarr.example/3/download?apikey=k&link=abc",
		MagnetURL:   "magnet:?xt=urn:btih:aaa111",
		InfoURL:     "https://tracker.example/details/1",
		CommentURL:  "https://tracker.example/details/1#comments",
	}

	raw, err := json.Marshal(release)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	// Every documented field is present under its expected key.
	for _, key := range []string{
		"guid", "title", "indexer", "indexer_id", "age", "publish_date", "size",
		"files", "grabs", "seeders", "leechers", "protocol", "categories",
		"info_hash", "download_url", "magnet_url", "info_url", "comment_url",
	} {
		assert.Contains(t, got, key, "missing json key %q", key)
	}

	assert.Equal(t, "Ubuntu 24.04 LTS", got["title"])
	assert.InDelta(t, 3.0, got["indexer_id"], 0)
	assert.InDelta(t, 120.0, got["seeders"], 0, "a reported count is a number, not a string")

	// The retained ReleaseResource bytes stay out of the contract: they would
	// carry every field a second time, under Prowlarr's own camelCase names.
	assert.NotContains(t, got, "raw")
	assert.NotContains(t, got, "Raw")
	assert.NotContains(t, string(raw), "sortTitle")
}

// TestReleaseJSONContractDistinguishesAnUnreportedCount pins the other half of
// the count contract: an indexer that publishes no seeder count emits null, not
// a zero a script would read as "nobody is seeding".
func TestReleaseJSONContractDistinguishesAnUnreportedCount(t *testing.T) {
	zero := 0

	raw, err := json.Marshal(Release{Grabs: &zero})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	require.Contains(t, got, "seeders")
	assert.Nil(t, got["seeders"], "an unreported count is null")
	assert.InDelta(t, 0.0, got["grabs"], 0, "a published zero is a zero")
}

// TestCategoryJSONContract pins the nested shape a category renders as, inside a
// release and on its own: the recursive sub_categories key and the description
// that "search categories" reads.
func TestCategoryJSONContract(t *testing.T) {
	raw, err := json.Marshal(Category{
		ID:          2000,
		Name:        "Movies",
		Description: "Movie releases",
		SubCategories: []Category{
			{ID: 2040, Name: "Movies/HD"},
		},
	})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	for _, key := range []string{"id", "name", "description", "sub_categories"} {
		assert.Contains(t, got, key, "missing Category json key %q", key)
	}

	subs, ok := got["sub_categories"].([]any)
	require.True(t, ok)
	require.Len(t, subs, 1)

	sub, ok := subs[0].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 2040.0, sub["id"], 0)
	assert.Equal(t, "Movies/HD", sub["name"])
	assert.Contains(t, sub, "sub_categories", "the nesting is recursive, not capped at Prowlarr's own two levels")
}
