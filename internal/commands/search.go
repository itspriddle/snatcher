package commands

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/prowlarr"
)

// defaultSearchLimit is how many results one search request asks for, and is
// always sent — even when --limit is not passed. Paging steps the offset by the
// limit, so a limit snatcher did not choose would make --pages walk in strides
// of whatever page size the server happened to default to.
const defaultSearchLimit = 100

// defaultSearchType is the generic keyword search, the one torznab type every
// indexer implements.
const defaultSearchType = "search"

// searchTypes are the torznab search types --search-type accepts, in the order
// help and error messages list them. An indexer may implement only some of the
// four typed searches; asking one for a type it does not support is Prowlarr's
// answer to give, not snatcher's to predict, so the set is a spelling check
// rather than a capability check.
var searchTypes = []string{defaultSearchType, "tvsearch", "movie", "music", "book"}

// searchResult is one rendered search result: the release the server returned,
// numbered by its 1-based position in the final rendered set.
//
// Index is declared before the embedded release so it leads the JSON object and
// the release's own fields are promoted flat beside it. That number is the same
// one the table's "#" column shows, so a human reading the table and a script
// reading --json name a release the same way.
type searchResult struct {
	Index int `json:"index"`

	prowlarr.Release
}

// searchColumns is the fixed column set the search table renders, in order:
// # AGE TITLE INDEXER SIZE GRABS SEEDERS LEECHERS. They are the columns the
// vision's Prowlarr window lists, so the CLI table and the forthcoming TUI table
// stay recognizably the same view.
//
// Unlike list/show/files/peers there is no --fields flag and so no keyed
// registry: a search result renders one way. Title is the single truncatable
// column, which nameColumnLimit requires, and the two server-derived strings
// (title, indexer name) are the sanitized ones.
var searchColumns = []column[searchResult]{
	{header: "#", rightAlign: true, value: func(r searchResult) string {
		return strconv.Itoa(r.Index)
	}},
	{header: "AGE", rightAlign: true, value: func(r searchResult) string {
		return searchAge(r.Age)
	}},
	{header: "TITLE", truncate: true, sanitize: true, value: func(r searchResult) string {
		return r.Title
	}},
	{header: "INDEXER", sanitize: true, value: func(r searchResult) string {
		return r.Indexer
	}},
	{header: "SIZE", rightAlign: true, value: func(r searchResult) string {
		return output.Bytes(r.Size)
	}},
	{header: "GRABS", rightAlign: true, value: func(r searchResult) string {
		return searchCount(r.Grabs)
	}},
	{header: "SEEDERS", rightAlign: true, value: func(r searchResult) string {
		return searchCount(r.Seeders)
	}},
	{header: "LEECHERS", rightAlign: true, value: func(r searchResult) string {
		return searchCount(r.Leechers)
	}},
}

