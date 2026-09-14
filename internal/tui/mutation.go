package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// mutationKind names one torrent verb.
type mutationKind int

const (
	mutateStart mutationKind = iota
	mutateForceStart
	mutateStop
	mutateVerify
	mutateReannounce
	mutateQueueUp
	mutateQueueDown
	mutateQueueTop
	mutateQueueBottom
	mutateRemove
	mutateRemoveData
	mutateLabels
	mutateMove
	mutateRename
	mutateTrackerAdd
	mutateTrackerReplace
	mutateTrackerClear
	mutateUpdate
)

// mutationVerb is how one verb is worded: asked in a confirm, announced while it
// runs, and reported when it lands. The done wording is the CLI's (mutationVerb
// in internal/commands/mutate.go), so the TUI and `snatcher torrent …` report an
// act the same way; internal/tui does not import internal/commands, so it is
// copied rather than shared.
type mutationVerb struct {
	ask, doing, done string

	// tail follows the count in the done report.
	tail string

	// run is the RPC the verb makes.
	run func(ctx context.Context, c *transmission.Client, sel transmission.Selector) error
}

// mutationVerbs are the verbs the list pane, the filter pane, and the action menu
// run.
var mutationVerbs = map[mutationKind]mutationVerb{
	mutateStart: {ask: "Start", doing: "Starting", done: "Started", run: func(ctx context.Context, c *transmission.Client, sel transmission.Selector) error {
		return c.StartTorrents(ctx, sel, false)
	}},
	mutateForceStart: {ask: "Force start", doing: "Force starting", done: "Started", run: func(ctx context.Context, c *transmission.Client, sel transmission.Selector) error {
		return c.StartTorrents(ctx, sel, true)
	}},
	mutateStop: {ask: "Stop", doing: "Stopping", done: "Stopped", run: bySelector((*transmission.Client).StopTorrents)},
	mutateVerify: {ask: "Verify", doing: "Queuing verification of", done: "Queued verification of",
		run: bySelector((*transmission.Client).VerifyTorrents)},
	mutateReannounce: {ask: "Reannounce", doing: "Reannouncing", done: "Reannounced",
		run: bySelector((*transmission.Client).ReannounceTorrents)},
	mutateQueueUp:   {ask: "Move up", doing: "Moving up", done: "Moved up", run: bySelector((*transmission.Client).QueueMoveUp)},
	mutateQueueDown: {ask: "Move down", doing: "Moving down", done: "Moved down", run: bySelector((*transmission.Client).QueueMoveDown)},
	mutateQueueTop: {ask: "Move to top", doing: "Moving to top", done: "Moved to top",
		run: bySelector((*transmission.Client).QueueMoveTop)},
	mutateQueueBottom: {ask: "Move to bottom", doing: "Moving to bottom", done: "Moved to bottom",
		run: bySelector((*transmission.Client).QueueMoveBottom)},
	mutateRemove: {ask: "Remove", doing: "Removing", done: "Removed", run: func(ctx context.Context, c *transmission.Client, sel transmission.Selector) error {
		return c.RemoveTorrents(ctx, sel, false)
	}},
	mutateRemoveData: {ask: "Remove", doing: "Removing", done: "Removed", tail: " and their data", run: func(ctx context.Context, c *transmission.Client, sel transmission.Selector) error {
		return c.RemoveTorrents(ctx, sel, true)
	}},

	// The prompt verbs carry arguments a selector cannot, so they have no run of
	// their own: each mutation brings one.
	mutateLabels: {ask: "Set labels on", doing: "Updating", done: "Updated"},
	mutateMove:   {ask: "Move", doing: "Moving", done: "Moved"},
	mutateRename: {ask: "Rename", doing: "Renaming", done: "Renamed"},

	// The tracker edits all write tracker-list. Their done word is the CLI's
	// `update`; each has its own ask so a failure names what was tried.
	mutateTrackerAdd:     {ask: "Add tracker to", doing: "Updating", done: "Updated"},
	mutateTrackerReplace: {ask: "Replace trackers on", doing: "Updating", done: "Updated"},
	mutateTrackerClear:   {ask: "Clear trackers on", doing: "Updating", done: "Updated"},

	// The per-torrent field changes share one kind: one RPC and the CLI's
	// `update` wording. Each brings its own ask on the mutation, so a failure
	// still names what was tried.
	mutateUpdate: {ask: "Update", doing: "Updating", done: "Updated"},
}

// bySelector adapts a client method that takes only a selector — a method
// expression, receiver first — to a verb's run signature, which keeps the
// context first.
func bySelector(
	f func(*transmission.Client, context.Context, transmission.Selector) error,
) func(context.Context, *transmission.Client, transmission.Selector) error {
	return func(ctx context.Context, c *transmission.Client, sel transmission.Selector) error {
		return f(c, ctx, sel)
	}
}

