package commands

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// The tree-wide flag names. Every reader goes through a helper rather than
// spelling a name again, so each flag is registered and read from exactly one
// place.
const (
	// flagConfig selects the config file every command reads or writes.
	flagConfig = "config"

	// flagTheme and flagNoNerdFont override, for one invocation, the two client
	// settings that decide how snatcher draws itself. Nothing renders either one
	// yet: they resolve and validate so the forthcoming TUI consumes a settled
	// contract rather than inventing one.
	//
	// Both are checked where they are read — resolveClientSetting validates each
	// rung as it resolves it — and nowhere else. Checking them ahead of every
	// command instead would let a stale SNATCHER_THEME in a shell profile fail
	// the whole CLI, including the "config" commands that repair it.
	flagTheme      = "theme"
	flagNoNerdFont = "no-nerd-font"
)

// The environment companions of the two appearance flags, the rung between the
// command line and the config file. They are declared here, beside the flags
// they stand in for, so a variable and its flag cannot drift apart.
//
// SNATCHER_NO_NERD_FONT is negative, like its flag and unlike the positive
// config key client.nerd_font: a truthy value turns Nerd Font glyphs off. An
// unset or empty value expresses no opinion and falls through to the config
// file.
const (
	envTheme      = "SNATCHER_THEME"
	envNoNerdFont = "SNATCHER_NO_NERD_FONT"
)

// AddGlobalFlags registers snatcher's tree-wide flags on cmd as persistent
// flags, so every subcommand at every depth inherits them. It is called once,
// on the root command, by internal/cli.
//
// A flag belongs here when it is meaningful everywhere and its wording must not
// drift: --config selects the file every command reads or writes, and --theme
// and --no-nerd-font override how snatcher draws itself for one invocation.
// Flags that only some commands accept (--server, --json, --jq) stay local to
// those commands, so they never appear in help for a command that ignores them.
//
// Tests that build a command standalone, with no root attached, call this on
// that command to get the same flags the production tree has.
func AddGlobalFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String(flagConfig, "", "config file path")
	cmd.PersistentFlags().String(flagTheme, "", fmt.Sprintf("color theme (%s)", strings.Join(clientThemes, ", ")))
	cmd.PersistentFlags().Bool(flagNoNerdFont, false, "use ASCII icons instead of Nerd Font glyphs")
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

// clientOverride is a client setting supplied from above the config file, paired
// with the rung that supplied it. A nil *clientOverride means neither rung had
// an opinion, leaving the config file — or the default — to settle the key.
type clientOverride struct {
	value  any
	source string
}

// overrideFromFlagOrEnv applies the two rungs above the config file in order —
// the command line, then the environment — and returns whichever spoke first,
// converted by parse. Nothing is returned when neither did.
//
// Presence on the command line is the flag's Changed state, never a comparison
// against its zero value, so an explicitly passed "--theme default" is an
// override reported as coming from the flag rather than mistaken for an unset
// one. The lookup climbs the command tree (see configFlag), and a flag
// registered nowhere simply has no opinion, so a command built standalone in a
// test does not panic.
//
// An empty or whitespace-only environment variable expresses no opinion and
// falls through, the same reading SNATCHER_SECRET_STORE gets. A value that does
// not parse is an error naming its rung: a typo is never silently ignored, at
// any level.
func overrideFromFlagOrEnv(cmd *cobra.Command, flagName, envName string, parse func(raw string) (any, error)) (*clientOverride, error) {
	if f := cmd.Flag(flagName); f != nil && f.Changed {
		value, err := parse(f.Value.String())
		if err != nil {
			return nil, fmt.Errorf("invalid --%s: %w", flagName, err)
		}

		return &clientOverride{value: value, source: sourceFlag}, nil
	}

	raw := os.Getenv(envName)
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	value, err := parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envName, err)
	}

	return &clientOverride{value: value, source: sourceEnv}, nil
}

// themeOverride resolves client.theme from --theme and SNATCHER_THEME. The name
// runs through the same validator a stored theme faces, so all three rungs
// accept exactly the same set — a theme snatcher would reject in the file is not
// quietly usable from the command line.
func themeOverride(cmd *cobra.Command) (*clientOverride, error) {
	return overrideFromFlagOrEnv(cmd, flagTheme, envTheme, func(raw string) (any, error) {
		name := strings.TrimSpace(raw)
		if err := validateTheme(name); err != nil {
			return nil, err
		}

		return name, nil
	})
}

// nerdFontOverride resolves client.nerd_font from --no-nerd-font and
// SNATCHER_NO_NERD_FONT.
//
// Both are negative while the config key is positive, so the answer is inverted
// here, in one place: --no-nerd-font, or a truthy variable, means nerd_font
// false. pflag also accepts --no-nerd-font=false, which is an explicit request
// for glyphs and inverts to true rather than meaning "no opinion" — only an
// absent flag and an empty variable mean that.
func nerdFontOverride(cmd *cobra.Command) (*clientOverride, error) {
	return overrideFromFlagOrEnv(cmd, flagNoNerdFont, envNoNerdFont, func(raw string) (any, error) {
		off, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("%q is not a boolean: use true or false", raw)
		}

		return !off, nil
	})
}
