package commands

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/config"
	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// serverShowResult is the JSON shape emitted by "server show": the local
// connection settings, the live server identity, and the live session settings.
// The password is never included; only whether a secret is stored. The
// identity fields and the settings array are omitted entirely when they were not
// fetched (--local, or an unconfigured first run), so the offline payload is
// exactly the connection block.
type serverShowResult struct {
	Name             string       `json:"name"`
	ServerURL        string       `json:"server_url"`
	Username         string       `json:"username"`
	ConfigPath       string       `json:"config_path"`
	SecretStored     bool         `json:"secret_stored"`
	IsDefault        bool         `json:"is_default"`
	Configured       bool         `json:"configured"`
	Version          string       `json:"version,omitempty"`
	RPCVersionSemver string       `json:"rpc_version_semver,omitempty"`
	Protocol         string       `json:"protocol,omitempty"`
	Settings         []settingRow `json:"settings,omitempty"`
}

// serverShowField is one selectable line of the connection block: the kebab-case
// key used by --fields and the piped output, the human label used on a TTY, and
// an accessor rendering the value. Setting keys live in transmission's settings
// registry and are
// selectable by the same --fields flag.
type serverShowField struct {
	key      string
	label    string
	value    func(serverShowResult) string
	sanitize bool

	// live marks a field that only exists once the server has been contacted. Such
	// fields are dropped entirely (not rendered empty) when the fetch was skipped,
	// mirroring the omitempty on their JSON counterparts.
	live bool
}

// serverShowFields is the connection block in display order. The keys are the
// JSON tags with "_" replaced by "-", matching the kebab-case convention the rest
// of the server namespace uses (flag names, setting keys). "configured" is
// deliberately absent: it exists only to disambiguate the JSON payload on a first
// run and has no useful plaintext rendering.
var serverShowFields = []serverShowField{
	{key: "name", label: "Name", sanitize: true,
		value: func(r serverShowResult) string { return r.Name }},
	{key: "server-url", label: "Server URL", sanitize: true,
		value: func(r serverShowResult) string { return r.ServerURL }},
	{key: "username", label: "Username", sanitize: true,
		value: func(r serverShowResult) string { return r.Username }},
	{key: "config-path", label: "Config path",
		value: func(r serverShowResult) string { return r.ConfigPath }},
	{key: "secret-stored", label: "Secret stored",
		value: func(r serverShowResult) string { return yesNo(r.SecretStored) }},
	{key: "is-default", label: "Default",
		value: func(r serverShowResult) string { return yesNo(r.IsDefault) }},
	{key: "version", label: "Version", sanitize: true, live: true,
		value: func(r serverShowResult) string { return r.Version }},
	{key: "rpc-version-semver", label: "RPC semver", sanitize: true, live: true,
		value: func(r serverShowResult) string { return r.RPCVersionSemver }},
	{key: "protocol", label: "Protocol", sanitize: true, live: true,
		value: func(r serverShowResult) string { return r.Protocol }},
}

