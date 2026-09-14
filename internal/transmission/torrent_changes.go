package transmission

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The torrent change builders turn one user-typed value into the neutral
// kebab-case keys Client.SetTorrents writes. They are shared by `torrent update`
// and the TUI's per-torrent limit prompts, so both accept exactly the same
// spellings and write exactly the same keys.
//
// Their errors name the value, never a flag: the CLI wraps them with the flag it
// read, and a prompt shows them as they are.

// The seed-ratio-mode and seed-idle-mode values: follow the session, a limit of
// the torrent's own, or no limit.
const (
	seedModeGlobal    = 0
	seedModeSingle    = 1
	seedModeUnlimited = 2
)

// BandwidthPriority maps low, normal, or high, matched case-insensitively, to
// Transmission's bandwidth-priority integer (-1, 0, 1).
func BandwidthPriority(raw string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "low":
		return -1, nil
	case "normal":
		return 0, nil
	case "high":
		return 1, nil
	}

	return 0, errors.New("must be low, normal, or high")
}

// RateLimitChanges builds the paired <kind>-limit / <kind>-limited keys, where
// kind is download or upload. "off" (case-insensitive) disables the limit and
// leaves the limit value untouched; a non-negative integer sets the limit in
// kB/s and enables it.
func RateLimitChanges(kind, raw string) (map[string]any, error) {
	if kind != "download" && kind != "upload" {
		return nil, fmt.Errorf("unknown rate limit %q", kind)
	}

	value := strings.TrimSpace(raw)

	if strings.EqualFold(value, "off") {
		return map[string]any{kind + "-limited": false}, nil
	}

	kbps, err := strconv.Atoi(value)
	if err != nil || kbps < 0 {
		return nil, errors.New("must be a non-negative integer (kB/s) or 'off'")
	}

	return map[string]any{kind + "-limit": kbps, kind + "-limited": true}, nil
}

// SeedRatioChanges builds the paired seed-ratio-limit / seed-ratio-mode keys. A
// non-negative number sets the limit and mode 1 (single); "global" sets mode 0;
// "unlimited" sets mode 2.
func SeedRatioChanges(raw string) (map[string]any, error) {
	value := strings.TrimSpace(raw)

	if changes, ok := seedModeChanges("seed-ratio-mode", value); ok {
		return changes, nil
	}

	ratio, err := strconv.ParseFloat(value, 64)
	if err != nil || ratio < 0 {
		return nil, errors.New("must be a non-negative number, 'global', or 'unlimited'")
	}

	return map[string]any{"seed-ratio-limit": ratio, "seed-ratio-mode": seedModeSingle}, nil
}

// SeedIdleChanges builds the paired seed-idle-limit / seed-idle-mode keys. A
// bare integer (minutes) or a Go-style duration (30m, 1h, 1h30m) sets the limit
// in whole minutes and mode 1 (single); "global" sets mode 0; "unlimited" sets
// mode 2. A duration with sub-minute precision (90s) is refused.
func SeedIdleChanges(raw string) (map[string]any, error) {
	value := strings.TrimSpace(raw)

	if changes, ok := seedModeChanges("seed-idle-mode", value); ok {
		return changes, nil
	}

	invalid := errors.New("must be whole minutes (integer or duration like 30m/1h), 'global', or 'unlimited'")

	minutes, err := strconv.Atoi(value)
	if err != nil {
		d, derr := time.ParseDuration(value)
		if derr != nil || d%time.Minute != 0 {
			return nil, invalid
		}

		minutes = int(d / time.Minute)
	}

	if minutes < 0 {
		return nil, invalid
	}

	return map[string]any{"seed-idle-limit": minutes, "seed-idle-mode": seedModeSingle}, nil
}

// seedModeChanges answers the two word values a seed limit accepts.
func seedModeChanges(modeKey, value string) (map[string]any, bool) {
	switch strings.ToLower(value) {
	case "global":
		return map[string]any{modeKey: seedModeGlobal}, true
	case "unlimited":
		return map[string]any{modeKey: seedModeUnlimited}, true
	}

	return nil, false
}
