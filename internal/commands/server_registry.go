// Package commands holds snatcher's CLI command groups, one file per group.
// Commands depend on the concrete *transmission.Client (built by the shared dial
// helper), the config/secrets/output packages, and cobra.
package commands

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/itspriddle/snatcher/internal/config"
	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/secrets"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// storeUnavailableRemedy names the fix in every unavailable-store warning. It
// deliberately names no provider internals: an operator needs to know that no
// OS secret service answered, not which helper binary the provider looked for.
const storeUnavailableRemedy = "Install or start an OS secret service, or keep passwords in a plaintext file at mode 0600 with 'server add --store=file' (or SNATCHER_SECRET_STORE=file)."

// storeUnavailableWarned guards warnStoreUnavailableOnce, so the warning dial
// emits fires once per process rather than once per call. A future polling
// client dials repeatedly and must not spam stderr.
var storeUnavailableWarned atomic.Bool

// warnStoreUnavailable writes a one-line stderr note that no OS secret store
// could be reached, what that means for the command in hand (consequence), and
// how to fix it.
func warnStoreUnavailable(cmd *cobra.Command, consequence string) {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: the OS secret store could not be reached; %s. %s\n", consequence, storeUnavailableRemedy)
}

// warnStoreUnavailableOnce is warnStoreUnavailable, emitted at most once for
// the lifetime of the process.
func warnStoreUnavailableOnce(cmd *cobra.Command, consequence string) {
	if storeUnavailableWarned.CompareAndSwap(false, true) {
		warnStoreUnavailable(cmd, consequence)
	}
}

// secretAbsent reports whether a secrets error means there is no stored secret
// to act on: either the store answered and holds no entry for that URL, or
// there is no reachable store at all. Both leave a pre-clear with nothing to
// do, so callers continue instead of aborting.
func secretAbsent(err error) bool {
	return errors.Is(err, secrets.ErrNotFound) || errors.Is(err, secrets.ErrUnavailable)
}

