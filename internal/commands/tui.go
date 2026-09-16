package commands

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/config"
	"github.com/itspriddle/snatcher/internal/prowlarr"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// DialResult is one dial as the TUI sees it: the client, the alias and URL the
// precedence chain resolved to, and the unavailable-store warning the dial
// emitted, if any.
type DialResult struct {
	// Client is the constructed Transmission client.
	Client *transmission.Client

	// Name is the alias the precedence chain resolved to.
	Name string

	// URL is that server's RPC URL.
	URL string

	// Warning is the note dial would have written to stderr — today only the
	// unavailable-store warning — trimmed, or empty.
	Warning string
}

// ServerEntry is one configured Transmission server, as the TUI's server picker
// lists it.
type ServerEntry struct {
	// Name is the alias.
	Name string

	// URL is the server's RPC URL.
	URL string

	// Default reports whether this is default_server.
	Default bool
}

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
// string meaning "whatever the precedence chain resolves to". A warning the dial
// would write to stderr is captured into DialResult.Warning instead: mid-session
// the TUI owns the alt screen, and a line written under it would corrupt the
// frame. The capture swaps cmd's error writer for the duration of the dial, so it
// is only safe while one dial is in flight at a time; the TUI's picker refuses a
// second switch while one is running, which is what makes it so.
func Dialer(cmd *cobra.Command) func(server string) (DialResult, error) {
	return func(server string) (DialResult, error) {
		var d dialed

		warning, err := captureDialWarning(cmd, func() error {
			var err error

			d, err = dialNamed(cmd, server)

			return err
		})
		if err != nil {
			return DialResult{}, err
		}

		return DialResult{Client: d.client, Name: d.name, URL: d.url, Warning: warning}, nil
	}
}

// ProwlarrDialResult is one Prowlarr dial as the TUI sees it, the counterpart of
// DialResult.
type ProwlarrDialResult struct {
	// Client is the constructed Prowlarr client.
	Client *prowlarr.Client

	// Name is the alias the dial resolved to.
	Name string

	// URL is that server's base URL.
	URL string

	// Warning is the note the dial would have written to stderr, trimmed, or
	// empty.
	Warning string
}

// ProwlarrDialer returns a function that constructs a *prowlarr.Client for a
// named Prowlarr server, exactly as the search commands do. It captures a dial
// warning the way Dialer does, and shares Dialer's constraint: the two swap the
// same error writer, so the TUI never lets a Prowlarr dial overlap a
// Transmission one.
func ProwlarrDialer(cmd *cobra.Command) func(server string) (ProwlarrDialResult, error) {
	return func(server string) (ProwlarrDialResult, error) {
		var d dialedProwlarr

		warning, err := captureDialWarning(cmd, func() error {
			var err error

			d, err = dialProwlarrNamed(cmd, server)

			return err
		})
		if err != nil {
			return ProwlarrDialResult{}, err
		}

		return ProwlarrDialResult{Client: d.client, Name: d.name, URL: d.url, Warning: warning}, nil
	}
}

// captureDialWarning runs dial with cmd's error writer swapped for a buffer, and
// returns what it wrote, trimmed. The writer is restored before it returns.
func captureDialWarning(cmd *cobra.Command, dial func() error) (string, error) {
	prev := cmd.ErrOrStderr()

	var captured bytes.Buffer

	cmd.SetErr(&captured)
	defer cmd.SetErr(prev)

	if err := dial(); err != nil {
		return "", err
	}

	return strings.TrimSpace(captured.String()), nil
}

// Servers returns a function that lists the configured Transmission servers,
// sorted by alias, for the TUI's server picker. Prowlarr servers are left out:
// the picker switches the torrents and settings windows, which only a
// Transmission server can back. An unconfigured snatcher lists nothing rather
// than failing, and no secret store is touched.
func Servers(cmd *cobra.Command) func() ([]ServerEntry, error) {
	return serversOfType(cmd, config.TypeTransmission, func(cfg config.Config) string { return cfg.DefaultServer })
}

// ProwlarrServers returns a function that lists the configured Prowlarr servers,
// sorted by alias, for the TUI's Prowlarr window and its server picker. It is
// Servers' counterpart, flagging default_prowlarr_server.
func ProwlarrServers(cmd *cobra.Command) func() ([]ServerEntry, error) {
	return serversOfType(cmd, config.TypeProwlarr, func(cfg config.Config) string { return cfg.DefaultProwlarrServer })
}

