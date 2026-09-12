package transmission_test

import (
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

	defs := transmission.SettingDefs()

	require.Len(t, defs, 40, "registry must hold exactly 40 settings")
	assert.Equal(t, expected, transmission.SettingKeys(), "registry keys must match the PRD set, in order")

	counts := map[transmission.SettingCategory]int{}
	var readOnly []string
	for _, def := range defs {
		counts[def.Category]++
		if def.ReadOnly {
			readOnly = append(readOnly, def.Key)
		}
	}

	assert.Equal(t, 10, counts[transmission.CategoryDownload], "download count")
	assert.Equal(t, 13, counts[transmission.CategoryNetwork], "network count")
	assert.Equal(t, 11, counts[transmission.CategoryBandwidth], "bandwidth count")
	assert.Equal(t, 6, counts[transmission.CategoryQueue], "queue count")

	assert.Equal(t, []string{"blocklist-size"}, readOnly, "only blocklist-size is read-only")
}

// TestSettingRegistryEveryDefHasAccessor guards against a registry entry with a
// nil accessor (which would panic when the list/get commands read it).
func TestSettingRegistryEveryDefHasAccessor(t *testing.T) {
	t.Parallel()

	for _, def := range transmission.SettingDefs() {
		assert.NotNil(t, def.Accessor, "%s must have an accessor", def.Key)
	}
}

// TestSettingRegistryEveryDefHasDesc guards against a settable registry entry
// without help text: every non-read-only setting must carry a non-empty desc so
// the per-setting flag and the man page have meaningful help. The read-only
// blocklist-size gets no flag and so needs no desc.
func TestSettingRegistryEveryDefHasDesc(t *testing.T) {
	t.Parallel()

	for _, def := range transmission.SettingDefs() {
		if def.ReadOnly {
			continue
		}
		assert.NotEmpty(t, def.Desc, "%s must have a non-empty desc", def.Key)
	}
}

// TestSettingRegistryDefaultsMatchType asserts each settable default's Go type
// matches the setting's declared type, and that the read-only setting has a nil
// default (no settable default).
func TestSettingRegistryDefaultsMatchType(t *testing.T) {
	t.Parallel()

	for _, def := range transmission.SettingDefs() {
		if def.ReadOnly {
			assert.Nil(t, def.Default, "%s is read-only and must have no default", def.Key)
			continue
		}

		switch def.Type {
		case transmission.SettingBool:
			assert.IsType(t, false, def.Default, "%s default", def.Key)
		case transmission.SettingInt:
			assert.IsType(t, 0, def.Default, "%s default", def.Key)
		case transmission.SettingInt64:
			assert.IsType(t, int64(0), def.Default, "%s default", def.Key)
		case transmission.SettingFloat:
			assert.IsType(t, 0.0, def.Default, "%s default", def.Key)
		case transmission.SettingString, transmission.SettingEnum:
			assert.IsType(t, "", def.Default, "%s default", def.Key)
		default:
			t.Fatalf("%s has unexpected type %d", def.Key, def.Type)
		}
	}
}

// TestServerDerivedFlags pins exactly which settings are treated as untrusted
// server text. A key dropped from this set would reach the terminal unsanitized.
func TestServerDerivedFlags(t *testing.T) {
	t.Parallel()

	var derived []string
	for _, def := range transmission.SettingDefs() {
		if def.ServerDerived {
			derived = append(derived, def.Key)
		}
	}

	assert.Equal(t, []string{"download-dir", "incomplete-dir", "encryption", "blocklist-url"}, derived)
}

func TestLookupSetting(t *testing.T) {
	t.Parallel()

	def, ok := transmission.LookupSetting("peer-port")
	require.True(t, ok)
	assert.Equal(t, "peer-port", def.Key)
	assert.Equal(t, transmission.CategoryNetwork, def.Category)
	assert.Equal(t, transmission.SettingInt, def.Type)
	assert.Equal(t, 51413, def.Default)

	_, ok = transmission.LookupSetting("does-not-exist")
	assert.False(t, ok)

	// Lookup is exact (kebab-case); a differently-cased key does not match.
	_, ok = transmission.LookupSetting("Peer-Port")
	assert.False(t, ok)
}

// TestReturnedDefsDoNotAliasTheRegistry checks a caller cannot rewrite the
// registry through a definition it was handed.
func TestReturnedDefsDoNotAliasTheRegistry(t *testing.T) {
	t.Parallel()

	def, ok := transmission.LookupSetting("encryption")
	require.True(t, ok)

	def.Allowed[0] = "tampered"

	again, ok := transmission.LookupSetting("encryption")
	require.True(t, ok)
	assert.Equal(t, []string{"allowed", "preferred", "required"}, again.Allowed)
}

func TestCategoryNames(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"download", "network", "bandwidth", "queue"}, transmission.CategoryNames())
}

