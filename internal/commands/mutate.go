package commands

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
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

	// Location is set on the move command to report the new download location the
	// torrent was moved to. It is omitted for the other mutation commands.
	Location string `json:"location,omitempty"`

	// Move is set on the move command to report whether the existing files were
	// relocated (move=true) or Transmission was repointed at an existing path
	// without moving (--in-place, move=false). It is omitted for the other
	// mutation commands.
	Move *bool `json:"move,omitempty"`

	// Fields is set on the update command to report the neutral kebab-case fields
	// (and their values) that were written. It is omitted for the other mutation
	// commands.
	Fields map[string]any `json:"fields,omitempty"`

	// Unmatched lists the requested selector tokens that matched no transmission. It is
	// omitted when every requested token matched (or for an --all selection).
	Unmatched []string `json:"unmatched,omitempty"`

	// all records whether the selection targeted every torrent, so the human
	// output can report a count rather than naming them all. It is not part of the
	// JSON contract.
	all bool
}

// mutationTorrent is a single affected torrent in a mutationResult, and the
// shared {id, hash, name} shape any command's JSON contract names a torrent
// with (rename embeds one too).
type mutationTorrent struct {
	ID   int64  `json:"id"`
	Hash string `json:"hash"`
	Name string `json:"name"`

	// Labels reports the torrent's labels after the write. It is set only by the
	// label commands; every other mutation command leaves it nil and the key is
	// absent. The pointer is what makes "no labels" ("labels": []) distinguish-
	// able from "this command does not report labels" (key absent) — a plain
	// []string with omitempty would omit both.
	Labels *[]string `json:"labels,omitempty"`
}

// resolveMutation fetches the full torrent list and matches it against sel to
// resolve the names of the affected torrents. An All selector matches every
// torrent (reported as a count). Otherwise each torrent matching a requested id
// or hash is included, and any requested token that matched nothing is recorded
// in Unmatched. When explicit selectors were given and none matched, it returns
// an error naming the unmatched tokens.
func resolveMutation(action string, sel transmission.Selector, torrents []transmission.Torrent) (mutationResult, error) {
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
		return mutationResult{}, fmt.Errorf("no torrents matched: %s", strings.Join(result.Unmatched, ", "))
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

	_, _ = fmt.Fprintf(out, "%s %s: %s\n", verb, pluralizeTorrents(result.Count), strings.Join(names, ", "))

	if len(result.Unmatched) > 0 {
		_, _ = fmt.Fprintf(out, "No torrents matched: %s\n", strings.Join(result.Unmatched, ", "))
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
	case "reannounce":
		return "Reannounced"
	case "update":
		return "Updated"
	case "queue-top":
		return "Moved to top"
	case "queue-up":
		return "Moved up"
	case "queue-down":
		return "Moved down"
	case "queue-bottom":
		return "Moved to bottom"
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

// confirmGate is the single home of the "prompt on an interactive terminal,
// refuse otherwise" check the gated mutation commands share. On a terminal it
// writes prompt and returns the user's answer. When stdin is not a terminal
// (piped input or scripts) it refuses with an error naming subject and returns
// false, so a gated mutation is never carried out unprompted — callers skip the
// gate entirely when --yes was given.
//
// Callers differ only in subject and prompt: remove, rename, and verify all
// route through here rather than repeating the *os.File / term.IsTerminal dance.
func confirmGate(cmd *cobra.Command, subject, prompt string) (bool, error) {
	in, ok := interactiveInput(cmd.InOrStdin())
	if !ok {
		return false, fmt.Errorf("refusing to %s without confirmation; pass --yes to proceed", subject)
	}

	return promptConfirm(cmd, in, prompt)
}

// terminalInput is an input that reports for itself whether it is attached to an
// interactive terminal. Real stdin is an *os.File and is checked with
// term.IsTerminal instead; nothing in production implements this. It exists so
// confirmGate's prompting branch can be driven by an in-memory reader in tests,
// which is otherwise reachable only by allocating a pty.
type terminalInput interface {
	io.Reader
	isTerminal() bool
}

// interactiveInput reports whether r is an interactive terminal, returning it as
// the reader to prompt on.
func interactiveInput(r io.Reader) (io.Reader, bool) {
	switch in := r.(type) {
	case terminalInput:
		return in, in.isTerminal()
	case *os.File:
		return in, term.IsTerminal(int(in.Fd()))
	default:
		return nil, false
	}
}

// promptConfirm writes msg to the command's output, reads a single line from in,
// and reports whether the answer was affirmative. Taking an io.Reader keeps it
// unit-testable with a strings.Reader, with no real TTY required.
func promptConfirm(cmd *cobra.Command, in io.Reader, msg string) (bool, error) {
	_, _ = fmt.Fprint(cmd.OutOrStdout(), msg)

	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("reading confirmation: %w", err)
	}

	return parseAffirmative(line), nil
}

// parseAffirmative reports whether a prompt answer is an affirmative "y"/"yes"
// (case-insensitive, surrounding whitespace ignored). Everything else — including
// an empty line — declines, matching the [y/N] default.
func parseAffirmative(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}
