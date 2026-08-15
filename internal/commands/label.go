package commands

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
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
	cmd.AddCommand(newLabelWriteCmd(labelWriteSpec{
		name:  "set",
		short: "Replace the labels on torrents",
		long: "Replace the labels on the selected torrents with the labels " +
			"given.\n\n" +
			"Select torrents by numeric id, 40-character SHA1 hash, or --all. " +
			"At least one selector is required; a bare 'snatcher torrent label " +
			"set' with no selector is an error, not a no-op.\n\n" +
			"--label may be repeated, and each occurrence is one label: the " +
			"value is never split on commas, and a label containing a comma (or " +
			"one that is empty once surrounding whitespace is trimmed) is " +
			"rejected before anything is sent. The set that gets written is " +
			"de-duplicated and sorted, so the order the labels were given in is " +
			"not preserved.\n\n" +
			"With no --label at all, 'label set' removes every label from the " +
			"selected torrents. That is how a selection is cleared — but it also " +
			"means a mistyped 'snatcher torrent label set 5' empties torrent 5's " +
			"labels rather than erroring.\n\n" +
			"A torrent whose labels already match is left out of the write " +
			"entirely; when no torrent would change, nothing is sent at all. " +
			"After the write the labels are re-read from the server and printed " +
			"in the same columns as 'snatcher torrent label list'.",
		action:     "label-set",
		labelUsage: "label to write, replacing the torrent's labels (repeatable)",
		merge: func(_, requested []string) []string {
			return requested
		},
	}))

	return cmd
}

// labelMerge computes a torrent's resulting label set from its current labels and
// the labels named on the command line. It is the only per-torrent difference
// between the write verbs — set ignores current, add unions, remove subtracts —
// and it may return an unsorted slice with duplicates, since the caller
// normalizes the result before writing it.
type labelMerge func(current, requested []string) []string

// labelWriteSpec describes one "label <verb>" write subcommand. The write verbs
// differ only in these values, so they share a single constructor rather than
// each carrying its own copy of the resolve → write → re-read → render pipeline.
//
// It is a struct rather than a positional parameter list because the values are
// otherwise several interchangeable strings and a bool, which at the call site
// reads as noise and hides which argument is which.
type labelWriteSpec struct {
	// name is the subcommand name, e.g. "set".
	name string

	// short and long are the cobra help texts.
	short string
	long  string

	// action is the mutationResult action key surfaced under --json, e.g.
	// "label-set".
	action string

	// labelUsage is the --label flag's help text, which names the verb's own
	// effect ("replacing", "to add", "to remove").
	labelUsage string

	// noLabelsErr is the error message returned when no --label was given. An
	// empty string means the verb is legal with no labels — that is what makes a
	// bare 'label set' a clear, while 'label add' and 'label remove' with no
	// --label are errors rather than no-ops.
	noLabelsErr string

	// merge computes each matched torrent's resulting label set.
	merge labelMerge
}

// newLabelWriteCmd builds one "label <verb>" subcommand. The flow is fixed:
// validate the labels (before dialing, so a bad one costs no round-trip and
// writes nothing), parse the selector, dial, read the current labels, resolve the
// selector, write, re-read, render.
//
// The selector is resolved through resolveMutation before any write, so a token
// that matched nothing errors with no part of the batch applied. Unlike the other
// mutation commands the plaintext output is the label table rather than a
// confirmation sentence: the useful answer to a label write is what the labels
// are now, per torrent. The consequence, accepted knowingly, is that unmatched
// tokens surface under --json but not in plaintext, which keeps the piped output
// a clean tab-separated contract.
func newLabelWriteCmd(spec labelWriteSpec) *cobra.Command {
	var (
		server     string
		asJSON     bool
		jqExpr     string
		all        bool
		fieldsFlag string
		rawLabels  []string
	)

	cmd := &cobra.Command{
		Use:           spec.name + " [id|hash]...",
		Short:         spec.short,
		Long:          spec.long,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			requested, err := parseLabels(rawLabels)
			if err != nil {
				return err
			}

			if spec.noLabelsErr != "" && len(requested) == 0 {
				return errors.New(spec.noLabelsErr)
			}

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

			result, err := resolveMutation(spec.action, sel, torrents)
			if err != nil {
				return err
			}

			matched := selectLabelTorrents(result, torrents)

			if err := applyLabelChanges(cmd.Context(), backend, matched, requested, spec.merge); err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			// Re-read so the reported labels are the server's view of the write
			// rather than an optimistic local echo, the same way files update
			// re-fetches before rendering.
			updated, err := backend.ListTorrents(cmd.Context(), sel, labelReadFields...)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			entries := labelEntries(selectLabelTorrents(result, updated))
			attachLabels(&result, entries)

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), result, jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), result)
			}

			writeListPlaintext(cmd, entries, columns, "No torrents.")

			return nil
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVar(&all, "all", false, "apply to every torrent")
	cmd.Flags().StringArrayVar(&rawLabels, "label", nil, spec.labelUsage)
	cmd.Flags().StringVar(&fieldsFlag, "fields", "", fmt.Sprintf("comma-separated columns to show, in order (%s)", strings.Join(labelAllFieldKeys, ", ")))

	return cmd
}

