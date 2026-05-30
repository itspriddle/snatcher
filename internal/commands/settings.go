package commands

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/torrent"
)

// settingType is the value type of a session setting, driving how a raw string
// argument is parsed and how a typed value is formatted for plaintext output.
type settingType int

const (
	// typeBool is a boolean setting parsed from "true"/"false".
	typeBool settingType = iota

	// typeInt is a plain int setting.
	typeInt

	// typeInt64 is an int64 setting (used for the speed-limit fields).
	typeInt64

	// typeFloat is a float64 setting (the seed-ratio limit).
	typeFloat

	// typeString is a free-form string setting.
	typeString

	// typeEnum is a string setting constrained to a fixed set of allowed values
	// (matched case-insensitively); see settingDef.allowed.
	typeEnum
)

// settingCategory is the grouping a setting belongs to, used by --category
// filtering and the CATEGORY column of the list output.
type settingCategory string

const (
	// categoryDownload groups download-location and seeding settings.
	categoryDownload settingCategory = "download"

	// categoryNetwork groups peer, port, encryption, and blocklist settings.
	categoryNetwork settingCategory = "network"

	// categoryBandwidth groups the speed-limit and alt-speed schedule settings.
	categoryBandwidth settingCategory = "bandwidth"

	// categoryQueue groups the download/seed queue settings.
	categoryQueue settingCategory = "queue"
)

// settingCategories lists every valid category in display order. It is the
// single source of truth for the --category validation message and for
// allCategories.
var settingCategories = []settingCategory{
	categoryDownload,
	categoryNetwork,
	categoryBandwidth,
	categoryQueue,
}

// settingDef describes a single session setting: the neutral kebab-case key,
// its category, value type, the Transmission default (used by the reset
// command), whether it is read-only (set/reset reject it), the allowed values
// for an enum, and an accessor returning the current typed value from a
// SessionSettings snapshot.
type settingDef struct {
	// key is the neutral, kebab-case setting key (the user-facing identifier and
	// the JSON contract's "key").
	key string

	// category is the setting's grouping.
	category settingCategory

	// typ is the value type, driving parsing and formatting.
	typ settingType

	// def is the Transmission default value, used by reset. It is nil for the
	// read-only blocklist-size, which has no settable default.
	def any

	// readOnly marks settings that appear in list/get but that set/reset reject.
	readOnly bool

	// allowed lists the permitted values for a typeEnum setting (lowercase),
	// matched case-insensitively. It is nil for non-enum settings.
	allowed []string

	// accessor returns the setting's current typed value from a session snapshot.
	accessor func(torrent.SessionSettings) any
}

