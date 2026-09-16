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
	"strconv"
	"strings"
	"sync/atomic"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/itspriddle/snatcher/internal/config"
	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/prowlarr"
	"github.com/itspriddle/snatcher/internal/secrets"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// storeUnavailableRemedy names the fix in every unavailable-store warning. It
// deliberately names no provider internals: an operator needs to know that no
// OS secret service answered, not which helper binary the provider looked for.
const storeUnavailableRemedy = "Install or start an OS secret service, or keep passwords in a plaintext file at mode 0600 with 'server add --store=file' (or SNATCHER_SECRET_STORE=file)."

// secretStatusUnknown is the consequence both reporting commands ("server list"
// and "server show") pass to warnStoreUnavailable: with no store to ask, they
// cannot tell whether a secret exists, and secret_stored is a bool with no
// third state. One wording, one constant, so the two cannot drift apart.
const secretStatusUnknown = `secret status could not be determined and is reported as "no"`

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

// newServerAddCmd builds "server add", which stores the server URL, type, and
// username in config and the server's secret in a secret store. The secret is a
// Transmission password or a Prowlarr API key, depending on --type; it is read
// from a hidden TTY prompt or, for scripting, from stdin via --password-stdin or
// --api-key-stdin.
//
// --store picks which store holds the secret. Whichever store is resolved is
// both written to and persisted with the server, so later commands resolve the
// same store without re-passing the flag.
func newServerAddCmd() *cobra.Command {
	var (
		name          string
		serverURL     string
		username      string
		typeFlag      string
		passwordStdin bool
		apiKeyStdin   bool
		storeFlag     string
	)

	cmd := &cobra.Command{
		Use:           "add",
		Short:         "Add or update a named server's URL, type, username, and secret",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Flag combinations settle first, before a secret is read from a
			// terminal or a config file is touched: a run that cannot be
			// carried out must say so with nothing typed and nothing written.
			serverType, err := parseServerType(typeFlag)
			if err != nil {
				return err
			}

			if err := validateAddSecretFlags(serverType, passwordStdin, apiKeyStdin, username); err != nil {
				return err
			}

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

			secret, err := readServerSecret(cmd, serverType, passwordStdin, apiKeyStdin)
			if err != nil {
				return err
			}

			configPath := configFlag(cmd)

			cfg, _, err := loadConfig(configPath)
			if err != nil {
				return err
			}
			if cfg.Servers == nil {
				cfg.Servers = map[string]config.Server{}
			}

			prev := cfg.Servers[alias]
			if err := checkServerTypeChange(alias, prev, serverType, cmd.Flags().Changed("type")); err != nil {
				return err
			}

			plan, err := planServerSecret(storeFlag, cmd.Flags().Changed("store"), prev, serverURL, secret, secretNoun(serverType))
			if err != nil {
				return err
			}

			cfg.Servers[alias] = config.Server{
				URL:         serverURL,
				Username:    username,
				Type:        storedServerType(serverType),
				SecretStore: string(plan.mode),
			}

			// The first server of each type registered when that type has no
			// default becomes it.
			setDefaultIfUnset(&cfg, alias, serverType)

			if err := commitServerSecret(plan, cfg, configPath); err != nil {
				return err
			}

			reportServerAdded(cmd, plan, alias, serverType)

			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "server alias (default: the URL host)")
	cmd.Flags().StringVar(&serverURL, "url", "", "server URL (e.g. http://host:9091/transmission/rpc)")
	cmd.Flags().StringVar(&username, "username", "", "username for HTTP Basic Auth (Transmission only)")
	cmd.Flags().StringVar(&typeFlag, "type", "", `kind of server: "transmission" (default) or "prowlarr"`)
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the password from stdin instead of a prompt")
	cmd.Flags().BoolVar(&apiKeyStdin, "api-key-stdin", false, "read the Prowlarr API key from stdin instead of a prompt")
	cmd.Flags().StringVar(&storeFlag, "store", "", `where to keep the secret: "keyring" (default), "file" (plaintext, mode 0600), or "none"`)

	return cmd
}

// validServerTypes lists the accepted --type values, quoted, for error messages.
const validServerTypes = `"transmission", "prowlarr"`

// setDefaultIfUnset points the default for serverType at alias when that type
// has no default yet.
//
// The two defaults are independent, so the first Prowlarr instance registered
// becomes the Prowlarr default without disturbing the Transmission one, and the
// reverse. A server type snatcher does not recognize is treated as Transmission,
// matching Server.ResolvedType's own default; the dial-time type check is where
// an unknown type is reported.
func setDefaultIfUnset(cfg *config.Config, alias, serverType string) {
	if serverType == config.TypeProwlarr {
		if cfg.DefaultProwlarrServer == "" {
			cfg.DefaultProwlarrServer = alias
		}

		return
	}

	if cfg.DefaultServer == "" {
		cfg.DefaultServer = alias
	}
}

