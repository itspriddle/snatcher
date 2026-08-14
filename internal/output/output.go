// Package output provides snatcher's CLI output layer: indented JSON rendering,
// human-readable formatters (bytes, speed, duration), and a jq filter so results
// can be read by eye or piped into automation.
//
// Render selection rule: --jq implies JSON input; plaintext is the default when
// neither --json nor --jq is set. The helpers here are generic and have no
// dependency on the Transmission layer.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/itchyny/gojq"
)

// jsonIndent is the indentation used for all JSON rendering.
const jsonIndent = "  "

// JSON writes v to w as indented JSON followed by a trailing newline.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", jsonIndent)

	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("output: encoding json: %w", err)
	}

	return nil
}

// JQ evaluates the jq program expr over the JSON representation of v and writes
// each result to w as indented JSON, one per line. An invalid expression or a
// runtime jq error returns a clear, non-panicking error.
func JQ(w io.Writer, v any, expr string) error {
	query, err := gojq.Parse(expr)
	if err != nil {
		return fmt.Errorf("output: invalid jq expression %q: %w", expr, err)
	}

	// Round-trip v through JSON so gojq sees plain map[string]any / []any / scalar
	// values with the same field names as the JSON output, honoring json tags.
	input, err := toJQInput(v)
	if err != nil {
		return err
	}

	iter := query.Run(input)
	for {
		result, ok := iter.Next()
		if !ok {
			break
		}

		if err, ok := result.(error); ok {
			return fmt.Errorf("output: evaluating jq expression %q: %w", expr, err)
		}

		if err := JSON(w, result); err != nil {
			return err
		}
	}

	return nil
}

// toJQInput marshals v to JSON and unmarshals it back into a generic value
// (map[string]any, []any, or scalar) suitable as gojq input.
func toJQInput(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("output: encoding value for jq: %w", err)
	}

	var input any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("output: decoding value for jq: %w", err)
	}

	return input, nil
}

// byteUnits are the IEC binary unit suffixes used by Bytes and Speed.
var byteUnits = [...]string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}

// Bytes formats n bytes as an IEC binary-prefixed string (e.g. "1.5 GiB").
// Values below 1 KiB are reported in whole bytes (e.g. "512 B").
func Bytes(n int64) string {
	return formatIEC(n) + " " + iecUnit(n)
}

// Speed formats n bytes/second as an IEC binary rate (e.g. "1.5 MiB/s").
func Speed(n int64) string {
	return Bytes(n) + "/s"
}

// formatIEC returns the numeric part of an IEC-formatted byte count. Sub-KiB
// values are whole numbers; larger values carry one decimal place.
func formatIEC(n int64) string {
	abs := n
	if abs < 0 {
		abs = -abs
	}

	const unit = 1024
	if abs < unit {
		return fmt.Sprintf("%d", n)
	}

	value := float64(n)
	exp := 0
	for math.Abs(value) >= unit && exp < len(byteUnits)-1 {
		value /= unit
		exp++
	}

	return fmt.Sprintf("%.1f", value)
}

// iecUnit returns the IEC unit suffix appropriate for n.
func iecUnit(n int64) string {
	abs := n
	if abs < 0 {
		abs = -abs
	}

	const unit = 1024
	exp := 0
	for abs >= unit && exp < len(byteUnits)-1 {
		abs /= unit
		exp++
	}

	return byteUnits[exp]
}

// Percent formats a 0–1 completion fraction as a whole-number percentage with a
// trailing "%" (e.g. 0.731 -> "73%"). Values are clamped to the 0–100 range and
// rounded to the nearest whole percent.
func Percent(fraction float64) string {
	pct := fraction * 100

	switch {
	case pct < 0:
		pct = 0
	case pct > 100:
		pct = 100
	}

	return fmt.Sprintf("%d%%", int(math.Round(pct)))
}

// Ratio formats an upload ratio with two decimal places (e.g. 1.5 -> "1.50").
// A negative ratio (Transmission's "unknown" sentinel) renders as "Inf".
func Ratio(ratio float64) string {
	if ratio < 0 {
		return "Inf"
	}

	return fmt.Sprintf("%.2f", ratio)
}

// Date formats a Unix timestamp (seconds) as a local "2006-01-02" date. A
// non-positive timestamp (unset/unknown) renders as a dash.
func Date(unix int64) string {
	if unix <= 0 {
		return "-"
	}

	return time.Unix(unix, 0).Format("2006-01-02")
}

