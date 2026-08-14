package commands

import (
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAddGlobalFlagsRegistersConfigPersistently pins that --config lands in the
// persistent set, not the local one: that is what makes one declaration on root
// reach every subcommand at every depth.
func TestAddGlobalFlagsRegistersConfigPersistently(t *testing.T) {
	root := &cobra.Command{Use: "snatcher"}
	AddGlobalFlags(root)

	require.NotNil(t, root.PersistentFlags().Lookup(flagConfig), "--config must be persistent")
	assert.Nil(t, root.Flags().Lookup(flagConfig), "--config must not also be a local flag")
}

// TestConfigFlagIsEmptyWithoutTheFlag covers a command built standalone, with no
// root above it — every test in this package does exactly that. Reading a flag
// nothing registered must answer "the default path", the same as an unset flag,
// rather than panicking.
func TestConfigFlagIsEmptyWithoutTheFlag(t *testing.T) {
	assert.Empty(t, configFlag(&cobra.Command{Use: "orphan"}))
}

// TestConfigFlagReadsAnInheritedValue is the promotion's real contract: a leaf
// command declares no --config of its own and still sees the one the root
// carries, from any depth.
func TestConfigFlagReadsAnInheritedValue(t *testing.T) {
	var got string

	leaf := &cobra.Command{
		Use:  "list",
		RunE: func(cmd *cobra.Command, _ []string) error { got = configFlag(cmd); return nil },
	}

	group := &cobra.Command{Use: "torrent"}
	group.AddCommand(leaf)

	root := &cobra.Command{Use: "snatcher"}
	AddGlobalFlags(root)
	root.AddCommand(group)

	// Before the subcommand, where a global flag belongs.
	root.SetArgs([]string{"--config", "/tmp/global.json", "torrent", "list"})
	require.NoError(t, root.Execute())
	assert.Equal(t, "/tmp/global.json", got)

	// And after it, which is where every existing invocation puts it.
	root.SetArgs([]string{"torrent", "list", "--config", "/tmp/trailing.json"})
	require.NoError(t, root.Execute())
	assert.Equal(t, "/tmp/trailing.json", got)
}

// TestAddGlobalFlagsRegistersAppearanceFlagsPersistently pins the two
// appearance flags alongside --config: they are global settings, so they belong
// on the root and reach every subcommand, not on the handful of commands that
// happen to read them today.
func TestAddGlobalFlagsRegistersAppearanceFlagsPersistently(t *testing.T) {
	root := &cobra.Command{Use: "snatcher"}
	AddGlobalFlags(root)

	for _, name := range []string{flagTheme, flagNoNerdFont} {
		require.NotNil(t, root.PersistentFlags().Lookup(name), "--%s must be persistent", name)
		assert.Nil(t, root.Flags().Lookup(name), "--%s must not also be a local flag", name)
	}

	// The usage text names the accepted themes, so --help answers what --theme
	// takes without a trip to the man page.
	assert.Equal(t, "color theme (default, dracula, nord)", root.PersistentFlags().Lookup(flagTheme).Usage)
}

// globalFlagCmd builds a standalone command carrying the tree-wide flags and
// parses args against it, so a test can exercise a flag's Changed state — the
// only thing that distinguishes "passed explicitly" from "left alone" — without
// standing up a command tree.
func globalFlagCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()

	cmd := &cobra.Command{
		Use:  "snatcher",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	AddGlobalFlags(cmd)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	require.NoError(t, cmd.Execute())

	return cmd
}

// TestThemeOverrideRungs covers the two rungs above the config file for
// client.theme, including the one that is easy to get wrong: an explicitly
// passed --theme default is an override reported as coming from the flag, not a
// flag mistaken for unset because its value equals the built-in default.
func TestThemeOverrideRungs(t *testing.T) {
	tests := []struct {
		name       string
		env        string
		args       []string
		wantValue  any
		wantSource string
	}{
		{name: "nothing set"},
		{name: "env alone", env: "dracula", wantValue: "dracula", wantSource: sourceEnv},
		{name: "env is trimmed", env: "  nord  ", wantValue: "nord", wantSource: sourceEnv},
		{name: "empty env has no opinion", env: "   "},
		{name: "flag alone", args: []string{"--theme", "nord"}, wantValue: "nord", wantSource: sourceFlag},
		{name: "flag outranks env", env: "dracula", args: []string{"--theme", "nord"}, wantValue: "nord", wantSource: sourceFlag},
		{
			name:       "flag passed the default value",
			env:        "dracula",
			args:       []string{"--theme", "default"},
			wantValue:  "default",
			wantSource: sourceFlag,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envTheme, tt.env)

			got, err := themeOverride(globalFlagCmd(t, tt.args...))
			require.NoError(t, err)

			if tt.wantSource == "" {
				assert.Nil(t, got, "an unset rung must express no opinion")
				return
			}

			require.NotNil(t, got)
			assert.Equal(t, tt.wantValue, got.value)
			assert.Equal(t, tt.wantSource, got.source)
		})
	}
}

