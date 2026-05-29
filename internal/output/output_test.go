package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSON(t *testing.T) {
	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	var buf bytes.Buffer
	require.NoError(t, JSON(&buf, payload{Name: "alice", Count: 3}))

	out := buf.String()

	// Indented (two-space) and newline-terminated.
	assert.True(t, strings.HasSuffix(out, "}\n"))
	assert.Contains(t, out, "\n  \"name\": \"alice\"")

	// Round-trips back to the same value.
	var got payload
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, payload{Name: "alice", Count: 3}, got)
}

func TestBytes(t *testing.T) {
	tests := []struct {
		name string
		n    int64
		want string
	}{
		{"zero", 0, "0 B"},
		{"sub kib", 512, "512 B"},
		{"one kib", 1024, "1.0 KiB"},
		{"one and a half kib", 1536, "1.5 KiB"},
		{"one mib", 1024 * 1024, "1.0 MiB"},
		{"one and a half gib", 1024 * 1024 * 1024 * 3 / 2, "1.5 GiB"},
		{"one tib", 1024 * 1024 * 1024 * 1024, "1.0 TiB"},
		{"negative", -1536, "-1.5 KiB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Bytes(tt.n))
		})
	}
}

func TestSpeed(t *testing.T) {
	tests := []struct {
		name string
		n    int64
		want string
	}{
		{"zero", 0, "0 B/s"},
		{"bytes", 900, "900 B/s"},
		{"one and a half mib", 1024 * 1024 * 3 / 2, "1.5 MiB/s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Speed(tt.n))
		})
	}
}

func TestPercent(t *testing.T) {
	tests := []struct {
		name     string
		fraction float64
		want     string
	}{
		{"zero", 0, "0%"},
		{"complete", 1, "100%"},
		{"two thirds rounds", 0.731, "73%"},
		{"rounds up", 0.999, "100%"},
		{"rounds to nearest", 0.255, "26%"},
		{"clamps negative", -0.5, "0%"},
		{"clamps over one", 1.5, "100%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Percent(tt.fraction))
		})
	}
}

func TestRatio(t *testing.T) {
	tests := []struct {
		name  string
		ratio float64
		want  string
	}{
		{"zero", 0, "0.00"},
		{"one and a half", 1.5, "1.50"},
		{"two decimals", 1.234, "1.23"},
		{"unknown is inf", -1, "Inf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Ratio(tt.ratio))
		})
	}
}

func TestDuration(t *testing.T) {
	tests := []struct {
		name    string
		seconds int64
		want    string
	}{
		{"zero", 0, "0s"},
		{"seconds only", 45, "45s"},
		{"minutes and seconds", 125, "2m 5s"},
		{"hours minutes seconds", 3*3600 + 4*60 + 5, "3h 4m 5s"},
		{"days hours minutes", 3*86400 + 4*3600 + 5*60, "3d 4h 5m"},
		{"exact day", 86400, "1d"},
		{"omits leading zero units", 3661, "1h 1m 1s"},
		{"negative", -90, "-1m 30s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Duration(tt.seconds))
		})
	}
}

func TestJQValidExpression(t *testing.T) {
	type payload struct {
		UploadSpeed int64  `json:"upload_speed"`
		Name        string `json:"name"`
	}

	var buf bytes.Buffer
	require.NoError(t, JQ(&buf, payload{UploadSpeed: 4096, Name: "alice"}, ".upload_speed"))

	assert.Equal(t, "4096\n", buf.String())
}

func TestJQMultipleResults(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, JQ(&buf, []int{1, 2, 3}, ".[]"))

	assert.Equal(t, "1\n2\n3\n", buf.String())
}

func TestJQObjectResult(t *testing.T) {
	type payload struct {
		Inner map[string]int `json:"inner"`
	}

	var buf bytes.Buffer
	require.NoError(t, JQ(&buf, payload{Inner: map[string]int{"a": 1}}, ".inner"))

	var got map[string]int
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, map[string]int{"a": 1}, got)
}

func TestJQInvalidExpressionReturnsError(t *testing.T) {
	var buf bytes.Buffer
	err := JQ(&buf, map[string]int{"a": 1}, ".[")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid jq expression")
	assert.Empty(t, buf.String())
}

func TestJQRuntimeErrorReturnsError(t *testing.T) {
	var buf bytes.Buffer
	// Indexing a number is a runtime error in jq.
	err := JQ(&buf, 5, ".foo")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "evaluating jq expression")
}
