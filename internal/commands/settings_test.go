package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// TestSettingRegistryCompleteness asserts the registry holds exactly the 40
// expected keys with the per-category counts the PRD specifies, and that only
// blocklist-size is read-only.
func TestSettingRegistryCompleteness(t *testing.T) {
	t.Parallel()

	expected := []string{
		// download (10)
		"download-dir", "start-added-torrents", "rename-partial-files",
		"incomplete-dir-enabled", "incomplete-dir", "seed-ratio-limited",
		"seed-ratio-limit", "idle-seeding-limit-enabled", "idle-seeding-limit",
		"cache-size-mib",
		// network (13)
		"peer-port", "peer-port-random-on-start", "port-forwarding-enabled",
		"encryption", "peer-limit-global", "peer-limit-per-torrent", "pex-enabled",
		"dht-enabled", "lpd-enabled", "utp-enabled", "blocklist-enabled",
		"blocklist-url", "blocklist-size",
		// bandwidth (11)
		"speed-limit-down-enabled", "speed-limit-down", "speed-limit-up-enabled",
		"speed-limit-up", "alt-speed-enabled", "alt-speed-down", "alt-speed-up",
		"alt-speed-time-enabled", "alt-speed-time-begin", "alt-speed-time-end",
		"alt-speed-time-day",
		// queue (6)
		"download-queue-enabled", "download-queue-size", "seed-queue-enabled",
		"seed-queue-size", "queue-stalled-enabled", "queue-stalled-minutes",
	}

	require.Len(t, settingRegistry, 40, "registry must hold exactly 40 settings")
	assert.Equal(t, expected, allSettingKeys(), "registry keys must match the PRD set, in order")

	counts := map[settingCategory]int{}
	var readOnly []string
	for _, def := range settingRegistry {
		counts[def.category]++
		if def.readOnly {
			readOnly = append(readOnly, def.key)
		}
	}

	assert.Equal(t, 10, counts[categoryDownload], "download count")
	assert.Equal(t, 13, counts[categoryNetwork], "network count")
	assert.Equal(t, 11, counts[categoryBandwidth], "bandwidth count")
	assert.Equal(t, 6, counts[categoryQueue], "queue count")

	assert.Equal(t, []string{"blocklist-size"}, readOnly, "only blocklist-size is read-only")
}

// TestSettingRegistryEveryDefHasAccessor guards against a registry entry with a
// nil accessor (which would panic when the list/get commands read it).
func TestSettingRegistryEveryDefHasAccessor(t *testing.T) {
	t.Parallel()

	for _, def := range settingRegistry {
		assert.NotNil(t, def.accessor, "%s must have an accessor", def.key)
	}
}

// TestSettingRegistryEveryDefHasDesc guards against a settable registry entry
// without help text: every non-read-only setting must carry a non-empty desc so
// the per-setting flag and the man page have meaningful help. The read-only
// blocklist-size gets no flag and so needs no desc.
func TestSettingRegistryEveryDefHasDesc(t *testing.T) {
	t.Parallel()

	for _, def := range settingRegistry {
		if def.readOnly {
			continue
		}
		assert.NotEmpty(t, def.desc, "%s must have a non-empty desc", def.key)
	}
}

// TestSettingRegistryDefaultsMatchType asserts each settable default's Go type
// matches the setting's declared type, and that the read-only setting has a nil
// default (no settable default).
func TestSettingRegistryDefaultsMatchType(t *testing.T) {
	t.Parallel()

	for _, def := range settingRegistry {
		if def.readOnly {
			assert.Nil(t, def.def, "%s is read-only and must have no default", def.key)
			continue
		}

		switch def.typ {
		case typeBool:
			assert.IsType(t, false, def.def, "%s default", def.key)
		case typeInt:
			assert.IsType(t, 0, def.def, "%s default", def.key)
		case typeInt64:
			assert.IsType(t, int64(0), def.def, "%s default", def.key)
		case typeFloat:
			assert.IsType(t, 0.0, def.def, "%s default", def.key)
		case typeString, typeEnum:
			assert.IsType(t, "", def.def, "%s default", def.key)
		default:
			t.Fatalf("%s has unexpected type %d", def.key, def.typ)
		}
	}
}

func TestLookupSetting(t *testing.T) {
	t.Parallel()

	def, ok := lookupSetting("peer-port")
	require.True(t, ok)
	assert.Equal(t, "peer-port", def.key)
	assert.Equal(t, categoryNetwork, def.category)
	assert.Equal(t, typeInt, def.typ)
	assert.Equal(t, 51413, def.def)

	_, ok = lookupSetting("does-not-exist")
	assert.False(t, ok)

	// Lookup is exact (kebab-case); a differently-cased key does not match.
	_, ok = lookupSetting("Peer-Port")
	assert.False(t, ok)
}

func TestAllCategoryNames(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"download", "network", "bandwidth", "queue"}, allCategoryNames())
}