// pluralTorrents is a torrent count with its noun, the CLI's pluralizeTorrents.
func pluralTorrents(n int) string {
	return fmt.Sprintf("%d %s", n, plural(n, "torrent"))
}

// countPrompt is a counted confirm question, the shape of the CLI's remove
// prompt: "Remove 3 torrents?", or "Remove 3 torrents in Seeding?" for a group.
func countPrompt(verb string, n int, group string) string {
	if group != "" {
		return fmt.Sprintf("%s %s in %s?", verb, pluralTorrents(n), group)
	}

	return fmt.Sprintf("%s %s?", verb, pluralTorrents(n))
}

// mutationMsg is the answer to a mutation.
type mutationMsg struct {
	gen  int
	kind mutationKind

	// ask is the mutation's ask, which names a failure; empty falls back to the
	// verb's.
	ask string

	count int
	err   error
}

// mutation is one verb bound to the torrents it will act on. The selector is
// fixed when the mutation is built — before any confirm — so the torrents a
// confirm counted are exactly the torrents the RPC names, whatever a poll does
// while the question is open.
type mutation struct {
	kind mutationKind
	sel  transmission.Selector

	// run overrides the verb's own run, for a verb whose RPC needs more than the
	// selector: the labels, the location, the new name.
	run func(ctx context.Context, c *transmission.Client, sel transmission.Selector) error

	// ask overrides the verb's ask, for the kinds that share one verb but not
	// one question: "Set download limit on", "Honor session limits on".
	ask string
}

// askWord is how the mutation is asked about and how its failure is named.
func (mu mutation) askWord() string {
	if mu.ask != "" {
		return mu.ask
	}

	return mutationVerbs[mu.kind].ask
}

// refuseMutation says why a mutation cannot run, or reports that it can.
//
// A selector with no IDs is refused here rather than trusted to the caller: the
// ids argument is how Transmission is told which torrents to act on, and a
// request that names none must never be what reaches the server. A selector
// with All set is refused too — every TUI mutation names its torrents.
func (m Model) refuseMutation(sel transmission.Selector) (string, bool) {
	switch {
	case m.client == nil:
		return "no server", true
	case m.live.switching:
		return "switching servers…", true
	case sel.All || len(sel.IDs) == 0 || len(sel.Hashes) > 0:
		return "nothing selected", true
	}

	return "", false
}

// selectionMutation is a verb over the list pane's selection: the marks, or the
// cursor row.
func (m Model) selectionMutation(kind mutationKind) (mutation, string, bool) {
	sel := m.listSelector()

	if why, refused := m.refuseMutation(sel); refused {
		return mutation{}, why, false
	}

	return mutation{kind: kind, sel: sel}, "", true
}

// verb is s, p, v, r, J, and K in the list pane. One torrent acts at once; more
// than one asks first and states the count.
func (m Model) verb(kind mutationKind) (Model, tea.Cmd) {
	mu, why, ok := m.selectionMutation(kind)
	if !ok {
		m.toast = why

		return m, nil
	}

	return m.confirmed(mu, "")
}

// confirmed runs a mutation, first asking when it names more than one torrent.
func (m Model) confirmed(mu mutation, group string) (Model, tea.Cmd) {
	if n := len(mu.sel.IDs); n > 1 || group != "" {
		question := countPrompt(mu.askWord(), n, group)

		return m.push(newConfirm(question, func(m Model) (Model, tea.Cmd) { return m.runMutation(mu) }, nil)), nil
	}

	return m.runMutation(mu)
}

// runMutation sends a mutation. The guards run again: a confirm can be answered
// after the state it was asked in has changed.
//
// There is no single-flight. Mutations are independent RPCs and a second s is
// harmless; what must not be lost is the refetch, which requestPoll queues.
func (m Model) runMutation(mu mutation) (Model, tea.Cmd) {
	if why, refused := m.refuseMutation(mu.sel); refused {
		m.toast = why

		return m, nil
	}

	verb := mutationVerbs[mu.kind]
	n := len(mu.sel.IDs)
	ctx, client, gen, ask := m.ctx, m.client, m.live.gen, mu.askWord()

	run := verb.run
	if mu.run != nil {
		run = mu.run
	}

	m.toast = verb.doing + " " + pluralTorrents(n) + "…"

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
		defer cancel()

		return mutationMsg{gen: gen, kind: mu.kind, ask: ask, count: n, err: run(ctx, client, mu.sel)}
	}
}

