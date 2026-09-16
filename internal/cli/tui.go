package cli

import (
	"context"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/itspriddle/snatcher/internal/browser"
	"github.com/itspriddle/snatcher/internal/clipboard"
	"github.com/itspriddle/snatcher/internal/commands"
	"github.com/itspriddle/snatcher/internal/tui"
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
		Dial:         dialer(commands.Dialer(cmd)),
		Servers:      servers(commands.Servers(cmd)),
		Config:       clientConfig{store: commands.ClientConfigStore(cmd)},
		Theme:        settings.Theme,
		NerdFont:     settings.NerdFont,
		NoColor:      settings.NoColor,
		PollInterval: settings.PollInterval,
		Clipboard:    nativeClipboard(),

		DialProwlarr:    prowlarrDialer(commands.ProwlarrDialer(cmd)),
		ProwlarrServers: servers(commands.ProwlarrServers(cmd)),
		OpenURL:         nativeBrowser(),
	})
}

// nativeClipboard returns the native clipboard writer yanks use beside OSC 52,
// or nil for OSC 52 only.
//
// Over SSH it is always nil: a native tool would write the remote machine's
// clipboard rather than the user's, and xclip with no DISPLAY would raise a
// failure toast for a copy OSC 52 already carried home. The environment is read
// here, with the launcher's other decisions, so internal/tui reads none.
func nativeClipboard() func(context.Context, string) error {
	if overSSH() {
		return nil
	}

	w := clipboard.Detect()
	if w == nil {
		return nil
	}

	return w.Write
}

// nativeBrowser returns the opener O and P use, or nil when there is none, in
// which case they yank the URL instead.
//
// Over SSH it is always nil, for the reason nativeClipboard is: the browser
// would open on the remote machine, not in front of the user.
func nativeBrowser() func(context.Context, string) error {
	if overSSH() {
		return nil
	}

	o := browser.Detect()
	if o == nil {
		return nil
	}

	return o.Open
}

// overSSH reports whether the process was started from an SSH session.
func overSSH() bool {
	return os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != ""
}

// dialer adapts commands.Dialer to the TUI's own DialResult.
func dialer(dial func(string) (commands.DialResult, error)) func(string) (tui.DialResult, error) {
	return func(server string) (tui.DialResult, error) {
		res, err := dial(server)

		return tui.DialResult{Client: res.Client, Name: res.Name, URL: res.URL, Warning: res.Warning}, err
	}
}

// prowlarrDialer adapts commands.ProwlarrDialer to the TUI's own
// ProwlarrDialResult.
func prowlarrDialer(dial func(string) (commands.ProwlarrDialResult, error)) func(string) (tui.ProwlarrDialResult, error) {
	return func(server string) (tui.ProwlarrDialResult, error) {
		res, err := dial(server)

		return tui.ProwlarrDialResult{Client: res.Client, Name: res.Name, URL: res.URL, Warning: res.Warning}, err
	}
}

// servers adapts commands.Servers or commands.ProwlarrServers to the TUI's own
// ServerEntry.
func servers(list func() ([]commands.ServerEntry, error)) func() ([]tui.ServerEntry, error) {
	return func() ([]tui.ServerEntry, error) {
		entries, err := list()
		if err != nil {
			return nil, err
		}

		out := make([]tui.ServerEntry, len(entries))
		for i, e := range entries {
			out[i] = tui.ServerEntry{Name: e.Name, URL: e.URL, Default: e.Default}
		}

		return out, nil
	}
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