// parseLabels validates and trims the --label values. It runs before the command
// dials, so a label the server would reject costs no round-trip and writes
// nothing.
//
// Transmission strips surrounding whitespace from a label and rejects one that is
// empty or contains a comma, so trimming and rejecting here keeps snatcher's idea
// of a label identical to the server's. The comma is load-bearing for snatcher
// too: the plaintext renderer joins a torrent's labels with one.
//
// The result is in argument order and always non-nil; de-duplicating and sorting
// is normalizeLabels' job, on the merged set.
func parseLabels(raw []string) ([]string, error) {
	labels := make([]string, 0, len(raw))

	for _, value := range raw {
		label := strings.TrimSpace(value)

		switch {
		case label == "":
			return nil, fmt.Errorf("invalid label %q: must not be empty", value)
		case strings.Contains(label, ","):
			return nil, fmt.Errorf("invalid label %q: must not contain a comma", value)
		}

		labels = append(labels, label)
	}

	return labels, nil
}

// normalizeLabels de-duplicates (exact, case-sensitive match) and sorts a label
// set byte-wise, returning a fresh non-nil slice — so "labels": [] renders rather
// than null, and the input slice is never mutated.
//
// Every verb writes a normalized set, so a torrent is left in the same canonical
// state whichever verb touched it, the grouped write's bucket key is stable, and
// comparing a resulting set against the current one is a plain slices.Equal. The
// cost is that the order the labels were supplied in is not preserved.
func normalizeLabels(labels []string) []string {
	sorted := make([]string, len(labels))
	copy(sorted, labels)
	slices.Sort(sorted)

	return slices.Compact(sorted)
}

// applyLabelChanges computes each torrent's resulting label set, drops the
// torrents that would not change, groups the rest by resulting set, and issues
// one SetTorrents call per distinct set.
//
// Grouping is what makes a per-torrent merge affordable: SetTorrents writes one
// changes map to one selector, so a selection landing on N distinct label sets
// costs N writes — exactly one in the common case (and always for set), and zero
// when nothing changes. Buckets are keyed on the NUL-joined set rather than the
// comma-joined one so the key cannot collide even if a server ever hands back a
// label containing the display separator, and they are written in key order so
// the request sequence is deterministic.
func applyLabelChanges(ctx context.Context, backend *transmission.Client, torrents []transmission.Torrent, requested []string, merge labelMerge) error {
	ids := make(map[string][]int64)
	sets := make(map[string][]string)

	for _, t := range torrents {
		next := normalizeLabels(merge(t.Labels, requested))
		if slices.Equal(next, normalizeLabels(t.Labels)) {
			continue
		}

		key := strings.Join(next, "\x00")
		ids[key] = append(ids[key], t.ID)
		sets[key] = next
	}

	for _, key := range slices.Sorted(maps.Keys(ids)) {
		sel := transmission.Selector{IDs: ids[key]}
		if err := backend.SetTorrents(ctx, sel, map[string]any{"labels": sets[key]}); err != nil {
			return err
		}
	}

	return nil
}

// attachLabels copies each affected torrent's post-write labels onto the JSON
// contract, matching by id so the re-read's ordering is irrelevant. Every
// affected torrent gets a pointer to a non-nil slice, so an empty label set
// renders as "labels": [] and never as null. A torrent that disappeared between
// the write and the re-read reports an empty set for the same reason.
func attachLabels(result *mutationResult, entries []labelListEntry) {
	byID := make(map[int64][]string, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry.Labels
	}

	for i := range result.Torrents {
		labels, ok := byID[result.Torrents[i].ID]
		if !ok {
			labels = []string{}
		}

		result.Torrents[i].Labels = &labels
	}
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

	return selectLabelTorrents(result, torrents), nil
}

// selectLabelTorrents narrows a fetched torrent list to the torrents a resolved
// mutation affects, preserving the list's (server) order. The write verbs call it
// twice with the same result: once on the pre-write read, to compute the merge,
// and once on the re-read, to render the server's post-write view.
func selectLabelTorrents(result mutationResult, torrents []transmission.Torrent) []transmission.Torrent {
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

	return selected
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