// parseServerType resolves an operator-written --type value to one of the
// config type constants. Matching is case-insensitive and tolerates surrounding
// whitespace, exactly as parseStoreMode does; an omitted value is Transmission,
// which is what every server registered before the flag existed is. An
// unrecognized value is an error naming the valid ones rather than a silent
// fallback, since quietly treating "prowlar" as Transmission would send an API
// key to a Basic Auth header.
//
// Parsing lives in the command layer, not in internal/config: the constants are
// structural, but which spellings an operator may type is policy, the same split
// the per-server secret_store field already has.
func parseServerType(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", config.TypeTransmission:
		return config.TypeTransmission, nil
	case config.TypeProwlarr:
		return config.TypeProwlarr, nil
	default:
		return "", fmt.Errorf("invalid --type %q: valid values are %s", raw, validServerTypes)
	}
}

// storedServerType returns the value written to the server's type field.
// Transmission is stored as the empty string so it is omitted from the file: a
// config written before the field existed round-trips byte-identically, and
// re-adding a Transmission server never introduces the key.
func storedServerType(serverType string) string {
	if serverType == config.TypeProwlarr {
		return config.TypeProwlarr
	}

	return ""
}

// validateAddSecretFlags rejects the flag combinations that name a credential
// the chosen server type does not have, before anything is read or written.
//
// Each type has exactly one secret and one way to pipe it in, so crossing them
// is always a mistake worth naming: --api-key-stdin on a Transmission server,
// and --password-stdin or --username on a Prowlarr one, which authenticates with
// an API key header and no user.
func validateAddSecretFlags(serverType string, passwordStdin, apiKeyStdin bool, username string) error {
	if serverType == config.TypeProwlarr {
		if passwordStdin {
			return errors.New("--password-stdin does not apply to a Prowlarr server: use --api-key-stdin")
		}
		if username != "" {
			return errors.New("--username does not apply to a Prowlarr server: it authenticates with an API key, not HTTP Basic Auth")
		}

		return nil
	}

	if apiKeyStdin {
		return fmt.Errorf("--api-key-stdin only applies to a Prowlarr server: pass --type %s, or use --password-stdin", config.TypeProwlarr)
	}

	return nil
}

// checkServerTypeChange refuses to silently change what kind of backend an
// existing alias names.
//
// Re-running "server add" on an alias to correct its URL is routine, and --type
// defaults to Transmission, so an omitted flag would otherwise turn a registered
// Prowlarr server into a Transmission one: its API key would be stranded under a
// name nothing reads, and the next search command would fail with a type error
// pointing at the wrong thing. An explicitly passed --type is a deliberate
// change and is allowed through.
func checkServerTypeChange(alias string, prev config.Server, serverType string, typeChanged bool) error {
	if typeChanged || prev.URL == "" || prev.ResolvedType() == serverType {
		return nil
	}

	return fmt.Errorf(
		"server %q is already a %s server: pass --type %s to update it, or --type %s to change what it is",
		alias, serverTypeName(prev.ResolvedType()), prev.ResolvedType(), serverType,
	)
}

// serverTypeName renders a server type for operator-facing text. The two types
// snatcher knows get their product spelling; anything else — a hand-edited
// config can hold any string — is quoted as the file spells it.
func serverTypeName(serverType string) string {
	switch serverType {
	case config.TypeTransmission:
		return "Transmission"
	case config.TypeProwlarr:
		return "Prowlarr"
	default:
		return strconv.Quote(serverType)
	}
}

// serverSecretPlan is everything "server add" settles about a password before
// anything is written: which store the entry being saved uses, where the entry
// it replaces kept its secret, and whether those are the same place.
type serverSecretPlan struct {
	// serverURL keys the secret in every store, and password is the one being
	// stored. An empty password means the server keeps no secret at all.
	serverURL string
	password  string

	// secret names what password is, for every operator-facing message about it:
	// "password" for a Transmission server, "API key" for a Prowlarr one (see
	// secretNoun). It is settled once, by the caller that already knows the
	// server type, and carried on the plan so the store probe, the commit, and
	// the report cannot end up calling the same credential different things.
	secret string

	// store holds the new entry's password, and mode names it. The same mode
	// is saved with the server, so config can never name a store the password
	// did not go to.
	store secretStore
	mode  storeMode

	// prev is the entry being replaced, and prevMode the store prev itself
	// names — never the one SNATCHER_SECRET_STORE points at (see
	// recordedStoreMode).
	prev     config.Server
	prevMode storeMode

	// rekeyed reports whether the entry being saved points somewhere other
	// than prev's secret: a different URL, a different store, or no previous
	// entry at all. The old and new secrets are then necessarily two different
	// entries, which is what lets commitServerSecret write one and drop the
	// other without either touching the wrong one.
	rekeyed bool
}

