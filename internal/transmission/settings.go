package transmission

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// SettingType is the value type of a session setting. It drives the type of the
// per-setting flag on "server update", the widget the TUI settings form draws,
// and how a raw string is parsed into a typed value (ParseSettingValue).
type SettingType int

const (
	// SettingBool is a boolean setting parsed from "true"/"false".
	SettingBool SettingType = iota

	// SettingInt is a plain int setting.
	SettingInt

	// SettingInt64 is an int64 setting (used for the speed-limit fields).
	SettingInt64

	// SettingFloat is a float64 setting (the seed-ratio limit).
	SettingFloat

	// SettingString is a free-form string setting.
	SettingString

	// SettingEnum is a string setting constrained to a fixed set of allowed
	// values (matched case-insensitively); see SettingDef.Allowed.
	SettingEnum
)

// SettingCategory is the grouping a setting belongs to, used by --category
// filtering, the CATEGORY column of the "server show" settings table, and the
// server tabs of the TUI settings window.
type SettingCategory string

const (
	// CategoryDownload groups download-location and seeding settings.
	CategoryDownload SettingCategory = "download"

	// CategoryNetwork groups peer, port, encryption, and blocklist settings.
	CategoryNetwork SettingCategory = "network"

	// CategoryBandwidth groups the speed-limit and alt-speed schedule settings.
	CategoryBandwidth SettingCategory = "bandwidth"

	// CategoryQueue groups the download/seed queue settings.
	CategoryQueue SettingCategory = "queue"
)

// SettingCategories lists every valid category in display order. It is the
// single source of truth for the --category validation message and for
// CategoryNames.
var SettingCategories = []SettingCategory{
	CategoryDownload,
	CategoryNetwork,
	CategoryBandwidth,
	CategoryQueue,
}

// SettingDef describes a single session setting: the neutral kebab-case key,
// its category, value type, the Transmission default (used by "server reset"),
// whether it is read-only (update/reset reject it), the allowed values for an
// enum, and an accessor returning the current typed value from a SessionSettings
// snapshot.
//
// It is domain metadata rather than command-layer wiring because two consumers
// need it — the CLI's server commands and the TUI's settings window — and the
// TUI may import this package but never internal/commands.
type SettingDef struct {
	// Key is the neutral, kebab-case setting key (the user-facing identifier and
	// the JSON contract's "key").
	Key string

	// Desc is a concise, one-line human description used as the help text for
	// the per-setting flag (server update --<key>), the man page, and the TUI's
	// hint line. Every settable setting must have a non-empty Desc; the read-only
	// blocklist-size, which gets no flag, leaves it empty.
	Desc string

	// Category is the setting's grouping.
	Category SettingCategory

	// Type is the value type, driving parsing and formatting.
	Type SettingType

	// Default is the Transmission default value, used by "server reset". It is
	// nil for the read-only blocklist-size, which has no settable default.
	Default any

	// ReadOnly marks settings that "server show" reports but that "server
	// update" and "server reset" reject.
	ReadOnly bool

	// Allowed lists the permitted values for a SettingEnum setting (lowercase),
	// matched case-insensitively. It is nil for non-enum settings.
	Allowed []string

	// Unit is a short display hint for a numeric setting whose raw value has a
	// unit ("KB/s", "min", "MiB"), or "" when the value speaks for itself. It is
	// a label only: values are never converted.
	Unit string

	// ServerDerived marks a setting whose current value comes from the remote
	// server as free-form or server-controlled text and is therefore untrusted:
	// it must pass through output.SanitizeTerminal on every human path (and is
	// never sanitized on the --json/--jq path, a structured contract). The string
	// keys carry free-form server text; encryption is an enum constrained on the
	// write path, but the value read back is server-controlled and could carry
	// escape sequences from a compromised or MITM'd server, so it is flagged too.
	// This package does not sanitize; consumers do.
	ServerDerived bool

	// Accessor returns the setting's current typed value from a session
	// snapshot.
	Accessor func(SessionSettings) any
}

