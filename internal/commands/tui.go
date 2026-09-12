package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// Dialer returns a function that constructs a *transmission.Client for a named
// server, exactly as every command does. It is the seam internal/cli uses to
// hand the TUI a way to connect without internal/tui ever importing this
// package — and without internal/cli reimplementing the precedence chain.
//
// The TUI is handed the dialer rather than an already-dialed client on purpose:
// the server picker switches named servers without leaving the TUI, which means
// re-dialing mid-session. A pre-dialed client would need a second seam for that;
// a dialer needs none.
//
// The returned function takes the same server argument dial does — the empty
// string meaning "whatever the precedence chain resolves to" — and returns the
// client alongside the resolved server URL.
func Dialer(cmd *cobra.Command) func(server string) (*transmission.Client, string, error) {
	return func(server string) (*transmission.Client, string, error) {
		return dial(cmd, server)
	}
}

// ClientSettings is the appearance and polling configuration the TUI needs,
// resolved for one invocation. It is deliberately a plain value: internal/tui
// receives settings, not a *cobra.Command and not a config.Config.
type ClientSettings struct {
	// Theme is the resolved color theme name, one of clientThemes.
	Theme string

	// NerdFont reports whether Nerd Font glyphs may be used for icons.
	NerdFont bool

	// PollInterval is how often the TUI refreshes server data.
	PollInterval time.Duration

	// NoColor reports whether NO_COLOR is set in the environment, resolved
	// with the same predicate the plaintext output paths use. It is answered
	// here rather than in internal/tui because the TUI reads no environment of
	// its own, and because a TUI that disagreed with `snatcher list` about what
	// NO_COLOR means would be a bug nobody could see from either side.
	NoColor bool
}

// ResolveClientSettings reads the client settings the TUI renders itself with,
// applying the same flag > env > config > default precedence every other
// consumer of these keys gets. An invalid stored value is the error "config get"
// gives, naming the key, so an operator knows to repair the file rather than the
// command line.
//
// The config file is loaded once here and handed to each lookup, matching
// resolveListDefaults. Resolution happens before the TUI dials, so a nonsense
// theme fails without a network round trip and before the alt screen is entered.
func ResolveClientSettings(cmd *cobra.Command) (ClientSettings, error) {
	cfg, _, err := loadConfig(configFlag(cmd))
	if err != nil {
		return ClientSettings{}, err
	}

	theme, err := clientSetting[string](cmd, cfg, keyTheme)
	if err != nil {
		return ClientSettings{}, err
	}

	nerdFont, err := clientSetting[bool](cmd, cfg, keyNerdFont)
	if err != nil {
		return ClientSettings{}, err
	}

	interval, err := clientSetting[string](cmd, cfg, keyPollInterval)
	if err != nil {
		return ClientSettings{}, err
	}

	// The registry already validated the duration's syntax and range on the way
	// out of clientSetting, and it validated the trimmed spelling, so trim the
	// same way here. A parse error is still reported against the key, matching
	// every other invalid-value message.
	poll, err := time.ParseDuration(strings.TrimSpace(interval))
	if err != nil {
		return ClientSettings{}, fmt.Errorf("config key %s: %w", keyPollInterval, err)
	}

	return ClientSettings{Theme: theme, NerdFont: nerdFont, PollInterval: poll, NoColor: noColor()}, nil
}
