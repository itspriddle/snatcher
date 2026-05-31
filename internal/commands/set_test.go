package commands

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// runSet executes the set command with args, returning stdout, stderr, and the
// Execute error. The command's output is a *bytes.Buffer, so the piped/plaintext
// path is exercised.
func runSet(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewSetCmd()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

func TestSetByID(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runSet(t, "--config", cfgPath, "--priority", "high", "1")
	require.NoError(t, err)

	assert.Equal(t, "Updated 1 torrent: ubuntu-24.04.iso\n", out)
}

func TestSetByHash(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runSet(t, "--config", cfgPath, "--priority", "normal", fullHashTwo)
	require.NoError(t, err)

	assert.Equal(t, "Updated 1 torrent: debian.iso\n", out)
}

func TestSetMultipleByID(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runSet(t, "--config", cfgPath, "--peer-limit", "30", "1", "2")
	require.NoError(t, err)

	assert.Equal(t, "Updated 2 torrents: ubuntu-24.04.iso, debian.iso\n", out)
}

func TestSetAll(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runSet(t, "--config", cfgPath, "--all", "--honors-session-limits=false")
	require.NoError(t, err)

	assert.Equal(t, "Updated all 2 torrents.\n", out)
}

// TestSetNothingToSetErrors guards that without any field flag the command
// errors and never contacts the server.
func TestSetNothingToSetErrors(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing to set: provide at least one field flag")

	_, ok := stub.request("torrent-set")
	assert.False(t, ok, "the set method must not be called when no field flag is given")
}

func TestSetNoSelectionErrors(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--priority", "high")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "specify torrent ids or hashes, or use --all")
}

func TestSetSelectorsAndAllErrors(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--priority", "high", "1", "--all")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot combine torrent selectors with --all")
}

func TestSetNoMatchErrors(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--priority", "high", "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no torrents matched: 99")
}

func TestSetMalformedSelectorErrors(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--priority", "high", "not-a-hash")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid torrent selector")

	_, ok := stub.request("torrent-set")
	assert.False(t, ok, "the set method must not be called for a malformed selector")
}

// TestSetInvalidPriorityErrors guards that an unrecognized --priority value is
// rejected before any backend call.
func TestSetInvalidPriorityErrors(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--priority", "urgent", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid value for --priority: must be low, normal, or high")

	_, ok := stub.request("torrent-set")
	assert.False(t, ok, "the set method must not be called for an invalid priority")
}

func TestSetInvalidQueuePositionErrors(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--queue-position", "-1", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid value for --queue-position: must be 0 or greater")

	_, ok := stub.request("torrent-set")
	assert.False(t, ok, "the set method must not be called for an invalid queue position")
}

func TestSetInvalidPeerLimitErrors(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--peer-limit", "0", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid value for --peer-limit: must be 1 or greater")

	_, ok := stub.request("torrent-set")
	assert.False(t, ok, "the set method must not be called for an invalid peer limit")
}

// TestSetPriorityValues asserts each --priority value maps to the correct
// bandwidth-priority integer on the wire (bespoke camelCase spelling).
func TestSetPriorityValues(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"low", `"bandwidthPriority":-1`},
		{"normal", `"bandwidthPriority":0`},
		{"high", `"bandwidthPriority":1`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
			defer closeSrv()

			_, _, err := runSet(t, "--config", cfgPath, "--priority", tc.name, "1")
			require.NoError(t, err)

			body, ok := stub.request("torrent-set")
			require.True(t, ok, "the bespoke torrent-set method was called")
			assert.Contains(t, body, tc.want)
			assert.Contains(t, body, `"ids":[1]`)
		})
	}
}

// TestSetSendsFieldsBespoke asserts the scalar/bool fields reach the bespoke
// torrent-set request with the correct camelCase/kebab-case wire spellings.
func TestSetSendsFieldsBespoke(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath,
		"--queue-position", "3",
		"--peer-limit", "40",
		"--honors-session-limits=true",
		"--sequential-download=true",
		"1")
	require.NoError(t, err)

	body, ok := stub.request("torrent-set")
	require.True(t, ok, "the bespoke torrent-set method was called")
	assert.Contains(t, body, `"queuePosition":3`)
	assert.Contains(t, body, `"peer-limit":40`)
	assert.Contains(t, body, `"honorsSessionLimits":true`)
	assert.Contains(t, body, `"sequentialDownload":true`)
}