// settingRegistry is the single source of truth for every session setting the
// snatcher settings commands operate on. It is ordered by category (download,
// network, bandwidth, queue) so list output and any derived ordering follow it.
// The defaults are Transmission's documented defaults, baked in because the
// Transmission RPC has no "reset to default" primitive.
var settingRegistry = []settingDef{
	// Download.
	{key: "download-dir", category: categoryDownload, typ: typeString, def: "~/Downloads",
		accessor: func(s torrent.SessionSettings) any { return s.DownloadDir }},
	{key: "start-added-torrents", category: categoryDownload, typ: typeBool, def: true,
		accessor: func(s torrent.SessionSettings) any { return s.StartAddedTorrents }},
	{key: "rename-partial-files", category: categoryDownload, typ: typeBool, def: true,
		accessor: func(s torrent.SessionSettings) any { return s.RenamePartialFiles }},
	{key: "incomplete-dir-enabled", category: categoryDownload, typ: typeBool, def: false,
		accessor: func(s torrent.SessionSettings) any { return s.IncompleteDirEnabled }},
	{key: "incomplete-dir", category: categoryDownload, typ: typeString, def: "",
		accessor: func(s torrent.SessionSettings) any { return s.IncompleteDir }},
	{key: "seed-ratio-limited", category: categoryDownload, typ: typeBool, def: false,
		accessor: func(s torrent.SessionSettings) any { return s.SeedRatioLimited }},
	{key: "seed-ratio-limit", category: categoryDownload, typ: typeFloat, def: 2.0,
		accessor: func(s torrent.SessionSettings) any { return s.SeedRatioLimit }},
	{key: "idle-seeding-limit-enabled", category: categoryDownload, typ: typeBool, def: false,
		accessor: func(s torrent.SessionSettings) any { return s.IdleSeedingLimitEnabled }},
	{key: "idle-seeding-limit", category: categoryDownload, typ: typeInt, def: 30,
		accessor: func(s torrent.SessionSettings) any { return s.IdleSeedingLimit }},
	{key: "cache-size-mib", category: categoryDownload, typ: typeInt, def: 4,
		accessor: func(s torrent.SessionSettings) any { return s.CacheSizeMiB }},

	// Network.
	{key: "peer-port", category: categoryNetwork, typ: typeInt, def: 51413,
		accessor: func(s torrent.SessionSettings) any { return s.PeerPort }},
	{key: "peer-port-random-on-start", category: categoryNetwork, typ: typeBool, def: false,
		accessor: func(s torrent.SessionSettings) any { return s.PeerPortRandomOnStart }},
	{key: "port-forwarding-enabled", category: categoryNetwork, typ: typeBool, def: true,
		accessor: func(s torrent.SessionSettings) any { return s.PortForwardingEnabled }},
	{key: "encryption", category: categoryNetwork, typ: typeEnum, def: "preferred",
		allowed:  []string{"allowed", "preferred", "required"},
		accessor: func(s torrent.SessionSettings) any { return s.Encryption }},
	{key: "peer-limit-global", category: categoryNetwork, typ: typeInt, def: 200,
		accessor: func(s torrent.SessionSettings) any { return s.PeerLimitGlobal }},
	{key: "peer-limit-per-torrent", category: categoryNetwork, typ: typeInt, def: 50,
		accessor: func(s torrent.SessionSettings) any { return s.PeerLimitPerTorrent }},
	{key: "pex-enabled", category: categoryNetwork, typ: typeBool, def: true,
		accessor: func(s torrent.SessionSettings) any { return s.PexEnabled }},
	{key: "dht-enabled", category: categoryNetwork, typ: typeBool, def: true,
		accessor: func(s torrent.SessionSettings) any { return s.DHTEnabled }},
	{key: "lpd-enabled", category: categoryNetwork, typ: typeBool, def: false,
		accessor: func(s torrent.SessionSettings) any { return s.LPDEnabled }},
	{key: "utp-enabled", category: categoryNetwork, typ: typeBool, def: true,
		accessor: func(s torrent.SessionSettings) any { return s.UTPEnabled }},
	{key: "blocklist-enabled", category: categoryNetwork, typ: typeBool, def: false,
		accessor: func(s torrent.SessionSettings) any { return s.BlocklistEnabled }},
	{key: "blocklist-url", category: categoryNetwork, typ: typeString, def: "http://www.example.com/blocklist",
		accessor: func(s torrent.SessionSettings) any { return s.BlocklistURL }},
	{key: "blocklist-size", category: categoryNetwork, typ: typeInt, def: nil, readOnly: true,
		accessor: func(s torrent.SessionSettings) any { return s.BlocklistSize }},

	// Bandwidth.
	{key: "speed-limit-down-enabled", category: categoryBandwidth, typ: typeBool, def: false,
		accessor: func(s torrent.SessionSettings) any { return s.SpeedLimitDownEnabled }},
	{key: "speed-limit-down", category: categoryBandwidth, typ: typeInt64, def: int64(100),
		accessor: func(s torrent.SessionSettings) any { return s.SpeedLimitDown }},
	{key: "speed-limit-up-enabled", category: categoryBandwidth, typ: typeBool, def: false,
		accessor: func(s torrent.SessionSettings) any { return s.SpeedLimitUpEnabled }},
	{key: "speed-limit-up", category: categoryBandwidth, typ: typeInt64, def: int64(100),
		accessor: func(s torrent.SessionSettings) any { return s.SpeedLimitUp }},
	{key: "alt-speed-enabled", category: categoryBandwidth, typ: typeBool, def: false,
		accessor: func(s torrent.SessionSettings) any { return s.AltSpeedEnabled }},
	{key: "alt-speed-down", category: categoryBandwidth, typ: typeInt64, def: int64(50),
		accessor: func(s torrent.SessionSettings) any { return s.AltSpeedDown }},
	{key: "alt-speed-up", category: categoryBandwidth, typ: typeInt64, def: int64(50),
		accessor: func(s torrent.SessionSettings) any { return s.AltSpeedUp }},
	{key: "alt-speed-time-enabled", category: categoryBandwidth, typ: typeBool, def: false,
		accessor: func(s torrent.SessionSettings) any { return s.AltSpeedTimeEnabled }},
	{key: "alt-speed-time-begin", category: categoryBandwidth, typ: typeInt, def: 540,
		accessor: func(s torrent.SessionSettings) any { return s.AltSpeedTimeBegin }},
	{key: "alt-speed-time-end", category: categoryBandwidth, typ: typeInt, def: 1020,
		accessor: func(s torrent.SessionSettings) any { return s.AltSpeedTimeEnd }},
	{key: "alt-speed-time-day", category: categoryBandwidth, typ: typeInt, def: 127,
		accessor: func(s torrent.SessionSettings) any { return s.AltSpeedTimeDay }},

	// Queue.
	{key: "download-queue-enabled", category: categoryQueue, typ: typeBool, def: true,
		accessor: func(s torrent.SessionSettings) any { return s.DownloadQueueEnabled }},
	{key: "download-queue-size", category: categoryQueue, typ: typeInt, def: 5,
		accessor: func(s torrent.SessionSettings) any { return s.DownloadQueueSize }},
	{key: "seed-queue-enabled", category: categoryQueue, typ: typeBool, def: false,
		accessor: func(s torrent.SessionSettings) any { return s.SeedQueueEnabled }},
	{key: "seed-queue-size", category: categoryQueue, typ: typeInt, def: 10,
		accessor: func(s torrent.SessionSettings) any { return s.SeedQueueSize }},
	{key: "queue-stalled-enabled", category: categoryQueue, typ: typeBool, def: true,
		accessor: func(s torrent.SessionSettings) any { return s.QueueStalledEnabled }},
	{key: "queue-stalled-minutes", category: categoryQueue, typ: typeInt, def: 30,
		accessor: func(s torrent.SessionSettings) any { return s.QueueStalledMinutes }},
}