// planServerSecret settles where "server add" puts the secret and what it owes
// the secret the alias already had, before anything is written. secret is the
// noun for what password holds, carried onto the plan (see secretNoun).
//
// The store is probed here (see storeForPassword): a secret with nowhere to go
// must abort the command with config and every store untouched, rather than
// registering a server that silently has none.
func planServerSecret(storeFlag string, flagChanged bool, prev config.Server, serverURL, password, secret string) (serverSecretPlan, error) {
	mode, err := addStoreMode(storeFlag, flagChanged, prev)
	if err != nil {
		return serverSecretPlan{}, err
	}

	prevMode, err := recordedStoreMode(prev)
	if err != nil {
		return serverSecretPlan{}, err
	}

	store, err := storeForPassword(mode, serverURL, password, secret)
	if err != nil {
		return serverSecretPlan{}, err
	}

	return serverSecretPlan{
		serverURL: serverURL,
		password:  password,
		secret:    secret,
		store:     store,
		mode:      mode,
		prev:      prev,
		prevMode:  prevMode,
		rekeyed:   prev.URL != serverURL || prevMode != mode,
	}, nil
}

// commitServerSecret carries out plan: write the password, save cfg, then drop
// the secret the alias used to keep.
//
// The order is the guarantee. The secret goes in before the config, so a store
// that refuses it after the probe cannot leave a registered server with no
// password. The old secret is dropped only once the config that stops naming it
// is safely on disk: until then it is still that server's live credential, and
// deleting it ahead of a save that then fails would leave a previously working
// server unable to authenticate. Dropping it last costs nothing, because a
// rekeyed add never writes to the entry it is about to clear — either the URL
// differs or the store does.
//
// A failed save takes the freshly written password back out again, best effort,
// so a credential — plaintext, under --store=file — is not stranded for an entry
// the config never came to name. Only when the add was rekeyed: otherwise the
// saved config still points this alias at exactly the URL and store just
// written, so that password is live and clearing it would break the server.
func commitServerSecret(plan serverSecretPlan, cfg config.Config, configPath string) error {
	if err := writeServerSecret(plan.store, plan.serverURL, plan.password); err != nil {
		return err
	}

	if err := config.Save(cfg, configPath); err != nil {
		if plan.password != "" && plan.rekeyed {
			_ = plan.store.Delete(plan.serverURL)
		}

		return err
	}

	// The server is saved either way at this point, so say that plainly rather
	// than returning a bare store error for a command that did register the
	// server — an operator whose old secret survived needs to know where it is.
	if err := clearPreviousSecret(plan.prev, plan.prevMode, plan.rekeyed); err != nil {
		return fmt.Errorf("the server was saved, but its previous %s could not be removed from the %q store: %w", plan.secret, plan.prevMode, err)
	}

	return nil
}

// reportServerAdded prints what "server add" saved and, for the plaintext file
// store, exactly what is now on disk. The secret is named for what it is —
// a password or an API key — so the report matches the flag the operator used.
// The noun comes off the plan, which is the same one every failure on the way
// here already used.
func reportServerAdded(cmd *cobra.Command, plan serverSecretPlan, alias, serverType string) {
	out := cmd.OutOrStdout()

	if plan.password == "" {
		_, _ = fmt.Fprintf(out, "Saved server %q (%s) with no %s stored\n", alias, plan.serverURL, plan.secret)

		// A Transmission server with no password is an ordinary configuration
		// (auth disabled). A Prowlarr instance always requires its API key, so
		// say plainly that the server is registered but unusable until one
		// arrives.
		if serverType == config.TypeProwlarr {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"Note: no API key is stored, so search commands will fail unless %s is set.\n", envAPIKey)
		}

		return
	}

	_, _ = fmt.Fprintf(out, "Saved credentials for server %q (%s)\n", alias, plan.serverURL)

	// The file store is plaintext by design; say where it landed, so an
	// operator who opted in knows exactly what is on disk.
	if fileStore, ok := plan.store.(*secrets.FileStore); ok {
		_, _ = fmt.Fprintf(out, "%s stored in plaintext at %s (mode 0600)\n", capitalizeFirst(plan.secret), fileStore.Path())
	}
}

// capitalizeFirst upper-cases the first letter of s, for a noun that starts a
// sentence. It is ASCII-only by design: its inputs are snatcher's own fixed
// nouns, never server- or operator-supplied text.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}

	return strings.ToUpper(s[:1]) + s[1:]
}

// clearPreviousSecret drops the secret an existing alias used to keep, when
// rekeyed says the entry replacing it no longer points at that secret: either
// the URL changed, or the store did.
//
// Both leave a secret nothing will ever read again. The store change is the
// sharper case: re-running "server add --store=none" on a server added with
// --store=file reports that no password is stored while the plaintext password
// stays readable in secrets.json, so the command's own output contradicts the
// disk after an explicit opt-out.
//
// prevMode is where the old secret lives: the store the previous entry itself
// names, not the one SNATCHER_SECRET_STORE points at for this invocation, which
// would delete from the wrong store (see recordedStoreMode). This is scoped to
// the alias being changed; other servers' secrets are untouched. A missing entry
// — or no reachable store to hold one — is fine.
func clearPreviousSecret(prev config.Server, prevMode storeMode, rekeyed bool) error {
	if prev.URL == "" || !rekeyed {
		return nil
	}

	prevStore, err := openStore(prevMode)
	if err != nil {
		return err
	}

	if err := prevStore.Delete(prev.URL); err != nil && !secretAbsent(err) {
		return err
	}

	return nil
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
// is a stable scripting contract: additive changes only. The secret is never
// included; only whether one is stored.
//
// Type is the server's resolved type, so a server registered before the field
// existed reports "transmission" rather than an empty string. IsDefault is per
// type: it reports whether the alias is the default for its own kind of backend,
// since a Transmission default and a Prowlarr default coexist.
type serverListResult struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	ServerURL    string `json:"server_url"`
	Username     string `json:"username"`
	SecretStored bool   `json:"secret_stored"`
	IsDefault    bool   `json:"is_default"`
}