// NewSearchCmd builds the "search" command, which searches the indexers a
// configured Prowlarr instance knows about and renders the releases as an
// aligned table on a TTY, as tab-separated rows when piped, as JSON (--json), or
// filtered through jq (--jq).
//
// Results are rendered in the server's own order — Prowlarr's own ranking across
// the indexers it queried. snatcher does not sort, filter, or de-duplicate them,
// so the "#" a user reads off the table is a position in a list Prowlarr chose.
//
// Unlike the torrent and server groups, this one both runs an action of its own
// and carries subcommands, so a subcommand name shadows a bare positional query.
// That trade-off is deliberate — it keeps the common case, "snatcher search
// ubuntu", the shortest thing to type — and --query is the complete escape. The
// Long text says so at the point of confusion rather than only in the man page,
// and enumerates the subcommands in the same parenthesized form the torrent
// group uses (pinned by TestSearchGroupLongNamesEverySubcommand).
func NewSearchCmd() *cobra.Command {
	var (
		server      string
		asJSON      bool
		jqExpr      string
		queryFlag   string
		indexerIDs  []int
		categoryIDs []int
		limit       int
		offset      int
		pages       int
		searchType  string
	)

	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search indexers with Prowlarr",
		Long: "Search the indexers a configured Prowlarr instance knows about.\n\n" +
			"The subcommands (grab, download, indexers, categories) act on a result and list " +
			"the ids --indexer and --category take. A subcommand name wins over a " +
			"bare query, so \"snatcher search indexers\" runs the subcommand; search " +
			"for that literal word with \"snatcher search --query indexers\".\n\n" +
			"The query is a positional argument, or --query for the same thing. " +
			"Restrict the search with the repeatable --indexer and --category id " +
			"flags, and walk further into the results with --pages.\n\n" +
			"Results keep Prowlarr's own order across the indexers it queried; " +
			"snatcher does not re-rank them. The leading # column numbers that " +
			"order from 1 and appears as the \"index\" field in --json output.\n\n" +
			"--server names a configured Prowlarr instance, not a Transmission " +
			"server; with it omitted the default Prowlarr server is used.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Everything the caller typed is settled before dialing, so a typo
			// costs no round trip.
			query, err := resolveSearchQuery(args, queryFlag)
			if err != nil {
				return err
			}

			resolvedType, err := parseSearchType(searchType)
			if err != nil {
				return err
			}

			if err := validateSearchPaging(limit, offset, pages); err != nil {
				return err
			}

			client, serverURL, err := dialProwlarr(cmd, server)
			if err != nil {
				return err
			}

			releases, err := searchPages(cmd.Context(), client, prowlarr.SearchParams{
				Query:      query,
				Type:       resolvedType,
				IndexerIDs: indexerIDs,
				Categories: categoryIDs,
				Limit:      limit,
				Offset:     offset,
			}, pages)
			if err != nil {
				return fmt.Errorf("searching %s: %w", serverURL, err)
			}

			results := numberSearchResults(releases)

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), jsonSlice(redactSearchResults(results)), jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), jsonSlice(redactSearchResults(results)))
			}

			writeListPlaintext(cmd, results, searchColumns, "No results.")

			return nil
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().StringVar(&queryFlag, "query", "", "search query, the flag form of the positional argument")
	cmd.Flags().IntSliceVar(&indexerIDs, "indexer", nil, "restrict the search to this indexer id (repeatable)")
	cmd.Flags().IntSliceVar(&categoryIDs, "category", nil, "restrict the search to this torznab category id (repeatable)")
	cmd.Flags().IntVar(&limit, "limit", defaultSearchLimit, "maximum results to request per page")
	cmd.Flags().IntVar(&offset, "offset", 0, "results to skip before the first page")
	cmd.Flags().IntVar(&pages, "pages", 1, "sequential pages to request, each stepping the offset by --limit")
	cmd.Flags().StringVar(&searchType, "search-type", defaultSearchType, fmt.Sprintf("torznab search type (%s)", strings.Join(searchTypes, ", ")))

	cmd.AddCommand(newSearchGrabCmd())
	cmd.AddCommand(newSearchDownloadCmd())
	cmd.AddCommand(newSearchIndexersCmd())
	cmd.AddCommand(newSearchCategoriesCmd())

	return cmd
}

// resolveSearchQuery settles what to search for, from the positional argument or
// the --query flag. Exactly one of the two must carry a non-blank value: the
// flag exists as the escape for a query that would otherwise read as a
// subcommand name, so accepting both would leave which one won unstated, and
// accepting neither would search for nothing at all.
func resolveSearchQuery(args []string, queryFlag string) (string, error) {
	positional := strings.TrimSpace(firstArg(args))
	flagged := strings.TrimSpace(queryFlag)

	switch {
	case positional != "" && flagged != "":
		return "", errors.New("query given twice: pass it as an argument or with --query, not both")
	case positional != "":
		return positional, nil
	case flagged != "":
		return flagged, nil
	default:
		return "", errors.New(`no query given: pass it as an argument (snatcher search "ubuntu") or with --query`)
	}
}

// parseSearchType normalizes a --search-type value, accepting it in any case and
// with surrounding whitespace, the same reading --type and --store get. An empty
// value is the flag's own default.
func parseSearchType(raw string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return defaultSearchType, nil
	}

	for _, name := range searchTypes {
		if normalized == name {
			return normalized, nil
		}
	}

	return "", fmt.Errorf("invalid --search-type %q: valid values are %s", raw, validSearchTypes())
}