// settingsByKey indexes the registry by key for O(1) lookup. It is built once at
// package init from settingRegistry, the source of truth.
var settingsByKey = func() map[string]settingDef {
	m := make(map[string]settingDef, len(settingRegistry))
	for _, def := range settingRegistry {
		m[def.key] = def
	}

	return m
}()

// lookupSetting returns the registry entry for a key and whether it exists.
// Lookup is exact (keys are already normalized kebab-case).
func lookupSetting(key string) (settingDef, bool) {
	def, ok := settingsByKey[key]

	return def, ok
}

// allSettingKeys returns every setting key in registry order, for the
// valid-keys list in error messages.
func allSettingKeys() []string {
	keys := make([]string, len(settingRegistry))
	for i, def := range settingRegistry {
		keys[i] = def.key
	}

	return keys
}

// allCategoryNames returns every category name in display order, for the
// valid-categories list in error messages.
func allCategoryNames() []string {
	names := make([]string, len(settingCategories))
	for i, c := range settingCategories {
		names[i] = string(c)
	}

	return names
}

// parseCategory validates a --category value (case-insensitively) and returns
// the canonical category. An unknown value returns an error listing the valid
// categories.
func parseCategory(s string) (settingCategory, error) {
	c := settingCategory(strings.ToLower(strings.TrimSpace(s)))
	for _, valid := range settingCategories {
		if c == valid {
			return c, nil
		}
	}

	return "", fmt.Errorf("invalid category %q: valid categories are %s", s, strings.Join(allCategoryNames(), ", "))
}

