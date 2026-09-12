package commands

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveClientSettingsDefaults checks the settings a first run gets: no
// client section in the file at all still yields the registry defaults, so the
// TUI launches against a config written before it existed.
func TestResolveClientSettingsDefaults(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)

	settings, err := ResolveClientSettings(dialCmd(t, cfgPath))
	require.NoError(t, err)

	assert.Equal(t, ClientSettings{Theme: "default", NerdFont: true, PollInterval: 5 * time.Second}, settings)
}

// TestResolveClientSettingsReadsFile checks stored values win over the defaults.
func TestResolveClientSettingsReadsFile(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"theme": "nord", "nerd_font": false, "poll_interval": "30s"}}`)

	settings, err := ResolveClientSettings(dialCmd(t, cfgPath))
	require.NoError(t, err)

	assert.Equal(t, ClientSettings{Theme: "nord", NerdFont: false, PollInterval: 30 * time.Second}, settings)
}

// TestResolveClientSettingsEnvOverridesFile checks the environment rung, which
// is the one a user is most likely to have exported in a shell profile.
func TestResolveClientSettingsEnvOverridesFile(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"theme": "nord"}}`)
	t.Setenv(envTheme, "dracula")

	settings, err := ResolveClientSettings(dialCmd(t, cfgPath))
	require.NoError(t, err)

	assert.Equal(t, "dracula", settings.Theme)
}

// TestResolveClientSettingsFlagOverridesEnv checks the top rung, and that it
// beats an exported variable rather than merely beating the file.
func TestResolveClientSettingsFlagOverridesEnv(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"theme": "nord"}}`)
	t.Setenv(envTheme, "dracula")

	cmd := dialCmd(t, cfgPath)
	require.NoError(t, cmd.PersistentFlags().Set(flagTheme, "default"))

	settings, err := ResolveClientSettings(cmd)
	require.NoError(t, err)

	assert.Equal(t, "default", settings.Theme)
}

// TestResolveClientSettingsNoNerdFontFlag checks the boolean flag rung, which
// has no value to parse and so resolves differently from --theme.
func TestResolveClientSettingsNoNerdFontFlag(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)

	cmd := dialCmd(t, cfgPath)
	require.NoError(t, cmd.PersistentFlags().Set(flagNoNerdFont, "true"))

	settings, err := ResolveClientSettings(cmd)
	require.NoError(t, err)

	assert.False(t, settings.NerdFont)
}

// TestResolveClientSettingsRejectsInvalidStoredValue checks that a hand-edited
// nonsense value fails naming the config key, so an operator knows to repair the
// file — and that it fails here, before the TUI has entered the alt screen.
func TestResolveClientSettingsRejectsInvalidStoredValue(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"theme": "neon"}}`)

	_, err := ResolveClientSettings(dialCmd(t, cfgPath))
	require.Error(t, err)

	assert.Contains(t, err.Error(), keyTheme)
}

// TestResolveClientSettingsRejectsInvalidPollInterval covers the one key the
// resolver parses itself rather than merely reading.
func TestResolveClientSettingsRejectsInvalidPollInterval(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"poll_interval": "soon"}}`)

	_, err := ResolveClientSettings(dialCmd(t, cfgPath))
	require.Error(t, err)

	assert.Contains(t, err.Error(), keyPollInterval)
}

// TestResolveClientSettingsReportsNoColor checks the TUI is told about NO_COLOR
// with the same predicate the plaintext output paths use — non-empty, per
// no-color.org, and not colorprofile's strconv.ParseBool, which would leave
// NO_COLOR=yes disabling color in "snatcher list" and not in the TUI.
func TestResolveClientSettingsReportsNoColor(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)

	settings, err := ResolveClientSettings(dialCmd(t, cfgPath))
	require.NoError(t, err)
	assert.False(t, settings.NoColor)

	for _, value := range []string{"1", "yes", "0", "false"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envNoColor, value)

			settings, err := ResolveClientSettings(dialCmd(t, cfgPath))
			require.NoError(t, err)

			assert.True(t, settings.NoColor, "any non-empty value sets NO_COLOR")
		})
	}
}

// TestDialerReportsUnconfigured checks that the dialer the TUI is handed fails
// with the CLI's own not-configured error. The launcher calls it before starting
// the program, so this is the message a user with an empty config sees on the
// terminal rather than inside an alt screen they cannot read.
func TestDialerReportsUnconfigured(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)

	_, _, err := Dialer(dialCmd(t, cfgPath))("")
	require.Error(t, err)

	assert.Contains(t, err.Error(), "not configured")
}

// TestDialerResolvesConfiguredServer checks the happy path: the dialer returns a
// client and the resolved URL, so the TUI can show which server it is on without
// re-resolving the precedence chain itself.
func TestDialerResolvesConfiguredServer(t *testing.T) {
	cfgPath := writeConfigFile(t, `{
		"servers": {"home": {"url": "http://localhost:9091/transmission/rpc", "username": "user"}},
		"default_server": "home"
	}`)

	client, url, err := Dialer(dialCmd(t, cfgPath))("")
	require.NoError(t, err)

	assert.NotNil(t, client)
	assert.Equal(t, "http://localhost:9091/transmission/rpc", url)
}