func TestParseCategory(t *testing.T) {
	t.Parallel()

	c, err := transmission.ParseCategory("network")
	require.NoError(t, err)
	assert.Equal(t, transmission.CategoryNetwork, c)

	c, err = transmission.ParseCategory("  Bandwidth ")
	require.NoError(t, err)
	assert.Equal(t, transmission.CategoryBandwidth, c)

	_, err = transmission.ParseCategory("bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid category "bogus"`)
	assert.Contains(t, err.Error(), "download, network, bandwidth, queue")
}

func TestFilterSettings(t *testing.T) {
	t.Parallel()

	// No filters returns the whole registry in order.
	all := transmission.FilterSettings("", "")
	assert.Len(t, all, 40)

	// Category filter narrows to a single category.
	network := transmission.FilterSettings(transmission.CategoryNetwork, "")
	assert.Len(t, network, 13)
	for _, def := range network {
		assert.Equal(t, transmission.CategoryNetwork, def.Category)
	}

	// Substring search is case-insensitive and matches across categories.
	speed := transmission.FilterSettings("", "SPEED")
	require.NotEmpty(t, speed)
	for _, def := range speed {
		assert.Contains(t, def.Key, "speed")
	}

	// Category and search combine.
	altSpeed := transmission.FilterSettings(transmission.CategoryBandwidth, "alt-speed")
	require.NotEmpty(t, altSpeed)
	for _, def := range altSpeed {
		assert.Equal(t, transmission.CategoryBandwidth, def.Category)
		assert.Contains(t, def.Key, "alt-speed")
	}

	// A search matching nothing returns an empty (non-nil) slice.
	none := transmission.FilterSettings("", "no-such-substring")
	assert.NotNil(t, none)
	assert.Empty(t, none)
}

func TestSettingsInCategoryPreservesRegistryOrder(t *testing.T) {
	t.Parallel()

	got := transmission.SettingsInCategory(transmission.CategoryDownload)
	want := []string{
		"download-dir", "start-added-torrents", "rename-partial-files",
		"incomplete-dir-enabled", "incomplete-dir", "seed-ratio-limited",
		"seed-ratio-limit", "idle-seeding-limit-enabled", "idle-seeding-limit",
		"cache-size-mib",
	}

	keys := make([]string, len(got))
	for i, def := range got {
		keys[i] = def.Key
	}
	assert.Equal(t, want, keys)
}

func TestValidateEnum(t *testing.T) {
	t.Parallel()

	def, ok := transmission.LookupSetting("encryption")
	require.True(t, ok)

	v, err := def.ValidateEnum("REQUIRED")
	require.NoError(t, err)
	assert.Equal(t, "required", v, "an enum normalizes to its lowercase allowed value")

	v, err = def.ValidateEnum(" preferred ")
	require.NoError(t, err)
	assert.Equal(t, "preferred", v, "surrounding whitespace is trimmed")

	_, err = def.ValidateEnum("tolerated")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be one of: allowed, preferred, required")
}

func TestFormatSettingValue(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "true", transmission.FormatSettingValue(true))
	assert.Equal(t, "false", transmission.FormatSettingValue(false))
	assert.Equal(t, "31129", transmission.FormatSettingValue(31129))
	assert.Equal(t, "100", transmission.FormatSettingValue(int64(100)))
	assert.Equal(t, "2.5", transmission.FormatSettingValue(2.5))
	assert.Equal(t, "3", transmission.FormatSettingValue(3.0), "float with no fraction has no trailing zeros")
	assert.Equal(t, "/data/Downloads", transmission.FormatSettingValue("/data/Downloads"))
}

// TestParseSettingValue covers every setting type, valid and invalid, plus the
// read-only refusal and enum case-folding.
func TestParseSettingValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		raw     string
		want    any
		wantErr string
	}{
		{name: "bool true", key: "dht-enabled", raw: "true", want: true},
		{name: "bool trimmed", key: "dht-enabled", raw: " false ", want: false},
		{name: "bool invalid", key: "dht-enabled", raw: "maybe", wantErr: "invalid value for dht-enabled"},
		{name: "int", key: "peer-port", raw: "31500", want: 31500},
		{name: "int invalid", key: "peer-port", raw: "31.5", wantErr: "invalid value for peer-port"},
		{name: "int64", key: "speed-limit-down", raw: "512", want: int64(512)},
		{name: "int64 invalid", key: "speed-limit-down", raw: "fast", wantErr: "invalid value for speed-limit-down"},
		{name: "float", key: "seed-ratio-limit", raw: "1.75", want: 1.75},
		{name: "float invalid", key: "seed-ratio-limit", raw: "lots", wantErr: "invalid value for seed-ratio-limit"},
		{name: "string trimmed", key: "download-dir", raw: "  /data  ", want: "/data"},
		{name: "string empty", key: "incomplete-dir", raw: "", want: ""},
		{name: "enum case-folded", key: "encryption", raw: "Required", want: "required"},
		{name: "enum invalid", key: "encryption", raw: "tolerated", wantErr: "must be one of: allowed, preferred, required"},
		{name: "read-only", key: "blocklist-size", raw: "10", wantErr: `setting "blocklist-size" is read-only`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def, ok := transmission.LookupSetting(tc.key)
			require.True(t, ok)

			got, err := transmission.ParseSettingValue(def, tc.raw)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
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
		def, ok := transmission.LookupSetting(key)
		require.True(t, ok, key)
		assert.Equal(t, want, def.Accessor(snap), key)
	}
}