// newServerAddCmd builds "server add", which stores the server URL and username
// in config and the password in a secret store. The password is read from a
// hidden TTY prompt or, for scripting, from stdin via --password-stdin.
//
// --store picks which store holds the password. Whichever store is resolved is
// both written to and persisted with the server, so later commands resolve the
// same store without re-passing the flag.
func newServerAddCmd() *cobra.Command {
	var (
		configPath    string
		name          string
		serverURL     string
		username      string
		passwordStdin bool
		storeFlag     string
	)

	cmd := &cobra.Command{
		Use:           "add",
		Short:         "Add or update a named server's URL, username, and password",
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

			// An omitted --name defaults to the lowercased URL host, matching the
			// legacy-migration rule so a single-server upgrade stays addressable by
			// the same alias.
			alias := name
			if alias == "" {
				alias = config.HostAlias(serverURL)
			}

			password, err := readPassword(cmd, passwordStdin)
			if err != nil {
				return err
			}

			cfg, _, err := loadConfig(configPath)
			if err != nil {
				return err
			}
			if cfg.Servers == nil {
				cfg.Servers = map[string]config.Server{}
			}

			prev := cfg.Servers[alias]

			// One mode decides both where the password is written and what is
			// saved with the server, so config can never name a store the
			// password did not go to.
			mode, err := addStoreMode(storeFlag, cmd.Flags().Changed("store"), prev)
			if err != nil {
				return err
			}

			srv := config.Server{URL: serverURL, Username: username, SecretStore: string(mode)}

			// Confirm the store can hold the password before anything is written.
			// A password with nowhere to go must leave config and every store
			// untouched rather than registering a server that silently has no
			// password.
			store, err := storeForPassword(mode, serverURL, password)
			if err != nil {
				return err
			}

			// When updating an existing alias to a different URL, drop the old URL's
			// secret so no orphaned entry is left behind. Scope this to the alias
			// being changed; other servers' secrets are untouched. A missing entry —
			// or no reachable store to hold one — is fine. The old URL's secret lives
			// in whichever store that entry named, which is not necessarily the one
			// the new entry will use.
			if prev.URL != "" && prev.URL != serverURL {
				prevStore, err := serverStore(prev)
				if err != nil {
					return err
				}

				if err := prevStore.Delete(prev.URL); err != nil && !secretAbsent(err) {
					return err
				}
			}

			// The secret is written before config, so a store that still refuses
			// the password after the probe leaves no half-saved server behind.
			if err := writeServerSecret(store, serverURL, password); err != nil {
				return err
			}

			cfg.Servers[alias] = srv

			// The first server registered when no default exists becomes the
			// default.
			if cfg.DefaultServer == "" {
				cfg.DefaultServer = alias
			}

			if err := config.Save(cfg, configPath); err != nil {
				return err
			}

			out := cmd.OutOrStdout()

			if password == "" {
				_, _ = fmt.Fprintf(out, "Saved server %q (%s) with no password stored\n", alias, serverURL)

				return nil
			}

			_, _ = fmt.Fprintf(out, "Saved credentials for server %q (%s)\n", alias, serverURL)

			// The file store is plaintext by design; say where it landed, so an
			// operator who opted in knows exactly what is on disk.
			if fileStore, ok := store.(*secrets.FileStore); ok {
				_, _ = fmt.Fprintf(out, "Password stored in plaintext at %s (mode 0600)\n", fileStore.Path())
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().StringVar(&name, "name", "", "server alias (default: the URL host)")
	cmd.Flags().StringVar(&serverURL, "url", "", "Transmission RPC URL (e.g. http://host:9091/transmission/rpc)")
	cmd.Flags().StringVar(&username, "username", "", "username for HTTP Basic Auth")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the password from stdin instead of a prompt")
	cmd.Flags().StringVar(&storeFlag, "store", "", `where to keep the password: "keyring" (default), "file" (plaintext, mode 0600), or "none"`)

	return cmd
}

// writeServerSecret persists password for serverURL in store.
//
// An empty password means "no stored secret" (e.g. a server with auth disabled).
// No empty entry is written; any existing one for this URL is dropped so a
// previously stored password isn't silently reused. An unreachable store is not
// a failure there: there is no password to store, and nothing can read a stale
// secret back out of a store that cannot be reached either.
func writeServerSecret(store secretStore, serverURL, password string) error {
	if password == "" {
		if err := store.Delete(serverURL); err != nil && !secretAbsent(err) {
			return err
		}

		return nil
	}

	return store.Set(serverURL, password)
}

// serverListResult is the JSON shape emitted for one server by "server list". It
// is a stable scripting contract: additive changes only. The password is never
// included; only whether a keyring secret exists.
type serverListResult struct {
	Name         string `json:"name"`
	ServerURL    string `json:"server_url"`
	Username     string `json:"username"`
	SecretStored bool   `json:"secret_stored"`
	IsDefault    bool   `json:"is_default"`
}

// newServerListCmd builds "server list", which enumerates every configured
// server with its alias, URL, username, whether a keyring secret is stored, and
// whether it is the default. Plaintext is an aligned table on a TTY and
// tab-separated, header-less rows when piped or NO_COLOR is set (the same
// contract as "torrent list"); --json and --jq emit a stable structured shape.
// Servers are listed sorted by alias.
func newServerListCmd() *cobra.Command {
	var (
		configPath string
		asJSON     bool
		jqExpr     string
	)

	cmd := &cobra.Command{
		Use:           "list",
		Aliases:       []string{"ls"},
		Short:         "List configured servers",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := loadConfig(configPath)
			if err != nil {
				return err
			}

			results, err := serverListResults(cmd, cfg)
			if err != nil {
				return err
			}

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), results, jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), results)
			}

			writeServerListPlaintext(cmd, results)

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// serverListResults builds the per-server result rows for "server list", sorted
// by alias, querying the keyring for whether each server's secret is stored.
//
// An unreachable secret store does not fail the listing: the rows are still
// emitted with secret_stored false and a single stderr note explains that the
// status could not be determined. Reporting false for "cannot tell" keeps the
// JSON and plaintext column shapes unchanged.
func serverListResults(cmd *cobra.Command, cfg config.Config) ([]serverListResult, error) {
	names := cfg.Names()
	results := make([]serverListResult, 0, len(names))

	storeUnavailable := false

	for _, name := range names {
		srv := cfg.Servers[name]

		secretStored := false
		if srv.URL != "" {
			store, err := serverStore(srv)
			if err != nil {
				return nil, err
			}

			_, err = store.Get(srv.URL)
			switch {
			case err == nil:
				secretStored = true
			case errors.Is(err, secrets.ErrUnavailable):
				storeUnavailable = true
			case !errors.Is(err, secrets.ErrNotFound):
				return nil, err
			}
		}

		results = append(results, serverListResult{
			Name:         name,
			ServerURL:    srv.URL,
			Username:     srv.Username,
			SecretStored: secretStored,
			IsDefault:    name == cfg.DefaultServer,
		})
	}

	if storeUnavailable {
		warnStoreUnavailable(cmd, `secret status could not be determined and is reported as "no"`)
	}

	return results, nil
}