// TestSetSendsFieldsJSONRPC asserts the scalar/bool fields reach the JSON-RPC
// torrent_set request with canonical snake_case wire spellings.
func TestSetSendsFieldsJSONRPC(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "6.0.0", jsonrpcListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath,
		"--priority", "high",
		"--queue-position", "3",
		"--peer-limit", "40",
		"--honors-session-limits=false",
		"--sequential-download=false",
		"1")
	require.NoError(t, err)

	body, ok := stub.request("torrent_set")
	require.True(t, ok, "the JSON-RPC torrent_set method was called")
	assert.Contains(t, body, `"bandwidth_priority":1`)
	assert.Contains(t, body, `"queue_position":3`)
	assert.Contains(t, body, `"peer_limit":40`)
	assert.Contains(t, body, `"honors_session_limits":false`)
	assert.Contains(t, body, `"sequential_download":false`)
}

// TestSetOnlySendsChangedFields guards the Changed() gating: an unset field flag
// must not reach the wire.
func TestSetOnlySendsChangedFields(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--priority", "high", "1")
	require.NoError(t, err)

	body, ok := stub.request("torrent-set")
	require.True(t, ok, "the bespoke torrent-set method was called")
	assert.Contains(t, body, `"bandwidthPriority":1`)
	assert.NotContains(t, body, "queuePosition", "an unset --queue-position must not be sent")
	assert.NotContains(t, body, "peer-limit", "an unset --peer-limit must not be sent")
	assert.NotContains(t, body, "honorsSessionLimits", "an unset --honors-session-limits must not be sent")
	assert.NotContains(t, body, "sequentialDownload", "an unset --sequential-download must not be sent")
}

