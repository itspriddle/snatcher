// Package config loads and persists snatcher's non-secret configuration.
//
// Config holds a map of named servers (each with a URL and username), an
// optional default server alias, and a client section holding snatcher's own
// settings (theme, icon set, poll cadence, list defaults). Server selection (the
// --server flag / SNATCHER_SERVER env var) is resolved by callers via Resolve;
// the config file itself stores only the inventory of servers and the default.
//
// The file is operator-owned and hand-editable, so Load and Save preserve keys
// this package does not model — both at the top level and inside the client
// object — rather than dropping them on the next write. Save rewrites the file
// with its keys in sorted order, and a modeled key in its canonical spelling:
// a hand-edited "Theme" is read as theme and written back as "theme", never as
// both.
//
// Secrets (passwords) are never stored here — they live in whichever secret
// store the server names (see internal/secrets). This file records only that
// store's name, in the per-server secret_store field.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNotConfigured signals that no config file was found at the resolved path.
// Callers receive a zero-value Config alongside this error and can treat it as
// a "first run" rather than a hard failure.
var ErrNotConfigured = errors.New("config: not configured")

// ErrUnknownServer signals that a requested server alias is not configured.
var ErrUnknownServer = errors.New("config: unknown server")

// ErrNoDefaultServer signals that no server alias was requested and no default
// server is configured to fall back to.
var ErrNoDefaultServer = errors.New("config: no default server")

const (
	// dirPerm is the permission for created parent directories.
	dirPerm = 0o700

	// filePerm is the permission enforced on the saved config file.
	filePerm = 0o600
)

// Server holds the non-secret connection details for one named Transmission
// server. The password is never stored here; it lives in a secret store keyed
// by URL (see internal/secrets).
//
// SecretStore names which store holds that password. It is written by
// "server add" — always the store that add actually wrote to — and read back by
// every command that needs the password, so a store is chosen once rather than
// re-passed. An empty value means the
// default store, so a config file written before the field existed loads
// unchanged; the valid values and their precedence are owned by the command
// layer, not by this package.
type Server struct {
	URL         string `json:"url"`
	Username    string `json:"username"`
	SecretStore string `json:"secret_store,omitempty"`
}

// Client holds snatcher's own settings — how the CLI and TUI behave, as opposed
// to which server they talk to. It is persisted as the top-level "client"
// object.
//
// Every field is a pointer so an explicitly-set zero value ("nerd_font": false)
// stays distinguishable from an absent key. That distinction is the whole point:
// an absent key means "use the default", which is what lets a key be unset again
// after it has been set.
//
// Values are stored, never interpreted. This package does not check that Theme
// names a known theme, that PollInterval parses as a duration, or that
// ListColumns names real columns — a hand-edit that is structurally valid JSON
// always loads, so the CLI can be used to repair it. Valid values and their
// precedence are owned by the command layer, exactly as for the per-server
// SecretStore field. PollInterval is kept as its raw string for that reason.
type Client struct {
	Theme        *string   `json:"theme,omitempty"`
	NerdFont     *bool     `json:"nerd_font,omitempty"`
	ColorDepth   *string   `json:"color_depth,omitempty"`
	PollInterval *string   `json:"poll_interval,omitempty"`
	ListColumns  *[]string `json:"list_columns,omitempty"`
	ListSort     *string   `json:"list_sort,omitempty"`
	ListReverse  *bool     `json:"list_reverse,omitempty"`
	Mouse        *bool     `json:"mouse,omitempty"`

	// unknown holds keys inside the client object that this package does not
	// model, so a hand-edited or newer-version key survives a Load/Save round
	// trip instead of being silently dropped.
	unknown map[string]json.RawMessage
}

// clientKeys is every key inside the client object that Client models, in its
// canonical spelling. Anything else found there — matched case-insensitively,
// see unknownKeys — is preserved verbatim in Client.unknown.
var clientKeys = []string{
	"theme", "nerd_font", "color_depth", "poll_interval", "list_columns", "list_sort", "list_reverse",
	"mouse",
}

// IsZero reports whether the client section holds nothing at all — no set key
// and no preserved unknown key. Save omits the "client" object entirely in that
// case, so a config file written by a version that predates the section stays
// byte-identical after a round trip.
func (c Client) IsZero() bool {
	return c.Theme == nil &&
		c.NerdFont == nil &&
		c.ColorDepth == nil &&
		c.PollInterval == nil &&
		c.ListColumns == nil &&
		c.ListSort == nil &&
		c.ListReverse == nil &&
		c.Mouse == nil &&
		len(c.unknown) == 0
}