// SanitizeTerminal neutralizes terminal control sequences in untrusted,
// server-derived strings (torrent names, labels, errors, download dirs, peer
// client strings) before they reach a human/plaintext render path. It removes
// C0 control bytes (0x00–0x1F, including ESC 0x1B), the DEL byte (0x7F), C1
// control runes (0x80–0x9F), the Unicode line and paragraph separators
// (U+2028/U+2029), and Unicode's bidirectional format controls (see
// isTerminalControl), so a maliciously crafted torrent name or peer client
// string cannot inject ANSI escapes that clear the screen, move the cursor, or
// spoof output, cannot forge a record in the tab-separated piped contract, and
// cannot reverse the visual order of a rendered row. Tabs, carriage returns,
// and newlines are C0 controls and are therefore stripped too, which also keeps
// tab-separated and line-oriented output columns intact.
//
// Legitimate text passes through untouched: accented Latin, CJK, wide and
// combining characters, emoji including their ZWJ sequences, and the
// zero-width joiner and non-joiner that Persian, Arabic, and Indic orthography
// depend on. The filter drops invisible characters only where they can reorder
// or forge a line, never merely for being invisible.
//
// The guarantee is scoped to format controls. A strong right-to-left run —
// Arabic or Hebrew text carrying no controls at all — still reorders the
// neutral characters beside it (digits, "%", "MiB/s") when a terminal lays out
// an aligned row, and stripping RTL script is not an option. Sanitizing removes
// every way an *invisible* character can reorder a row; it does not make every
// row visually unambiguous.
//
// The original string is returned unchanged when it contains no control
// characters, so the common case avoids any allocation. SanitizeTerminal must
// NOT be applied to --json/--jq output, which is a structured contract that
// consumers handle themselves.
func SanitizeTerminal(s string) string {
	if !strings.ContainsFunc(s, isTerminalControl) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isTerminalControl(r) {
			continue
		}
		b.WriteRune(r)
	}

	return b.String()
}

// isTerminalControl reports whether r is one of the runes SanitizeTerminal
// removes: a C0 control (0x00–0x1F), DEL (0x7F), a C1 control (0x80–0x9F), the
// Unicode line and paragraph separators (U+2028/U+2029), or one of Unicode's
// bidirectional format controls — U+061C (ALM), U+200B (ZWSP), U+200E/U+200F
// (LRM/RLM), U+202A–U+202E (the embeddings and overrides), and U+2066–U+2069
// (the isolates). Those are exactly the runes with the Bidi_Control property,
// plus the zero-width space that hides beside them.
//
// The bidi controls matter because a terminal honors them the way a browser
// does. U+202E (RLO) reverses the display order of everything after it, so a
// peer-supplied client string ending in RLO would visually swap the Down, Up,
// and Encrypted cells that follow it on the same aligned row — spoofing values
// snatcher itself rendered. The isolate controls U+2066–U+2069 do the same
// inside a bounded run, U+202A–U+202D are the older embedding/override forms,
// and the marks (ALM/LRM/RLM) nudge the direction of the neutral characters
// around them. None of them carries meaning snatcher needs to render, so all
// are dropped rather than escaped.
//
// U+2028 and U+2029 are dropped for a program's benefit rather than the eye's.
// snatcher's piped plaintext is header-less tab-separated rows, so a line
// separator inside a server-derived string forges an entire extra record for
// any consumer that splits on Unicode line breaks (Python's str.splitlines,
// older JavaScript JSON parsers). Every other character such a consumer treats
// as a break — \v, \f, 0x1C–0x1E, and U+0085 — already falls under C0/C1, so
// leaving these two would have been an arbitrary hole.
//
// The invisible set stops there and is deliberately not "every invisible
// format character". U+200C (ZWNJ) and U+200D (ZWJ) are kept: they are required
// orthography — Persian and Arabic word forms, and conjunct versus chillu
// selection in Devanagari, Bengali, Tamil, Malayalam, and Sinhala — and a ZWJ
// sequence is what makes a family emoji a single glyph. U+2060 (word joiner)
// and U+FEFF (ZWNBSP) are knowingly excluded on the same reasoning. None of the
// four has any reordering power, so dropping them would corrupt legitimate
// names on every plaintext path for no security gain.
func isTerminalControl(r rune) bool {
	switch {
	case r < 0x20, r == 0x7F, r >= 0x80 && r <= 0x9F:
		return true
	case r == 0x061C: // ALM
		return true
	case r == 0x200B, r == 0x200E, r == 0x200F: // ZWSP, LRM, RLM
		return true
	case r == 0x2028, r == 0x2029: // LINE SEPARATOR, PARAGRAPH SEPARATOR
		return true
	case r >= 0x202A && r <= 0x202E: // LRE, RLE, PDF, LRO, RLO
		return true
	case r >= 0x2066 && r <= 0x2069: // LRI, RLI, FSI, PDI
		return true
	default:
		return false
	}
}

// ETA formats a torrent's estimated time remaining. A negative value is
// Transmission's "unknown / not applicable" sentinel and renders as a dash;
// any other value is formatted as a compact Duration. It is the single source
// of truth for the ETA sentinel rule, shared by the CLI list output and the
// future TUI so neither re-encodes the convention.
func ETA(eta int64) string {
	if eta < 0 {
		return "-"
	}

	return Duration(eta)
}

// Duration formats a number of seconds as a compact "Nd Nh Nm Ns" string,
// omitting leading zero units (e.g. 273905 -> "3d 4h 5m 5s"). Zero yields "0s".
func Duration(seconds int64) string {
	if seconds == 0 {
		return "0s"
	}

	negative := seconds < 0
	if negative {
		seconds = -seconds
	}

	const (
		minute = 60
		hour   = 60 * minute
		day    = 24 * hour
	)

	days := seconds / day
	seconds %= day
	hours := seconds / hour
	seconds %= hour
	minutes := seconds / minute
	secs := seconds % minute

	parts := make([]string, 0, 4)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if secs > 0 {
		parts = append(parts, fmt.Sprintf("%ds", secs))
	}

	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}

	if negative {
		return "-" + out
	}

	return out
}
