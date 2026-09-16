package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/itspriddle/snatcher/internal/prowlarr"
)

// searchMsg is the answer to runSearch: one page of results.
type searchMsg struct {
	gen, seq, offset int
	releases         []prowlarr.Release
	err              error
}

// runSearch runs one page of a search.
func runSearch(ctx context.Context, client *prowlarr.Client, gen, seq int, params prowlarr.SearchParams) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, prowlarrTimeout)
		defer cancel()

		releases, err := client.Search(ctx, params)

		return searchMsg{gen: gen, seq: seq, offset: params.Offset, releases: releases, err: err}
	}
}

// newQueryInput builds the search pane's query field, with the readline keymap
// every input shares.
func newQueryInput(st Styles) textinput.Model {
	input := textinput.New()
	input.Prompt = "/ "
	input.Placeholder = "search Prowlarr"
	input.KeyMap = textInputKeyMap()
	input.SetStyles(st.Input)

	return input
}

// beginQuery is `/`: focus the search pane and hand its field the keyboard.
func (m Model) beginQuery() (Model, tea.Cmd) {
	m = m.setFocus(paneSearch)
	m.prowlarr.queryInput.CursorEnd()

	cmd := m.prowlarr.queryInput.Focus()
	m.inputFocused, m.owner = true, ownerQuery

	return m, cmd
}

// queryForward hands a message to the query field.
func (m Model) queryForward(msg tea.Msg) (Model, tea.Cmd) {
	input, cmd := m.prowlarr.queryInput.Update(msg)
	m.prowlarr.queryInput = input

	return m, cmd
}

// queryBlur blurs the query field, keeping its text: it is the next search's
// draft. The search pane keeps focus, so a second esc leaves the window.
func (m Model) queryBlur() Model {
	m.prowlarr.queryInput.Blur()

	return m
}

// submitSearch is enter on the search pane: search for the field's text from the
// first page, and move to the results. enter always starts over, never pages.
func (m Model) submitSearch() (Model, tea.Cmd) {
	if why, refused := m.refuseRelease(); refused {
		m.toast = why

		return m, nil
	}

	query := strings.TrimSpace(m.prowlarr.queryInput.Value())
	if query == "" {
		m.toast = "type a query first"

		return m, nil
	}

	if m.owner == ownerQuery {
		m = m.blurInput()
	}

	m.prowlarr.params.Query = query
	m = m.setFocus(paneResults)

	return m.startSearch(0)
}

// resubmitSearch is R in the Prowlarr window: run the last search again from its
// first page, or retry a dial that failed.
func (m Model) resubmitSearch() (Model, tea.Cmd) {
	w := m.prowlarr

	switch {
	case w.client == nil && !w.dialing && !w.unconfigured:
		return m.dialDefaultProwlarr()
	case !w.searched:
		if why, refused := m.refuseRelease(); refused {
			m.toast = why

			return m, nil
		}

		m.toast = "no search to resubmit"

		return m, nil
	}

	if why, refused := m.refuseRelease(); refused {
		m.toast = why

		return m, nil
	}

	return m.startSearch(0)
}

// fetchMore is +: ask for the page after the last one, appending it.
func (m Model) fetchMore() (Model, tea.Cmd) {
	w := m.prowlarr

	if why, refused := m.refuseRelease(); refused {
		m.toast = why

		return m, nil
	}

	switch {
	case !w.searched:
		m.toast = "no search yet: press / to search"
	case w.inFlight:
		m.toast = "search in flight"
	case w.exhausted:
		m.toast = "no more results"
	default:
		return m.startSearch(w.nextOffset)
	}

	return m, nil
}

// startSearch runs the submitted search from offset. The first page replaces
// the results, so it clears them at once rather than leaving the last query's
// rows under the new one; a later page keeps them.
func (m Model) startSearch(offset int) (Model, tea.Cmd) {
	w := m.prowlarr
	w.seq++
	w.inFlight = true
	w.err = nil

	if offset == 0 {
		w.searched, w.exhausted, w.nextOffset = true, false, 0
		w.hasCursor = false
		w = w.setResults(nil)
		m.toast = "searching for " + w.params.Query + "…"
	} else {
		m.toast = "fetching more results…"
	}

	params := w.params
	params.Offset = offset
	m.prowlarr = w

	return m, runSearch(m.ctx, w.client, w.gen, w.seq, params)
}

// searched installs a page. A reply from another server or a superseded search
// is dropped; so is a page whose offset is not the one + asked for, which only
// a reply racing a resubmit could produce.
func (m Model) searched(msg searchMsg) Model {
	w := m.prowlarr
	if msg.gen != w.gen || msg.seq != w.seq {
		return m
	}

	w.inFlight = false

	if msg.err != nil {
		w.err = msg.err
		m.prowlarr = w

		m.toast = "search failed: " + errorLine(msg.err)
		if msg.offset > 0 {
			m.toast = "more results: " + errorLine(msg.err)
		}

		return m
	}

	switch msg.offset {
	case 0:
		w = w.setResults(msg.releases)
		m.toast = pluralResults(len(msg.releases))
	case w.nextOffset:
		w = w.setResults(append(slices.Clone(w.results), msg.releases...))
		m.toast = fmt.Sprintf("+%s (%d in all)", pluralResults(len(msg.releases)), len(w.results))
	default:
		m.prowlarr = w

		return m
	}

	w.nextOffset = msg.offset + len(msg.releases)
	w.exhausted = len(msg.releases) < w.params.Limit
	m.prowlarr = w

	return m
}

// pluralResults is "1 result" or "N results".
func pluralResults(n int) string {
	return fmt.Sprintf("%d %s", n, plural(n, "result"))
}
