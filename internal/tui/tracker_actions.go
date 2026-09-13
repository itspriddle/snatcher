package tui

import (
	"cmp"
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// trackerListKey is the torrent-set field every tracker edit writes. It is the
// only tracker mutation snatcher has — the CLI's `torrent update --tracker` and
// `--clear-trackers` write it too — so the TUI's edits and the CLI's produce the
// same wire value.
const trackerListKey = "tracker-list"

// trackerPrefillMsg is the answer to the detail read replace needs before it can
// prompt: the poll does not carry a torrent's trackers.
type trackerPrefillMsg struct {
	gen      int
	mu       mutation
	torrent  transmission.Torrent
	trackers []transmission.Tracker
	err      error
}

// trackerList renders trackers as Transmission's tracker list: announce URLs one
// per line within a tier, a blank line between tiers, tiers in order.
func trackerList(trackers []transmission.Tracker) string {
	sorted := slices.Clone(trackers)
	slices.SortStableFunc(sorted, func(a, b transmission.Tracker) int { return cmp.Compare(a.Tier, b.Tier) })

	var b strings.Builder

	for i, t := range sorted {
		switch {
		case i == 0:
		case t.Tier != sorted[i-1].Tier:
			b.WriteString("\n\n")
		default:
			b.WriteString("\n")
		}

		b.WriteString(t.Announce)
	}

	return b.String()
}

// parseAnnounce checks one announce URL: a scheme a tracker speaks, a host, and
// no whitespace — a newline would split the tracker list into tiers nobody
// asked for.
func parseAnnounce(raw string) (string, error) {
	s := strings.TrimSpace(raw)

	switch {
	case s == "":
		return "", errors.New("an announce URL is required")
	case strings.ContainsAny(s, " \t\r\n"):
		return "", errors.New("an announce URL cannot contain spaces")
	}

	u, err := url.Parse(s)
	if err != nil {
		return "", errors.New("not a valid URL")
	}

	switch u.Scheme {
	case "http", "https", "udp", "ws", "wss":
	default:
		return "", errors.New("announce URLs are http, https, udp, ws, or wss")
	}

	if u.Host == "" {
		return "", errors.New("an announce URL needs a host")
	}

	return s, nil
}

// setTrackerList is a mutation's run that writes a tracker list.
func setTrackerList(list string) func(context.Context, *transmission.Client, transmission.Selector) error {
	return func(ctx context.Context, c *transmission.Client, sel transmission.Selector) error {
		return c.SetTorrents(ctx, sel, map[string]any{trackerListKey: list})
	}
}

// trackerTarget is the one torrent an add or a replace edits. Both are
// single-torrent: a tracker list is per torrent, and one URL typed for several
// would read as a bulk edit nobody confirmed.
func (m Model) trackerTarget(kind mutationKind) (mutation, transmission.Torrent, string, bool) {
	mu, why, ok := m.selectionMutation(kind)
	if !ok {
		return mutation{}, transmission.Torrent{}, why, false
	}

	if len(mu.sel.IDs) != 1 {
		return mutation{}, transmission.Torrent{}, "trackers: one torrent at a time", false
	}

	t, ok := torrentByID(m.torrents.all, mu.sel.IDs[0])
	if !ok {
		return mutation{}, transmission.Torrent{}, "nothing selected", false
	}

	return mu, t, "", true
}

// addTracker adds one announce URL as a new tier, what Transmission's own
// tracker-add did: a backup tracker, tried after the ones already there. The run
// reads the current list and writes it back with the URL appended, so a poll
// between the two cannot change what is sent; a torrent that vanished fails the
// read and nothing is written.
func (m Model) addTracker() (Model, tea.Cmd) {
	mu, t, why, ok := m.trackerTarget(mutateTrackerAdd)
	if !ok {
		m.toast = why

		return m, nil
	}

	title := "Add tracker to " + output.SanitizeTerminal(t.Name)

	return m.openPrompt(newPrompt(m.styles, title, "announce URL, added as a new tier", "",
		func(m Model, value string) (Model, tea.Cmd) {
			announce, err := parseAnnounce(value)
			if err != nil {
				return m.rejectPrompt(err), nil
			}

			mu.run = func(ctx context.Context, c *transmission.Client, sel transmission.Selector) error {
				detail, err := c.TorrentDetail(ctx, t.ID)
				if err != nil {
					return err
				}

				list := trackerList(detail.Trackers)
				if list != "" {
					list += "\n\n"
				}

				return setTrackerList(list+announce)(ctx, c, sel)
			}

			return m.pop().runMutation(mu)
		}))
}

// replaceTrackers replaces one torrent's trackers with the URLs typed, prefilled
// with the ones it has. The list comes from the detail pane when it describes
// this torrent, and is read first otherwise.
func (m Model) replaceTrackers() (Model, tea.Cmd) {
	mu, t, why, ok := m.trackerTarget(mutateTrackerReplace)
	if !ok {
		m.toast = why

		return m, nil
	}

	if p := m.torrents.detail; p.hasDetail && p.detail.ID == t.ID {
		return m.promptReplace(mu, t, p.detail.Trackers)
	}

	ctx, client, gen := m.ctx, m.client, m.live.gen
	m.toast = "trackers: fetching…"

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
		defer cancel()

		detail, err := client.TorrentDetail(ctx, t.ID)

		return trackerPrefillMsg{gen: gen, mu: mu, torrent: t, trackers: detail.Trackers, err: err}
	}
}