// settingRegistry is the single source of truth for every session setting:
// "server show" reads it, "server update" generates one flag per settable entry
// from it, "server reset" restores its defaults, and the TUI builds its server
// settings tabs from it. It is ordered by category (download, network,
// bandwidth, queue) so output and any derived ordering follow it. The defaults
// are Transmission's documented defaults, baked in because the Transmission RPC
// has no "reset to default" primitive.
var settingRegistry = []SettingDef{
	// Download.
	{Key: "download-dir", Category: CategoryDownload, Type: SettingString, Default: "~/Downloads", ServerDerived: true,
		Desc:     "Default directory for completed downloads",
		Accessor: func(s SessionSettings) any { return s.DownloadDir }},
	{Key: "start-added-torrents", Category: CategoryDownload, Type: SettingBool, Default: true,
		Desc:     "Start torrents automatically when added",
		Accessor: func(s SessionSettings) any { return s.StartAddedTorrents }},
	{Key: "rename-partial-files", Category: CategoryDownload, Type: SettingBool, Default: true,
		Desc:     "Append .part to incomplete files",
		Accessor: func(s SessionSettings) any { return s.RenamePartialFiles }},
	{Key: "incomplete-dir-enabled", Category: CategoryDownload, Type: SettingBool, Default: false,
		Desc:     "Keep incomplete downloads in a separate directory",
		Accessor: func(s SessionSettings) any { return s.IncompleteDirEnabled }},
	{Key: "incomplete-dir", Category: CategoryDownload, Type: SettingString, Default: "", ServerDerived: true,
		Desc:     "Directory for incomplete downloads (when enabled)",
		Accessor: func(s SessionSettings) any { return s.IncompleteDir }},
	{Key: "seed-ratio-limited", Category: CategoryDownload, Type: SettingBool, Default: false,
		Desc:     "Stop seeding at the global seed-ratio limit",
		Accessor: func(s SessionSettings) any { return s.SeedRatioLimited }},
	{Key: "seed-ratio-limit", Category: CategoryDownload, Type: SettingFloat, Default: 2.0,
		Desc:     "Global seed ratio to stop seeding at (when limited)",
		Accessor: func(s SessionSettings) any { return s.SeedRatioLimit }},
	{Key: "idle-seeding-limit-enabled", Category: CategoryDownload, Type: SettingBool, Default: false,
		Desc:     "Stop seeding torrents that have been idle too long",
		Accessor: func(s SessionSettings) any { return s.IdleSeedingLimitEnabled }},
	{Key: "idle-seeding-limit", Category: CategoryDownload, Type: SettingInt, Default: 30, Unit: "min",
		Desc:     "Idle time in minutes before stopping seeding (when enabled)",
		Accessor: func(s SessionSettings) any { return s.IdleSeedingLimit }},
	{Key: "cache-size-mib", Category: CategoryDownload, Type: SettingInt, Default: 4, Unit: "MiB",
		Desc:     "Disk cache size in MiB",
		Accessor: func(s SessionSettings) any { return s.CacheSizeMiB }},

	// Network.
	{Key: "peer-port", Category: CategoryNetwork, Type: SettingInt, Default: 51413,
		Desc:     "Port to listen for incoming peer connections",
		Accessor: func(s SessionSettings) any { return s.PeerPort }},
	{Key: "peer-port-random-on-start", Category: CategoryNetwork, Type: SettingBool, Default: false,
		Desc:     "Pick a random peer port on each start",
		Accessor: func(s SessionSettings) any { return s.PeerPortRandomOnStart }},
	{Key: "port-forwarding-enabled", Category: CategoryNetwork, Type: SettingBool, Default: true,
		Desc:     "Request UPnP/NAT-PMP port forwarding",
		Accessor: func(s SessionSettings) any { return s.PortForwardingEnabled }},
	{Key: "encryption", Category: CategoryNetwork, Type: SettingEnum, Default: "preferred", ServerDerived: true,
		Allowed:  []string{"allowed", "preferred", "required"},
		Desc:     "Peer encryption mode (allowed, preferred, required)",
		Accessor: func(s SessionSettings) any { return s.Encryption }},
	{Key: "peer-limit-global", Category: CategoryNetwork, Type: SettingInt, Default: 200,
		Desc:     "Maximum peer connections across all torrents",
		Accessor: func(s SessionSettings) any { return s.PeerLimitGlobal }},
	{Key: "peer-limit-per-torrent", Category: CategoryNetwork, Type: SettingInt, Default: 50,
		Desc:     "Maximum peer connections per torrent",
		Accessor: func(s SessionSettings) any { return s.PeerLimitPerTorrent }},
	{Key: "pex-enabled", Category: CategoryNetwork, Type: SettingBool, Default: true,
		Desc:     "Enable Peer Exchange (PEX)",
		Accessor: func(s SessionSettings) any { return s.PexEnabled }},
	{Key: "dht-enabled", Category: CategoryNetwork, Type: SettingBool, Default: true,
		Desc:     "Enable the Distributed Hash Table (DHT)",
		Accessor: func(s SessionSettings) any { return s.DHTEnabled }},
	{Key: "lpd-enabled", Category: CategoryNetwork, Type: SettingBool, Default: false,
		Desc:     "Enable Local Peer Discovery (LPD)",
		Accessor: func(s SessionSettings) any { return s.LPDEnabled }},
	{Key: "utp-enabled", Category: CategoryNetwork, Type: SettingBool, Default: true,
		Desc:     "Enable the uTP transport protocol",
		Accessor: func(s SessionSettings) any { return s.UTPEnabled }},
	{Key: "blocklist-enabled", Category: CategoryNetwork, Type: SettingBool, Default: false,
		Desc:     "Block peers listed in the blocklist",
		Accessor: func(s SessionSettings) any { return s.BlocklistEnabled }},
	{Key: "blocklist-url", Category: CategoryNetwork, Type: SettingString, Default: "http://www.example.com/blocklist", ServerDerived: true,
		Desc:     "URL to fetch the peer blocklist from",
		Accessor: func(s SessionSettings) any { return s.BlocklistURL }},
	{Key: "blocklist-size", Category: CategoryNetwork, Type: SettingInt, Default: nil, ReadOnly: true,
		Accessor: func(s SessionSettings) any { return s.BlocklistSize }},

	// Bandwidth.
	{Key: "speed-limit-down-enabled", Category: CategoryBandwidth, Type: SettingBool, Default: false,
		Desc:     "Enable the global download speed limit",
		Accessor: func(s SessionSettings) any { return s.SpeedLimitDownEnabled }},
	{Key: "speed-limit-down", Category: CategoryBandwidth, Type: SettingInt64, Default: int64(100), Unit: "KB/s",
		Desc:     "Global download speed limit in KB/s (when enabled)",
		Accessor: func(s SessionSettings) any { return s.SpeedLimitDown }},
	{Key: "speed-limit-up-enabled", Category: CategoryBandwidth, Type: SettingBool, Default: false,
		Desc:     "Enable the global upload speed limit",
		Accessor: func(s SessionSettings) any { return s.SpeedLimitUpEnabled }},
	{Key: "speed-limit-up", Category: CategoryBandwidth, Type: SettingInt64, Default: int64(100), Unit: "KB/s",
		Desc:     "Global upload speed limit in KB/s (when enabled)",
		Accessor: func(s SessionSettings) any { return s.SpeedLimitUp }},
	{Key: "alt-speed-enabled", Category: CategoryBandwidth, Type: SettingBool, Default: false,
		Desc:     "Use the alternative (throttled) speed limits now",
		Accessor: func(s SessionSettings) any { return s.AltSpeedEnabled }},
	{Key: "alt-speed-down", Category: CategoryBandwidth, Type: SettingInt64, Default: int64(50), Unit: "KB/s",
		Desc:     "Alternative download speed limit in KB/s",
		Accessor: func(s SessionSettings) any { return s.AltSpeedDown }},
	{Key: "alt-speed-up", Category: CategoryBandwidth, Type: SettingInt64, Default: int64(50), Unit: "KB/s",
		Desc:     "Alternative upload speed limit in KB/s",
		Accessor: func(s SessionSettings) any { return s.AltSpeedUp }},
	{Key: "alt-speed-time-enabled", Category: CategoryBandwidth, Type: SettingBool, Default: false,
		Desc:     "Switch to alternative speeds on a daily schedule",
		Accessor: func(s SessionSettings) any { return s.AltSpeedTimeEnabled }},
	{Key: "alt-speed-time-begin", Category: CategoryBandwidth, Type: SettingInt, Default: 540, Unit: "min after midnight",
		Desc:     "Alternative-speed schedule start (minutes after midnight)",
		Accessor: func(s SessionSettings) any { return s.AltSpeedTimeBegin }},
	{Key: "alt-speed-time-end", Category: CategoryBandwidth, Type: SettingInt, Default: 1020, Unit: "min after midnight",
		Desc:     "Alternative-speed schedule end (minutes after midnight)",
		Accessor: func(s SessionSettings) any { return s.AltSpeedTimeEnd }},
	{Key: "alt-speed-time-day", Category: CategoryBandwidth, Type: SettingInt, Default: 127, Unit: "bitfield, Sun=1",
		Desc:     "Days the alternative-speed schedule runs (bitfield, Sun=1)",
		Accessor: func(s SessionSettings) any { return s.AltSpeedTimeDay }},

	// Queue.
	{Key: "download-queue-enabled", Category: CategoryQueue, Type: SettingBool, Default: true,
		Desc:     "Limit how many torrents download at once",
		Accessor: func(s SessionSettings) any { return s.DownloadQueueEnabled }},
	{Key: "download-queue-size", Category: CategoryQueue, Type: SettingInt, Default: 5,
		Desc:     "Maximum torrents downloading at once (when enabled)",
		Accessor: func(s SessionSettings) any { return s.DownloadQueueSize }},
	{Key: "seed-queue-enabled", Category: CategoryQueue, Type: SettingBool, Default: false,
		Desc:     "Limit how many torrents seed at once",
		Accessor: func(s SessionSettings) any { return s.SeedQueueEnabled }},
	{Key: "seed-queue-size", Category: CategoryQueue, Type: SettingInt, Default: 10,
		Desc:     "Maximum torrents seeding at once (when enabled)",
		Accessor: func(s SessionSettings) any { return s.SeedQueueSize }},
	{Key: "queue-stalled-enabled", Category: CategoryQueue, Type: SettingBool, Default: true,
		Desc:     "Ignore stalled torrents when counting the queue",
		Accessor: func(s SessionSettings) any { return s.QueueStalledEnabled }},
	{Key: "queue-stalled-minutes", Category: CategoryQueue, Type: SettingInt, Default: 30, Unit: "min",
		Desc:     "Idle minutes before a transfer counts as stalled",
		Accessor: func(s SessionSettings) any { return s.QueueStalledMinutes }},
}