// filterSettings returns the registry entries matching the optional category and
// the optional case-insensitive key substring, preserving registry order. An
// empty category matches every category; an empty search matches every key.
func filterSettings(category settingCategory, search string) []settingDef {
	needle := strings.ToLower(strings.TrimSpace(search))

	out := make([]settingDef, 0, len(settingRegistry))
	for _, def := range settingRegistry {
		if category != "" && def.category != category {
			continue
		}
		if needle != "" && !strings.Contains(def.key, needle) {
			continue
		}
		out = append(out, def)
	}

	return out
}

// parseSettingValue parses a raw string argument into the typed value for the
// setting, per its type. Bools and enums match case-insensitively. Parse
// failures return a clear, type-specific error.
func (d settingDef) parseSettingValue(raw string) (any, error) {
	switch d.typ {
	case typeBool:
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return nil, fmt.Errorf("invalid value for %s: must be true or false", d.key)
		}
	case typeInt:
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("invalid value for %s: must be an integer", d.key)
		}

		return n, nil
	case typeInt64:
		n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid value for %s: must be an integer", d.key)
		}

		return n, nil
	case typeFloat:
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid value for %s: must be a number", d.key)
		}

		return f, nil
	case typeEnum:
		lowered := strings.ToLower(strings.TrimSpace(raw))
		for _, allowed := range d.allowed {
			if lowered == allowed {
				return allowed, nil
			}
		}

		return nil, fmt.Errorf("invalid value for %s: must be one of: %s", d.key, strings.Join(d.allowed, ", "))
	case typeString:
		return raw, nil
	default:
		return nil, fmt.Errorf("invalid value for %s: unsupported type", d.key)
	}
}

// formatSettingValue renders a typed value to its plaintext string: bools as
// "true"/"false", ints in decimal, floats via strconv with the shortest exact
// representation, and strings as-is. It does not sanitize; callers route
// server-derived string values through output.SanitizeTerminal on the human
// path.
func formatSettingValue(v any) string {
	switch val := v.(type) {
	case bool:
		return strconv.FormatBool(val)
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case string:
		return val
	default:
		return fmt.Sprintf("%v", val)
	}
}

// serverDerivedSettings is the set of settings whose current value comes from
// the remote server and is therefore untrusted: it must pass through
// output.SanitizeTerminal on the human/plaintext path (and is never sanitized on
// the --json/--jq path, a structured contract). The string keys carry free-form
// server text; encryption is an enum we constrain on the write path, but the
// value read back is server-controlled and could carry escape sequences from a
// compromised or MITM'd server, so it is sanitized too.
var serverDerivedSettings = map[string]bool{
	"download-dir":   true,
	"incomplete-dir": true,
	"blocklist-url":  true,
	"encryption":     true,
}

// settingRow is the per-setting element of the settings JSON contract: a stable,
// jq-friendly {key, category, value} object whose Value carries the native typed
// value (bool / number / string). It is the array element emitted by --json/--jq.
type settingRow struct {
	Key      string `json:"key"`
	Category string `json:"category"`
	Value    any    `json:"value"`
}

// NewSettingsCmd builds the "settings" command. With no subcommand it lists every
// session setting in a list-style table (CATEGORY KEY VALUE), filterable by
// --category and --search, in plaintext / --json / --jq modes. It dials the
// backend via the shared dial helper and carries its own --config flag, like
// list and the auth subcommands.
func NewSettingsCmd() *cobra.Command {
	var (
		configPath   string
		asJSON       bool
		jqExpr       string
		categoryFlag string
		searchFlag   string
	)

	cmd := &cobra.Command{
		Use:           "settings",
		Short:         "Manage server settings",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var category settingCategory
			if categoryFlag != "" {
				parsed, err := parseCategory(categoryFlag)
				if err != nil {
					return err
				}
				category = parsed
			}

			backend, serverURL, err := dial(configPath)
			if err != nil {
				return err
			}

			session, err := backend.GetSession(cmd.Context())
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			defs := filterSettings(category, searchFlag)

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), settingRows(defs, session), jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), settingRows(defs, session))
			}

			writeSettingsPlaintext(cmd, defs, session)

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().StringVar(&categoryFlag, "category", "", "filter by category (download, network, bandwidth, queue)")
	cmd.Flags().StringVar(&searchFlag, "search", "", "filter to keys containing this text (case-insensitive)")

	cmd.AddCommand(newSettingsGetCmd())
	cmd.AddCommand(newSettingsSetCmd())
	cmd.AddCommand(newSettingsResetCmd())

	return cmd
}