// trackersFetched opens the replace prompt once the list it is prefilled with
// has arrived.
func (m Model) trackersFetched(msg trackerPrefillMsg) (Model, tea.Cmd) {
	if msg.gen != m.live.gen {
		return m, nil
	}

	if msg.err != nil {
		m.toast = "trackers: " + errorLine(msg.err)

		return m, nil
	}

	m.toast = ""

	return m.promptReplace(msg.mu, msg.torrent, msg.trackers)
}

// promptReplace is the replace prompt. The URLs it sends are joined into one
// tier, exactly as `torrent update --tracker a --tracker b` joins them, so the
// prompt and the CLI write the same value. An empty prompt is refused: clear is
// its own leaf, and a confirmed one.
func (m Model) promptReplace(mu mutation, t transmission.Torrent, trackers []transmission.Tracker) (Model, tea.Cmd) {
	current := make([]string, 0, len(trackers))
	for _, tr := range trackers {
		current = append(current, output.SanitizeTerminal(tr.Announce))
	}

	title := "Replace trackers on " + output.SanitizeTerminal(t.Name)

	return m.openPrompt(newPrompt(m.styles, title, "space-separated announce URLs", strings.Join(current, " "),
		func(m Model, value string) (Model, tea.Cmd) {
			fields := strings.Fields(value)
			if len(fields) == 0 {
				return m.rejectPrompt(errors.New("at least one announce URL; clear removes them all")), nil
			}

			urls := make([]string, 0, len(fields))

			for _, f := range fields {
				announce, err := parseAnnounce(f)
				if err != nil {
					return m.rejectPrompt(err), nil
				}

				urls = append(urls, announce)
			}

			mu.run = setTrackerList(strings.Join(urls, "\n"))

			return m.pop().runMutation(mu)
		}))
}

// clearTrackers removes every tracker from the selection. It always asks,
// whatever the count: a torrent with no trackers finds no peers but through DHT
// and PEX, and there is no undo.
func (m Model) clearTrackers() (Model, tea.Cmd) {
	mu, why, ok := m.selectionMutation(mutateTrackerClear)
	if !ok {
		m.toast = why

		return m, nil
	}

	mu.run = setTrackerList("")
	question := countPrompt(mutationVerbs[mutateTrackerClear].ask, len(mu.sel.IDs), "")

	return m.push(newConfirm(question, func(m Model) (Model, tea.Cmd) { return m.runMutation(mu) }, nil)), nil
}

// trackerHandlers are the action menu's trackers leaves.
func trackerHandlers() map[Action]handler {
	return map[Action]handler{
		actionTrackerAdd:     func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.addTracker() },
		actionTrackerReplace: func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.replaceTrackers() },
		actionTrackerClear:   func(m Model, _ tea.KeyPressMsg) (Model, tea.Cmd) { return m.clearTrackers() },
	}
}
