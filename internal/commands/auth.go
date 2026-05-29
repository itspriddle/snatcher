// Package commands holds snatcher's CLI command groups, one file per group.
// Commands depend only on the vendor-neutral torrent.Backend (built by the
// shared dial helper), the config/secrets/output packages, and cobra; they
// never import a concrete client directly.
package commands

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/itspriddle/snatcher/internal/config"
	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/secrets"
	"github.com/itspriddle/snatcher/internal/torrent"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// NewAuthCmd builds the "auth" parent command and its set/show/test/remove
// subcommands. Each subcommand carries its own --config flag (so version,
// completion, and help never see it).
func NewAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "auth",
		Short:         "Manage server authentication",
		Long:          "Configure and verify the connection to your Transmission server.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(
		newAuthSetCmd(),
		newAuthShowCmd(),
		newAuthTestCmd(),
		newAuthRemoveCmd(),
	)

	return cmd
}

// newAuthSetCmd builds "auth set", which stores the server URL and username in
// config and the password in the OS keyring. The password is read from a hidden
// TTY prompt or, for scripting, from stdin via --password-stdin.
func newAuthSetCmd() *cobra.Command {
	var (
		configPath    string
		serverURL     string
		username      string
		passwordStdin bool
	)

	cmd := &cobra.Command{
		Use:           "set",
		Short:         "Set server URL, username, and password",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if serverURL == "" {
				return errors.New("--url is required")
			}
			if err := validateURL(serverURL); err != nil {
				return err
			}

			password, err := readPassword(cmd, passwordStdin)
			if err != nil {
				return err
			}

			// When rekeying to a different server URL, drop the old URL's keyring
			// entry so no orphaned secret is left behind. A missing entry is fine.
			existing, _, err := loadConfig(configPath)
			if err != nil {
				return err
			}
			if existing.ServerURL != "" && existing.ServerURL != serverURL {
				if err := secrets.Delete(existing.ServerURL); err != nil && !errors.Is(err, secrets.ErrNotFound) {
					return err
				}
			}

			if err := config.Save(config.Config{ServerURL: serverURL, Username: username}, configPath); err != nil {
				return err
			}

			if err := secrets.Set(serverURL, password); err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Saved credentials for %s\n", serverURL)

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().StringVar(&serverURL, "url", "", "Transmission RPC URL (e.g. http://host:9091/transmission/rpc)")
	cmd.Flags().StringVar(&username, "username", "", "username for HTTP Basic Auth")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the password from stdin instead of a prompt")

	return cmd
}

// authShowResult is the JSON shape emitted by "auth show". The password is never
// included; only whether a keyring secret exists.
type authShowResult struct {
	ServerURL    string `json:"server_url"`
	Username     string `json:"username"`
	ConfigPath   string `json:"config_path"`
	SecretStored bool   `json:"secret_stored"`
	Configured   bool   `json:"configured"`
}

// newAuthShowCmd builds "auth show", which prints the configured server URL,
// username, resolved config path, and whether a keyring secret exists. It never
// reveals the secret value.
func newAuthShowCmd() *cobra.Command {
	var (
		configPath string
		asJSON     bool
	)

	cmd := &cobra.Command{
		Use:           "show",
		Short:         "Show the current authentication settings",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolvedPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}

			cfg, configured, err := loadConfig(configPath)
			if err != nil {
				return err
			}

			secretStored := false
			if cfg.ServerURL != "" {
				if _, err := secrets.Get(cfg.ServerURL); err == nil {
					secretStored = true
				} else if !errors.Is(err, secrets.ErrNotFound) {
					return err
				}
			}

			result := authShowResult{
				ServerURL:    cfg.ServerURL,
				Username:     cfg.Username,
				ConfigPath:   resolvedPath,
				SecretStored: secretStored,
				Configured:   configured,
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), result)
			}

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Server URL:    %s\n", orNotSet(result.ServerURL))
			_, _ = fmt.Fprintf(out, "Username:      %s\n", orNotSet(result.Username))
			_, _ = fmt.Fprintf(out, "Config path:   %s\n", result.ConfigPath)
			_, _ = fmt.Fprintf(out, "Secret stored: %s\n", yesNo(result.SecretStored))

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")

	return cmd
}

// authTestResult is the JSON shape emitted by "auth test" on success.
type authTestResult struct {
	Connected        bool   `json:"connected"`
	ServerURL        string `json:"server_url"`
	Version          string `json:"version"`
	Protocol         string `json:"protocol"`
	RPCVersionSemver string `json:"rpc_version_semver"`
}