// writeServerListPlaintext renders the server list in the appropriate plaintext
// mode: an aligned, headered table on a TTY, or plain tab-separated, header-less
// rows when piped or when NO_COLOR is set (the same contract as "torrent list").
// Server-derived strings (alias, URL, username) are sanitized of terminal
// control sequences here; the JSON/jq paths are never sanitized.
func writeServerListPlaintext(cmd *cobra.Command, results []serverListResult) {
	out := cmd.OutOrStdout()

	rows := make([][]string, len(results))
	for i, r := range results {
		rows[i] = []string{
			output.SanitizeTerminal(r.Name),
			output.SanitizeTerminal(r.ServerURL),
			output.SanitizeTerminal(r.Username),
			yesNo(r.SecretStored),
			yesNo(r.IsDefault),
		}
	}

	if isTTY(out) {
		writeServerListTable(out, rows)
		return
	}

	for _, row := range rows {
		_, _ = fmt.Fprintln(out, strings.Join(row, "\t"))
	}
}

// writeServerListTable writes a whitespace-aligned table with a header row. An
// empty server list writes a friendly "No servers configured." line.
func writeServerListTable(w io.Writer, rows [][]string) {
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(w, "No servers configured.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	_, _ = fmt.Fprintln(tw, strings.Join([]string{"NAME", "URL", "USERNAME", "SECRET", "DEFAULT"}, "\t"))
	for _, row := range rows {
		_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
	}

	_ = tw.Flush()
}

// newServerDefaultCmd builds "server default <alias>", which sets the configured
// default server to an existing alias and persists it. An unknown alias errors
// clearly, listing the valid names.
func newServerDefaultCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:           "default <alias>",
		Short:         "Set the default server",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := args[0]

			cfg, _, err := loadConfig(configPath)
			if err != nil {
				return err
			}

			if _, ok := cfg.Servers[alias]; !ok {
				names := strings.Join(cfg.Names(), ", ")
				return fmt.Errorf("unknown server %q: configured servers are %s", alias, names)
			}

			cfg.DefaultServer = alias

			if err := config.Save(cfg, configPath); err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Default server set to %q\n", alias)

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")

	return cmd
}

// serverPingResult is the JSON shape emitted by "server ping" on success.
type serverPingResult struct {
	Connected        bool   `json:"connected"`
	ServerURL        string `json:"server_url"`
	Version          string `json:"version"`
	Protocol         string `json:"protocol"`
	RPCVersionSemver string `json:"rpc_version_semver"`
}

// newServerPingCmd builds "server ping [alias]", which dials a server and
// performs a real ServerInfo round-trip, printing the connection status, server
// version, negotiated protocol, and rpc semver. It pings the named server, or
// the configured default when the alias is omitted. It exits non-zero on
// failure.
func newServerPingCmd() *cobra.Command {
	var (
		configPath string
		asJSON     bool
	)

	cmd := &cobra.Command{
		Use:           "ping [alias]",
		Short:         "Test the connection to a server",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, serverURL, err := dial(cmd, configPath, firstArg(args))
			if err != nil {
				return err
			}

			info, err := backend.ServerInfo(cmd.Context())
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			result := serverPingResult{
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

// newServerRemoveCmd builds "server remove [alias]", which removes a named server
// (or the configured default when the alias is omitted): it deletes the server's
// entry from config, deletes its keyring secret, and persists. When the removed
// server was the default, the default is repointed to the sole remaining server
// if exactly one remains, otherwise it is cleared.
func newServerRemoveCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:           "remove [alias]",
		Aliases:       []string{"rm"},
		Short:         "Remove a configured server",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := firstArg(args)

			cfg, configured, err := loadConfig(configPath)
			if err != nil {
				return err
			}

			if !configured || len(cfg.Servers) == 0 {
				return errors.New("no servers configured")
			}

			name, srv, err := config.Resolve(cfg, alias)
			if err != nil {
				return resolveServerError(cfg, alias, err)
			}

			// An unreachable store must not strand the config entry: the server is
			// removed either way, with a note that a secret may survive in a store
			// snatcher cannot reach to delete it from.
			if srv.URL != "" {
				store, err := serverStore(srv)
				if err != nil {
					return err
				}

				err = store.Delete(srv.URL)
				switch {
				case errors.Is(err, secrets.ErrUnavailable):
					warnStoreUnavailable(cmd, fmt.Sprintf("a stored password for %s may remain", output.SanitizeTerminal(srv.URL)))
				case err != nil && !errors.Is(err, secrets.ErrNotFound):
					return err
				}
			}

			delete(cfg.Servers, name)

			// When the removed server was the default, repoint it to the sole
			// remaining server if exactly one is left; otherwise clear it (the user
			// picks a new default with 'server default').
			if cfg.DefaultServer == name {
				cfg.DefaultServer = ""
				if remaining := cfg.Names(); len(remaining) == 1 {
					cfg.DefaultServer = remaining[0]
				}
			}

			if err := config.Save(cfg, configPath); err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed server %q (%s)\n", name, srv.URL)

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")

	return cmd
}

// firstArg returns the first positional argument, or the empty string when none
// was given. Server subcommands that take an optional [alias] use it so an
// omitted alias resolves to the configured default.
func firstArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}

	return ""
}

