package commands

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// The session settings registry itself — every key, its type, category,
// default, and accessor — lives in internal/transmission (SettingDefs and
// friends), where the TUI can reach it too. This file holds only the command
// layer's use of it: flag generation, flag reading, and the plaintext writers.

// settingRow is the per-setting element of the settings JSON contract: a stable,
// jq-friendly {key, category, value} object whose Value carries the native typed
// value (bool / number / string). It is the array element emitted by --json/--jq.
type settingRow struct {
	Key      string `json:"key"`
	Category string `json:"category"`
	Value    any    `json:"value"`
}

// registerSettingFlags adds one flag per settable registry entry to the update
// command. The flag name is the setting key and its type follows the setting's
// SettingType; the help text comes from the setting's Desc. The read-only
// blocklist-size gets no flag (it has no Desc and cannot be written).
func registerSettingFlags(cmd *cobra.Command) {
	for _, def := range transmission.SettingDefs() {
		if def.ReadOnly {
			continue
		}

		switch def.Type {
		case transmission.SettingBool:
			cmd.Flags().Bool(def.Key, false, def.Desc)
		case transmission.SettingInt:
			cmd.Flags().Int(def.Key, 0, def.Desc)
		case transmission.SettingInt64:
			cmd.Flags().Int64(def.Key, 0, def.Desc)
		case transmission.SettingFloat:
			cmd.Flags().Float64(def.Key, 0, def.Desc)
		case transmission.SettingString, transmission.SettingEnum:
			cmd.Flags().String(def.Key, "", def.Desc)
		}
	}
}

// collectSettingChanges builds the ordered definitions to report and the neutral
// kebab-case change map from the setting flags that were explicitly set (detected
// via cmd.Flags().Changed). Enum flags are validated case-insensitively, and an
// invalid value returns a clear error (and no map entry) before any write, so
// nothing is sent on a bad value. Unset flags are omitted entirely. Order follows
// the registry, so output is stable regardless of flag order on the command line.
func collectSettingChanges(cmd *cobra.Command) ([]transmission.SettingDef, map[string]any, error) {
	var defs []transmission.SettingDef
	changes := make(map[string]any)

	for _, def := range transmission.SettingDefs() {
		if def.ReadOnly || !cmd.Flags().Changed(def.Key) {
			continue
		}

		value, err := settingFlagValue(cmd, def)
		if err != nil {
			return nil, nil, err
		}

		defs = append(defs, def)
		changes[def.Key] = value
	}

	return defs, changes, nil
}

// settingFlagValue reads the typed value of a changed setting flag, returning it
// as the neutral typed value written to the change map. Enum values are validated
// case-insensitively and normalized to their canonical lowercase form; all other
// types are read directly from the typed cobra flag.
func settingFlagValue(cmd *cobra.Command, def transmission.SettingDef) (any, error) {
	switch def.Type {
	case transmission.SettingBool:
		return cmd.Flags().GetBool(def.Key)
	case transmission.SettingInt:
		return cmd.Flags().GetInt(def.Key)
	case transmission.SettingInt64:
		return cmd.Flags().GetInt64(def.Key)
	case transmission.SettingFloat:
		return cmd.Flags().GetFloat64(def.Key)
	case transmission.SettingEnum:
		raw, err := cmd.Flags().GetString(def.Key)
		if err != nil {
			return nil, err
		}

		return def.ValidateEnum(raw)
	case transmission.SettingString:
		return cmd.Flags().GetString(def.Key)
	default:
		return nil, fmt.Errorf("invalid value for %s: unsupported type", def.Key)
	}
}

// writtenSettingRows builds the {key, category, value} JSON rows from the values
// just written (not a re-read), in the given definition order. The values are the
// native typed values from the change map, matching the --json/--jq contract.
func writtenSettingRows(defs []transmission.SettingDef, changes map[string]any) []settingRow {
	rows := make([]settingRow, len(defs))
	for i, def := range defs {
		rows[i] = settingRow{
			Key:      def.Key,
			Category: string(def.Category),
			Value:    changes[def.Key],
		}
	}

	return rows
}

// settingRows builds the {key, category, value} JSON rows for the given
// definitions from a session snapshot, in registry order. Values are the native
// typed values (never sanitized — JSON/jq is a structured contract). An empty
// set serializes as "[]" rather than "null".
func settingRows(defs []transmission.SettingDef, session transmission.SessionSettings) []settingRow {
	rows := make([]settingRow, len(defs))
	for i, def := range defs {
		rows[i] = settingRow{
			Key:      def.Key,
			Category: string(def.Category),
			Value:    def.Accessor(session),
		}
	}

	return rows
}

// settingValueCell renders a setting's value as plaintext, neutralizing terminal
// control sequences for the server-derived settings (SettingDef.ServerDerived)
// so a crafted download-dir cannot inject ANSI escapes. It serves both the read
// path (a value from a session snapshot) and the write path (a value just sent),
// which differ only in where the value came from. The JSON/jq paths never call
// it.
func settingValueCell(key string, value any) string {
	v := transmission.FormatSettingValue(value)
	if def, ok := transmission.LookupSetting(key); ok && def.ServerDerived {
		v = output.SanitizeTerminal(v)
	}

	return v
}

// writeSettingsPiped writes plain, tab-separated CATEGORY KEY VALUE rows with no
// header — the stable scripting contract. An empty set writes nothing.
func writeSettingsPiped(w io.Writer, rows []settingRow) {
	for _, row := range rows {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", row.Category, row.Key, settingValueCell(row.Key, row.Value))
	}
}

// writeSettingsTable writes a whitespace-aligned CATEGORY KEY VALUE table with a
// header row. An empty set writes a friendly "No settings." line.
func writeSettingsTable(w io.Writer, rows []settingRow) {
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(w, "No settings.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	_, _ = fmt.Fprintln(tw, "CATEGORY\tKEY\tVALUE")
	for _, row := range rows {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", row.Category, row.Key, settingValueCell(row.Key, row.Value))
	}

	_ = tw.Flush()
}
