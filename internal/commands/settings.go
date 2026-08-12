package commands

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// settingType is the value type of a session setting, driving the type of the
// per-setting flag on "server update" and how a typed value is formatted for
// plaintext output.
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
// filtering and the CATEGORY column of the "server show" settings table.
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
// its category, value type, the Transmission default (used by "server reset"),
// whether it is read-only (update/reset reject it), the allowed values for an
// enum, and an accessor returning the current typed value from a SessionSettings
// snapshot.
type settingDef struct {
	// key is the neutral, kebab-case setting key (the user-facing identifier and
	// the JSON contract's "key").
	key string

	// category is the setting's grouping.
	category settingCategory

	// typ is the value type, driving parsing and formatting.
	typ settingType

	// def is the Transmission default value, used by "server reset". It is nil for
	// the read-only blocklist-size, which has no settable default.
	def any

	// readOnly marks settings that "server show" reports but that "server update"
	// and "server reset" reject.
	readOnly bool

	// allowed lists the permitted values for a typeEnum setting (lowercase),
	// matched case-insensitively. It is nil for non-enum settings.
	allowed []string

	// desc is a concise, one-line human description used as the help text for the
	// per-setting flag (server update --<key>) and the man page. Every settable
	// setting must have a non-empty desc; the read-only blocklist-size, which gets
	// no flag, leaves it empty.
	desc string

	// accessor returns the setting's current typed value from a session snapshot.
	accessor func(transmission.SessionSettings) any
}