func TestSetJSON(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runSet(t, "--config", cfgPath, "--json", "--priority", "high", "--peer-limit", "40", "1")
	require.NoError(t, err)

	var got struct {
		Action   string `json:"action"`
		Count    int    `json:"count"`
		Torrents []struct {
			ID   int64  `json:"id"`
			Hash string `json:"hash"`
			Name string `json:"name"`
		} `json:"torrents"`
		Fields map[string]any `json:"fields"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	assert.Equal(t, "set", got.Action)
	assert.Equal(t, 1, got.Count)
	require.Len(t, got.Torrents, 1)
	assert.Equal(t, int64(1), got.Torrents[0].ID)
	assert.Equal(t, fullHashOne, got.Torrents[0].Hash)
	assert.Equal(t, "ubuntu-24.04.iso", got.Torrents[0].Name)

	// JSON numbers decode as float64.
	assert.Equal(t, float64(1), got.Fields["bandwidth-priority"])
	assert.Equal(t, float64(40), got.Fields["peer-limit"])
	_, hasQueue := got.Fields["queue-position"]
	assert.False(t, hasQueue, "an unset field is omitted from the fields map")
}

func TestSetJQ(t *testing.T) {
	cfgPath, _, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	out, _, err := runSet(t, "--config", cfgPath, "--jq", ".fields[\"bandwidth-priority\"]", "--priority", "high", "1")
	require.NoError(t, err)

	assert.Equal(t, "1", strings.TrimSpace(out))
}

// TestSetRateLimitsBespoke asserts the --download-limit / --upload-limit
// sentinel branches reach the bespoke wire with the paired camelCase keys.
func TestSetRateLimitsBespoke(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			"download numeric",
			[]string{"--download-limit", "500"},
			[]string{`"downloadLimit":500`, `"downloadLimited":true`},
		},
		{
			"download off",
			[]string{"--download-limit", "off"},
			[]string{`"downloadLimited":false`},
		},
		{
			"upload numeric",
			[]string{"--upload-limit", "250"},
			[]string{`"uploadLimit":250`, `"uploadLimited":true`},
		},
		{
			"upload off",
			[]string{"--upload-limit", "OFF"},
			[]string{`"uploadLimited":false`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
			defer closeSrv()

			args := append([]string{"--config", cfgPath}, tc.args...)
			args = append(args, "1")

			_, _, err := runSet(t, args...)
			require.NoError(t, err)

			body, ok := stub.request("torrent-set")
			require.True(t, ok, "the bespoke torrent-set method was called")
			for _, want := range tc.want {
				assert.Contains(t, body, want)
			}
		})
	}
}

// TestSetRateLimitOffOmitsLimit guards that disabling a limit with "off" does
// not also send a limit value (only the *Limited toggle is written).
func TestSetRateLimitOffOmitsLimit(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--download-limit", "off", "1")
	require.NoError(t, err)

	body, ok := stub.request("torrent-set")
	require.True(t, ok, "the bespoke torrent-set method was called")
	assert.Contains(t, body, `"downloadLimited":false`)
	assert.NotContains(t, body, "downloadLimit\":", "a disabled limit must not send a limit value")
}

// TestSetRateLimitsJSONRPC asserts the rate-limit branches reach the JSON-RPC
// wire with canonical snake_case keys.
func TestSetRateLimitsJSONRPC(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "6.0.0", jsonrpcListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath,
		"--download-limit", "500",
		"--upload-limit", "off",
		"1")
	require.NoError(t, err)

	body, ok := stub.request("torrent_set")
	require.True(t, ok, "the JSON-RPC torrent_set method was called")
	assert.Contains(t, body, `"download_limit":500`)
	assert.Contains(t, body, `"download_limited":true`)
	assert.Contains(t, body, `"upload_limited":false`)
}

// TestSetSeedRatioBespoke asserts each --seed-ratio sentinel branch maps to the
// paired seedRatioLimit / seedRatioMode wire keys.
func TestSetSeedRatioBespoke(t *testing.T) {
	cases := []struct {
		name string
		val  string
		want []string
		miss string
	}{
		{"numeric", "2", []string{`"seedRatioLimit":2`, `"seedRatioMode":1`}, ""},
		{"global", "global", []string{`"seedRatioMode":0`}, "seedRatioLimit"},
		{"unlimited", "unlimited", []string{`"seedRatioMode":2`}, "seedRatioLimit"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
			defer closeSrv()

			_, _, err := runSet(t, "--config", cfgPath, "--seed-ratio", tc.val, "1")
			require.NoError(t, err)

			body, ok := stub.request("torrent-set")
			require.True(t, ok, "the bespoke torrent-set method was called")
			for _, want := range tc.want {
				assert.Contains(t, body, want)
			}
			if tc.miss != "" {
				assert.NotContains(t, body, tc.miss, "a mode-only ratio must not send a limit value")
			}
		})
	}
}

// TestSetSeedRatioJSONRPC asserts a fractional ratio reaches the JSON-RPC wire
// with canonical snake_case keys.
func TestSetSeedRatioJSONRPC(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "6.0.0", jsonrpcListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--seed-ratio", "1.5", "1")
	require.NoError(t, err)

	body, ok := stub.request("torrent_set")
	require.True(t, ok, "the JSON-RPC torrent_set method was called")
	assert.Contains(t, body, `"seed_ratio_limit":1.5`)
	assert.Contains(t, body, `"seed_ratio_mode":1`)
}

// TestSetSeedIdleBespoke asserts each --seed-idle sentinel branch maps to the
// paired seedIdleLimit / seedIdleMode wire keys, including duration parsing to
// whole minutes.
func TestSetSeedIdleBespoke(t *testing.T) {
	cases := []struct {
		name string
		val  string
		want []string
		miss string
	}{
		{"minutes", "30", []string{`"seedIdleLimit":30`, `"seedIdleMode":1`}, ""},
		{"duration minutes", "45m", []string{`"seedIdleLimit":45`, `"seedIdleMode":1`}, ""},
		{"duration hours", "1h30m", []string{`"seedIdleLimit":90`, `"seedIdleMode":1`}, ""},
		{"global", "global", []string{`"seedIdleMode":0`}, "seedIdleLimit"},
		{"unlimited", "unlimited", []string{`"seedIdleMode":2`}, "seedIdleLimit"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
			defer closeSrv()

			_, _, err := runSet(t, "--config", cfgPath, "--seed-idle", tc.val, "1")
			require.NoError(t, err)

			body, ok := stub.request("torrent-set")
			require.True(t, ok, "the bespoke torrent-set method was called")
			for _, want := range tc.want {
				assert.Contains(t, body, want)
			}
			if tc.miss != "" {
				assert.NotContains(t, body, tc.miss, "a mode-only idle limit must not send a limit value")
			}
		})
	}
}

// TestSetSeedIdleJSONRPC asserts a duration reaches the JSON-RPC wire as whole
// minutes with canonical snake_case keys.
func TestSetSeedIdleJSONRPC(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "6.0.0", jsonrpcListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--seed-idle", "2h", "1")
	require.NoError(t, err)

	body, ok := stub.request("torrent_set")
	require.True(t, ok, "the JSON-RPC torrent_set method was called")
	assert.Contains(t, body, `"seed_idle_limit":120`)
	assert.Contains(t, body, `"seed_idle_mode":1`)
}

// TestSetInvalidLimitErrors guards that bad sentinel values are rejected before
// any RPC call.
func TestSetInvalidLimitErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"negative download", []string{"--download-limit", "-5"}, "invalid value for --download-limit"},
		{"non-numeric download", []string{"--download-limit", "fast"}, "invalid value for --download-limit"},
		{"negative upload", []string{"--upload-limit", "-1"}, "invalid value for --upload-limit"},
		{"bad seed-ratio", []string{"--seed-ratio", "lots"}, "invalid value for --seed-ratio"},
		{"negative seed-ratio", []string{"--seed-ratio", "-2"}, "invalid value for --seed-ratio"},
		{"sub-minute seed-idle", []string{"--seed-idle", "90s"}, "invalid value for --seed-idle"},
		{"bad seed-idle", []string{"--seed-idle", "soon"}, "invalid value for --seed-idle"},
		{"negative seed-idle", []string{"--seed-idle", "-30"}, "invalid value for --seed-idle"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
			defer closeSrv()

			args := append([]string{"--config", cfgPath}, tc.args...)
			args = append(args, "1")

			_, _, err := runSet(t, args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)

			_, ok := stub.request("torrent-set")
			assert.False(t, ok, "the set method must not be called for an invalid value")
		})
	}
}

// TestSetCombinesScalarAndLimitFields guards that scalar (US-003) and limit
// (US-004) flags combine into a single torrent-set request.
func TestSetCombinesScalarAndLimitFields(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath,
		"--priority", "high",
		"--download-limit", "500",
		"--seed-ratio", "2.0",
		"1")
	require.NoError(t, err)

	body, ok := stub.request("torrent-set")
	require.True(t, ok, "the bespoke torrent-set method was called")
	assert.Contains(t, body, `"bandwidthPriority":1`)
	assert.Contains(t, body, `"downloadLimit":500`)
	assert.Contains(t, body, `"downloadLimited":true`)
	assert.Contains(t, body, `"seedRatioLimit":2`)
	assert.Contains(t, body, `"seedRatioMode":1`)
}

// TestSetLabelsBespoke asserts --label replaces the label set on the bespoke
// wire (labels passes through the mechanical transform unchanged).
func TestSetLabelsBespoke(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--label", "linux", "--label", "iso", "1")
	require.NoError(t, err)

	body, ok := stub.request("torrent-set")
	require.True(t, ok, "the bespoke torrent-set method was called")
	assert.Contains(t, body, `"labels":["linux","iso"]`)
}

// TestSetLabelsJSONRPC asserts --label reaches the JSON-RPC wire.
func TestSetLabelsJSONRPC(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "6.0.0", jsonrpcListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--label", "linux", "--label", "iso", "1")
	require.NoError(t, err)

	body, ok := stub.request("torrent_set")
	require.True(t, ok, "the JSON-RPC torrent_set method was called")
	assert.Contains(t, body, `"labels":["linux","iso"]`)
}

// TestSetClearLabels asserts --clear-labels sends an empty label list.
func TestSetClearLabels(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--clear-labels", "1")
	require.NoError(t, err)

	body, ok := stub.request("torrent-set")
	require.True(t, ok, "the bespoke torrent-set method was called")
	assert.Contains(t, body, `"labels":[]`)
}

// TestSetLabelMutualExclusion guards that --label and --clear-labels together
// are rejected before any RPC call.
func TestSetLabelMutualExclusion(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--label", "linux", "--clear-labels", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--label and --clear-labels cannot be used together")

	_, ok := stub.request("torrent-set")
	assert.False(t, ok, "the set method must not be called for the mutual-exclusion error")
}

// TestSetTrackersBespoke asserts --tracker replaces the tracker list as a
// newline-joined string on the bespoke wire (camelCase trackerList).
func TestSetTrackersBespoke(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath,
		"--tracker", "https://a.example/announce",
		"--tracker", "https://b.example/announce",
		"1")
	require.NoError(t, err)

	body, ok := stub.request("torrent-set")
	require.True(t, ok, "the bespoke torrent-set method was called")
	assert.Contains(t, body, `"trackerList":"https://a.example/announce\nhttps://b.example/announce"`)
}

// TestSetTrackersJSONRPC asserts --tracker reaches the JSON-RPC wire as a
// newline-joined string with the canonical snake_case key.
func TestSetTrackersJSONRPC(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "6.0.0", jsonrpcListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath,
		"--tracker", "https://a.example/announce",
		"--tracker", "https://b.example/announce",
		"1")
	require.NoError(t, err)

	body, ok := stub.request("torrent_set")
	require.True(t, ok, "the JSON-RPC torrent_set method was called")
	assert.Contains(t, body, `"tracker_list":"https://a.example/announce\nhttps://b.example/announce"`)
}

// TestSetClearTrackers asserts --clear-trackers sends an empty tracker list.
func TestSetClearTrackers(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--clear-trackers", "1")
	require.NoError(t, err)

	body, ok := stub.request("torrent-set")
	require.True(t, ok, "the bespoke torrent-set method was called")
	assert.Contains(t, body, `"trackerList":""`)
}

// TestSetTrackerMutualExclusion guards that --tracker and --clear-trackers
// together are rejected before any RPC call.
func TestSetTrackerMutualExclusion(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath,
		"--tracker", "https://a.example/announce", "--clear-trackers", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--tracker and --clear-trackers cannot be used together")

	_, ok := stub.request("torrent-set")
	assert.False(t, ok, "the set method must not be called for the mutual-exclusion error")
}

// TestSetGroup asserts --group sends the bandwidth group name verbatim (group
// passes through the mechanical transform unchanged on both protocols).
func TestSetGroup(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
	defer closeSrv()

	_, _, err := runSet(t, "--config", cfgPath, "--group", "throttled", "1")
	require.NoError(t, err)

	body, ok := stub.request("torrent-set")
	require.True(t, ok, "the bespoke torrent-set method was called")
	assert.Contains(t, body, `"group":"throttled"`)
}

func TestSetNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runSet(t, "--config", cfgPath, "--priority", "high", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// TestSetSanitizesNameInHumanButNotJSON guards the security boundary: a torrent
// name carrying an ANSI escape is neutralized in the human confirmation but
// preserved verbatim in the structured --json contract.
func TestSetSanitizesNameInHumanButNotJSON(t *testing.T) {
	const body = `{
		"arguments": {"torrents": [
			{"id": 1, "hashString": "0123456789abcdef0123456789abcdef01234567", "name": "\u001b[2Jmalicious", "status": 0}
		]},
		"result": "success",
		"tag": 1
	}`

	cfgPath, _, closeSrv := removeConfigPath(t, "", body)
	defer closeSrv()

	human, _, err := runSet(t, "--config", cfgPath, "--priority", "high", "1")
	require.NoError(t, err)
	assert.NotContains(t, human, "\x1b", "raw ESC must not reach the human confirmation")
	assert.Contains(t, human, "[2Jmalicious", "visible text survives sanitization")

	jsonOut, _, err := runSet(t, "--config", cfgPath, "--json", "--priority", "high", "1")
	require.NoError(t, err)
	assert.Contains(t, jsonOut, "\\u001b", "the raw escape is preserved in the JSON contract")
}
