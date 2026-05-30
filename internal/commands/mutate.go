package commands

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/torrent"
)

// mutationResult is the stable structured contract emitted by the mutation
// commands (start, stop) under --json/--jq. Its json tags are a scripting
// surface, chosen deliberately here so start (which adds a Now field) and users
// depend on them. The torrent names it carries are server-derived and untrusted;
// they are emitted verbatim in the JSON contract (consumers handle them) but must
// be passed through output.SanitizeTerminal on the human path — see
// writeMutationPlaintext.
type mutationResult struct {
	// Action names the operation performed, e.g. "stop" or "start".
	Action string `json:"action"`

	// Count is the number of torrents the action affected (the length of
	// Torrents, or the total torrent count for an --all selection).
	Count int `json:"count"`

	// Torrents are the affected torrents, resolved by name after the action.
	Torrents []mutationTorrent `json:"torrents"`

	// Now is set on the start command to report whether the queue was bypassed
	// (torrent-start-now). It is omitted for stop.
	Now *bool `json:"now,omitempty"`

	// DeleteData is set on the remove command to report whether the torrents'
	// downloaded local files were deleted (delete-local-data). It is omitted for
	// start and stop.
	DeleteData *bool `json:"delete_data,omitempty"`

	// Unmatched lists the requested selector tokens that matched no torrent. It is
	// omitted when every requested token matched (or for an --all selection).
	Unmatched []string `json:"unmatched,omitempty"`

	// all records whether the selection targeted every torrent, so the human
	// output can report a count rather than naming them all. It is not part of the
	// JSON contract.
	all bool
}

// mutationTorrent is a single affected torrent in a mutationResult.
type mutationTorrent struct {
	ID   int64  `json:"id"`
	Hash string `json:"hash"`
	Name string `json:"name"`
}

// resolveMutation fetches the full torrent list and matches it against sel to
// resolve the names of the affected torrents. An All selector matches every
// torrent (reported as a count). Otherwise each torrent matching a requested id
// or hash is included, and any requested token that matched nothing is recorded
// in Unmatched. When explicit selectors were given and none matched, it returns
// an error naming the unmatched tokens.
func resolveMutation(action string, sel torrent.Selector, torrents []torrent.Torrent) (mutationResult, error) {
	result := mutationResult{Action: action, all: sel.All}

	if sel.All {
		result.Count = len(torrents)
		result.Torrents = make([]mutationTorrent, 0, len(torrents))
		for _, t := range torrents {
			result.Torrents = append(result.Torrents, mutationTorrent{ID: t.ID, Hash: t.Hash, Name: t.Name})
		}

		return result, nil
	}

	wantID := make(map[int64]bool, len(sel.IDs))
	for _, id := range sel.IDs {
		wantID[id] = true
	}

	wantHash := make(map[string]bool, len(sel.Hashes))
	for _, hash := range sel.Hashes {
		wantHash[hash] = true
	}

	matchedID := make(map[int64]bool, len(sel.IDs))
	matchedHash := make(map[string]bool, len(sel.Hashes))

	result.Torrents = make([]mutationTorrent, 0, len(sel.IDs)+len(sel.Hashes))
	for _, t := range torrents {
		if wantID[t.ID] {
			matchedID[t.ID] = true
			result.Torrents = append(result.Torrents, mutationTorrent{ID: t.ID, Hash: t.Hash, Name: t.Name})
			continue
		}
		if wantHash[t.Hash] {
			matchedHash[t.Hash] = true
			result.Torrents = append(result.Torrents, mutationTorrent{ID: t.ID, Hash: t.Hash, Name: t.Name})
		}
	}

	for _, id := range sel.IDs {
		if !matchedID[id] {
			result.Unmatched = append(result.Unmatched, strconv.FormatInt(id, 10))
		}
	}
	for _, hash := range sel.Hashes {
		if !matchedHash[hash] {
			result.Unmatched = append(result.Unmatched, hash)
		}
	}

	result.Count = len(result.Torrents)

	if result.Count == 0 {
		return mutationResult{}, fmt.Errorf("no torrents matched: %s", joinTokens(result.Unmatched))
	}

	return result, nil
}

// renderMutation writes the result of a mutation in the appropriate output mode:
// --jq filters the JSON, --json emits the structured contract, and otherwise a
// plain human confirmation is printed. Render precedence mirrors list:
// --jq → --json → plaintext.
func renderMutation(cmd *cobra.Command, result mutationResult, jqExpr string, asJSON bool) error {
	if jqExpr != "" {
		return output.JQ(cmd.OutOrStdout(), result, jqExpr)
	}

	if asJSON {
		return output.JSON(cmd.OutOrStdout(), result)
	}

	writeMutationPlaintext(cmd, result)

	return nil
}

// writeMutationPlaintext prints a single-line confirmation naming the affected
// torrents (a count for an --all selection). Every server-derived name is passed
// through output.SanitizeTerminal first, since names are untrusted swarm-supplied
// strings. Any unmatched tokens are surfaced on a trailing line.
func writeMutationPlaintext(cmd *cobra.Command, result mutationResult) {
	out := cmd.OutOrStdout()
	verb := mutationVerb(result.Action)

	if result.all {
		_, _ = fmt.Fprintf(out, "%s all %s.\n", verb, pluralizeTorrents(result.Count))

		return
	}

	names := make([]string, len(result.Torrents))
	for i, t := range result.Torrents {
		names[i] = output.SanitizeTerminal(t.Name)
	}

	_, _ = fmt.Fprintf(out, "%s %s: %s\n", verb, pluralizeTorrents(result.Count), joinTokens(names))

	if len(result.Unmatched) > 0 {
		_, _ = fmt.Fprintf(out, "No torrents matched: %s\n", joinTokens(result.Unmatched))
	}
}

// mutationVerb maps an action name to its past-tense confirmation verb.
func mutationVerb(action string) string {
	switch action {
	case "start":
		return "Started"
	case "stop":
		return "Stopped"
	case "remove":
		return "Removed"
	case "verify":
		return "Queued verification of"
	default:
		return action
	}
}

// pluralizeTorrents renders a torrent count with the correctly pluralized noun,
// e.g. "1 torrent" or "2 torrents".
func pluralizeTorrents(n int) string {
	if n == 1 {
		return "1 torrent"
	}

	return fmt.Sprintf("%d torrents", n)
}

// joinTokens joins tokens with ", " for inclusion in a confirmation or error
// message.
func joinTokens(tokens []string) string {
	out := ""
	for i, tok := range tokens {
		if i > 0 {
			out += ", "
		}
		out += tok
	}

	return out
}