// newAuthTestCmd builds "auth test", which dials the backend and performs a real
// ServerInfo round-trip, printing the connection status, server version,
// negotiated protocol, and rpc semver. It exits non-zero on failure.
func newAuthTestCmd() *cobra.Command {
	var (
		configPath string
		asJSON     bool
	)

	cmd := &cobra.Command{
		Use:           "test",
		Short:         "Test the connection to the configured server",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			backend, serverURL, err := dial(configPath)
			if err != nil {
				return err
			}

			info, err := backend.ServerInfo(cmd.Context())
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			result := authTestResult{
				Connected:        true,
				ServerURL:        serverURL,
				Version:          info.Version,
				Protocol:         info.Protocol,
				RPCVersionSemver: info.RPCVersionSemver,
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), result)
			}

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Connected to %s\n", serverURL)
			_, _ = fmt.Fprintf(out, "Version:      %s\n", result.Version)
			_, _ = fmt.Fprintf(out, "Protocol:     %s\n", result.Protocol)
			_, _ = fmt.Fprintf(out, "RPC version:  %s\n", result.RPCVersionSemver)

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")

	return cmd
}

// newAuthRemoveCmd builds "auth remove", which deletes the keyring secret and
// clears the credentials from config.
func newAuthRemoveCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:           "remove",
		Short:         "Remove stored credentials",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := loadConfig(configPath)
			if err != nil {
				return err
			}

			if cfg.ServerURL != "" {
				if err := secrets.Delete(cfg.ServerURL); err != nil && !errors.Is(err, secrets.ErrNotFound) {
					return err
				}
			}

			if err := config.Save(config.Config{}, configPath); err != nil {
				return err
			}

			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Removed stored credentials")

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")

	return cmd
}

// dial loads config (honoring configPath), reads the password from the keyring,
// and constructs the Transmission backend as a torrent.Backend. It returns the
// backend and the resolved server URL. It is the single seam where commands
// cross into a concrete backend.
func dial(configPath string) (torrent.Backend, string, error) {
	cfg, configured, err := loadConfig(configPath)
	if err != nil {
		return nil, "", err
	}

	if !configured || cfg.ServerURL == "" {
		return nil, "", errors.New("not configured: run 'snatcher auth set' first")
	}

	password, err := secrets.Get(cfg.ServerURL)
	if err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return nil, "", err
	}

	backend := transmission.New(cfg.ServerURL, transmission.WithBasicAuth(cfg.Username, password))

	return backend, cfg.ServerURL, nil
}

// loadConfig loads the config at configPath (or the default path) and reports
// whether a config file was present. A missing file is not an error: it yields a
// zero-value Config with configured=false.
func loadConfig(configPath string) (config.Config, bool, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		if errors.Is(err, config.ErrNotConfigured) {
			return cfg, false, nil
		}
		return config.Config{}, false, err
	}

	return cfg, true, nil
}

// resolveConfigPath returns the path config will read, honoring an explicit
// --config value and otherwise the default path.
func resolveConfigPath(configPath string) (string, error) {
	if configPath != "" {
		return configPath, nil
	}

	return config.DefaultPath()
}

// readPassword obtains the password either from stdin (when passwordStdin is
// set, for scripting) or from a hidden TTY prompt. The trailing newline of a
// piped password is trimmed; the prompt path uses term.ReadPassword so the input
// never echoes.
func readPassword(cmd *cobra.Command, passwordStdin bool) (string, error) {
	if passwordStdin {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("reading password from stdin: %w", err)
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}

	in, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(in.Fd())) {
		return "", errors.New("no terminal available for password prompt; use --password-stdin")
	}

	_, _ = fmt.Fprint(cmd.OutOrStdout(), "Password: ")
	data, err := term.ReadPassword(int(in.Fd()))
	_, _ = fmt.Fprintln(cmd.OutOrStdout())
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}

	return string(data), nil
}

// validateURL rejects a server URL that is not an absolute http(s) URL.
func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid --url %q: %w", raw, err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid --url %q: scheme must be http or https", raw)
	}

	if u.Host == "" {
		return fmt.Errorf("invalid --url %q: missing host", raw)
	}

	// Embedded userinfo (e.g. https://user:pass@host) would persist credentials
	// into config.json; passwords must live only in the OS keyring.
	if u.User != nil {
		return fmt.Errorf("invalid --url %q: must not contain embedded credentials (user:pass@); set --username and the password separately", raw)
	}

	return nil
}

// orNotSet returns s, or "(not set)" when s is empty, for plaintext output.
func orNotSet(s string) string {
	if s == "" {
		return "(not set)"
	}

	return s
}

// yesNo renders a bool as "yes"/"no" for plaintext output.
func yesNo(b bool) string {
	if b {
		return "yes"
	}

	return "no"
}
