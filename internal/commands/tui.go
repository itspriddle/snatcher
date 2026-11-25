package commands

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/theme"
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

// ServerEntry is one configured server, as the TUI's server picker lists it.
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
		prev := cmd.ErrOrStderr()

		var captured bytes.Buffer

		cmd.SetErr(&captured)
		defer cmd.SetErr(prev)

		d, err := dialNamed(cmd, server)
		if err != nil {
			return DialResult{}, err
		}

		warning := strings.TrimSpace(captured.String())

		return DialResult{Client: d.client, Name: d.name, URL: d.url, Warning: warning}, nil
	}
}

// Servers returns a function that lists the configured servers, sorted by
// alias, for the TUI's server picker. An unconfigured snatcher lists nothing
// rather than failing, and no secret store is touched.
func Servers(cmd *cobra.Command) func() ([]ServerEntry, error) {
	return func() ([]ServerEntry, error) {
		cfg, configured, err := loadConfig(configFlag(cmd))
		if err != nil {
			return nil, err
		}

		if !configured {
			return []ServerEntry{}, nil
		}

		names := cfg.Names()
		out := make([]ServerEntry, 0, len(names))

		for _, name := range names {
			out = append(out, ServerEntry{Name: name, URL: cfg.Servers[name].URL, Default: name == cfg.DefaultServer})
		}

		return out, nil
	}
}

// ClientSettings is the appearance and polling configuration the TUI needs,
// resolved for one invocation. It is deliberately a plain value: internal/tui
// receives settings, not a *cobra.Command and not a config.Config.
type ClientSettings struct {
	// Theme is the resolved color theme name: a builtin, or a theme file in the
	// themes directory.
	Theme string

	// ColorDepth is the resolved client.color_depth, one of colorDepths.
	ColorDepth string

	// Warnings describes each theme file that was skipped because it does not
	// parse. A broken file is only an error when it is the selected theme;
	// otherwise the launcher prints these and carries on.
	Warnings []string

	// NerdFont reports whether Nerd Font glyphs may be used for icons.
	NerdFont bool

	// Mouse reports whether the TUI responds to mouse clicks and the wheel.
	Mouse bool

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

	name, err := clientSetting[string](cmd, cfg, keyTheme)
	if err != nil {
		return ClientSettings{}, err
	}

	nerdFont, err := clientSetting[bool](cmd, cfg, keyNerdFont)
	if err != nil {
		return ClientSettings{}, err
	}

	mouse, err := clientSetting[bool](cmd, cfg, keyMouse)
	if err != nil {
		return ClientSettings{}, err
	}

	depth, err := clientSetting[string](cmd, cfg, keyColorDepth)
	if err != nil {
		return ClientSettings{}, err
	}

	themes, err := themeRegistry(configFlag(cmd))
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

	return ClientSettings{
		Theme:        name,
		ColorDepth:   depth,
		NerdFont:     nerdFont,
		Mouse:        mouse,
		PollInterval: poll,
		NoColor:      noColor(),
		Warnings:     themes.Warnings(),
	}, nil
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
	keyTheme:      {flag: "--" + flagTheme, env: envTheme},
	keyNerdFont:   {flag: "--" + flagNoNerdFont, env: envNoNerdFont},
	keyColorDepth: {flag: "--" + flagColorDepth, env: envColorDepth},
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
// and the themes directory are read fresh on every call, so a value written by
// Set, or a theme file added mid-session, is reflected by the next Entries.
func (s ConfigStore) Entries() ([]ClientEntry, error) {
	cfg, _, err := loadConfig(s.path)
	if err != nil {
		return nil, err
	}

	resolved, err := clientEntries(s.cmd, cfg)
	if err != nil {
		return nil, err
	}

	themes, err := themeRegistry(s.path)
	if err != nil {
		return nil, err
	}

	out := make([]ClientEntry, len(resolved))
	for i, e := range resolved {
		entry := ClientEntry{Key: e.Key, Value: e.Value, Source: e.Source}

		switch e.Key {
		case keyTheme:
			entry.Allowed = themes.Names()
		case keyColorDepth:
			entry.Allowed = slices.Clone(colorDepths)
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

	return validateClientValue(def, s.path, value)
}

// Set validates value and, only if it is acceptable, writes it to the config
// file. An invalid value leaves the file untouched.
func (s ConfigStore) Set(key string, value any) error {
	def, err := lookupClientKey(key)
	if err != nil {
		return err
	}

	if err := validateClientValue(def, s.path, value); err != nil {
		return err
	}

	_, err = writeClientKey(def, s.path, value)

	return err
}

// ThemeLookup returns the TUI's theme lookup: the builtins plus the theme files
// beside the config file this invocation reads. The directory is re-read on
// every call, so a theme file saved mid-session applies at the next re-theme.
func ThemeLookup(cmd *cobra.Command) func(name string) (theme.Spec, error) {
	path := configFlag(cmd)

	return func(name string) (theme.Spec, error) {
		reg, err := themeRegistry(path)
		if err != nil {
			return theme.Spec{}, err
		}

		return reg.Lookup(name)
	}
}
