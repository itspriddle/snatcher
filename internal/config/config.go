// Package config loads and persists snatcher's non-secret configuration.
//
// Config holds a map of named servers (each with a URL and username) plus an
// optional default server alias. Server selection (the --server flag /
// SNATCHER_SERVER env var) is resolved by callers via Resolve; the config file
// itself stores only the inventory of servers and the default.
//
// Secrets (passwords) are never stored here — they live in the OS keyring (see
// internal/secrets).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
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
// server. The password is never stored here; it lives in the OS keyring keyed
// by URL (see internal/secrets).
type Server struct {
	URL      string `json:"url"`
	Username string `json:"username"`
}

// Config holds snatcher's non-secret configuration: the set of named servers
// and which one to use by default.
type Config struct {
	Servers       map[string]Server `json:"servers"`
	DefaultServer string            `json:"default_server"`

	// ServerURL and Username are transitional convenience fields. They are not
	// persisted in the new schema (Save migrates them into Servers) and are
	// populated on Load from the resolved default server so existing callers
	// keep working until they move to Resolve. New code should use Servers /
	// Resolve instead.
	ServerURL string `json:"-"`
	Username  string `json:"-"`
}

// onDisk is the persisted JSON shape. It is decoded separately so the legacy
// flat fields can be recovered for migration without polluting Config's tags.
type onDisk struct {
	Servers       map[string]Server `json:"servers"`
	DefaultServer string            `json:"default_server"`

	// Legacy single-server fields, read only for migration.
	LegacyServerURL string `json:"server_url"`
	LegacyUsername  string `json:"username"`
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
// Legacy single-server config files (a flat server_url/username pair with no
// servers map) are migrated transparently into a single named server keyed by
// the URL host, set as the default. A default_server that names a missing
// server is reported as an error.
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

	var disk onDisk
	if err := json.Unmarshal(raw, &disk); err != nil {
		return Config{}, fmt.Errorf("config: parsing %q: %w", path, err)
	}

	cfg := Config{
		Servers:       disk.Servers,
		DefaultServer: disk.DefaultServer,
	}

	migrateLegacy(&cfg, disk.LegacyServerURL, disk.LegacyUsername)

	if err := validate(cfg); err != nil {
		return Config{}, err
	}

	applyDefaultShim(&cfg)

	return cfg, nil
}

// Save persists cfg to path (or the default path when path is empty), creating
// parent directories at 0700 and enforcing file mode 0600. It writes only the
// new schema (servers + default_server); the legacy flat server_url/username
// keys are never written. No secret is ever written; Config holds only
// non-secret fields.
//
// As a transitional convenience, a Config built with only the legacy
// ServerURL/Username fields (and no Servers) is migrated into a single named
// server before writing.
func Save(cfg Config, path string) error {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return err
		}
		path = p
	}

	normalizeForSave(&cfg)

	if err := validate(cfg); err != nil {
		return err
	}

	servers := cfg.Servers
	if servers == nil {
		servers = map[string]Server{}
	}

	disk := struct {
		Servers       map[string]Server `json:"servers"`
		DefaultServer string            `json:"default_server"`
	}{
		Servers:       servers,
		DefaultServer: cfg.DefaultServer,
	}

	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encoding: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("config: creating config directory: %w", err)
	}

	if err := os.WriteFile(path, data, filePerm); err != nil {
		return fmt.Errorf("config: writing %q: %w", path, err)
	}

	// WriteFile honors filePerm only when creating the file; enforce the mode on
	// an existing file too.
	if err := os.Chmod(path, filePerm); err != nil {
		return fmt.Errorf("config: setting permissions on %q: %w", path, err)
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

// migrateLegacy promotes a legacy flat server_url/username pair into the new
// servers map when the file predates the multi-server schema (a non-empty
// legacy URL and no servers). The migrated server is keyed by the lowercased
// URL host and becomes the default.
func migrateLegacy(cfg *Config, legacyURL, legacyUser string) {
	if legacyURL == "" || len(cfg.Servers) > 0 {
		return
	}

	alias := HostAlias(legacyURL)

	cfg.Servers = map[string]Server{
		alias: {URL: legacyURL, Username: legacyUser},
	}
	cfg.DefaultServer = alias
}

// normalizeForSave migrates a Config built with only the transitional flat
// ServerURL/Username fields into the servers map so callers that have not yet
// moved to the new shape still persist a valid new-schema file.
func normalizeForSave(cfg *Config) {
	if len(cfg.Servers) > 0 || cfg.ServerURL == "" {
		return
	}

	alias := HostAlias(cfg.ServerURL)

	cfg.Servers = map[string]Server{
		alias: {URL: cfg.ServerURL, Username: cfg.Username},
	}
	if cfg.DefaultServer == "" {
		cfg.DefaultServer = alias
	}
}

// applyDefaultShim populates the transitional ServerURL/Username fields from the
// default server so existing callers that read them keep working. It is a no-op
// when no default is set.
func applyDefaultShim(cfg *Config) {
	if cfg.DefaultServer == "" {
		return
	}

	if srv, ok := cfg.Servers[cfg.DefaultServer]; ok {
		cfg.ServerURL = srv.URL
		cfg.Username = srv.Username
	}
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
// raw string when the URL cannot be parsed, so a server is never dropped during
// migration. It is exported so the "auth set --name" default matches the
// migration rule.
func HostAlias(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.ToLower(raw)
	}

	return strings.ToLower(u.Host)
}
