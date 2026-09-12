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
	"github.com/charmbracelet/colorprofile"

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

	// NoColor is set when NO_COLOR is in the environment, resolved by the
	// command layer with the same predicate the CLI's plaintext output uses.
	// colorprofile has a NO_COLOR check of its own, but it parses the value
	// with strconv.ParseBool, so NO_COLOR=yes would strip color from
	// `snatcher list` and not from the TUI; the command layer decides, and the
	// TUI keeps reading no environment of its own.
	NoColor bool

	// PollInterval is how often T4's refresh spine will poll.
	PollInterval time.Duration

	// Version is the string the status bar shows.
	Version string

	// Torrents seeds the torrent list. A nil value means the mock set, which
	// is what T1 runs on; T4 replaces it with a live poll.
	Torrents []transmission.Torrent

	// Config reads and writes snatcher's own client settings for the settings
	// window. It is nil in tests and whenever the launcher did not wire one, in
	// which case the General and Polling tabs are shown read-only.
	Config ClientConfig
}

// ClientEntry is one client setting as the settings window sees it.
type ClientEntry struct {
	// Key is the dotted config key, e.g. "client.theme".
	Key string

	// Value is the effective value: a string or a bool for the keys the window
	// edits.
	Value any

	// Source is the rung that supplied Value: "flag", "env", "config", or
	// "default". A flag or env value is an override for this run and is shown
	// read-only, since writing the file would not change what the user sees.
	Source string

	// Allowed lists the fixed choices for a key that has them (client.theme),
	// and is nil otherwise.
	Allowed []string

	// SetBy names the flag or environment variable behind a flag or env Source.
	SetBy string
}

// ClientConfig is the client-config store the launcher injects. The TUI never
// reads the config file or validates a client value itself: both belong to the
// command layer, which the TUI does not import.
type ClientConfig interface {
	// Entries resolves every client key for this invocation.
	Entries() ([]ClientEntry, error)

	// Validate reports whether value is acceptable for key.
	Validate(key string, value any) error

	// Set validates value and, only if it is acceptable, persists it.
	Set(key string, value any) error
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
	if _, err := tea.NewProgram(m, programOptions(ctx, deps)...).Run(); err != nil && !errors.Is(err, tea.ErrInterrupted) {
		return err
	}

	return nil
}

// programOptions is the option set Run starts the program with.
//
// It is a function rather than a slice literal inside Run so the NO_COLOR
// branch is testable without a terminal: starting a program to find out whether
// it was told to drop color is not a test this suite can run.
//
// Forcing the Ascii profile is all NO_COLOR does. bubbletea still detects and
// still reports a tea.ColorProfileMsg, so the forced profile reaches the model
// by exactly the path a detected one does, and monochrome rendering has one
// implementation rather than two.
func programOptions(ctx context.Context, deps Deps) []tea.ProgramOption {
	opts := []tea.ProgramOption{tea.WithContext(ctx)}

	if deps.NoColor {
		opts = append(opts, tea.WithColorProfile(colorprofile.Ascii))
	}

	return opts
}