// settingsByKey indexes the registry by key for O(1) lookup. It is built once at
// package init from settingRegistry, the source of truth.
var settingsByKey = func() map[string]SettingDef {
	m := make(map[string]SettingDef, len(settingRegistry))
	for _, def := range settingRegistry {
		m[def.Key] = def
	}

	return m
}()

// copyDef returns def with its Allowed slice cloned, so a caller that appends to
// or rewrites a returned definition cannot reach into the registry.
func copyDef(def SettingDef) SettingDef {
	def.Allowed = slices.Clone(def.Allowed)

	return def
}

// SettingDefs returns every setting definition in registry order. The result is
// a fresh copy on every call.
func SettingDefs() []SettingDef {
	defs := make([]SettingDef, len(settingRegistry))
	for i, def := range settingRegistry {
		defs[i] = copyDef(def)
	}

	return defs
}

// LookupSetting returns the registry entry for a key and whether it exists.
// Lookup is exact (keys are already normalized kebab-case).
func LookupSetting(key string) (SettingDef, bool) {
	def, ok := settingsByKey[key]
	if !ok {
		return SettingDef{}, false
	}

	return copyDef(def), true
}

// SettingKeys returns every setting key in registry order, for the valid-keys
// list in error messages.
func SettingKeys() []string {
	keys := make([]string, len(settingRegistry))
	for i, def := range settingRegistry {
		keys[i] = def.Key
	}

	return keys
}

