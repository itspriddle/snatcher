package cli

import (
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/itspriddle/snatcher/internal/commands"
	"github.com/itspriddle/snatcher/internal/tui"
	"github.com/itspriddle/snatcher/internal/version"
)

// isTerminal reports whether v is an interactive terminal.
//
// It deliberately ignores NO_COLOR, unlike the isTTY helper in
// internal/commands: there, NO_COLOR folds into "not a terminal" because the
// question being asked is whether to draw a colored, aligned table. Here the
// question is whether to launch a full-screen application at all, and
// "monochrome" is not an answer to it. NO_COLOR reaches the TUI as
// Deps.NoColor, which draws it without color rather than not at all.
func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}

	return term.IsTerminal(int(f.Fd()))
}

// launchTUI resolves everything the TUI needs from this invocation and runs it.
//
// Both resolutions happen here rather than inside internal/tui, which imports
// neither internal/commands nor internal/config: the TUI is handed values and a
// dialer. An invalid stored setting therefore fails on the terminal, naming the
// config key, before the alt screen is entered.
func launchTUI(cmd *cobra.Command) error {
	settings, err := commands.ResolveClientSettings(cmd)
	if err != nil {
		return err
	}

	return tui.Run(cmd.Context(), tui.Deps{
		Dial:         commands.Dialer(cmd),
		Config:       clientConfig{store: commands.ClientConfigStore(cmd)},
		Theme:        settings.Theme,
		NerdFont:     settings.NerdFont,
		NoColor:      settings.NoColor,
		PollInterval: settings.PollInterval,
		Version:      version.FullVersion(),
	})
}

// clientConfig adapts commands.ConfigStore to tui.ClientConfig. The two packages
// each declare their own entry type because neither may import the other; this
// is the one place they meet.
type clientConfig struct {
	store commands.ConfigStore
}

var _ tui.ClientConfig = clientConfig{}

// Entries implements tui.ClientConfig.
func (c clientConfig) Entries() ([]tui.ClientEntry, error) {
	entries, err := c.store.Entries()
	if err != nil {
		return nil, err
	}

	out := make([]tui.ClientEntry, len(entries))
	for i, e := range entries {
		out[i] = tui.ClientEntry{Key: e.Key, Value: e.Value, Source: e.Source, Allowed: e.Allowed, SetBy: e.SetBy}
	}

	return out, nil
}

// Validate implements tui.ClientConfig.
func (c clientConfig) Validate(key string, value any) error {
	return c.store.Validate(key, value)
}

// Set implements tui.ClientConfig.
func (c clientConfig) Set(key string, value any) error {
	return c.store.Set(key, value)
}