// unknownKeys decodes data as a JSON object and returns the keys that modeled
// does not cover, or nil when it covers them all.
//
// Matching is case-insensitive because encoding/json is: it falls back to a
// case-insensitive field match, so a hand-edited "Theme" is decoded into the
// Theme field. Preserving that key as unknown as well would write it back
// beside the canonical "theme" on the next Save — two copies of one setting,
// and an "unset" that changes nothing because the shadow copy loads again.
func unknownKeys(data []byte, modeled []string) (map[string]json.RawMessage, error) {
	var rest map[string]json.RawMessage
	if err := json.Unmarshal(data, &rest); err != nil {
		return nil, err
	}

	known := make(map[string]struct{}, len(modeled))
	for _, key := range modeled {
		known[strings.ToLower(key)] = struct{}{}
	}

	for key := range rest {
		if _, ok := known[strings.ToLower(key)]; ok {
			delete(rest, key)
		}
	}

	if len(rest) == 0 {
		return nil, nil
	}

	return rest, nil
}

// UnmarshalJSON decodes the modeled keys and stashes every other key so Save can
// write it back.
func (c *Client) UnmarshalJSON(data []byte) error {
	// plain drops the methods, so unmarshaling into it does not recurse.
	type plain Client

	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = Client(decoded)

	rest, err := unknownKeys(data, clientKeys)
	if err != nil {
		return err
	}
	c.unknown = rest

	return nil
}

// MarshalJSON writes the set keys alongside the unknown keys captured on load. A
// nil field is written as an absent key, not as null, so unsetting a key removes
// it from the file.
func (c Client) MarshalJSON() ([]byte, error) {
	obj := make(map[string]json.RawMessage, len(c.unknown)+len(clientKeys))
	maps.Copy(obj, c.unknown)

	if err := errors.Join(
		putField(obj, "theme", c.Theme),
		putField(obj, "nerd_font", c.NerdFont),
		putField(obj, "color_depth", c.ColorDepth),
		putField(obj, "poll_interval", c.PollInterval),
		putField(obj, "list_columns", c.ListColumns),
		putField(obj, "list_sort", c.ListSort),
		putField(obj, "list_reverse", c.ListReverse),
		putField(obj, "mouse", c.Mouse),
	); err != nil {
		return nil, err
	}

	return json.Marshal(obj)
}

// putField encodes one optional client key into obj, deleting the key when the
// field is unset. Deleting matters as much as writing: it is what stops an
// unset key from being resurrected out of the preserved unknown keys.
func putField[T any](obj map[string]json.RawMessage, key string, value *T) error {
	if value == nil {
		delete(obj, key)
		return nil
	}

	raw, err := json.Marshal(*value)
	if err != nil {
		return fmt.Errorf("config: encoding client.%s: %w", key, err)
	}
	obj[key] = raw

	return nil
}

// Config holds snatcher's non-secret configuration: the set of named servers,
// which one to use by default, and snatcher's own client settings. It is also
// the persisted JSON shape: the file decodes directly into a Config.
type Config struct {
	Servers       map[string]Server `json:"servers"`
	DefaultServer string            `json:"default_server"`
	Client        Client            `json:"client"`

	// unknown holds top-level keys this package does not model, preserved across
	// a Load/Save round trip for the same reason as Client.unknown.
	unknown map[string]json.RawMessage
}

// configKeys is every top-level key Config models, in its canonical spelling.
// Anything else is preserved in Config.unknown (see unknownKeys).
var configKeys = []string{"servers", "default_server", "client"}

// UnmarshalJSON decodes the modeled top-level keys and stashes every other one
// so Save can write it back.
func (c *Config) UnmarshalJSON(data []byte) error {
	// plain drops the methods, so unmarshaling into it does not recurse.
	type plain Config

	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = Config(decoded)

	rest, err := unknownKeys(data, configKeys)
	if err != nil {
		return err
	}
	c.unknown = rest

	return nil
}

// MarshalJSON writes the modeled top-level keys alongside the unknown keys
// captured on load. An empty client section is omitted entirely.
func (c Config) MarshalJSON() ([]byte, error) {
	obj := make(map[string]json.RawMessage, len(c.unknown)+len(configKeys))
	maps.Copy(obj, c.unknown)

	servers := c.Servers
	if servers == nil {
		servers = map[string]Server{}
	}

	rawServers, err := json.Marshal(servers)
	if err != nil {
		return nil, fmt.Errorf("config: encoding servers: %w", err)
	}
	obj["servers"] = rawServers

	rawDefault, err := json.Marshal(c.DefaultServer)
	if err != nil {
		return nil, fmt.Errorf("config: encoding default_server: %w", err)
	}
	obj["default_server"] = rawDefault

	if c.Client.IsZero() {
		delete(obj, "client")
	} else {
		rawClient, clientErr := json.Marshal(c.Client)
		if clientErr != nil {
			return nil, fmt.Errorf("config: encoding client: %w", clientErr)
		}
		obj["client"] = rawClient
	}

	return json.Marshal(obj)
}

