// Package config loads and persists snatcher's non-secret configuration.
//
// Configuration is resolved with layered precedence (flag > env > file >
// default) via an isolated spf13/viper instance. Secrets (passwords) are never
// stored here — they live in the OS keyring (see internal/secrets).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// ErrNotConfigured signals that no config file was found at the resolved path.
// Callers receive a zero-value Config alongside this error and can treat it as
// a "first run" rather than a hard failure.
var ErrNotConfigured = errors.New("config: not configured")

const (
	// envPrefix namespaces environment variables, e.g. SNATCHER_SERVER_URL.
	envPrefix = "SNATCHER"

	// configType is the on-disk config file format.
	configType = "json"

	// dirPerm is the permission for created parent directories.
	dirPerm = 0o700

	// filePerm is the permission enforced on the saved config file.
	filePerm = 0o600
)

// Config holds snatcher's non-secret configuration. It is intentionally small
// and extensible; new fields gain snake_case mapstructure and json tags.
type Config struct {
	ServerURL string `mapstructure:"server_url" json:"server_url"`
	Username  string `mapstructure:"username" json:"username"`
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

// Load reads configuration with layered precedence (env > file > default) using
// an isolated viper instance. When path is empty, the default path is used;
// otherwise path overrides it.
//
// A missing config file is not an error: Load returns a zero-value Config and
// ErrNotConfigured so first-run callers can detect the "not configured" state
// without a hard failure.
func Load(path string) (Config, error) {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return Config{}, err
		}
		path = p
	}

	v := newViper()
	v.SetConfigFile(path)

	var cfg Config

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) || errors.Is(err, os.ErrNotExist) {
			// No file yet: env/defaults still apply, but signal "not configured".
			if unErr := v.Unmarshal(&cfg); unErr != nil {
				return Config{}, fmt.Errorf("config: unmarshaling: %w", unErr)
			}
			return cfg, ErrNotConfigured
		}
		return Config{}, fmt.Errorf("config: reading %q: %w", path, err)
	}

	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: unmarshaling: %w", err)
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

	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("config: creating config directory: %w", err)
	}

	v := newViper()
	v.SetConfigFile(path)
	v.Set("server_url", cfg.ServerURL)
	v.Set("username", cfg.Username)

	if err := v.WriteConfigAs(path); err != nil {
		return fmt.Errorf("config: writing %q: %w", path, err)
	}

	if err := os.Chmod(path, filePerm); err != nil {
		return fmt.Errorf("config: setting permissions on %q: %w", path, err)
	}

	return nil
}

// newViper builds an isolated viper instance (never the global singleton) wired
// for JSON config, SNATCHER_* env overrides, and zero-value defaults.
func newViper() *viper.Viper {
	v := viper.New()
	v.SetConfigType(configType)

	v.SetDefault("server_url", "")
	v.SetDefault("username", "")

	v.SetEnvPrefix(envPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	return v
}