// serversOfType lists the configured servers of one type, flagging the alias
// defaultOf names. An unconfigured snatcher lists nothing rather than failing,
// and no secret store is touched.
func serversOfType(cmd *cobra.Command, typ string, defaultOf func(config.Config) string) func() ([]ServerEntry, error) {
	return func() ([]ServerEntry, error) {
		cfg, configured, err := loadConfig(configFlag(cmd))
		if err != nil {
			return nil, err
		}

		if !configured {
			return []ServerEntry{}, nil
		}

		names := cfg.NamesOfType(typ)
		def := defaultOf(cfg)
		out := make([]ServerEntry, 0, len(names))

		for _, name := range names {
			out = append(out, ServerEntry{Name: name, URL: cfg.Servers[name].URL, Default: name == def})
		}

		return out, nil
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

// ClientEntry is one client setting as the TUI's settings window sees it: its
// effective value for this invocation, the rung that supplied it, and the fixed
// choices it accepts. internal/cli converts it to tui.ClientEntry; this package
// never imports internal/tui.
type ClientEntry struct {
	// Key is the dotted config key, e.g. "client.theme".
	Key string

	// Value is the effective value in the key's natural type.
	Value any

	// Source is the rung that supplied Value: flag, env, config, or default.
	Source string

	// Allowed lists the fixed choices for a key that has them (client.theme),
	// and is nil otherwise.
	Allowed []string

	// SetBy names the flag ("--theme") or environment variable
	// ("SNATCHER_THEME") behind a flag or env Source, so the TUI can say why a
	// setting cannot be edited for this run. It is empty for the lower rungs.
	SetBy string
}

// clientOverrideNames spells, per overridable key, the flag and variable that
// sit above the config file — the same pair each key's override function reads.
var clientOverrideNames = map[string]struct{ flag, env string }{
	keyTheme:    {flag: "--" + flagTheme, env: envTheme},
	keyNerdFont: {flag: "--" + flagNoNerdFont, env: envNoNerdFont},
}

// ConfigStore is the client-config seam the TUI writes through: read every key
// with its source, validate a value, and persist one. It wraps the same
// resolve/validate/write functions "config list" and "config set" use, so the
// settings window cannot accept a value the CLI rejects or store it anywhere
// the CLI would not.
type ConfigStore struct {
	cmd  *cobra.Command
	path string
}

// ClientConfigStore returns a ConfigStore for this invocation. The --config path
// is captured once, so every read and write in a TUI session targets the file
// the process was launched against.
func ClientConfigStore(cmd *cobra.Command) ConfigStore {
	return ConfigStore{cmd: cmd, path: configFlag(cmd)}
}

// Entries resolves every client key for this invocation, sorted by key. The file
// is read fresh on every call, so a value written by Set is reflected by the
// next Entries.
func (s ConfigStore) Entries() ([]ClientEntry, error) {
	cfg, _, err := loadConfig(s.path)
	if err != nil {
		return nil, err
	}

	resolved, err := clientEntries(s.cmd, cfg)
	if err != nil {
		return nil, err
	}

	out := make([]ClientEntry, len(resolved))
	for i, e := range resolved {
		entry := ClientEntry{Key: e.Key, Value: e.Value, Source: e.Source}

		if e.Key == keyTheme {
			entry.Allowed = slices.Clone(clientThemes)
		}

		names := clientOverrideNames[e.Key]

		switch e.Source {
		case sourceFlag:
			entry.SetBy = names.flag
		case sourceEnv:
			entry.SetBy = names.env
		}

		out[i] = entry
	}

	return out, nil
}

// Validate reports whether value is acceptable for key, with the error "config
// set" would give.
func (s ConfigStore) Validate(key string, value any) error {
	def, err := lookupClientKey(key)
	if err != nil {
		return err
	}

	return validateClientValue(def, value)
}

// Set validates value and, only if it is acceptable, writes it to the config
// file. An invalid value leaves the file untouched.
func (s ConfigStore) Set(key string, value any) error {
	def, err := lookupClientKey(key)
	if err != nil {
		return err
	}

	if err := validateClientValue(def, value); err != nil {
		return err
	}

	_, err = writeClientKey(def, s.path, value)

	return err
}
