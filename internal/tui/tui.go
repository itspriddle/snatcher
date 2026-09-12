// Package tui implements snatcher's terminal UI: the window and pane layout,
// the focus router, the keymap registry, and the esc ladder.
//
// It sits beside internal/commands rather than under it. It depends on
// internal/transmission, internal/output, and nothing else of snatcher's;
// internal/cli launches it and injects everything it needs through Deps, so the
// dial precedence chain and the client-settings registry keep living in
// internal/commands and this package never imports them.
//
// The keymap registry in keymap.go is this package's .surface: every key the UI
// responds to is one row of it, and the collision test in keymap_test.go is what
// catches a keymap regression in review rather than in a user's terminal.
package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// Deps is everything the TUI is given from outside. It is a plain struct of
// values and one function: the TUI resolves no configuration, reads no
// environment, and parses no flags of its own.
type Deps struct {
	// Dial constructs a client for a named server, or for the configured
	// default when the name is empty. The TUI keeps the dialer rather than a
	// dialed client so T4's server picker can switch servers without a new
	// seam.
	Dial func(server string) (*transmission.Client, string, error)

	// Theme is the resolved color theme name.
	Theme string

	// NerdFont reports whether Nerd Font glyphs may be used.
	NerdFont bool

	// PollInterval is how often T4's refresh spine will poll.
	PollInterval time.Duration

	// Version is the string the status bar shows.
	Version string

	// Torrents seeds the torrent list. A nil value means the mock set, which
	// is what T1 runs on; T4 replaces it with a live poll.
	Torrents []transmission.Torrent
}

// Run dials, then starts the program and blocks until it exits.
//
// Dialing happens before tea.NewProgram, deliberately. An unconfigured snatcher
// must print the CLI's own "not configured: run 'snatcher server add' first" on
// the terminal and exit non-zero, not flash an alt screen carrying an error the
// user cannot copy out of. Everything that can fail from configuration fails
// here, above the alt screen.
func Run(ctx context.Context, deps Deps) error {
	client, serverURL, err := deps.Dial("")
	if err != nil {
		return err
	}

	m := New(deps)
	m.client = client
	m.serverURL = serverURL

	// ctrl+c and SIGINT are how a TUI is meant to be left, so the sentinel
	// bubbletea reports them with is not an error to show the user.
	if _, err := tea.NewProgram(m, tea.WithContext(ctx)).Run(); err != nil && !errors.Is(err, tea.ErrInterrupted) {
		return err
	}

	return nil
}