// newSettingsGetCmd builds "settings get <key>", which prints one setting's
// current value. Plaintext emits only the bare value (so it composes in scripts,
// e.g. PORT=$(snatcher settings get peer-port)); --json emits a single {key,
// category, value} object and --jq filters that object. An unknown key errors
// with the list of valid keys.
func newSettingsGetCmd() *cobra.Command {
	var (
		configPath string
		asJSON     bool
		jqExpr     string
	)

	cmd := &cobra.Command{
		Use:           "get <key>",
		Short:         "Show one setting's value",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			def, ok := lookupSetting(args[0])
			if !ok {
				return fmt.Errorf("unknown setting %q: valid settings are %s", args[0], strings.Join(allSettingKeys(), ", "))
			}

			backend, serverURL, err := dial(configPath)
			if err != nil {
				return err
			}

			session, err := backend.GetSession(cmd.Context())
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			row := settingRow{
				Key:      def.key,
				Category: string(def.category),
				Value:    def.accessor(session),
			}

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), row, jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), row)
			}

			_, _ = fmt.Fprintln(cmd.OutOrStdout(), settingCell(def, session))

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// newSettingsSetCmd builds "settings set <key> <value> [<key> <value>...]",
// which writes one or more settings in a single call. Pairs are validated
// all-or-nothing before any network write: unknown keys, the read-only
// blocklist-size, and type/enum parse failures are collected and reported
// together, so an invalid pair aborts the whole command with no partial write.
// On success it writes via backend.SetSession, re-fetches via GetSession, and
// reports the server's actual post-set values (key: value per line on
// plaintext; the {key, category, value} array on --json/--jq).
func newSettingsSetCmd() *cobra.Command {
	var (
		configPath string
		asJSON     bool
		jqExpr     string
	)

	cmd := &cobra.Command{
		Use:           "set <key> <value> [<key> <value>...]",
		Short:         "Set one or more settings",
		Args:          cobra.MinimumNArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			defs, changes, err := parseSettingAssignments(args)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(configPath)
			if err != nil {
				return err
			}

			if err := backend.SetSession(cmd.Context(), changes); err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			session, err := backend.GetSession(cmd.Context())
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), settingRows(defs, session), jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), settingRows(defs, session))
			}

			out := cmd.OutOrStdout()
			for _, def := range defs {
				_, _ = fmt.Fprintf(out, "%s: %s\n", def.key, settingCell(def, session))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// parseSettingAssignments turns the positional key/value arguments into the
// ordered definitions to report and the neutral-key change map to write. It
// validates all-or-nothing: an odd argument count, unknown keys, the read-only
// blocklist-size, and per-value parse failures are all collected and returned
// together as a single error so nothing is sent on any failure. The returned
// defs preserve argument order; a key repeated on the command line takes its
// last value (and is reported once).
func parseSettingAssignments(args []string) ([]settingDef, map[string]any, error) {
	if len(args)%2 != 0 {
		return nil, nil, fmt.Errorf("set requires an even number of arguments (key value pairs); got %d", len(args))
	}

	var (
		problems []string
		order    []string
		defs     = make(map[string]settingDef)
		changes  = make(map[string]any)
	)

	for i := 0; i < len(args); i += 2 {
		key, raw := args[i], args[i+1]

		def, ok := lookupSetting(key)
		if !ok {
			problems = append(problems, fmt.Sprintf("unknown setting %q", key))
			continue
		}

		if def.readOnly {
			problems = append(problems, fmt.Sprintf("setting %q is read-only", key))
			continue
		}

		value, err := def.parseSettingValue(raw)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}

		if _, seen := defs[key]; !seen {
			order = append(order, key)
		}
		defs[key] = def
		changes[key] = value
	}

	if len(problems) > 0 {
		return nil, nil, fmt.Errorf("invalid settings: %s\nvalid settings are %s",
			strings.Join(problems, "; "), strings.Join(allSettingKeys(), ", "))
	}

	ordered := make([]settingDef, len(order))
	for i, key := range order {
		ordered[i] = defs[key]
	}

	return ordered, changes, nil
}

