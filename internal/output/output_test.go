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
		{"sub kb", 512, "512 B"},
		{"one kb", 1024, "1.0 KB"},
		{"one and a half kb", 1536, "1.5 KB"},
		{"one mb", 1024 * 1024, "1.0 MB"},
		{"one and a half gb", 1024 * 1024 * 1024 * 3 / 2, "1.5 GB"},
		{"one tb", 1024 * 1024 * 1024 * 1024, "1.0 TB"},
		{"negative", -1536, "-1.5 KB"},
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
		{"one and a half mb", 1024 * 1024 * 3 / 2, "1.5 MB/s"},
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

func TestSanitizeTerminal(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Debian ISO", "Debian ISO"},
		{"unicode", "Café — naïve 日本語", "Café — naïve 日本語"},
		{"esc clear screen", "\x1b[2Jmalicious", "[2Jmalicious"},
		{"esc only", "\x1b", ""},
		{"c0 controls", "a\x00b\x07c\x1bd", "abcd"},
		{"tab", "a\tb", "ab"},
		{"newline and cr", "a\r\nb", "ab"},
		{"del", "a\x7fb", "ab"},
		{"c1 controls", "a\u0085b", "ab"},
		{"empty", "", ""},

		// Bidi marks and the zero-width space that hides beside them.
		{"arabic letter mark", "a\u061cb", "ab"},
		{"zero width space", "a\u200bb", "ab"},
		{"left to right mark", "a\u200eb", "ab"},
		{"right to left mark", "a\u200fb", "ab"},

		// U+2028/U+2029: a line separator inside a name forges an extra record
		// in the header-less tab-separated piped contract.
		{"line separator", "a\u2028b", "ab"},
		{"paragraph separator", "a\u2029b", "ab"},

		// U+202A-U+202E: bidi embeddings and overrides.
		{"left to right embedding", "a\u202ab", "ab"},
		{"pop directional formatting", "a\u202cb", "ab"},
		{"right to left override", "a\u202eb", "ab"},
		{"whole override range", "a\u202a\u202b\u202c\u202d\u202eb", "ab"},

		// U+2066-U+2069: bidi isolates.
		{"left to right isolate", "a\u2066b", "ab"},
		{"first strong isolate", "a\u2068b", "ab"},
		{"pop directional isolate", "a\u2069b", "ab"},
		{"whole isolate range", "a\u2066\u2067\u2068\u2069b", "ab"},

		// The runes neighboring the stripped ranges stay untouched.
		{"arabic semicolon below the arabic letter mark", "a\u061bb", "a\u061bb"},
		{"hair space below the direction marks", "a\u200ab", "a\u200ab"},
		{"hyphen above the direction marks", "a\u2010b", "a\u2010b"},
		{"hyphenation point below the separators", "a\u2027b", "a\u2027b"},
		{"narrow no-break space above the override range", "a\u202fb", "a\u202fb"},
		{"superscript zero above the isolate range", "a\u2070b", "a\u2070b"},

		// The invisible characters that carry no reordering power are kept:
		// dropping them would corrupt required orthography for no security gain.
		{"zero width non-joiner", "a\u200cb", "a\u200cb"},
		{"zero width joiner", "a\u200db", "a\u200db"},
		{"word joiner", "a\u2060b", "a\u2060b"},
		{"zero width no-break space", "a\ufeffb", "a\ufeffb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeTerminal(tt.in)
			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, "\x1b", "no raw ESC survives sanitization")
			assert.NotContains(t, got, "\u202e", "no direction override survives sanitization")
		})
	}
}

// TestSanitizeTerminalDefeatsBidiRowSpoofing pins the case that motivated
// widening the filter. A peer chooses its own client string, and U+202E (RLO)
// reverses the display order of everything after it, so an unsanitized cell
// would visually swap the Down, Up, and Encrypted cells rendered to its right
// on the same aligned "snatcher torrent peers" row.
//
// It covers the format-control half of the problem only. Per SanitizeTerminal's
// doc, a strong RTL run carrying no controls at all still reorders the neutral
// cells beside it, and that residual is not something sanitizing can remove.
// The command-level counterpart is TestPeersSanitizesHumanOutputNotJSON.
func TestSanitizeTerminalDefeatsBidiRowSpoofing(t *testing.T) {
	const hostileClient = "qBittorrent 4.6\u202e"

	row := func(client string) string {
		return strings.Join([]string{"10.0.0.5", "51413", client, "42%", "1.5 MB/s", "0 B/s", "yes"}, "\t")
	}

	// The fixture must really carry the override, or the assertions below hold
	// for the wrong reason.
	require.Contains(t, row(hostileClient), "\u202e")

	got := row(SanitizeTerminal(hostileClient))

	assert.Equal(t, "10.0.0.5\t51413\tqBittorrent 4.6\t42%\t1.5 MB/s\t0 B/s\tyes", got)

	for _, r := range []rune{'\u200b', '\u200e', '\u200f', '\u202a', '\u202e', '\u2066', '\u2069'} {
		assert.NotContains(t, got, string(r), "no bidi control reaches the rendered row")
	}
}

// TestSanitizeTerminalPreservesLegitimateText guards the widened filter against
// becoming a mojibake source: only controls are dropped, never ordinary text.
func TestSanitizeTerminalPreservesLegitimateText(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"accented latin", "Café naïve Ünïcödé"},
		{"cjk", "日本語 中文 한국어"},
		{"emoji", "🚀 📦 ✅"},
		{"combining marks", "e\u0301 a\u0300 n\u0303"},
		{"rtl script without controls", "مرحبا بالعالم"},
		{"persian word form with zwnj", "می\u200cخواهم"},
		{"devanagari conjunct with zwj", "क्\u200dष"},
		{"family emoji zwj sequence", "👨\u200d👩\u200d👧"},
		{"mixed", "Ubuntu 24.04 — 日本語 🚀"},
		{"symbols and punctuation", "«quoted» — 50 % ± 3 … ✓"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.in, SanitizeTerminal(tt.in))
		})
	}
}

// TestJSONAndJQNeverSanitize pins the other half of the sanitization boundary:
// --json and --jq are a structured contract consumers handle themselves, so
// every byte the server sent survives them, control sequences included.
func TestJSONAndJQNeverSanitize(t *testing.T) {
	type peer struct {
		Client string `json:"client"`
	}

	const hostile = "qBittorrent 4.6\u202e\u200b\x1b[2J"

	t.Run("json", func(t *testing.T) {
		var buf bytes.Buffer
		require.NoError(t, JSON(&buf, peer{Client: hostile}))

		assert.Contains(t, buf.String(), "\u202e", "the direction override is written verbatim")

		var got peer
		require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
		assert.Equal(t, hostile, got.Client)
	})

	t.Run("jq", func(t *testing.T) {
		var buf bytes.Buffer
		require.NoError(t, JQ(&buf, peer{Client: hostile}, ".client"))

		var got string
		require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
		assert.Equal(t, hostile, got)
	})
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

func TestETA(t *testing.T) {
	tests := []struct {
		name string
		eta  int64
		want string
	}{
		{"unknown is dash", -1, "-"},
		{"large negative is dash", -100, "-"},
		{"zero", 0, "0s"},
		{"seconds", 45, "45s"},
		{"hours minutes seconds", 3*3600 + 4*60 + 5, "3h 4m 5s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ETA(tt.eta))
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
