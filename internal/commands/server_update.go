package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// newServerUpdateCmd builds "server update", which writes one or more session
// settings via one typed flag per settable setting (flag name == setting key),
// mirroring "torrent update". Only the flags explicitly given are written
// (detected via cmd.Flags().Changed), so unspecified settings are never sent; a
// bare invocation with no setting flag is an error, not a no-op. Enum flags are
// validated case-insensitively before any network write, so an invalid value
// aborts with no partial change. On success it writes via backend.SetSession
// and, rather than re-fetching, echoes the written values (key: value per line on
// plaintext in registry order; the {key, category, value} array on --json/--jq).
func newServerUpdateCmd() *cobra.Command {
	var (
		server string
		asJSON bool
		jqExpr string
	)

	cmd := &cobra.Command{
		Use:   "update [options]",
		Short: "Change server settings",
		Long: "Change one or more server (session) settings, each given as a typed " +
			"flag (e.g. --peer-port 51413 --dht-enabled=false). The flag name is the " +
			"setting key.\n\n" +
			"Only the flags you pass are written, so unspecified settings are left " +
			"unchanged. At least one setting flag is required; a bare invocation is " +
			"an error, not a no-op. Bool settings are enabled with a bare --key and " +
			"disabled with --key=false.\n\n" +
			"After writing, the values you set are echoed back (this is what was " +
			"written, not a re-read, so server-side normalization is not reflected). " +
			"Use 'snatcher server reset' to restore settings to their defaults, and " +
			"'snatcher server show' to read them.\n\n" +
			"Units: speed-limit-down, speed-limit-up, alt-speed-down, and " +
			"alt-speed-up are in KB/s (e.g. --speed-limit-down 100 is 100 KB/s). " +
			"alt-speed-time-begin and alt-speed-time-end are minutes after midnight " +
			"(e.g. 540 is 09:00).",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			defs, changes, err := collectSettingChanges(cmd)
			if err != nil {
				return err
			}

			if len(changes) == 0 {
				return fmt.Errorf("nothing to update: provide at least one setting flag")
			}

			backend, serverURL, err := dial(cmd, server)
			if err != nil {
				return err
			}

			if err := backend.SetSession(cmd.Context(), changes); err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			return writeSettingChanges(cmd, writtenSettingRows(defs, changes), jqExpr, asJSON)
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	registerSettingFlags(cmd)

	return cmd
}

// newServerResetCmd builds "server reset <key>...", which restores one or more
// settings to their Transmission defaults (baked into the registry, since the RPC
// has no reset primitive). Every key is validated up front — an unknown key errors
// with the valid-keys list and the read-only blocklist-size is rejected — so a bad
// key aborts before any network write. On success it writes every default in one
// backend.SetSession, re-fetches via GetSession, and reports the server's confirmed
// values using the same output shape as update (key: value on plaintext; the
// {key, category, value} array on --json/--jq), in registry order.
func newServerResetCmd() *cobra.Command {
	var (
		server string
		asJSON bool
		jqExpr string
	)

	cmd := &cobra.Command{
		Use:   "reset <key>...",
		Short: "Reset settings to their defaults",
		Long: "Restore one or more server (session) settings to their Transmission " +
			"defaults.\n\n" +
			"Every key is validated before anything is sent, so an unknown or " +
			"read-only key aborts with no partial change. Passing no key is an error, " +
			"not a no-op. After writing, the settings are re-read and the server's " +
			"confirmed values are reported.",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			defs, changes, err := collectSettingResets(args)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(cmd, server)
			if err != nil {
				return err
			}

			if err := backend.SetSession(cmd.Context(), changes); err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			session, err := backend.GetSession(cmd.Context())
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			return writeSettingChanges(cmd, settingRows(defs, session), jqExpr, asJSON)
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// collectSettingResets validates the requested keys and builds the ordered
// definitions to report plus the kebab-case change map holding each setting's
// baked-in default. Unknown and read-only keys error before any network call.
// Duplicate keys collapse, and the definitions follow registry order so output is
// stable regardless of argument order.
func collectSettingResets(keys []string) ([]transmission.SettingDef, map[string]any, error) {
	if len(keys) == 0 {
		return nil, nil, fmt.Errorf("nothing to reset: provide at least one setting key")
	}

	wanted := make(map[string]bool, len(keys))
	for _, key := range keys {
		def, ok := transmission.LookupSetting(key)
		if !ok {
			return nil, nil, fmt.Errorf("unknown setting %q: valid settings are %s", key, strings.Join(transmission.SettingKeys(), ", "))
		}

		if def.ReadOnly {
			return nil, nil, fmt.Errorf("setting %q is read-only", key)
		}

		wanted[def.Key] = true
	}

	var defs []transmission.SettingDef
	changes := make(map[string]any, len(wanted))

	for _, def := range transmission.SettingDefs() {
		if !wanted[def.Key] {
			continue
		}

		defs = append(defs, def)
		changes[def.Key] = def.Default
	}

	return defs, changes, nil
}

// writeSettingChanges renders the rows a write command wants to report, honoring
// the shared --jq → --json → plaintext precedence. Plaintext is one "key: value"
// line per row, with server-derived values sanitized; the JSON/jq paths emit the
// rows unsanitized, as the structured contract requires.
func writeSettingChanges(cmd *cobra.Command, rows []settingRow, jqExpr string, asJSON bool) error {
	if jqExpr != "" {
		return output.JQ(cmd.OutOrStdout(), rows, jqExpr)
	}

	if asJSON {
		return output.JSON(cmd.OutOrStdout(), rows)
	}

	out := cmd.OutOrStdout()
	for _, row := range rows {
		_, _ = fmt.Fprintf(out, "%s: %s\n", row.Key, settingValueCell(row.Key, row.Value))
	}

	return nil
}
