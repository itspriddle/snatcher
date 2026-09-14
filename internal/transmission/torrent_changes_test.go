package transmission_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// TestBandwidthPriority pins the three priority words and their integers.
func TestBandwidthPriority(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw     string
		want    int
		wantErr bool
	}{
		{raw: "low", want: -1},
		{raw: "Normal", want: 0},
		{raw: " HIGH ", want: 1},
		{raw: "urgent", wantErr: true},
		{raw: "", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()

			got, err := transmission.BandwidthPriority(tc.raw)
			if tc.wantErr {
				require.EqualError(t, err, "must be low, normal, or high")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestRateLimitChanges pins that off touches only the limited flag and a number
// sets both keys.
func TestRateLimitChanges(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, kind, raw string
		want            map[string]any
		wantErr         bool
	}{
		{name: "off", kind: "download", raw: "off", want: map[string]any{"download-limited": false}},
		{name: "OFF", kind: "upload", raw: "OFF", want: map[string]any{"upload-limited": false}},
		{name: "number", kind: "download", raw: " 500 ", want: map[string]any{"download-limit": 500, "download-limited": true}},
		{name: "zero", kind: "upload", raw: "0", want: map[string]any{"upload-limit": 0, "upload-limited": true}},
		{name: "negative", kind: "download", raw: "-5", wantErr: true},
		{name: "word", kind: "download", raw: "fast", wantErr: true},
		{name: "float", kind: "download", raw: "1.5", wantErr: true},
		{name: "bad kind", kind: "peer", raw: "5", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := transmission.RateLimitChanges(tc.kind, tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestSeedRatioChanges pins the ratio's number and word forms.
func TestSeedRatioChanges(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw     string
		want    map[string]any
		wantErr bool
	}{
		{raw: "2.0", want: map[string]any{"seed-ratio-limit": 2.0, "seed-ratio-mode": 1}},
		{raw: "0", want: map[string]any{"seed-ratio-limit": 0.0, "seed-ratio-mode": 1}},
		{raw: "Global", want: map[string]any{"seed-ratio-mode": 0}},
		{raw: "unlimited", want: map[string]any{"seed-ratio-mode": 2}},
		{raw: "-2", wantErr: true},
		{raw: "lots", wantErr: true},
		{raw: "", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()

			got, err := transmission.SeedRatioChanges(tc.raw)
			if tc.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestSeedIdleChanges pins minutes, whole-minute durations, the words, and the
// refusal of a sub-minute duration.
func TestSeedIdleChanges(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw     string
		want    map[string]any
		wantErr bool
	}{
		{raw: "30", want: map[string]any{"seed-idle-limit": 30, "seed-idle-mode": 1}},
		{raw: "1h30m", want: map[string]any{"seed-idle-limit": 90, "seed-idle-mode": 1}},
		{raw: "2h", want: map[string]any{"seed-idle-limit": 120, "seed-idle-mode": 1}},
		{raw: "Global", want: map[string]any{"seed-idle-mode": 0}},
		{raw: "UNLIMITED", want: map[string]any{"seed-idle-mode": 2}},
		{raw: "90s", wantErr: true},
		{raw: "-30", wantErr: true},
		{raw: "-30m", wantErr: true},
		{raw: "soon", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()

			got, err := transmission.SeedIdleChanges(tc.raw)
			if tc.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