// newServerListCmd builds "server list", which enumerates every configured
// server with its alias, type, URL, username, whether a secret is stored, and
// whether it is the default for its own type. Plaintext is an aligned table on
// a TTY and tab-separated, header-less rows when piped or NO_COLOR is set (the
// same contract as "torrent list"); --json and --jq emit a stable structured
// shape. Servers are listed sorted by alias.
func newServerListCmd() *cobra.Command {
	var (
		asJSON bool
		jqExpr string
	)

	cmd := &cobra.Command{
		Use:           "list",
		Aliases:       []string{"ls"},
		Short:         "List configured servers",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := loadConfig(configFlag(cmd))
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

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// serverListResults builds the per-server result rows for "server list", sorted
// by alias, asking the store each server itself names whether its secret is
// stored (see serverSecretStored).
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

		secretStored, unavailable, err := serverSecretStored(srv)
		if err != nil {
			return nil, err
		}

		storeUnavailable = storeUnavailable || unavailable

		serverType := srv.ResolvedType()

		results = append(results, serverListResult{
			Name:         name,
			Type:         serverType,
			ServerURL:    srv.URL,
			Username:     srv.Username,
			SecretStored: secretStored,
			IsDefault:    isDefaultForType(cfg, name, serverType),
		})
	}

	if storeUnavailable {
		warnStoreUnavailable(cmd, secretStatusUnknown)
	}

	return results, nil
}

// isDefaultForType reports whether name is the configured default for its own
// type, so a Transmission server and a Prowlarr instance can both be marked
// default without either shadowing the other. A type snatcher does not
// recognize is read as Transmission, matching Server.ResolvedType's own default.
func isDefaultForType(cfg config.Config, name, serverType string) bool {
	if serverType == config.TypeProwlarr {
		return name == cfg.DefaultProwlarrServer
	}

	return name == cfg.DefaultServer
}

// writeServerListPlaintext renders the server list in the appropriate plaintext
// mode: an aligned, headered table on a TTY, or plain tab-separated, header-less
// rows when piped or when NO_COLOR is set (the same contract as "torrent list").
// Server-derived strings (alias, type, URL, username) are sanitized of terminal
// control sequences here; the JSON/jq paths are never sanitized.
func writeServerListPlaintext(cmd *cobra.Command, results []serverListResult) {
	out := cmd.OutOrStdout()

	rows := make([][]string, len(results))
	for i, r := range results {
		rows[i] = []string{
			output.SanitizeTerminal(r.Name),
			output.SanitizeTerminal(r.Type),
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

	_, _ = fmt.Fprintln(tw, strings.Join([]string{"NAME", "TYPE", "URL", "USERNAME", "SECRET", "DEFAULT"}, "\t"))
	for _, row := range rows {
		_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
	}

	_ = tw.Flush()
}

// newServerDefaultCmd builds "server default <alias>", which sets the configured
// default server to an existing alias and persists it. An unknown alias errors
// clearly, listing the valid names.
//
// Which default it sets is inferred from the alias rather than chosen with a
// flag: a Prowlarr instance becomes default_prowlarr_server and a Transmission
// server default_server, so setting one never repoints the other. The
// confirmation line names which of the two was set, since that is the only
// visible difference between the two runs.
func newServerDefaultCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "default <alias>",
		Short:         "Set the default server for its type",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := args[0]
			configPath := configFlag(cmd)

			cfg, _, err := loadConfig(configPath)
			if err != nil {
				return err
			}

			srv, ok := cfg.Servers[alias]
			if !ok {
				names := strings.Join(cfg.Names(), ", ")
				return fmt.Errorf("unknown server %q: configured servers are %s", alias, names)
			}

			label := "Default server"
			if srv.ResolvedType() == config.TypeProwlarr {
				cfg.DefaultProwlarrServer = alias
				label = "Default Prowlarr server"
			} else {
				cfg.DefaultServer = alias
			}

			if err := config.Save(cfg, configPath); err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s set to %q\n", label, alias)

			return nil
		},
	}

	return cmd
}

// serverPingResult is the JSON shape emitted by "server ping" for a Transmission
// server on success.
type serverPingResult struct {
	Connected        bool   `json:"connected"`
	ServerURL        string `json:"server_url"`
	Type             string `json:"type"`
	Version          string `json:"version"`
	Protocol         string `json:"protocol"`
	RPCVersionSemver string `json:"rpc_version_semver"`
}