// newSettingsResetCmd builds "settings reset <key>", which restores a single
// setting to its Transmission default (baked into the registry, since the RPC
// has no reset primitive). An unknown key errors with the valid-keys list and
// the read-only blocklist-size is rejected before any network write. On success
// it writes the default via backend.SetSession, re-fetches via GetSession, and
// reports the server's confirmed value using the same output shape as set
// (key: value on plaintext; the {key, category, value} array on --json/--jq).
func newSettingsResetCmd() *cobra.Command {
	var (
		configPath string
		asJSON     bool
		jqExpr     string
	)

	cmd := &cobra.Command{
		Use:           "reset <key>",
		Short:         "Reset one setting to its default",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]

			def, ok := lookupSetting(key)
			if !ok {
				return fmt.Errorf("unknown setting %q: valid settings are %s", key, strings.Join(allSettingKeys(), ", "))
			}

			if def.readOnly {
				return fmt.Errorf("setting %q is read-only", key)
			}

			backend, serverURL, err := dial(configPath)
			if err != nil {
				return err
			}

			changes := map[string]any{def.key: def.def}
			if err := backend.SetSession(cmd.Context(), changes); err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			session, err := backend.GetSession(cmd.Context())
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			defs := []settingDef{def}

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), settingRows(defs, session), jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), settingRows(defs, session))
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", def.key, settingCell(def, session))

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// settingRows builds the {key, category, value} JSON rows for the given
// definitions from a session snapshot, in registry order. Values are the native
// typed values (never sanitized — JSON/jq is a structured contract). An empty
// set serializes as "[]" rather than "null".
func settingRows(defs []settingDef, session torrent.SessionSettings) []settingRow {
	rows := make([]settingRow, len(defs))
	for i, def := range defs {
		rows[i] = settingRow{
			Key:      def.key,
			Category: string(def.category),
			Value:    def.accessor(session),
		}
	}

	return rows
}

// writeSettingsPlaintext renders the settings in the appropriate plaintext mode:
// an aligned CATEGORY KEY VALUE table with a header on a TTY, or plain
// tab-separated, header-less rows when piped or when NO_COLOR is set. It mirrors
// the list command's TTY/piped split (isTTY).
func writeSettingsPlaintext(cmd *cobra.Command, defs []settingDef, session torrent.SessionSettings) {
	out := cmd.OutOrStdout()

	if isTTY(out) {
		writeSettingsTable(out, defs, session)
		return
	}

	writeSettingsPiped(out, defs, session)
}

// settingCell renders a setting's current value as plaintext, neutralizing
// terminal control sequences for server-derived string settings (sanitize) so a
// crafted download-dir cannot inject ANSI escapes. The JSON/jq paths never call
// it.
func settingCell(def settingDef, session torrent.SessionSettings) string {
	v := formatSettingValue(def.accessor(session))
	if serverDerivedSettings[def.key] {
		v = output.SanitizeTerminal(v)
	}

	return v
}

// writeSettingsPiped writes plain, tab-separated CATEGORY KEY VALUE rows with no
// header — the stable scripting contract. An empty set writes nothing.
func writeSettingsPiped(w io.Writer, defs []settingDef, session torrent.SessionSettings) {
	for _, def := range defs {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", def.category, def.key, settingCell(def, session))
	}
}

// writeSettingsTable writes a whitespace-aligned CATEGORY KEY VALUE table with a
// header row. An empty set writes a friendly "No settings." line.
func writeSettingsTable(w io.Writer, defs []settingDef, session torrent.SessionSettings) {
	if len(defs) == 0 {
		_, _ = fmt.Fprintln(w, "No settings.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	_, _ = fmt.Fprintln(tw, "CATEGORY\tKEY\tVALUE")
	for _, def := range defs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", def.category, def.key, settingCell(def, session))
	}

	_ = tw.Flush()
}