func TestParseCategory(t *testing.T) {
	t.Parallel()

	c, err := parseCategory("network")
	require.NoError(t, err)
	assert.Equal(t, categoryNetwork, c)

	c, err = parseCategory("  Bandwidth ")
	require.NoError(t, err)
	assert.Equal(t, categoryBandwidth, c)

	_, err = parseCategory("bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid category "bogus"`)
	assert.Contains(t, err.Error(), "download, network, bandwidth, queue")
}

func TestFilterSettings(t *testing.T) {
	t.Parallel()

	// No filters returns the whole registry in order.
	all := filterSettings("", "")
	assert.Len(t, all, 40)

	// Category filter narrows to a single category.
	network := filterSettings(categoryNetwork, "")
	assert.Len(t, network, 13)
	for _, def := range network {
		assert.Equal(t, categoryNetwork, def.category)
	}

	// Substring search is case-insensitive and matches across categories.
	speed := filterSettings("", "SPEED")
	require.NotEmpty(t, speed)
	for _, def := range speed {
		assert.Contains(t, def.key, "speed")
	}

	// Category and search combine.
	altSpeed := filterSettings(categoryBandwidth, "alt-speed")
	require.NotEmpty(t, altSpeed)
	for _, def := range altSpeed {
		assert.Equal(t, categoryBandwidth, def.category)
		assert.Contains(t, def.key, "alt-speed")
	}

	// A search matching nothing returns an empty (non-nil) slice.
	none := filterSettings("", "no-such-substring")
	assert.Empty(t, none)
}

func TestFilterSettingsPreservesRegistryOrder(t *testing.T) {
	t.Parallel()

	got := filterSettings(categoryDownload, "")
	want := []string{
		"download-dir", "start-added-torrents", "rename-partial-files",
		"incomplete-dir-enabled", "incomplete-dir", "seed-ratio-limited",
		"seed-ratio-limit", "idle-seeding-limit-enabled", "idle-seeding-limit",
		"cache-size-mib",
	}

	keys := make([]string, len(got))
	for i, def := range got {
		keys[i] = def.key
	}
	assert.Equal(t, want, keys)
}

func TestValidateEnum(t *testing.T) {
	t.Parallel()

	def, ok := lookupSetting("encryption")
	require.True(t, ok)

	v, err := def.validateEnum("REQUIRED")
	require.NoError(t, err)
	assert.Equal(t, "required", v, "an enum normalizes to its lowercase allowed value")

	v, err = def.validateEnum(" preferred ")
	require.NoError(t, err)
	assert.Equal(t, "preferred", v, "surrounding whitespace is trimmed")

	_, err = def.validateEnum("tolerated")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be one of: allowed, preferred, required")
}

func TestFormatSettingValue(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "true", formatSettingValue(true))
	assert.Equal(t, "false", formatSettingValue(false))
	assert.Equal(t, "31129", formatSettingValue(31129))
	assert.Equal(t, "100", formatSettingValue(int64(100)))
	assert.Equal(t, "2.5", formatSettingValue(2.5))
	assert.Equal(t, "3", formatSettingValue(3.0), "float with no fraction has no trailing zeros")
	assert.Equal(t, "/data/Downloads", formatSettingValue("/data/Downloads"))
}

// TestAccessorsReadSnapshot spot-checks that accessors read the matching field
// from a populated SessionSettings snapshot, across each value type.
func TestAccessorsReadSnapshot(t *testing.T) {
	t.Parallel()

	snap := transmission.SessionSettings{
		DownloadDir:    "/data",
		PeerPort:       31129,
		SpeedLimitDown: 512,
		SeedRatioLimit: 1.75,
		Encryption:     "required",
		DHTEnabled:     true,
	}

	cases := map[string]any{
		"download-dir":     "/data",
		"peer-port":        31129,
		"speed-limit-down": int64(512),
		"seed-ratio-limit": 1.75,
		"encryption":       "required",
		"dht-enabled":      true,
	}

	for key, want := range cases {
		def, ok := lookupSetting(key)
		require.True(t, ok, key)
		assert.Equal(t, want, def.accessor(snap), key)
	}
}

func TestWriteSettingsTableAligned(t *testing.T) {
	t.Parallel()

	rows := []settingRow{{Key: "peer-port", Category: "network", Value: 51413}}

	var buf bytes.Buffer
	writeSettingsTable(&buf, rows)

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2, "header row plus one row per setting")

	// Header row is present and ordered CATEGORY KEY VALUE.
	assert.True(t, strings.HasPrefix(lines[0], "CATEGORY"))
	assert.Contains(t, lines[0], "KEY")
	assert.Contains(t, lines[0], "VALUE")

	// Columns are whitespace-aligned: each column header's start index matches the
	// start of the corresponding cell on the data row.
	catIdx := strings.Index(lines[0], "CATEGORY")
	keyIdx := strings.Index(lines[0], "KEY")
	valIdx := strings.Index(lines[0], "VALUE")
	assert.Equal(t, "network", lines[1][catIdx:catIdx+len("network")], "CATEGORY cell aligns under its header")
	assert.Equal(t, "peer-port", lines[1][keyIdx:keyIdx+len("peer-port")], "KEY cell aligns under its header")
	assert.Equal(t, "51413", lines[1][valIdx:valIdx+len("51413")], "VALUE cell aligns under its header")
}

func TestWriteSettingsTableEmpty(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writeSettingsTable(&buf, nil)

	assert.Equal(t, "No settings.", strings.TrimSpace(buf.String()))
}

func TestWriteSettingsTableSanitizesControlChars(t *testing.T) {
	t.Parallel()

	rows := []settingRow{{Key: "download-dir", Category: "download", Value: "\x1b[2Jmalicious"}}

	var buf bytes.Buffer
	writeSettingsTable(&buf, rows)

	out := buf.String()
	assert.NotContains(t, out, "\x1b", "raw ESC from a server-derived value must not reach the table output")
	assert.Contains(t, out, "[2Jmalicious", "visible text survives sanitization")
}

func TestWriteSettingsPipedTabSeparated(t *testing.T) {
	t.Parallel()

	rows := []settingRow{
		{Key: "download-dir", Category: "download", Value: "/data"},
		{Key: "peer-port", Category: "network", Value: 51413},
	}

	var buf bytes.Buffer
	writeSettingsPiped(&buf, rows)

	assert.Equal(t, "download\tdownload-dir\t/data\nnetwork\tpeer-port\t51413\n", buf.String())
}
