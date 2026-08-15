package commands

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/prowlarr"
)

// This file holds the machinery for naming *one* release out of a search: the
// flags, the validation, and the resolution. It is shared by every search
// subcommand that acts on a single result rather than listing many, so those
// commands differ only in what they do once the release is in hand.
//
// There are two ways to name a release, and the difference between them is the
// whole design:
//
//   - by position — a query and the 1-based # a search printed. snatcher caches
//     no result state between invocations, so the query is re-run and the index
//     resolved from the fresh result set. That is exact only while the results
//     have not shifted, which is why the resolved release is echoed before it is
//     acted on.
//   - by guid — the indexer-assigned id plus the single indexer that issued it.
//     Nothing is re-run and nothing can shift. `guid` is in --json output for
//     exactly this reason: a script reads it and comes back with the exact form.

// releaseSelector holds the flags that name one release. It owns both halves of
// naming it — validating what the caller typed and turning that into a
// prowlarr.Release — so the two forms cannot drift between commands.
type releaseSelector struct {
	// guid names a release exactly, with indexerIDs naming the one indexer that
	// issued it. Empty selects the positional form.
	guid string

	// indexerIDs restricts the re-run in the positional form, and is the single
	// indexer that issued guid in the exact form.
	indexerIDs []int

	// categoryIDs restricts the re-run to these torznab categories.
	categoryIDs []int

	// limit, offset, and pages page the re-run, exactly as they page a search.
	limit  int
	offset int
	pages  int

	// searchType is the torznab search type the re-run asks for.
	searchType string
}

// bind registers the selection flags on cmd. They are deliberately the same
// flags `search` itself takes, with the same names and defaults: an index only
// names the same release when the re-run is the same search, so a caller who
// filtered a search has to be able to repeat that filter verbatim.
func (s *releaseSelector) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&s.guid, "guid", "",
		"name the release exactly by its indexer-assigned guid instead of by position (needs exactly one --indexer)")
	cmd.Flags().IntSliceVar(&s.indexerIDs, "indexer", nil,
		"indexer id: restricts the re-run, or names the release's own indexer with --guid (repeatable)")
	cmd.Flags().IntSliceVar(&s.categoryIDs, "category", nil,
		"restrict the re-run to this torznab category id (repeatable)")
	cmd.Flags().IntVar(&s.limit, "limit", defaultSearchLimit, "maximum results to request per page of the re-run")
	cmd.Flags().IntVar(&s.offset, "offset", 0, "results to skip before the first page of the re-run")
	cmd.Flags().IntVar(&s.pages, "pages", 1, "sequential pages of the re-run to request, each stepping the offset by --limit")
	cmd.Flags().StringVar(&s.searchType, "search-type", defaultSearchType,
		fmt.Sprintf("torznab search type of the re-run (%s)", strings.Join(searchTypes, ", ")))
}

// searchTarget names one release, in exactly one of the two forms.
type searchTarget struct {
	// query is the search term to re-run. Empty in the exact form.
	query string

	// index is the 1-based position to resolve from that re-run. Zero in the
	// exact form.
	index int

	// guid is the indexer-assigned release id. Empty in the positional form.
	guid string

	// indexerID is the indexer that issued guid. Zero in the positional form.
	indexerID int
}

// exact reports whether the target names a release by guid rather than by its
// position in a re-run.
func (t searchTarget) exact() bool {
	return t.guid != ""
}

// releasePlan is a fully settled description of which release to act on:
// nothing in it depends on anything the caller typed still being parsed, and
// nothing in it has touched the network.
type releasePlan struct {
	target searchTarget
	params prowlarr.SearchParams
	pages  int
}

// plan validates everything the caller typed and settles it into a releasePlan.
// It is pure, so a command runs it from its cobra Args validator — rejecting a
// contradictory invocation before RunE opens a secret store — and again in RunE
// for the plan itself.
func (s releaseSelector) plan(args []string) (releasePlan, error) {
	target, err := s.target(args)
	if err != nil {
		return releasePlan{}, err
	}

	searchType, err := parseSearchType(s.searchType)
	if err != nil {
		return releasePlan{}, err
	}

	if err := validateSearchPaging(s.limit, s.offset, s.pages); err != nil {
		return releasePlan{}, err
	}

	return releasePlan{
		target: target,
		params: prowlarr.SearchParams{
			Query:      target.query,
			Type:       searchType,
			IndexerIDs: s.indexerIDs,
			Categories: s.categoryIDs,
			Limit:      s.limit,
			Offset:     s.offset,
		},
		pages: s.pages,
	}, nil
}

