package tui

import (
	"context"
	"errors"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// The action menu's "other" leaves: the per-torrent fields `torrent update`
// writes that have no key. Each is one torrent-set over the selection.
const (
	actionTorrentHonorLimitsOn  Action = "torrent.honor-limits.on"
	actionTorrentHonorLimitsOff Action = "torrent.honor-limits.off"
	actionTorrentLimitDownload  Action = "torrent.limit.download"
	actionTorrentLimitUpload    Action = "torrent.limit.upload"
	actionTorrentLimitPeers     Action = "torrent.limit.peers"
	actionTorrentLimitSeedRatio Action = "torrent.limit.seed-ratio"
	actionTorrentLimitSeedIdle  Action = "torrent.limit.seed-idle"
	actionTorrentSequentialOn   Action = "torrent.sequential.on"
	actionTorrentSequentialOff  Action = "torrent.sequential.off"
	actionTorrentPriorityHigh   Action = "torrent.priority.high"
	actionTorrentPriorityNormal Action = "torrent.priority.normal"
	actionTorrentPriorityLow    Action = "torrent.priority.low"
	actionTorrentGroup          Action = "torrent.group"
)

// updateLeaf is one per-torrent field change: either a fixed set of changes, or
// a prompt whose value parse turns into changes. The keys are the neutral
// kebab-case ones `torrent update` builds, so the TUI and the CLI write the same
// fields the same way.
type updateLeaf struct {
	action Action

	// ask words the change in a confirm, a prompt's title, and a failure.
	ask string

	// changes are a fixed leaf's fields.
	changes map[string]any

	// hint and parse are a prompt leaf's; parse is nil for a fixed leaf.
	hint  string
	parse func(raw string) (map[string]any, error)
}

// updateLeaves is every "other" leaf. The handlers are built from it, and the
// tests check the menu and the unbound table against it.
var updateLeaves = []updateLeaf{
	{action: actionTorrentHonorLimitsOn, ask: "Honor session limits on", changes: map[string]any{"honors-session-limits": true}},
	{action: actionTorrentHonorLimitsOff, ask: "Ignore session limits on", changes: map[string]any{"honors-session-limits": false}},
	{action: actionTorrentLimitDownload, ask: "Set download limit on", hint: "off, or KB/s", parse: func(raw string) (map[string]any, error) {
		return transmission.RateLimitChanges("download", raw)
	}},
	{action: actionTorrentLimitUpload, ask: "Set upload limit on", hint: "off, or KB/s", parse: func(raw string) (map[string]any, error) {
		return transmission.RateLimitChanges("upload", raw)
	}},
	{action: actionTorrentLimitPeers, ask: "Set peer limit on", hint: "1 or more", parse: parsePeerLimit},
	{action: actionTorrentLimitSeedRatio, ask: "Set seed ratio limit on", hint: "global, unlimited, or a ratio like 2.0",
		parse: transmission.SeedRatioChanges},
	{action: actionTorrentLimitSeedIdle, ask: "Set seed idle limit on", hint: "global, unlimited, minutes, or a duration like 1h30m",
		parse: transmission.SeedIdleChanges},
	{action: actionTorrentSequentialOn, ask: "Enable sequential download on", changes: map[string]any{"sequential-download": true}},
	{action: actionTorrentSequentialOff, ask: "Disable sequential download on", changes: map[string]any{"sequential-download": false}},
	{action: actionTorrentPriorityHigh, ask: "Set high priority on", changes: map[string]any{"bandwidth-priority": 1}},
	{action: actionTorrentPriorityNormal, ask: "Set normal priority on", changes: map[string]any{"bandwidth-priority": 0}},
	{action: actionTorrentPriorityLow, ask: "Set low priority on", changes: map[string]any{"bandwidth-priority": -1}},
	{action: actionTorrentGroup, ask: "Set bandwidth group on", hint: "a bandwidth group name; empty clears", parse: parseGroup},
}

// otherMenu is the action menu's "other" submenu, in vision.md's order.
func otherMenu() []menuItem {
	return []menuItem{
		{label: "honor session limits", sub: []menuItem{
			{label: "on", action: actionTorrentHonorLimitsOn},
			{label: "off", action: actionTorrentHonorLimitsOff},
		}},
		{label: "download limit…", action: actionTorrentLimitDownload},
		{label: "upload limit…", action: actionTorrentLimitUpload},
		{label: "peer limit…", action: actionTorrentLimitPeers},
		{label: "seed ratio limit…", action: actionTorrentLimitSeedRatio},
		{label: "seed idle limit…", action: actionTorrentLimitSeedIdle},
		{label: "sequential download", note: "(4.1+)", sub: []menuItem{
			{label: "on", action: actionTorrentSequentialOn},
			{label: "off", action: actionTorrentSequentialOff},
		}},
		{label: "bandwidth priority", sub: []menuItem{
			{label: "high", action: actionTorrentPriorityHigh},
			{label: "normal", action: actionTorrentPriorityNormal},
			{label: "low", action: actionTorrentPriorityLow},
		}},
		{label: "bandwidth group…", action: actionTorrentGroup},
	}
}

// setTorrentChanges is a mutation's run that writes per-torrent fields.
func setTorrentChanges(changes map[string]any) func(context.Context, *transmission.Client, transmission.Selector) error {
	return func(ctx context.Context, c *transmission.Client, sel transmission.Selector) error {
		return c.SetTorrents(ctx, sel, changes)
	}
}

// updateSelection runs a fixed leaf over the selection, asking first above one
// torrent.
func (m Model) updateSelection(leaf updateLeaf) (Model, tea.Cmd) {
	mu, why, ok := m.selectionMutation(mutateUpdate)
	if !ok {
		m.toast = why

		return m, nil
	}

	mu.ask = leaf.ask
	mu.run = setTorrentChanges(leaf.changes)

	return m.confirmed(mu, "")
}

// promptUpdate asks for a prompt leaf's value. The prompt's title states the
// count, so answering it is the confirmation. It starts empty: the detail the
// TUI reads carries no per-torrent limits to prefill from.
func (m Model) promptUpdate(leaf updateLeaf) (Model, tea.Cmd) {
	mu, why, ok := m.selectionMutation(mutateUpdate)
	if !ok {
		m.toast = why

		return m, nil
	}

	mu.ask = leaf.ask
	title := leaf.ask + " " + pluralTorrents(len(mu.sel.IDs))

	return m.openPrompt(newPrompt(m.styles, title, leaf.hint, "", func(m Model, value string) (Model, tea.Cmd) {
		changes, err := leaf.parse(value)
		if err != nil {
			return m.rejectPrompt(err), nil
		}

		mu.run = setTorrentChanges(changes)

		return m.pop().runMutation(mu)
	}))
}

// parsePeerLimit parses the peer limit prompt: a whole number, at least one.
func parsePeerLimit(raw string) (map[string]any, error) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return nil, errors.New("must be 1 or greater")
	}

	return map[string]any{"peer-limit": n}, nil
}

// parseGroup parses the bandwidth group prompt. An empty group clears it, as
// `torrent update --group ""` does.
func parseGroup(raw string) (map[string]any, error) {
	return map[string]any{"group": strings.TrimSpace(raw)}, nil
}

// torrentUpdateHandlers are the "other" leaves.
func torrentUpdateHandlers() map[Action]handler {
	out := make(map[Action]handler, len(updateLeaves))

	for _, leaf := range updateLeaves {
		if leaf.parse != nil {
			out[leaf.action] = func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.promptUpdate(leaf) }

			continue
		}

		out[leaf.action] = func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.updateSelection(leaf) }
	}

	return out
}