// prowlarrPingResult is the JSON shape emitted by "server ping" for a Prowlarr
// instance on success.
//
// It is a separate shape rather than a widened serverPingResult because the two
// backends answer different questions: Prowlarr's REST API negotiates no
// protocol and has no RPC version, and Transmission reports no application name.
// Emitting the union would put a permanently empty key in each, on a contract
// that only grows.
type prowlarrPingResult struct {
	Connected bool   `json:"connected"`
	ServerURL string `json:"server_url"`
	Type      string `json:"type"`
	Version   string `json:"version"`
	AppName   string `json:"app_name"`
}

// newServerPingCmd builds "server ping [alias]", which dials a server and
// performs a real round-trip, reporting what it found. It pings the named
// server, or the configured default when the alias is omitted. It exits
// non-zero on failure.
//
// The round-trip it makes depends on the resolved server's type: a session
// read for a Transmission server, a system-status read for a Prowlarr instance.
// An omitted alias falls back to the Transmission default, which is what every
// existing invocation resolved to; a Prowlarr instance is pinged by naming it
// (or through SNATCHER_SERVER).
func newServerPingCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:           "ping [alias]",
		Short:         "Test the connection to a server",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := resolveDialTarget(cmd, firstArg(args), config.TypeTransmission)
			if err != nil {
				return err
			}

			if target.server.ResolvedType() == config.TypeProwlarr {
				return pingProwlarr(cmd, target.server, asJSON)
			}

			return pingTransmission(cmd, target.server, asJSON)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")

	return cmd
}

// pingTransmission performs the Transmission half of "server ping": a real
// ServerInfo round-trip reporting the version, negotiated protocol, and rpc
// semver.
func pingTransmission(cmd *cobra.Command, srv config.Server, asJSON bool) error {
	backend, serverURL, err := connectTransmission(cmd, srv)
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
		Type:             srv.ResolvedType(),
		Version:          info.Version,
		Protocol:         info.Protocol,
		RPCVersionSemver: info.RPCVersionSemver,
	}

	if asJSON {
		return output.JSON(cmd.OutOrStdout(), result)
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Connected to %s\n", serverURL)
	_, _ = fmt.Fprintf(out, "Type:         %s\n", output.SanitizeTerminal(result.Type))
	_, _ = fmt.Fprintf(out, "Version:      %s\n", result.Version)
	_, _ = fmt.Fprintf(out, "Protocol:     %s\n", result.Protocol)
	_, _ = fmt.Fprintf(out, "RPC version:  %s\n", result.RPCVersionSemver)

	return nil
}

// pingProwlarr performs the Prowlarr half of "server ping": a system-status
// round-trip reporting the instance's application name and version. Those
// strings come off the wire, so the plaintext path sanitizes them; the --json
// path does not.
func pingProwlarr(cmd *cobra.Command, srv config.Server, asJSON bool) error {
	client, serverURL, err := connectProwlarr(cmd, srv)
	if err != nil {
		return err
	}

	status, err := client.SystemStatus(cmd.Context())
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", serverURL, err)
	}

	result := prowlarrPingResult{
		Connected: true,
		ServerURL: serverURL,
		Type:      srv.ResolvedType(),
		Version:   status.Version,
		AppName:   status.AppName,
	}

	if asJSON {
		return output.JSON(cmd.OutOrStdout(), result)
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Connected to %s\n", serverURL)
	_, _ = fmt.Fprintf(out, "Type:         %s\n", output.SanitizeTerminal(result.Type))
	_, _ = fmt.Fprintf(out, "Version:      %s\n", output.SanitizeTerminal(result.Version))
	_, _ = fmt.Fprintf(out, "App:          %s\n", output.SanitizeTerminal(result.AppName))

	return nil
}

// newServerRemoveCmd builds "server remove [alias]", which removes a named server
// (or the configured default when the alias is omitted): it deletes the server's
// entry from config, deletes its stored secret, and persists. When the removed
// server was a default, only the default for its own type is repointed or
// cleared (see repointDefaultAfterRemove).
//
// The secret is deleted from the store the server itself names, not the one
// SNATCHER_SECRET_STORE selects for this invocation: removal is cleanup, and the
// only store worth cleaning is the one holding the password.
func newServerRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "remove [alias]",
		Aliases:       []string{"rm"},
		Short:         "Remove a configured server",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := firstArg(args)
			configPath := configFlag(cmd)

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
				store, err := recordedStore(srv)
				if err != nil {
					return err
				}

				err = store.Delete(srv.URL)
				switch {
				case errors.Is(err, secrets.ErrUnavailable):
					warnStoreUnavailable(cmd, fmt.Sprintf("a stored %s for %s may remain",
						secretNoun(srv.ResolvedType()), output.SanitizeTerminal(srv.URL)))
				case err != nil && !errors.Is(err, secrets.ErrNotFound):
					return err
				}
			}

			delete(cfg.Servers, name)

			repointDefaultAfterRemove(&cfg, name, srv.ResolvedType())

			if err := config.Save(cfg, configPath); err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed server %q (%s)\n", name, srv.URL)

			return nil
		},
	}

	return cmd
}

