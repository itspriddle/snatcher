package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// labelListEntry is the JSON contract of "torrent label list", and the row type
// every label subcommand renders in plaintext. Its json tags are a stable
// scripting surface — treat them as API.
type labelListEntry struct {
	ID     int64    `json:"id"`
	Hash   string   `json:"hash"`
	Name   string   `json:"name"`
	Labels []string `json:"labels"`
}

// labelFields is the label commands' field registry: it maps a selectable
// --fields key to the column that renders it, for both the TTY aligned table and
// the piped tab-separated rows. Only name is truncatable, per the
// one-truncatable-column-per-registry invariant nameColumnLimit relies on.
//
// name and labels are both server-derived and untrusted, so both are sanitized
// on the human paths (and never on --json/--jq). The labels cell joins with a
// comma, exactly as list's own labels column does; that stays unambiguous
// because a label containing a comma is rejected on the way in.
var labelFields = map[string]column[labelListEntry]{
	"id": {header: "ID", rightAlign: true, value: func(e labelListEntry) string {
		return strconv.FormatInt(e.ID, 10)
	}},
	"hash": {header: "Hash", value: func(e labelListEntry) string {
		return e.Hash
	}},
	"name": {header: "Name", truncate: true, sanitize: true, value: func(e labelListEntry) string {
		return e.Name
	}},
	"labels": {header: "Labels", sanitize: true, value: func(e labelListEntry) string {
		return strings.Join(e.Labels, ",")
	}},
}

// labelDefaultFieldKeys is the label commands' default column set, in order. It
// mirrors list's two-slice split rather than files' single slice because hash is
// too wide to earn a place in the default table.
var labelDefaultFieldKeys = []string{"id", "name", "labels"}

// labelAllFieldKeys is every key --fields accepts, in registry order. It is what
// the --fields help text names, and is pinned to labelFields by
// TestLabelFieldKeysCoverRegistry so the help can never drift from the registry.
var labelAllFieldKeys = []string{"id", "hash", "name", "labels"}

// labelReadFields is the projection every label command reads with: the identity
// the rows render plus the labels themselves. Four fields, not the full record —
// an --all selection reads every torrent, so the payload is worth keeping small.
var labelReadFields = []transmission.Field{
	transmission.FieldID,
	transmission.FieldHash,
	transmission.FieldName,
	transmission.FieldLabels,
}

// NewLabelCmd builds the "label" parent command and its subcommands, which read
// and change Transmission's per-torrent labels. With no subcommand it prints help
// (consistent with queue and port).
//
// Labels are already writable through "torrent update --label", which replaces a
// torrent's whole label set; this group exists so a single label can be read,
// added, or removed across a selection without the caller performing the
// read-modify-write itself.
func NewLabelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "label",
		Short: "Read and change torrent labels",
		Long: "Read and change the labels on one or more torrents.\n\n" +
			"Labels are free-form strings Transmission stores per torrent. " +
			"Matching is exact and case-sensitive: 'Foo' and 'foo' are two " +
			"distinct labels.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newLabelListCmd())

	return cmd
}

// newLabelListCmd builds the "label list" subcommand, which reads the labels on
// the selected torrents. It is the read half of the group and the renderer the
// write verbs reuse: one row per torrent, from the shared labelFields registry.
//
// The flow mirrors the other selector-taking commands: parse the selector, then
// dial, then a single ListTorrents projecting only the four fields a row needs.
// Selector resolution runs through resolveMutation (via labelSelection) so a
// token that matched nothing is reported the same way it is everywhere else.
func newLabelListCmd() *cobra.Command {
	var (
		server     string
		asJSON     bool
		jqExpr     string
		all        bool
		fieldsFlag string
	)

	cmd := &cobra.Command{
		Use:     "list [id|hash]...",
		Aliases: []string{"ls"},
		Short:   "List the labels on torrents",
		Long: "List the labels on the selected torrents.\n\n" +
			"Select torrents by numeric id, 40-character SHA1 hash, or --all. " +
			"At least one selector is required; a bare 'snatcher torrent label " +
			"list' is an error, not a no-op.\n\n" +
			"On a terminal the output is an aligned table with a header; when " +
			"piped (or when NO_COLOR is set) it is tab-separated rows with no " +
			"header. A torrent with no labels renders an empty cell, so the piped " +
			"form stays parseable.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := transmission.ParseSelector(args, all)
			if err != nil {
				return err
			}

			columns, err := parseFields(labelFields, labelDefaultFieldKeys, fieldsFlag)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(cmd, server)
			if err != nil {
				return err
			}

			torrents, err := backend.ListTorrents(cmd.Context(), sel, labelReadFields...)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			matched, err := labelSelection(sel, torrents)
			if err != nil {
				return err
			}

			entries := labelEntries(matched)

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), jsonSlice(entries), jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), jsonSlice(entries))
			}

			writeListPlaintext(cmd, entries, columns, "No torrents.")

			return nil
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVar(&all, "all", false, "list the labels on every torrent")
	cmd.Flags().StringVar(&fieldsFlag, "fields", "", fmt.Sprintf("comma-separated columns to show, in order (%s)", strings.Join(labelAllFieldKeys, ", ")))

	return cmd
}

// labelSelection filters a fetched torrent list to the torrents sel names. The
// server already filters by the selector's ids, so this is belt-and-braces on
// that path; what it is really for is resolveMutation's selector semantics —
// the "no torrents matched" error when explicit tokens all missed, and the
// partial-miss tolerance when only some did. Delegating keeps one implementation
// of those rules across every selector-taking command.
//
// resolveMutation's mutationResult is discarded here: it carries only the
// {id, hash, name} shape, and a label row needs the labels too. The returned
// slice is in server order.
func labelSelection(sel transmission.Selector, torrents []transmission.Torrent) ([]transmission.Torrent, error) {
	// The action name only labels the discarded mutationResult; the write verbs
	// name their own when they need it in the JSON contract.
	result, err := resolveMutation("label", sel, torrents)
	if err != nil {
		return nil, err
	}

	matched := make(map[int64]bool, len(result.Torrents))
	for _, t := range result.Torrents {
		matched[t.ID] = true
	}

	selected := make([]transmission.Torrent, 0, len(result.Torrents))
	for _, t := range torrents {
		if matched[t.ID] {
			selected = append(selected, t)
		}
	}

	return selected, nil
}

// labelEntries projects torrents onto the row/JSON type both the read and the
// write verbs render. Labels is always non-nil, so a torrent with no labels
// emits "labels": [] rather than null.
func labelEntries(torrents []transmission.Torrent) []labelListEntry {
	entries := make([]labelListEntry, len(torrents))
	for i, t := range torrents {
		labels := t.Labels
		if labels == nil {
			labels = []string{}
		}

		entries[i] = labelListEntry{ID: t.ID, Hash: t.Hash, Name: t.Name, Labels: labels}
	}

	return entries
}
