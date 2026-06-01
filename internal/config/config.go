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
// and which one to use by default. It is also the persisted JSON shape: the
// file decodes directly into a Config.
type Config struct {
	Servers       map[string]Server `json:"servers"`
	DefaultServer string            `json:"default_server"`
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
// parent directories at 0700 and enforcing file mode 0600. No secret is ever
// written; Config holds only non-secret fields.
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