// settingRegistry is the single source of truth for every session setting the
// server commands operate on: "server show" reads it, "server update" generates
// one flag per settable entry from it, and "server reset" restores its defaults.
// It is ordered by category (download, network, bandwidth, queue) so output and
// any derived ordering follow it. The defaults are Transmission's documented
// defaults, baked in because the Transmission RPC has no "reset to default"
// primitive.
var settingRegistry = []settingDef{
	// Download.
	{key: "download-dir", category: categoryDownload, typ: typeString, def: "~/Downloads",
		desc:     "Default directory for completed downloads",
		accessor: func(s transmission.SessionSettings) any { return s.DownloadDir }},
	{key: "start-added-torrents", category: categoryDownload, typ: typeBool, def: true,
		desc:     "Start torrents automatically when added",
		accessor: func(s transmission.SessionSettings) any { return s.StartAddedTorrents }},
	{key: "rename-partial-files", category: categoryDownload, typ: typeBool, def: true,
		desc:     "Append .part to incomplete files",
		accessor: func(s transmission.SessionSettings) any { return s.RenamePartialFiles }},
	{key: "incomplete-dir-enabled", category: categoryDownload, typ: typeBool, def: false,
		desc:     "Keep incomplete downloads in a separate directory",
		accessor: func(s transmission.SessionSettings) any { return s.IncompleteDirEnabled }},
	{key: "incomplete-dir", category: categoryDownload, typ: typeString, def: "",
		desc:     "Directory for incomplete downloads (when enabled)",
		accessor: func(s transmission.SessionSettings) any { return s.IncompleteDir }},
	{key: "seed-ratio-limited", category: categoryDownload, typ: typeBool, def: false,
		desc:     "Stop seeding at the global seed-ratio limit",
		accessor: func(s transmission.SessionSettings) any { return s.SeedRatioLimited }},
	{key: "seed-ratio-limit", category: categoryDownload, typ: typeFloat, def: 2.0,
		desc:     "Global seed ratio to stop seeding at (when limited)",
		accessor: func(s transmission.SessionSettings) any { return s.SeedRatioLimit }},
	{key: "idle-seeding-limit-enabled", category: categoryDownload, typ: typeBool, def: false,
		desc:     "Stop seeding torrents that have been idle too long",
		accessor: func(s transmission.SessionSettings) any { return s.IdleSeedingLimitEnabled }},
	{key: "idle-seeding-limit", category: categoryDownload, typ: typeInt, def: 30,
		desc:     "Idle time in minutes before stopping seeding (when enabled)",
		accessor: func(s transmission.SessionSettings) any { return s.IdleSeedingLimit }},
	{key: "cache-size-mib", category: categoryDownload, typ: typeInt, def: 4,
		desc:     "Disk cache size in MiB",
		accessor: func(s transmission.SessionSettings) any { return s.CacheSizeMiB }},

	// Network.
	{key: "peer-port", category: categoryNetwork, typ: typeInt, def: 51413,
		desc:     "Port to listen for incoming peer connections",
		accessor: func(s transmission.SessionSettings) any { return s.PeerPort }},
	{key: "peer-port-random-on-start", category: categoryNetwork, typ: typeBool, def: false,
		desc:     "Pick a random peer port on each start",
		accessor: func(s transmission.SessionSettings) any { return s.PeerPortRandomOnStart }},
	{key: "port-forwarding-enabled", category: categoryNetwork, typ: typeBool, def: true,
		desc:     "Request UPnP/NAT-PMP port forwarding",
		accessor: func(s transmission.SessionSettings) any { return s.PortForwardingEnabled }},
	{key: "encryption", category: categoryNetwork, typ: typeEnum, def: "preferred",
		allowed:  []string{"allowed", "preferred", "required"},
		desc:     "Peer encryption mode (allowed, preferred, required)",
		accessor: func(s transmission.SessionSettings) any { return s.Encryption }},
	{key: "peer-limit-global", category: categoryNetwork, typ: typeInt, def: 200,
		desc:     "Maximum peer connections across all torrents",
		accessor: func(s transmission.SessionSettings) any { return s.PeerLimitGlobal }},
	{key: "peer-limit-per-torrent", category: categoryNetwork, typ: typeInt, def: 50,
		desc:     "Maximum peer connections per torrent",
		accessor: func(s transmission.SessionSettings) any { return s.PeerLimitPerTorrent }},
	{key: "pex-enabled", category: categoryNetwork, typ: typeBool, def: true,
		desc:     "Enable Peer Exchange (PEX)",
		accessor: func(s transmission.SessionSettings) any { return s.PexEnabled }},
	{key: "dht-enabled", category: categoryNetwork, typ: typeBool, def: true,
		desc:     "Enable the Distributed Hash Table (DHT)",
		accessor: func(s transmission.SessionSettings) any { return s.DHTEnabled }},
	{key: "lpd-enabled", category: categoryNetwork, typ: typeBool, def: false,
		desc:     "Enable Local Peer Discovery (LPD)",
		accessor: func(s transmission.SessionSettings) any { return s.LPDEnabled }},
	{key: "utp-enabled", category: categoryNetwork, typ: typeBool, def: true,
		desc:     "Enable the uTP transport protocol",
		accessor: func(s transmission.SessionSettings) any { return s.UTPEnabled }},
	{key: "blocklist-enabled", category: categoryNetwork, typ: typeBool, def: false,
		desc:     "Block peers listed in the blocklist",
		accessor: func(s transmission.SessionSettings) any { return s.BlocklistEnabled }},
	{key: "blocklist-url", category: categoryNetwork, typ: typeString, def: "http://www.example.com/blocklist",
		desc:     "URL to fetch the peer blocklist from",
		accessor: func(s transmission.SessionSettings) any { return s.BlocklistURL }},
	{key: "blocklist-size", category: categoryNetwork, typ: typeInt, def: nil, readOnly: true,
		accessor: func(s transmission.SessionSettings) any { return s.BlocklistSize }},

	// Bandwidth.
	{key: "speed-limit-down-enabled", category: categoryBandwidth, typ: typeBool, def: false,
		desc:     "Enable the global download speed limit",
		accessor: func(s transmission.SessionSettings) any { return s.SpeedLimitDownEnabled }},
	{key: "speed-limit-down", category: categoryBandwidth, typ: typeInt64, def: int64(100),
		desc:     "Global download speed limit in KB/s (when enabled)",
		accessor: func(s transmission.SessionSettings) any { return s.SpeedLimitDown }},
	{key: "speed-limit-up-enabled", category: categoryBandwidth, typ: typeBool, def: false,
		desc:     "Enable the global upload speed limit",
		accessor: func(s transmission.SessionSettings) any { return s.SpeedLimitUpEnabled }},
	{key: "speed-limit-up", category: categoryBandwidth, typ: typeInt64, def: int64(100),
		desc:     "Global upload speed limit in KB/s (when enabled)",
		accessor: func(s transmission.SessionSettings) any { return s.SpeedLimitUp }},
	{key: "alt-speed-enabled", category: categoryBandwidth, typ: typeBool, def: false,
		desc:     "Use the alternative (throttled) speed limits now",
		accessor: func(s transmission.SessionSettings) any { return s.AltSpeedEnabled }},
	{key: "alt-speed-down", category: categoryBandwidth, typ: typeInt64, def: int64(50),
		desc:     "Alternative download speed limit in KB/s",
		accessor: func(s transmission.SessionSettings) any { return s.AltSpeedDown }},
	{key: "alt-speed-up", category: categoryBandwidth, typ: typeInt64, def: int64(50),
		desc:     "Alternative upload speed limit in KB/s",
		accessor: func(s transmission.SessionSettings) any { return s.AltSpeedUp }},
	{key: "alt-speed-time-enabled", category: categoryBandwidth, typ: typeBool, def: false,
		desc:     "Switch to alternative speeds on a daily schedule",
		accessor: func(s transmission.SessionSettings) any { return s.AltSpeedTimeEnabled }},
	{key: "alt-speed-time-begin", category: categoryBandwidth, typ: typeInt, def: 540,
		desc:     "Alternative-speed schedule start (minutes after midnight)",
		accessor: func(s transmission.SessionSettings) any { return s.AltSpeedTimeBegin }},
	{key: "alt-speed-time-end", category: categoryBandwidth, typ: typeInt, def: 1020,
		desc:     "Alternative-speed schedule end (minutes after midnight)",
		accessor: func(s transmission.SessionSettings) any { return s.AltSpeedTimeEnd }},
	{key: "alt-speed-time-day", category: categoryBandwidth, typ: typeInt, def: 127,
		desc:     "Days the alternative-speed schedule runs (bitfield, Sun=1)",
		accessor: func(s transmission.SessionSettings) any { return s.AltSpeedTimeDay }},

	// Queue.
	{key: "download-queue-enabled", category: categoryQueue, typ: typeBool, def: true,
		desc:     "Limit how many torrents download at once",
		accessor: func(s transmission.SessionSettings) any { return s.DownloadQueueEnabled }},
	{key: "download-queue-size", category: categoryQueue, typ: typeInt, def: 5,
		desc:     "Maximum torrents downloading at once (when enabled)",
		accessor: func(s transmission.SessionSettings) any { return s.DownloadQueueSize }},
	{key: "seed-queue-enabled", category: categoryQueue, typ: typeBool, def: false,
		desc:     "Limit how many torrents seed at once",
		accessor: func(s transmission.SessionSettings) any { return s.SeedQueueEnabled }},
	{key: "seed-queue-size", category: categoryQueue, typ: typeInt, def: 10,
		desc:     "Maximum torrents seeding at once (when enabled)",
		accessor: func(s transmission.SessionSettings) any { return s.SeedQueueSize }},
	{key: "queue-stalled-enabled", category: categoryQueue, typ: typeBool, def: true,
		desc:     "Ignore stalled torrents when counting the queue",
		accessor: func(s transmission.SessionSettings) any { return s.QueueStalledEnabled }},
	{key: "queue-stalled-minutes", category: categoryQueue, typ: typeInt, def: 30,
		desc:     "Idle minutes before a transfer counts as stalled",
		accessor: func(s transmission.SessionSettings) any { return s.QueueStalledMinutes }},
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

// validateEnum matches a raw enum value case-insensitively (and after trimming)
// against the setting's allowed list, returning the canonical lowercase value. A
// value outside the list returns a clear error naming the allowed values.
func (d settingDef) validateEnum(raw string) (string, error) {
	lowered := strings.ToLower(strings.TrimSpace(raw))
	for _, allowed := range d.allowed {
		if lowered == allowed {
			return allowed, nil
		}
	}

	return "", fmt.Errorf("invalid value for %s: must be one of: %s", d.key, strings.Join(d.allowed, ", "))
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

// registerSettingFlags adds one flag per settable registry entry to the update
// command. The flag name is the setting key and its type follows the setting's
// settingType; the help text comes from the setting's desc. The read-only
// blocklist-size gets no flag (it has no desc and cannot be written).
func registerSettingFlags(cmd *cobra.Command) {
	for _, def := range settingRegistry {
		if def.readOnly {
			continue
		}

		switch def.typ {
		case typeBool:
			cmd.Flags().Bool(def.key, false, def.desc)
		case typeInt:
			cmd.Flags().Int(def.key, 0, def.desc)
		case typeInt64:
			cmd.Flags().Int64(def.key, 0, def.desc)
		case typeFloat:
			cmd.Flags().Float64(def.key, 0, def.desc)
		case typeString, typeEnum:
			cmd.Flags().String(def.key, "", def.desc)
		}
	}
}

// collectSettingChanges builds the ordered definitions to report and the neutral
// kebab-case change map from the setting flags that were explicitly set (detected
// via cmd.Flags().Changed). Enum flags are validated case-insensitively, and an
// invalid value returns a clear error (and no map entry) before any write, so
// nothing is sent on a bad value. Unset flags are omitted entirely. Order follows
// the registry, so output is stable regardless of flag order on the command line.
func collectSettingChanges(cmd *cobra.Command) ([]settingDef, map[string]any, error) {
	var defs []settingDef
	changes := make(map[string]any)

	for _, def := range settingRegistry {
		if def.readOnly || !cmd.Flags().Changed(def.key) {
			continue
		}

		value, err := settingFlagValue(cmd, def)
		if err != nil {
			return nil, nil, err
		}

		defs = append(defs, def)
		changes[def.key] = value
	}

	return defs, changes, nil
}

// settingFlagValue reads the typed value of a changed setting flag, returning it
// as the neutral typed value written to the change map. Enum values are validated
// case-insensitively and normalized to their canonical lowercase form; all other
// types are read directly from the typed cobra flag.
func settingFlagValue(cmd *cobra.Command, def settingDef) (any, error) {
	switch def.typ {
	case typeBool:
		return cmd.Flags().GetBool(def.key)
	case typeInt:
		return cmd.Flags().GetInt(def.key)
	case typeInt64:
		return cmd.Flags().GetInt64(def.key)
	case typeFloat:
		return cmd.Flags().GetFloat64(def.key)
	case typeEnum:
		raw, err := cmd.Flags().GetString(def.key)
		if err != nil {
			return nil, err
		}

		return def.validateEnum(raw)
	case typeString:
		return cmd.Flags().GetString(def.key)
	default:
		return nil, fmt.Errorf("invalid value for %s: unsupported type", def.key)
	}
}

// writtenSettingRows builds the {key, category, value} JSON rows from the values
// just written (not a re-read), in the given definition order. The values are the
// native typed values from the change map, matching the --json/--jq contract.
func writtenSettingRows(defs []settingDef, changes map[string]any) []settingRow {
	rows := make([]settingRow, len(defs))
	for i, def := range defs {
		rows[i] = settingRow{
			Key:      def.key,
			Category: string(def.category),
			Value:    changes[def.key],
		}
	}

	return rows
}

// settingRows builds the {key, category, value} JSON rows for the given
// definitions from a session snapshot, in registry order. Values are the native
// typed values (never sanitized — JSON/jq is a structured contract). An empty
// set serializes as "[]" rather than "null".
func settingRows(defs []settingDef, session transmission.SessionSettings) []settingRow {
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

// settingValueCell renders a setting's value as plaintext, neutralizing terminal
// control sequences for the server-derived string settings (sanitize) so a
// crafted download-dir cannot inject ANSI escapes. It serves both the read path
// (a value from a session snapshot) and the write path (a value just sent), which
// differ only in where the value came from. The JSON/jq paths never call it.
func settingValueCell(key string, value any) string {
	v := formatSettingValue(value)
	if serverDerivedSettings[key] {
		v = output.SanitizeTerminal(v)
	}

	return v
}

// writeSettingsPiped writes plain, tab-separated CATEGORY KEY VALUE rows with no
// header — the stable scripting contract. An empty set writes nothing.
func writeSettingsPiped(w io.Writer, rows []settingRow) {
	for _, row := range rows {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", row.Category, row.Key, settingValueCell(row.Key, row.Value))
	}
}

// writeSettingsTable writes a whitespace-aligned CATEGORY KEY VALUE table with a
// header row. An empty set writes a friendly "No settings." line.
func writeSettingsTable(w io.Writer, rows []settingRow) {
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(w, "No settings.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	_, _ = fmt.Fprintln(tw, "CATEGORY\tKEY\tVALUE")
	for _, row := range rows {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", row.Category, row.Key, settingValueCell(row.Key, row.Value))
	}

	_ = tw.Flush()
}