// CategoryNames returns every category name in display order, for the
// valid-categories list in error messages.
func CategoryNames() []string {
	names := make([]string, len(SettingCategories))
	for i, c := range SettingCategories {
		names[i] = string(c)
	}

	return names
}

// ParseCategory validates a --category value (case-insensitively) and returns
// the canonical category. An unknown value returns an error listing the valid
// categories.
func ParseCategory(s string) (SettingCategory, error) {
	c := SettingCategory(strings.ToLower(strings.TrimSpace(s)))
	if slices.Contains(SettingCategories, c) {
		return c, nil
	}

	return "", fmt.Errorf("invalid category %q: valid categories are %s", s, strings.Join(CategoryNames(), ", "))
}

// FilterSettings returns the registry entries matching the optional category and
// the optional case-insensitive key substring, preserving registry order. An
// empty category matches every category; an empty search matches every key. A
// filter matching nothing returns an empty, non-nil slice.
func FilterSettings(category SettingCategory, search string) []SettingDef {
	needle := strings.ToLower(strings.TrimSpace(search))

	out := make([]SettingDef, 0, len(settingRegistry))
	for _, def := range settingRegistry {
		if category != "" && def.Category != category {
			continue
		}
		if needle != "" && !strings.Contains(def.Key, needle) {
			continue
		}
		out = append(out, copyDef(def))
	}

	return out
}

