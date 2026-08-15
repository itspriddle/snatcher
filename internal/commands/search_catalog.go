package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/prowlarr"
)

// The two catalog listings under "search" answer the same question from two
// sides: what may be passed to --indexer and to --category. Both are read-only
// views of the Prowlarr instance's own configuration, and both are the data
// source the TUI's two Prowlarr dropdowns will read.
//
// Like "search" itself, neither offers --fields: a lookup table has one useful
// shape, and the ids these print are the point of the command. Every rendered
// string other than the ids is server-derived and so sanitized on the plaintext
// paths, never on --json/--jq.

// searchIndexerColumns is the fixed column set "search indexers" renders, in
// order: ID NAME PROTOCOL PRIVACY ENABLED PRIORITY. NAME is the single
// truncatable column, which nameColumnLimit requires.
var searchIndexerColumns = []column[prowlarr.Indexer]{
	{header: "ID", rightAlign: true, value: func(i prowlarr.Indexer) string {
		return strconv.Itoa(i.ID)
	}},
	{header: "NAME", truncate: true, sanitize: true, value: func(i prowlarr.Indexer) string {
		return i.Name
	}},
	{header: "PROTOCOL", sanitize: true, value: func(i prowlarr.Indexer) string {
		return i.Protocol
	}},
	{header: "PRIVACY", sanitize: true, value: func(i prowlarr.Indexer) string {
		return i.Privacy
	}},
	{header: "ENABLED", value: func(i prowlarr.Indexer) string {
		return yesNo(i.Enable)
	}},
	{header: "PRIORITY", rightAlign: true, value: func(i prowlarr.Indexer) string {
		return strconv.Itoa(i.Priority)
	}},
}

// newSearchIndexersCmd builds "search indexers", which lists the indexers a
// configured Prowlarr instance is set up with, in the server's own order.
//
// It is how the numeric ids "search --indexer" takes are discovered: Prowlarr's
// web UI shows indexers by name, and nothing else in snatcher maps a name to an
// id. ENABLED and PRIORITY are here because they explain a search's results — a
// disabled indexer contributes nothing to a search that does not name it, and
// priority is the order Prowlarr resolves ties in.
//
// Prowlarr's IndexerResource also carries each indexer's settings fields, which
// on a private tracker hold its passkey or API key. Those are deliberately not
// decoded (see prowlarr.Indexer), so no render mode can print them.
func newSearchIndexersCmd() *cobra.Command {
	var (
		server string
		asJSON bool
		jqExpr string
	)

	cmd := &cobra.Command{
		Use:   "indexers",
		Short: "List the indexers Prowlarr is configured with",
		Long: "List the indexers a configured Prowlarr instance is set up with.\n\n" +
			"The ID column is the number \"snatcher search --indexer\" takes. " +
			"Indexers are listed in the server's own order; snatcher does not " +
			"re-sort them.\n\n" +
			"--server names a configured Prowlarr instance, not a Transmission " +
			"server; with it omitted the default Prowlarr server is used.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, serverURL, err := dialProwlarr(cmd, server)
			if err != nil {
				return err
			}

			indexers, err := client.Indexers(cmd.Context())
			if err != nil {
				return fmt.Errorf("listing indexers on %s: %w", serverURL, err)
			}

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), jsonSlice(indexers), jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), jsonSlice(indexers))
			}

			writeListPlaintext(cmd, indexers, searchIndexerColumns, "No indexers configured.")

			return nil
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// searchCategory is one torznab category in the flattened rendering "search
// categories" produces. Prowlarr returns a tree; the command renders a list, so
// each row names its parent explicitly rather than nesting.
//
// ParentID is a pointer so a top-level category is null rather than 0 — 0 is not
// a torznab category id, but a script should not have to know that to tell a
// root from a child. The tree itself is not emitted: prowlarr.Category's
// sub_categories would carry every child twice, once nested and once as its own
// row.
type searchCategory struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ParentID    *int   `json:"parent_id"`

	// depth is how far the category sits below a root, used only to indent the
	// TTY table. It is unexported so it stays out of the JSON contract, where
	// parent_id already expresses the same thing exactly.
	depth int
}

// categoryIndent is one level of nesting in the TTY table.
const categoryIndent = "  "

// indentedName renders the category's name preceded by one indent per level of
// nesting, so the tree Prowlarr returned is legible in a flat table.
func (c searchCategory) indentedName() string {
	return strings.Repeat(categoryIndent, c.depth) + c.Name
}

