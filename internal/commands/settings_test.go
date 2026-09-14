package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The registry's own tests live beside it, in
// internal/transmission/settings_test.go. These cover the command layer's
// plaintext writers only.

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