// validSearchTypes returns the accepted --search-type values, quoted, for the
// error message. It is derived from searchTypes so the two cannot drift.
func validSearchTypes() string {
	quoted := make([]string, len(searchTypes))
	for i, name := range searchTypes {
		quoted[i] = strconv.Quote(name)
	}

	return strings.Join(quoted, ", ")
}

// validateSearchPaging rejects the paging values that cannot describe a request.
// None of the three can be wrong by default — 100, 0, and 1 are all valid — so a
// value out of range is always one the caller typed.
func validateSearchPaging(limit, offset, pages int) error {
	if limit < 1 {
		return fmt.Errorf("invalid --limit %d: must be at least 1", limit)
	}

	if offset < 0 {
		return fmt.Errorf("invalid --offset %d: must not be negative", offset)
	}

	if pages < 1 {
		return fmt.Errorf("invalid --pages %d: must be at least 1", pages)
	}

	return nil
}

// searchPreallocLimit bounds the accumulator's initial capacity in searchPages.
//
// --limit is a user-supplied count of results to *ask for*, not a promise of
// what arrives, so sizing the slice straight from it hands a typo to the
// allocator: --limit 100000000 reserves tens of gigabytes of address space that
// no result set will ever fill, and a limit near the integer maximum overflows
// makeslice's size computation and panics before the first request is even
// issued. Past this bound append grows the slice from data actually received,
// as every other list path does.
const searchPreallocLimit = 1024

// searchPages issues the sequential requests --pages asks for, stepping the
// offset by the limit each time and concatenating the results in the order they
// arrive.
//
// A page returning fewer results than it asked for is the last one: the indexers
// had nothing more to give, so the remaining pages would only cost round trips.
// That also keeps an indexer that ignores limit/offset entirely — and so answers
// every page identically — from being asked N times.
func searchPages(ctx context.Context, client *prowlarr.Client, params prowlarr.SearchParams, pages int) ([]prowlarr.Release, error) {
	all := make([]prowlarr.Release, 0, min(params.Limit, searchPreallocLimit))

	for page := range pages {
		request := params
		request.Offset = params.Offset + page*params.Limit

		releases, err := client.Search(ctx, request)
		if err != nil {
			return nil, err
		}

		all = append(all, releases...)

		if len(releases) < params.Limit {
			break
		}
	}

	return all, nil
}

// numberSearchResults pairs each release with its 1-based position in the final
// rendered set, after every page has been concatenated — so the number is a
// position in what the user is actually shown, not in the page it arrived on.
func numberSearchResults(releases []prowlarr.Release) []searchResult {
	results := make([]searchResult, len(releases))
	for i, release := range releases {
		results[i] = searchResult{Index: i + 1, Release: release}
	}

	return results
}

// redactSearchResults returns the results with the API key stripped from every
// release's URL fields, for the structured output modes only.
//
// The plaintext table renders no URL at all, so this is the whole exposure:
// Prowlarr's downloadUrl is a proxy link that carries the key as a query
// parameter (a torrent client fetching it cannot send a header), and an indexer
// may build its info or comment URL the same way. --json and --jq are a
// scripting contract, which is exactly what makes emitting the key there
// dangerous — a filtered result lands in a log, a pipeline, or a paste.
//
// It runs on a copy at the render boundary, so the releases the rest of the
// command holds — the DownloadURL search download fetches, the Raw bytes search
// grab re-POSTs — stay byte-exact. A redacted URL is for reading, not for
// acting on.
func redactSearchResults(results []searchResult) []searchResult {
	redacted := make([]searchResult, len(results))
	for i, result := range results {
		redacted[i] = searchResult{Index: result.Index, Release: result.Redacted()}
	}

	return redacted
}

// searchAge renders a release's age in whole days, the unit Prowlarr reports it
// in, in the "Nd" shape output.Duration uses. A negative age is an indexer
// publishing a date in the future; it renders as a dash rather than as a
// negative day count.
func searchAge(days int) string {
	if days < 0 {
		return "-"
	}

	return strconv.Itoa(days) + "d"
}

// searchCount renders one of the nullable count columns. Prowlarr reports null
// for a count the indexer does not publish — a usenet release has no seeders,
// and some indexers report no grab count — which is distinct from a published
// zero and renders as a dash.
func searchCount(n *int) string {
	if n == nil {
		return "-"
	}

	return strconv.Itoa(*n)
}