// searchCategoryTableColumns is the TTY rendering of "search categories": ID and
// an indented NAME, so a parent and its children read as a tree.
var searchCategoryTableColumns = []column[searchCategory]{
	searchCategoryIDColumn,
	{header: "NAME", truncate: true, sanitize: true, value: func(c searchCategory) string {
		return c.indentedName()
	}},
}

// searchCategoryPipedColumns is the piped rendering: the same two columns with
// the name exactly as the server sent it.
//
// The indent is a TTY affordance and stays there. Piped output is snatcher's
// full-value scripting contract, and leading spaces are decoration a consumer
// would have to strip. Nothing is lost: torznab category names already spell
// their own parent ("Movies/HD" under "Movies"), and --json carries parent_id.
var searchCategoryPipedColumns = []column[searchCategory]{
	searchCategoryIDColumn,
	{header: "NAME", sanitize: true, value: func(c searchCategory) string {
		return c.Name
	}},
}

// searchCategoryIDColumn is the ID column both category renderings share — the
// number "snatcher search --category" takes.
var searchCategoryIDColumn = column[searchCategory]{
	header: "ID", rightAlign: true, value: func(c searchCategory) string {
		return strconv.Itoa(c.ID)
	},
}

// newSearchCategoriesCmd builds "search categories", which lists the torznab
// categories a configured Prowlarr instance knows about.
//
// It is how the numeric ids "search --category" takes are discovered. Prowlarr
// returns the categories as a tree; the listing flattens it depth-first in the
// server's order, indenting children under their parent on a terminal and
// naming the parent with a parent_id field in --json.
//
// The full default tree is shown, not just the categories the configured
// indexers happen to support: Prowlarr does not report the latter as a set, and
// deriving it would mean interrogating every indexer.
func newSearchCategoriesCmd() *cobra.Command {
	var (
		server string
		asJSON bool
		jqExpr string
	)

	cmd := &cobra.Command{
		Use:   "categories",
		Short: "List the torznab categories Prowlarr knows about",
		Long: "List the torznab categories a configured Prowlarr instance knows about.\n\n" +
			"The ID column is the number \"snatcher search --category\" takes. " +
			"Prowlarr returns the categories as a tree; this flattens it in the " +
			"server's own order, indenting sub-categories under their parent on a " +
			"terminal and naming the parent with a \"parent_id\" field in --json " +
			"output.\n\n" +
			"--server names a configured Prowlarr instance, not a Transmission " +
			"server; with it omitted the default Prowlarr server is used.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, serverURL, err := dialProwlarr(cmd, server)
			if err != nil {
				return err
			}

			tree, err := client.Categories(cmd.Context())
			if err != nil {
				return fmt.Errorf("listing categories on %s: %w", serverURL, err)
			}

			rows := flattenCategories(tree)

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), jsonSlice(rows), jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), jsonSlice(rows))
			}

			writeSearchCategoryPlaintext(cmd, rows)

			return nil
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// flattenCategories walks the category tree depth-first, emitting each category
// immediately before its own sub-categories, so a parent is always the row above
// its first child and the server's order is preserved at every level.
func flattenCategories(tree []prowlarr.Category) []searchCategory {
	rows := make([]searchCategory, 0, len(tree))

	var walk func(categories []prowlarr.Category, parent *int, depth int)
	walk = func(categories []prowlarr.Category, parent *int, depth int) {
		for _, category := range categories {
			rows = append(rows, searchCategory{
				ID:          category.ID,
				Name:        category.Name,
				Description: category.Description,
				ParentID:    parent,
				depth:       depth,
			})

			id := category.ID
			walk(category.SubCategories, &id, depth+1)
		}
	}

	walk(tree, nil, 0)

	return rows
}

// writeSearchCategoryPlaintext renders the flattened categories in the
// appropriate plaintext mode. It stands in for writeListPlaintext because the
// two modes render the NAME column differently — indented on a TTY, verbatim
// when piped — which one column set cannot express.
func writeSearchCategoryPlaintext(cmd *cobra.Command, rows []searchCategory) {
	out := cmd.OutOrStdout()

	if isTTY(out) {
		writeListTable(out, rows, searchCategoryTableColumns, terminalWidth(out), "No categories.")
		return
	}

	writeListPiped(out, rows, searchCategoryPipedColumns)
}