// dial loads config (honoring configPath), resolves which named server to use,
// reads that server's password from the store the server resolves to, and
// constructs the concrete *transmission.Client. It returns the client and the
// resolved server URL. It is the single seam where commands construct the
// client. cmd supplies the stderr stream for the unavailable-store warning.
//
// Server selection precedence is: the server argument (the --server flag) >
// the SNATCHER_SERVER environment variable > the configured default server.
// Password precedence is SNATCHER_PASSWORD > the resolved store (see
// serverPassword). Store selection precedence is SNATCHER_SECRET_STORE > the
// server's secret_store > the keyring (see resolveStoreMode).
func dial(cmd *cobra.Command, configPath, server string) (*transmission.Client, string, error) {
	cfg, configured, err := loadConfig(configPath)
	if err != nil {
		return nil, "", err
	}

	if !configured || len(cfg.Servers) == 0 {
		return nil, "", errors.New("not configured: run 'snatcher server add' first")
	}

	if server == "" {
		server = os.Getenv("SNATCHER_SERVER")
	}

	_, srv, err := config.Resolve(cfg, server)
	if err != nil {
		return nil, "", resolveServerError(cfg, server, err)
	}

	password, err := serverPassword(cmd, srv)
	if err != nil {
		return nil, "", err
	}

	backend := transmission.New(srv.URL, transmission.WithBasicAuth(srv.Username, password))

	return backend, srv.URL, nil
}

// serverPassword returns the password to dial srv with.
//
// SNATCHER_PASSWORD comes first and settles it: no store is resolved, opened, or
// read, whatever mode srv names, so SNATCHER_SECRET_STORE=none plus
// SNATCHER_PASSWORD is a working unattended configuration with nothing written to
// disk. No unavailable-store warning is emitted on that path either — the store
// was never needed, so there is nothing to warn about.
//
// Otherwise the store srv resolves to answers. An unreachable store is not fatal
// on a read path: the server may not need auth at all, and if it does the request
// fails later with the transmission auth error, which already says what to do.
// Get yields an empty password alongside its error, so the client is built
// without one.
func serverPassword(cmd *cobra.Command, srv config.Server) (string, error) {
	if password, ok := passwordFromEnv(); ok {
		return password, nil
	}

	store, err := serverStore(srv)
	if err != nil {
		return "", err
	}

	password, err := store.Get(srv.URL)
	switch {
	case errors.Is(err, secrets.ErrUnavailable):
		warnStoreUnavailableOnce(cmd, "connecting without a stored password")
	case err != nil && !errors.Is(err, secrets.ErrNotFound):
		return "", err
	}

	return password, nil
}

// resolveServerError turns a config.Resolve sentinel into a clear, actionable
// error message: an unknown alias names the offending value and points at
// 'server list'; a missing default explains how to pick a server or set one.
func resolveServerError(cfg config.Config, server string, err error) error {
	switch {
	case errors.Is(err, config.ErrUnknownServer):
		return fmt.Errorf("unknown server %q: run 'snatcher server list' to see configured servers", server)
	case errors.Is(err, config.ErrNoDefaultServer):
		names := strings.Join(cfg.Names(), ", ")
		return fmt.Errorf("no default server set: pass --server <alias> or set one with 'snatcher server default <alias>' (configured: %s)", names)
	default:
		return err
	}
}

// addServerFlag registers the shared --server flag on a dialing command, binding
// it to target. Every command that connects to a server registers it through
// this helper so the flag name and usage stay consistent and it appears only on
// dialing commands.
func addServerFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVar(target, "server", "", "target configured server (default: configured default)")
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

	password, err := promptHidden(cmd, in, "Password: ")
	if err != nil {
		return "", err
	}

	// An empty password is a valid choice (a server with auth disabled); the
	// caller reports it and skips storing a secret, so there is nothing to
	// confirm.
	if password == "" {
		return "", nil
	}

	confirm, err := promptHidden(cmd, in, "Confirm password: ")
	if err != nil {
		return "", err
	}

	if confirm != password {
		return "", errors.New("passwords do not match")
	}

	return password, nil
}

// promptHidden writes label to the command's output and reads a line from the
// terminal without echoing it.
func promptHidden(cmd *cobra.Command, in *os.File, label string) (string, error) {
	_, _ = fmt.Fprint(cmd.OutOrStdout(), label)
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