// SettingsInCategory returns every setting in one category, in registry order.
func SettingsInCategory(c SettingCategory) []SettingDef {
	return FilterSettings(c, "")
}

// ValidateEnum matches a raw enum value case-insensitively (and after trimming)
// against the setting's allowed list, returning the canonical lowercase value. A
// value outside the list returns a clear error naming the allowed values.
func (d SettingDef) ValidateEnum(raw string) (string, error) {
	lowered := strings.ToLower(strings.TrimSpace(raw))
	if slices.Contains(d.Allowed, lowered) {
		return lowered, nil
	}

	return "", fmt.Errorf("invalid value for %s: must be one of: %s", d.Key, strings.Join(d.Allowed, ", "))
}

// FormatSettingValue renders a typed value to its plaintext string: bools as
// "true"/"false", ints in decimal, floats via strconv with the shortest exact
// representation, and strings as-is. It does not sanitize; callers route
// ServerDerived values through output.SanitizeTerminal on the human path.
func FormatSettingValue(v any) string {
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

// ParseSettingValue converts a raw string into the typed value a setting is
// written with — the inverse of FormatSettingValue — so an interactive editor
// and a command-line flag accept the same spellings. Surrounding whitespace is
// trimmed for every type. A read-only setting is refused outright, and every
// other failure names the key.
func ParseSettingValue(def SettingDef, raw string) (any, error) {
	if def.ReadOnly {
		return nil, fmt.Errorf("setting %q is read-only", def.Key)
	}

	trimmed := strings.TrimSpace(raw)

	switch def.Type {
	case SettingBool:
		v, err := strconv.ParseBool(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid value for %s: %q is not a boolean: use true or false", def.Key, raw)
		}

		return v, nil
	case SettingInt:
		v, err := strconv.Atoi(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid value for %s: %q is not a whole number", def.Key, raw)
		}

		return v, nil
	case SettingInt64:
		v, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid value for %s: %q is not a whole number", def.Key, raw)
		}

		return v, nil
	case SettingFloat:
		v, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid value for %s: %q is not a number", def.Key, raw)
		}

		return v, nil
	case SettingString:
		return trimmed, nil
	case SettingEnum:
		return def.ValidateEnum(raw)
	}

	return nil, fmt.Errorf("invalid value for %s: unsupported type", def.Key)
}