// newServerShowCmd builds "server show [alias]", the single read command for a
// server: its local connection settings (name, URL, username, resolved config
// path, whether a secret is stored, whether it is the default), its live
// identity (version, rpc semver, negotiated protocol), and every live session
// setting. It shows the named server, or the configured default when the alias is
// omitted. It never reveals the secret value.
//
// Identity and settings both project from a single session-get, so the whole view
// costs one round-trip. --local skips it entirely for offline use, and an
// unconfigured first run reports the empty connection block rather than erroring.
// --category and --search filter the settings in every output mode; --fields
// selects lines on the plaintext path only, mirroring "torrent show". Render
// precedence is the usual --jq → --json → plaintext.
func newServerShowCmd() *cobra.Command {
	var (
		asJSON       bool
		jqExpr       string
		local        bool
		categoryFlag string
		searchFlag   string
		fieldsFlag   string
	)

	cmd := &cobra.Command{
		Use:   "show [alias]",
		Short: "Show a server's connection and settings",
		Long: "Show everything about a server: its local connection settings, its " +
			"version and negotiated RPC protocol, and every session setting.\n\n" +
			"Shows the named server alias, or the configured default when the alias " +
			"is omitted. The stored password is never revealed, only whether one " +
			"exists. Use --local to print just the connection settings without " +
			"contacting the server.\n\n" +
			"--category and --search filter the settings in every output mode. " +
			"--fields selects which lines the plaintext output shows, naming " +
			"connection keys, setting keys, or both; it has no effect on --json " +
			"(always the full object) or --jq (which selects fields itself), and it " +
			"selects without reordering — each section keeps its canonical order.\n\n" +
			"Use 'snatcher server update' to change settings and " +
			"'snatcher server reset' to restore their defaults.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if local {
				for _, name := range []string{"category", "search", "fields"} {
					if cmd.Flags().Changed(name) {
						return fmt.Errorf("--local and --%s cannot be used together", name)
					}
				}
			}

			if cmd.Flags().Changed("fields") {
				for _, name := range []string{"category", "search"} {
					if cmd.Flags().Changed(name) {
						return fmt.Errorf("--fields and --%s cannot be used together", name)
					}
				}
			}

			var category transmission.SettingCategory
			if categoryFlag != "" {
				parsed, err := transmission.ParseCategory(categoryFlag)
				if err != nil {
					return err
				}
				category = parsed
			}

			fields, settingKeys, err := parseServerShowFields(fieldsFlag)
			if err != nil {
				return err
			}

			result, configured, err := loadServerShowResult(cmd, firstArg(args))
			if err != nil {
				return err
			}

			// An unconfigured first run has nothing to dial; report the empty
			// connection block rather than a resolve error.
			live := configured && !local
			if live {
				backend, serverURL, err := dial(cmd, firstArg(args))
				if err != nil {
					return err
				}

				info, session, err := backend.Session(cmd.Context())
				if err != nil {
					return fmt.Errorf("connecting to %s: %w", serverURL, err)
				}

				result.Version = info.Version
				result.RPCVersionSemver = info.RPCVersionSemver
				result.Protocol = info.Protocol
				result.Settings = settingRows(selectSettings(category, searchFlag, settingKeys), session)
			}

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), result, jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), result)
			}

			writeServerShow(cmd, result, fields, live)

			return nil
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVar(&local, "local", false, "show only the local connection settings, without contacting the server")
	cmd.Flags().StringVar(&categoryFlag, "category", "", "show only settings in this category (download, network, bandwidth, queue)")
	cmd.Flags().StringVar(&searchFlag, "search", "", "show only settings whose key contains this text (case-insensitive)")
	cmd.Flags().StringVar(&fieldsFlag, "fields", "", "comma-separated connection or setting keys to show (default: all)")

	return cmd
}

// loadServerShowResult reads the local connection settings for the named alias
// (or the configured default). It reports whether a server is configured at all;
// on a first run with no servers it returns an empty, unconfigured result rather
// than a resolve error, so "server show" stays informative before setup.
//
// Whether a secret is stored is asked of the store the server itself names (see
// serverSecretStored). An unreachable one does not fail the read: secret_stored
// is reported false with a single stderr note, matching "server list".
func loadServerShowResult(cmd *cobra.Command, alias string) (serverShowResult, bool, error) {
	configPath := configFlag(cmd)

	resolvedPath, err := resolveConfigPath(configPath)
	if err != nil {
		return serverShowResult{}, false, err
	}

	cfg, configured, err := loadConfig(configPath)
	if err != nil {
		return serverShowResult{}, false, err
	}

	if !configured || len(cfg.Servers) == 0 {
		return serverShowResult{ConfigPath: resolvedPath, Configured: configured}, false, nil
	}

	name, srv, err := config.Resolve(cfg, alias)
	if err != nil {
		return serverShowResult{}, false, resolveServerError(cfg, alias, err)
	}

	secretStored, storeUnavailable, err := serverSecretStored(srv)
	if err != nil {
		return serverShowResult{}, false, err
	}

	if storeUnavailable {
		warnStoreUnavailable(cmd, secretStatusUnknown)
	}

	return serverShowResult{
		Name:         name,
		ServerURL:    srv.URL,
		Username:     srv.Username,
		ConfigPath:   resolvedPath,
		SecretStored: secretStored,
		IsDefault:    name == cfg.DefaultServer,
		Configured:   configured,
	}, true, nil
}

// selectSettings resolves which settings to report. --fields names them
// explicitly (keys, in registry order); otherwise the optional --category and
// --search filters apply. The two paths are mutually exclusive at the flag level,
// so at most one of them is ever in play.
func selectSettings(category transmission.SettingCategory, search string, keys map[string]bool) []transmission.SettingDef {
	if keys == nil {
		return transmission.FilterSettings(category, search)
	}

	defs := make([]transmission.SettingDef, 0, len(keys))
	for _, def := range transmission.SettingDefs() {
		if keys[def.Key] {
			defs = append(defs, def)
		}
	}

	return defs
}