// DefaultPath returns the default config file path. It resolves to
// $XDG_CONFIG_HOME/snatcher/config.json when XDG_CONFIG_HOME is set, otherwise
// ~/.config/snatcher/config.json. This is identical on macOS and Linux; we do
// not use os.UserConfigDir() because its behavior diverges across platforms.
func DefaultPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "snatcher", "config.json"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: resolving home directory: %w", err)
	}

	return filepath.Join(home, ".config", "snatcher", "config.json"), nil
}

// Load reads the config file. When path is empty, the default path is used;
// otherwise path overrides it.
//
// A missing config file is not an error: Load returns a zero-value Config and
// ErrNotConfigured so first-run callers can detect the "not configured" state
// without a hard failure.
//
// A default_server that names a missing server is reported as an error.
//
// Client values are not checked for meaning: a structurally valid file always
// loads, so an operator who hand-edits a nonsense theme can still fix it with
// the CLI instead of a text editor. Malformed JSON, and a value whose JSON type
// does not match its field, remain hard errors.
func Load(path string) (Config, error) {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return Config{}, err
		}
		path = p
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, ErrNotConfigured
		}
		return Config{}, fmt.Errorf("config: reading %q: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("config: parsing %q: %w", path, err)
	}

	if err := validate(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// Save persists cfg to path (or the default path when path is empty), creating
// parent directories at 0700 and enforcing file mode 0600. The write is atomic:
// see writeAtomic. No secret is ever written; Config holds only non-secret
// fields.
func Save(cfg Config, path string) error {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return err
		}
		path = p
	}

	if err := validate(cfg); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encoding: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("config: creating config directory: %w", err)
	}

	return writeAtomic(path, data)
}

// writeAtomic writes data to a temp file in the target's own directory, then
// renames it over the target. An interrupted or failed write therefore leaves
// the previous config.json intact rather than a truncated one, and a reader
// racing the write sees one version or the other, never a partial file.
//
// The temp file is created at 0600 and renamed, so the result is 0600 no matter
// how loose the replaced file's permissions were. Staying in the same directory
// is what keeps the rename atomic: across filesystems it would be a copy.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return fmt.Errorf("config: creating temp file in %q: %w", dir, err)
	}

	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		// A no-op once the rename below has succeeded; the cleanup that matters
		// is the one on every early return.
		_ = os.Remove(tmpName)
	}()

	// CreateTemp already creates at 0600, but say so explicitly: this is the
	// mode the renamed file inherits.
	if err := tmp.Chmod(filePerm); err != nil {
		return fmt.Errorf("config: setting permissions on %q: %w", tmpName, err)
	}

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("config: writing %q: %w", tmpName, err)
	}

	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("config: syncing %q: %w", tmpName, err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: closing %q: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: replacing %q: %w", path, err)
	}

	return nil
}

// Resolve returns the requested server. When name is empty it returns the
// configured default server. It returns ErrUnknownServer for an unknown name
// and ErrNoDefaultServer when name is empty and no default is set.
func Resolve(cfg Config, name string) (string, Server, error) {
	if name == "" {
		if cfg.DefaultServer == "" {
			return "", Server{}, ErrNoDefaultServer
		}
		name = cfg.DefaultServer
	}

	srv, ok := cfg.Servers[name]
	if !ok {
		return "", Server{}, fmt.Errorf("%w: %q", ErrUnknownServer, name)
	}

	return name, srv, nil
}

// Names returns the configured server aliases sorted ascending.
func (c Config) Names() []string {
	names := make([]string, 0, len(c.Servers))
	for name := range c.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	return names
}

// validate guards against a default_server that names a server which is not
// present in the servers map (e.g. after a hand-edit).
func validate(cfg Config) error {
	if cfg.DefaultServer == "" || len(cfg.Servers) == 0 {
		return nil
	}

	if _, ok := cfg.Servers[cfg.DefaultServer]; !ok {
		return fmt.Errorf("config: default_server %q does not match any configured server", cfg.DefaultServer)
	}

	return nil
}

// HostAlias derives a server alias from a URL: the lowercased host, including
// the port when present (e.g. "localhost:9091"), so servers that share a
// hostname but differ by port do not collide. It falls back to the lowercased
// raw string when the URL cannot be parsed, so an alias is always produced. It
// is the default alias used by "server add" when --name is omitted.
func HostAlias(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.ToLower(raw)
	}

	return strings.ToLower(u.Host)
}
