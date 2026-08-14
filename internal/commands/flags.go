package commands

import "github.com/spf13/cobra"

// flagConfig names the persistent --config flag. Every reader goes through
// configFlag rather than spelling the name again, so the flag is registered and
// read from exactly one place.
const flagConfig = "config"

// AddGlobalFlags registers snatcher's tree-wide flags on cmd as persistent
// flags, so every subcommand at every depth inherits them. It is called once,
// on the root command, by internal/cli.
//
// A flag belongs here when it is meaningful everywhere and its wording must not
// drift: --config selects the file every command reads or writes. Flags that
// only some commands accept (--server, --json, --jq) stay local to those
// commands, so they never appear in help for a command that ignores them.
//
// Tests that build a command standalone, with no root attached, call this on
// that command to get the same flags the production tree has.
func AddGlobalFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String(flagConfig, "", "config file path")
}

// configFlag returns the --config value in force for cmd, or the empty string
// meaning "the default path".
//
// The lookup is cmd.Flag, which climbs the command tree, so an inherited
// persistent --config resolves from any depth whether or not cobra has merged
// the parent's flag set into this command's yet. A command with no --config
// registered anywhere above it — one constructed standalone in a test — yields
// the empty string rather than panicking, which is the same answer an unset flag
// gives.
func configFlag(cmd *cobra.Command) string {
	f := cmd.Flag(flagConfig)
	if f == nil {
		return ""
	}

	return f.Value.String()
}