// repointDefaultAfterRemove settles the two defaults once name has been deleted
// from cfg.
//
// Only the default belonging to the removed server's own type moves: it is
// repointed to the sole remaining server of that type when exactly one is left,
// and otherwise cleared, leaving the operator to pick a new one with
// 'server default'. Removing a Prowlarr instance therefore never touches the
// Transmission default, or the reverse — and a default is never repointed at a
// server of the other type, which no command could dial through it anyway.
//
// The other type's default is still cleared when it happens to name the removed
// alias, which only a hand-edited config can arrange. Leaving it would name a
// server that no longer exists, and the save that follows would then fail
// validation on a removal that had otherwise succeeded.
func repointDefaultAfterRemove(cfg *config.Config, name, serverType string) {
	if cfg.DefaultServer == name {
		cfg.DefaultServer = soleRemainingOfType(*cfg, serverType, config.TypeTransmission)
	}

	if cfg.DefaultProwlarrServer == name {
		cfg.DefaultProwlarrServer = soleRemainingOfType(*cfg, serverType, config.TypeProwlarr)
	}
}

// soleRemainingOfType returns the one alias left of defaultType, or the empty
// string when the removed server was of some other type or the count is not
// exactly one.
func soleRemainingOfType(cfg config.Config, serverType, defaultType string) string {
	if serverType != defaultType {
		return ""
	}

	if remaining := cfg.NamesOfType(defaultType); len(remaining) == 1 {
		return remaining[0]
	}

	return ""
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

// dial loads config (honoring the persistent --config flag cmd carries),
// resolves which named server to use, reads that server's password from the
// store the server resolves to, and constructs the concrete
// *transmission.Client. It returns the client and the resolved server URL. It is
// the single seam where commands construct the client. cmd also supplies the
// stderr stream for the unavailable-store warning.
//
// Server selection precedence is: the server argument (the --server flag) >
// the SNATCHER_SERVER environment variable > the configured default server.
// Password precedence is SNATCHER_PASSWORD > the resolved store (see
// serverPassword). Store selection precedence is SNATCHER_SECRET_STORE > the
// server's secret_store > the keyring (see resolveStoreMode).
func dial(cmd *cobra.Command, server string) (*transmission.Client, string, error) {
	d, err := dialNamed(cmd, server)
	if err != nil {
		return nil, "", err
	}

	return d.client, d.url, nil
}

// dialed is a Transmission client together with the alias and URL the
// precedence chain resolved it from.
type dialed struct {
	client *transmission.Client
	name   string
	url    string
}

// dialNamed is dial, keeping the resolved alias. Commands have no use for the
// alias once the client exists; the TUI's status bar and server picker do.
func dialNamed(cmd *cobra.Command, server string) (dialed, error) {
	target, err := dialTarget(cmd, server, config.TypeTransmission)
	if err != nil {
		return dialed{}, err
	}

	client, url, err := connectTransmission(cmd, target.server)
	if err != nil {
		return dialed{}, err
	}

	return dialed{client: client, name: target.name, url: url}, nil
}

// connectTransmission reads srv's password and builds the Transmission client
// for it. It is dial's back half, split out so "server ping" — which resolves
// the server itself in order to dispatch on its type — constructs the client
// exactly as every other command does.
func connectTransmission(cmd *cobra.Command, srv config.Server) (*transmission.Client, string, error) {
	password, err := serverPassword(cmd, srv)
	if err != nil {
		return nil, "", err
	}

	backend := transmission.New(srv.URL, transmission.WithBasicAuth(srv.Username, password))

	return backend, srv.URL, nil
}

// dialProwlarr is dial's Prowlarr counterpart: it resolves which configured
// Prowlarr instance to use, reads its API key from the store that instance
// resolves to (or SNATCHER_API_KEY), and constructs the *prowlarr.Client. It
// returns the client and the resolved server URL.
//
// Selection precedence matches dial exactly — the --server flag > the
// SNATCHER_SERVER environment variable > the configured default — except that
// the default it falls back to is default_prowlarr_server, so a Transmission
// default and a Prowlarr default coexist without either shadowing the other.
func dialProwlarr(cmd *cobra.Command, server string) (*prowlarr.Client, string, error) {
	d, err := dialProwlarrNamed(cmd, server)
	if err != nil {
		return nil, "", err
	}

	return d.client, d.url, nil
}

// dialedProwlarr is a Prowlarr client together with the alias and URL the
// precedence chain resolved it from.
type dialedProwlarr struct {
	client *prowlarr.Client
	name   string
	url    string
}

// dialProwlarrNamed is dialProwlarr, keeping the resolved alias, as dialNamed
// is dial's: the TUI's status bar and server picker need it.
func dialProwlarrNamed(cmd *cobra.Command, server string) (dialedProwlarr, error) {
	target, err := dialTarget(cmd, server, config.TypeProwlarr)
	if err != nil {
		return dialedProwlarr{}, err
	}

	client, url, err := connectProwlarr(cmd, target.server)
	if err != nil {
		return dialedProwlarr{}, err
	}

	return dialedProwlarr{client: client, name: target.name, url: url}, nil
}

// connectProwlarr reads srv's API key and builds the Prowlarr client for it. It
// is dialProwlarr's back half, split out for the same reason as
// connectTransmission.
func connectProwlarr(cmd *cobra.Command, srv config.Server) (*prowlarr.Client, string, error) {
	apiKey, err := serverAPIKey(cmd, srv)
	if err != nil {
		return nil, "", err
	}

	client, err := prowlarr.NewClient(srv.URL, prowlarr.WithAPIKey(apiKey))
	if err != nil {
		return nil, "", err
	}

	return client, srv.URL, nil
}

// dialTarget resolves which configured server a dialing command should use and
// checks it is the kind of backend that command needs. It is the shared front
// half of dial and dialProwlarr, so both apply the same precedence and produce
// the same errors.
//
// A resolved server of the wrong type is refused here rather than dialed: a
// Prowlarr instance handed to the Transmission client would fail with an opaque
// protocol error, and — worse in the other direction — a Transmission password
// would be offered to a Prowlarr host. The error names the alias, what it
// actually is, and where the alias came from, since an alias supplied by an
// exported SNATCHER_SERVER is the case an operator is least likely to guess.
func dialTarget(cmd *cobra.Command, server, wantType string) (resolvedServer, error) {
	target, err := resolveDialTarget(cmd, server, wantType)
	if err != nil {
		return resolvedServer{}, err
	}

	if got := target.server.ResolvedType(); got != wantType {
		return resolvedServer{}, serverTypeMismatchError(target.name, got, wantType, target.fromEnv)
	}

	return target, nil
}

// resolvedServer is a configured server a command has selected: the alias it was
// found under, its settings, and whether the alias came from SNATCHER_SERVER
// rather than a flag or argument — which only an error message needs, and only
// because an alias supplied by an exported variable is the case an operator is
// least likely to guess.
type resolvedServer struct {
	name    string
	server  config.Server
	fromEnv bool
}

// resolveDialTarget resolves which configured server a command acts on, applying
// the fixed precedence: the server argument (the --server flag, or a positional
// alias) > the SNATCHER_SERVER environment variable > the default server for
// defaultType.
//
// It imposes no constraint on the resolved server's type; defaultType only
// chooses which of the two per-type defaults an omitted alias falls back to.
// Commands that need one particular backend go through dialTarget, which adds
// the type check; "server ping" resolves here and dispatches on what it finds.
func resolveDialTarget(cmd *cobra.Command, server, defaultType string) (resolvedServer, error) {
	cfg, configured, err := loadConfig(configFlag(cmd))
	if err != nil {
		return resolvedServer{}, err
	}

	if !configured || len(cfg.Servers) == 0 {
		return resolvedServer{}, errors.New("not configured: run 'snatcher server add' first")
	}

	fromEnv := false
	if server == "" {
		server = os.Getenv(envServer)
		fromEnv = server != ""
	}

	resolve := config.Resolve
	if defaultType == config.TypeProwlarr {
		resolve = config.ResolveProwlarr
	}

	name, srv, err := resolve(cfg, server)
	if err != nil {
		return resolvedServer{}, resolveServerError(cfg, server, err)
	}

	return resolvedServer{name: name, server: srv, fromEnv: fromEnv}, nil
}

// serverTypeMismatchError reports that the resolved alias names the wrong kind
// of backend for the command in hand, and how to select the right one.
func serverTypeMismatchError(name, got, want string, fromEnv bool) error {
	remedy := "pass --server <alias> to select one"
	if fromEnv {
		remedy = fmt.Sprintf("the alias came from %s; pass --server <alias> to override it", envServer)
	}

	return fmt.Errorf("server %q is a %s server; this command needs a %s server: %s",
		name, serverTypeName(got), serverTypeName(want), remedy)
}

// serverPassword returns the password to dial a Transmission srv with, and
// serverAPIKey the API key for a Prowlarr one. Both are serverSecret with the
// noun the unavailable-store warning should use.
func serverPassword(cmd *cobra.Command, srv config.Server) (string, error) {
	return serverSecret(cmd, srv, "connecting without a stored password")
}

func serverAPIKey(cmd *cobra.Command, srv config.Server) (string, error) {
	return serverSecret(cmd, srv, "connecting without a stored API key")
}

// serverSecret returns the secret to dial srv with: its password or its API key,
// whichever its type calls for.
//
// The environment override for that type comes first and settles it (see
// secretFromEnv): no store is resolved, opened, or read, whatever mode srv
// names, so SNATCHER_SECRET_STORE=none plus SNATCHER_PASSWORD — or
// SNATCHER_API_KEY — is a working unattended configuration with nothing written
// to disk. No unavailable-store warning is emitted on that path either: the
// store was never needed, so there is nothing to warn about.
//
// Otherwise the store srv resolves to answers. An unreachable store is not fatal
// on a read path: the server may not need auth at all, and if it does the request
// fails later with the backend's auth error, which already says what to do. Get
// yields an empty secret alongside its error, so the client is built without one.
func serverSecret(cmd *cobra.Command, srv config.Server, unavailableConsequence string) (string, error) {
	if secret, ok := secretFromEnv(srv); ok {
		return secret, nil
	}

	store, err := serverStore(srv)
	if err != nil {
		return "", err
	}

	secret, err := store.Get(srv.URL)
	switch {
	case errors.Is(err, secrets.ErrUnavailable):
		warnStoreUnavailableOnce(cmd, unavailableConsequence)
	case err != nil && !errors.Is(err, secrets.ErrNotFound):
		return "", err
	}

	return secret, nil
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
	case errors.Is(err, config.ErrNoDefaultProwlarrServer):
		return noDefaultProwlarrError(cfg)
	default:
		return err
	}
}