// TestNerdFontOverrideRungs covers the negation: the flag and the variable are
// negative ("no nerd font"), the config key is positive, and the inversion
// happens in exactly one place.
func TestNerdFontOverrideRungs(t *testing.T) {
	tests := []struct {
		name       string
		env        string
		args       []string
		wantValue  any
		wantSource string
	}{
		{name: "nothing set"},
		{name: "truthy env turns glyphs off", env: "1", wantValue: false, wantSource: sourceEnv},
		{name: "true env turns glyphs off", env: "true", wantValue: false, wantSource: sourceEnv},
		{name: "falsy env turns glyphs on", env: "false", wantValue: true, wantSource: sourceEnv},
		{name: "empty env has no opinion", env: "  "},
		{name: "flag turns glyphs off", args: []string{"--no-nerd-font"}, wantValue: false, wantSource: sourceFlag},
		{
			name:       "negated flag turns glyphs on",
			args:       []string{"--no-nerd-font=false"},
			wantValue:  true,
			wantSource: sourceFlag,
		},
		{
			name:       "flag outranks env",
			env:        "false",
			args:       []string{"--no-nerd-font"},
			wantValue:  false,
			wantSource: sourceFlag,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envNoNerdFont, tt.env)

			got, err := nerdFontOverride(globalFlagCmd(t, tt.args...))
			require.NoError(t, err)

			if tt.wantSource == "" {
				assert.Nil(t, got, "an unset rung must express no opinion")
				return
			}

			require.NotNil(t, got)
			assert.Equal(t, tt.wantValue, got.value)
			assert.Equal(t, tt.wantSource, got.source)
		})
	}
}

// TestOverridesRejectInvalidValues pins that a bad value at either rung is an
// error naming that rung and what is accepted — never a silent fall through to
// the config file, which would leave an operator wondering why their override
// did nothing.
func TestOverridesRejectInvalidValues(t *testing.T) {
	tests := []struct {
		name     string
		envName  string
		envValue string
		args     []string
		wantErr  []string
	}{
		{
			name:    "flag theme",
			args:    []string{"--theme", "solarized"},
			wantErr: []string{"--theme", `"solarized"`, "not a known theme", "dracula"},
		},
		{
			name:     "env theme",
			envName:  envTheme,
			envValue: "solarized",
			wantErr:  []string{envTheme, `"solarized"`, "not a known theme", "dracula"},
		},
		{
			name:     "env nerd font",
			envName:  envNoNerdFont,
			envValue: "yes-please",
			wantErr:  []string{envNoNerdFont, `"yes-please"`, "not a boolean", "true or false"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envName != "" {
				t.Setenv(tt.envName, tt.envValue)
			}

			err := ValidateGlobalSettings(globalFlagCmd(t, tt.args...))
			require.Error(t, err)

			for _, want := range tt.wantErr {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

// TestValidateGlobalSettingsAcceptsAnUnconfiguredInvocation checks the ordinary
// case — no flags, no variables — costs nothing, since the hook runs ahead of
// every command.
func TestValidateGlobalSettingsAcceptsAnUnconfiguredInvocation(t *testing.T) {
	assert.NoError(t, ValidateGlobalSettings(globalFlagCmd(t)))
}

// TestValidateGlobalSettingsIgnoresUnregisteredFlags covers a command built
// standalone, with none of the tree-wide flags on it: reading a flag nothing
// registered must be "no opinion" rather than a panic, the same answer
// configFlag gives.
func TestValidateGlobalSettingsIgnoresUnregisteredFlags(t *testing.T) {
	assert.NoError(t, ValidateGlobalSettings(&cobra.Command{Use: "orphan"}))
}