// target settles which of the two forms was asked for, and rejects the mixture
// of the two. --guid is the exact form and takes no positional arguments; the
// positional form takes both a query and a number, since a query with no number
// names a result set rather than a release.
func (s releaseSelector) target(args []string) (searchTarget, error) {
	if s.guid != "" {
		return s.exactTarget(args)
	}

	return positionalTarget(args)
}

// exactTarget builds the guid form's target, requiring the one indexer that
// issued the guid. A guid is unique only within an indexer, so zero of them
// leaves the release unnamed and several of them name several releases.
func (s releaseSelector) exactTarget(args []string) (searchTarget, error) {
	if len(args) > 0 {
		return searchTarget{}, errors.New(
			"--guid names a release exactly and takes no positional arguments: " +
				"drop the query and number, or drop --guid")
	}

	if len(s.indexerIDs) == 0 {
		return searchTarget{}, errors.New(
			`--guid needs the indexer that issued it: pass exactly one --indexer ` +
				`(the ID column of "snatcher search indexers")`)
	}

	if len(s.indexerIDs) > 1 {
		return searchTarget{}, fmt.Errorf(
			"--guid needs exactly one --indexer, got %d: a guid is unique only within one indexer",
			len(s.indexerIDs))
	}

	return searchTarget{guid: s.guid, indexerID: s.indexerIDs[0]}, nil
}

// positionalTarget builds the "query and number" form's target. The number is
// the # a search printed, so it is 1-based and anything below 1 names no row
// that was ever on screen.
func positionalTarget(args []string) (searchTarget, error) {
	if len(args) != 2 {
		return searchTarget{}, errors.New(
			`name a release by position with a query and a result number (e.g. "ubuntu" 3), ` +
				"or exactly with --guid and --indexer")
	}

	query := strings.TrimSpace(args[0])
	if query == "" {
		return searchTarget{}, errors.New("the query is empty: it is the search to re-run the result number against")
	}

	index, err := strconv.Atoi(args[1])
	if err != nil {
		return searchTarget{}, fmt.Errorf(
			"invalid result number %q: it is the # column of a search result", args[1])
	}

	if index < 1 {
		return searchTarget{}, fmt.Errorf("invalid result number %d: results are numbered from 1", index)
	}

	return searchTarget{query: query, index: index}, nil
}

// resolve produces the release to act on. The exact form builds it outright; the
// positional form re-runs the search and reads the index off the fresh results,
// which is where the number can name a release the caller never saw.
func (p releasePlan) resolve(ctx context.Context, client *prowlarr.Client, serverURL string) (prowlarr.Release, error) {
	if p.target.exact() {
		return prowlarr.ReleaseRef(p.target.guid, p.target.indexerID), nil
	}

	releases, err := searchPages(ctx, client, p.params, p.pages)
	if err != nil {
		return prowlarr.Release{}, fmt.Errorf("searching %s: %w", serverURL, err)
	}

	if p.target.index > len(releases) {
		return prowlarr.Release{}, fmt.Errorf(
			"no result %d: the search returned %s", p.target.index, pluralizeResults(len(releases)))
	}

	return releases[p.target.index-1], nil
}

// releaseLabel names a release for a human, as "title [indexer]". A release
// named only by guid has neither, so it falls back to what the caller did give:
// the guid, on the indexer id they named it with. The result is server-derived
// and must be sanitized by the caller before it reaches a terminal.
func releaseLabel(release prowlarr.Release) string {
	name := release.Title
	if name == "" {
		name = release.GUID
	}

	indexer := release.Indexer
	if indexer == "" {
		indexer = "indexer " + strconv.Itoa(release.IndexerID)
	}

	return fmt.Sprintf("%s [%s]", name, indexer)
}

// pluralizeResults renders a search result count, mirroring pluralizeTorrents.
func pluralizeResults(n int) string {
	if n == 1 {
		return "1 result"
	}

	return fmt.Sprintf("%d results", n)
}