// noDefaultProwlarrError explains that no Prowlarr instance is selected, in the
// terms of whichever situation the operator is actually in: none registered at
// all, or several registered with none of them the default.
func noDefaultProwlarrError(cfg config.Config) error {
	names := cfg.NamesOfType(config.TypeProwlarr)
	if len(names) == 0 {
		return fmt.Errorf("no Prowlarr server configured: add one with 'snatcher server add --type %s --url <url>'", config.TypeProwlarr)
	}

	return fmt.Errorf(
		"no default Prowlarr server set: pass --server <alias> or set one with 'snatcher server default <alias>' (configured Prowlarr servers: %s)",
		strings.Join(names, ", "),
	)
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

// readServerSecret obtains the secret "server add" stores for a server of
// serverType: a Transmission password or a Prowlarr API key. Each type has its
// own reader because the two are typed differently — see readAPIKey.
func readServerSecret(cmd *cobra.Command, serverType string, passwordStdin, apiKeyStdin bool) (string, error) {
	if serverType == config.TypeProwlarr {
		return readAPIKey(cmd, apiKeyStdin)
	}

	return readPassword(cmd, passwordStdin)
}

// readPassword obtains the password either from stdin (when passwordStdin is
// set, for scripting) or from a hidden TTY prompt. The trailing newline of a
// piped password is trimmed; the prompt path uses term.ReadPassword so the input
// never echoes.
func readPassword(cmd *cobra.Command, passwordStdin bool) (string, error) {
	if passwordStdin {
		return readSecretStdin(cmd, "password")
	}

	in, err := promptTerminal(cmd, "password", "--password-stdin")
	if err != nil {
		return "", err
	}

	password, err := promptHidden(cmd, in, "Password: ", "password")
	if err != nil {
		return "", err
	}

	// An empty password is a valid choice (a server with auth disabled); the
	// caller reports it and skips storing a secret, so there is nothing to
	// confirm.
	if password == "" {
		return "", nil
	}

	confirm, err := promptHidden(cmd, in, "Confirm password: ", "password")
	if err != nil {
		return "", err
	}

	if confirm != password {
		return "", errors.New("passwords do not match")
	}

	return password, nil
}

// readAPIKey obtains a Prowlarr API key either from stdin (when apiKeyStdin is
// set) or from a hidden TTY prompt.
//
// Unlike readPassword it asks once, with no confirmation step. A password is
// typed, so a mistype is invisible and worth catching before it is stored; an
// API key is pasted from Prowlarr's settings page, where a paste-twice prompt is
// friction that catches nothing. A wrong key surfaces on the next command that
// uses it.
func readAPIKey(cmd *cobra.Command, apiKeyStdin bool) (string, error) {
	if apiKeyStdin {
		return readSecretStdin(cmd, "API key")
	}

	in, err := promptTerminal(cmd, "API key", "--api-key-stdin")
	if err != nil {
		return "", err
	}

	return promptHidden(cmd, in, "API key: ", "API key")
}

// readSecretStdin reads a piped secret, trimming the trailing newline a shell
// pipeline adds. noun names the secret in the error, so a failed read says which
// one it was reading.
func readSecretStdin(cmd *cobra.Command, noun string) (string, error) {
	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", fmt.Errorf("reading %s from stdin: %w", noun, err)
	}

	return strings.TrimRight(string(data), "\r\n"), nil
}

// promptTerminal returns the command's input as a terminal, or an error naming
// the stdin flag to use instead when there is no terminal to prompt on.
func promptTerminal(cmd *cobra.Command, noun, stdinFlag string) (*os.File, error) {
	in, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(in.Fd())) {
		return nil, fmt.Errorf("no terminal available for %s prompt; use %s", noun, stdinFlag)
	}

	return in, nil
}

// promptHidden writes label to the command's output and reads a line from the
// terminal without echoing it. noun names the secret in a read error.
func promptHidden(cmd *cobra.Command, in *os.File, label, noun string) (string, error) {
	_, _ = fmt.Fprint(cmd.OutOrStdout(), label)
	data, err := term.ReadPassword(int(in.Fd()))
	_, _ = fmt.Fprintln(cmd.OutOrStdout())
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", noun, err)
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
	// into config.json; passwords must live only in a secret store.
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