// mutated settles a mutation. A success is reported in the CLI's words and then
// refetched at once: Transmission's transitions are not instantaneous (start
// goes through queued, verify through queued-to-verify), so the rows are what
// the server says next rather than a guess drawn now. A failure keeps the rows
// the last poll drew.
func (m Model) mutated(msg mutationMsg) (Model, tea.Cmd) {
	if msg.gen != m.live.gen {
		return m, nil
	}

	verb := mutationVerbs[msg.kind]

	if msg.err != nil {
		ask := msg.ask
		if ask == "" {
			ask = verb.ask
		}

		m.toast = strings.ToLower(ask) + ": " + errorLine(msg.err)

		return m, nil
	}

	m.toast = verb.done + " " + pluralTorrents(msg.count) + verb.tail

	return m.requestPoll()
}

// removeSelection is d in the list pane. It always asks, whatever the count.
func (m Model) removeSelection() (Model, tea.Cmd) {
	mu, why, ok := m.selectionMutation(mutateRemove)
	if !ok {
		m.toast = why

		return m, nil
	}

	return m.askRemove(mu.sel, ""), nil
}

// askRemove is the first of remove's two questions. The prompt and the report
// say "Remove", as `snatcher torrent remove` does, though the key's help says
// delete.
func (m Model) askRemove(sel transmission.Selector, group string) Model {
	question := countPrompt("Remove", len(sel.IDs), group)

	return m.push(newConfirm(question, func(m Model) (Model, tea.Cmd) { return m.askDeleteData(sel), nil }, nil))
}

// askDeleteData is remove's second question, asked separately so deleting files
// on disk is never the side effect of answering the first. It is strict: enter
// does not answer it, so only a deliberate y deletes data.
func (m Model) askDeleteData(sel transmission.Selector) Model {
	o := newConfirm("Also delete downloaded data?",
		func(m Model) (Model, tea.Cmd) { return m.runMutation(mutation{kind: mutateRemoveData, sel: sel}) },
		func(m Model) (Model, tea.Cmd) { return m.runMutation(mutation{kind: mutateRemove, sel: sel}) },
	)
	o.secondary = "y deletes the files on disk, n keeps them, esc cancels"
	o.strict = true

	return m.push(o)
}

// groupSelector names every torrent a sidebar row matches, from the unfiltered
// set, as explicit IDs. It never sets All: even the All row becomes a list of
// IDs, so the confirm's count and the RPC agree.
func groupSelector(item filterItem, all []transmission.Torrent) transmission.Selector {
	f := item.view()
	sel := transmission.Selector{IDs: []int64{}}

	for _, t := range all {
		if f.matches(t) {
			sel.IDs = append(sel.IDs, t.ID)
		}
	}

	return sel
}

// groupAction is s, p, v, and d in the filter pane: act on every torrent in the
// cursor row's group. It always asks, even for one member, and names the group:
// the blast radius is the group, and focus alone must not be what stands
// between one torrent and four hundred.
func (m Model) groupAction(kind mutationKind) (Model, tea.Cmd) {
	item := m.torrents.filter.current()
	sel := groupSelector(item, m.torrents.all)

	switch why, refused := m.refuseMutation(sel); {
	case refused && len(sel.IDs) == 0 && m.client != nil && !m.live.switching:
		m.toast = "no torrents in " + item.label

		return m, nil
	case refused:
		m.toast = why

		return m, nil
	}

	if kind == mutateRemove {
		return m.askRemove(sel, item.label), nil
	}

	return m.confirmed(mutation{kind: kind, sel: sel}, item.label)
}

// mutationHandlers are the torrent verbs in the list pane and the action menu,
// and the group actions in the filter pane.
func mutationHandlers() map[Action]handler {
	verb := func(kind mutationKind) handler {
		return func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.verb(kind) }
	}

	group := func(kind mutationKind) handler {
		return func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.groupAction(kind) }
	}

	return map[Action]handler{
		actionTorrentStart:      verb(mutateStart),
		actionTorrentStop:       verb(mutateStop),
		actionTorrentVerify:     verb(mutateVerify),
		actionTorrentReann:      verb(mutateReannounce),
		actionQueueDown:         verb(mutateQueueDown),
		actionQueueUp:           verb(mutateQueueUp),
		actionQueueTop:          verb(mutateQueueTop),
		actionQueueBottom:       verb(mutateQueueBottom),
		actionTorrentForceStart: verb(mutateForceStart),
		actionTorrentRemove:     func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.removeSelection() },

		actionGroupStart:  group(mutateStart),
		actionGroupStop:   group(mutateStop),
		actionGroupVerify: group(mutateVerify),
		actionGroupRemove: group(mutateRemove),
	}
}
