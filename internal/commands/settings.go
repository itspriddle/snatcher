package commands

import (
	"fmt"
	"strconv"
	"strings"

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