// parseServerShowFields resolves a --fields value into the connection fields to
// render and the set of setting keys to report. An empty spec selects everything
// (nil results, meaning "unfiltered"). Keys are matched case-insensitively after
// trimming, and an unknown key returns an error listing the valid keys. Selection
// does not reorder: each section keeps its canonical order.
func parseServerShowFields(spec string) ([]serverShowField, map[string]bool, error) {
	if strings.TrimSpace(spec) == "" {
		return serverShowFields, nil, nil
	}

	wanted := make(map[string]bool)
	for _, raw := range strings.Split(spec, ",") {
		key := strings.ToLower(strings.TrimSpace(raw))
		if key == "" {
			continue
		}

		if !isServerShowField(key) {
			return nil, nil, fmt.Errorf("invalid field %q: valid fields are %s", strings.TrimSpace(raw), validServerShowFieldKeys())
		}

		wanted[key] = true
	}

	if len(wanted) == 0 {
		return nil, nil, fmt.Errorf("no fields selected: valid fields are %s", validServerShowFieldKeys())
	}

	var fields []serverShowField
	for _, field := range serverShowFields {
		if wanted[field.key] {
			fields = append(fields, field)
		}
	}

	settingKeys := make(map[string]bool)
	for key := range wanted {
		if _, ok := transmission.LookupSetting(key); ok {
			settingKeys[key] = true
		}
	}

	return fields, settingKeys, nil
}

// isServerShowField reports whether a key names a connection field or a setting.
func isServerShowField(key string) bool {
	for _, field := range serverShowFields {
		if field.key == key {
			return true
		}
	}

	_, ok := transmission.LookupSetting(key)

	return ok
}

// validServerShowFieldKeys lists every selectable --fields key in display order —
// the connection fields first, then the settings in registry order.
func validServerShowFieldKeys() string {
	settingKeys := transmission.SettingKeys()

	keys := make([]string, 0, len(serverShowFields)+len(settingKeys))
	for _, field := range serverShowFields {
		keys = append(keys, field.key)
	}
	keys = append(keys, settingKeys...)

	return strings.Join(keys, ", ")
}

// writeServerShow renders the result as plaintext: on a TTY an aligned
// "Label: value" connection block, a blank line, then the CATEGORY KEY VALUE
// settings table; when piped or when NO_COLOR is set, header-less
// "key<TAB>value" connection lines followed directly by the tab-separated
// settings rows, with no blank separator so the stream stays script-stable. The
// connection block and the settings table use separate tabwriters so each aligns
// on its own widths. live reports whether the server was contacted; when it was
// not, the live-only fields are dropped rather than rendered empty. The JSON/jq
// paths never reach here.
func writeServerShow(cmd *cobra.Command, result serverShowResult, fields []serverShowField, live bool) {
	out := cmd.OutOrStdout()
	shown := visibleServerShowFields(fields, live)

	if isTTY(out) {
		writeServerShowBlock(out, result, shown)

		if len(result.Settings) > 0 {
			if len(shown) > 0 {
				_, _ = fmt.Fprintln(out)
			}
			writeSettingsTable(out, result.Settings)
		}

		return
	}

	for _, field := range shown {
		_, _ = fmt.Fprintf(out, "%s\t%s\n", field.key, serverShowCell(field, result))
	}

	writeSettingsPiped(out, result.Settings)
}

// visibleServerShowFields drops the live-only fields when the server was not
// contacted, so an offline view reports nothing it could not know rather than a
// row of blanks.
func visibleServerShowFields(fields []serverShowField, live bool) []serverShowField {
	if live {
		return fields
	}

	shown := make([]serverShowField, 0, len(fields))
	for _, field := range fields {
		if !field.live {
			shown = append(shown, field)
		}
	}

	return shown
}

// writeServerShowBlock writes the aligned "Label: value" connection block. Unset
// strings render as "(not set)" via orNotSet, matching the rest of the registry
// commands.
func writeServerShowBlock(w io.Writer, result serverShowResult, fields []serverShowField) {
	if len(fields) == 0 {
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 1, ' ', 0)
	for _, field := range fields {
		_, _ = fmt.Fprintf(tw, "%s:\t%s\n", field.label, orNotSet(serverShowCell(field, result)))
	}
	_ = tw.Flush()
}

// serverShowCell renders one connection field, neutralizing terminal control
// sequences for the values that come from the config file or the server itself
// (both untrusted on the human path). The JSON/jq paths never call it.
func serverShowCell(field serverShowField, result serverShowResult) string {
	v := field.value(result)
	if field.sanitize {
		v = output.SanitizeTerminal(v)
	}

	return v
}
