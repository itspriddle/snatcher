package commands

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
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

// entryFor finds one key in a ClientConfigStore listing.
func entryFor(t *testing.T, entries []ClientEntry, key string) ClientEntry {
	t.Helper()

	for _, e := range entries {
		if e.Key == key {
			return e
		}
	}

	require.FailNowf(t, "missing entry", "no entry for %s", key)

	return ClientEntry{}
}

// TestClientConfigStoreEntriesReportSources checks each rung is reported, with
// the name of the flag or variable behind an override so the TUI can say why a
// field is read-only.
func TestClientConfigStoreEntriesReportSources(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"theme": "nord"}}`)
	t.Setenv(envNoNerdFont, "1")

	entries, err := ClientConfigStore(dialCmd(t, cfgPath)).Entries()
	require.NoError(t, err)

	theme := entryFor(t, entries, keyTheme)
	assert.Equal(t, ClientEntry{Key: keyTheme, Value: "nord", Source: sourceConfig, Allowed: clientThemes}, theme)

	nerd := entryFor(t, entries, keyNerdFont)
	assert.Equal(t, ClientEntry{Key: keyNerdFont, Value: false, Source: sourceEnv, SetBy: envNoNerdFont}, nerd)

	poll := entryFor(t, entries, keyPollInterval)
	assert.Equal(t, ClientEntry{Key: keyPollInterval, Value: "5s", Source: sourceDefault}, poll)
}

// TestClientConfigStoreEntriesReportTheFlag checks the top rung names the flag.
func TestClientConfigStoreEntriesReportTheFlag(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)

	cmd := dialCmd(t, cfgPath)
	require.NoError(t, cmd.PersistentFlags().Set(flagTheme, "dracula"))

	entries, err := ClientConfigStore(cmd).Entries()
	require.NoError(t, err)

	theme := entryFor(t, entries, keyTheme)
	assert.Equal(t, sourceFlag, theme.Source)
	assert.Equal(t, "--theme", theme.SetBy)
	assert.Equal(t, "dracula", theme.Value)
}

// TestClientConfigStoreSetValidatesBeforeWriting checks a rejected value leaves
// the file byte-for-byte as it was.
func TestClientConfigStoreSetValidatesBeforeWriting(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}, "client": {"theme": "nord"}}`)

	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	store := ClientConfigStore(dialCmd(t, cfgPath))

	require.Error(t, store.Validate(keyTheme, "neon"))
	require.Error(t, store.Set(keyTheme, "neon"))
	require.Error(t, store.Set(keyPollInterval, "2h"))
	require.Error(t, store.Set("client.nope", "x"))

	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	assert.Equal(t, string(before), string(after))
}

// TestClientConfigStoreSetWritesToTheConfigPath checks a write lands in the file
// named by --config, and that the next Entries reflects it.
func TestClientConfigStoreSetWritesToTheConfigPath(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)

	store := ClientConfigStore(dialCmd(t, cfgPath))

	require.NoError(t, store.Set(keyPollInterval, "30s"))
	require.NoError(t, store.Set(keyNerdFont, false))

	cfg, _, err := loadConfig(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, cfg.Client.PollInterval)
	require.NotNil(t, cfg.Client.NerdFont)
	assert.Equal(t, "30s", *cfg.Client.PollInterval)
	assert.False(t, *cfg.Client.NerdFont)

	entries, err := store.Entries()
	require.NoError(t, err)
	assert.Equal(t, sourceConfig, entryFor(t, entries, keyPollInterval).Source)
}

// TestDialerReportsUnconfigured checks that the dialer the TUI is handed fails
// with the CLI's own not-configured error. The launcher calls it before starting
// the program, so this is the message a user with an empty config sees on the
// terminal rather than inside an alt screen they cannot read.
func TestDialerReportsUnconfigured(t *testing.T) {
	cfgPath := writeConfigFile(t, `{"servers": {}}`)

	_, err := Dialer(dialCmd(t, cfgPath))("")
	require.Error(t, err)

	assert.Contains(t, err.Error(), "not configured")
}

// TestDialerResolvesConfiguredServer checks the happy path: the dialer returns a
// client, the resolved URL, and the alias, so the TUI can show which server it is
// on without re-resolving the precedence chain itself.
func TestDialerResolvesConfiguredServer(t *testing.T) {
	keyring.MockInit()

	cfgPath := writeConfigFile(t, `{
		"servers": {"home": {"url": "http://localhost:9091/transmission/rpc", "username": "user"}},
		"default_server": "home"
	}`)

	res, err := Dialer(dialCmd(t, cfgPath))("")
	require.NoError(t, err)

	assert.NotNil(t, res.Client)
	assert.Equal(t, "home", res.Name)
	assert.Equal(t, "http://localhost:9091/transmission/rpc", res.URL)
	assert.Empty(t, res.Warning)
}

// TestDialerCapturesTheStoreWarning checks the unavailable-store note a dial
// raises comes back as DialResult.Warning and never reaches stderr, which the
// TUI's alt screen owns, and that the command's error writer is restored.
func TestDialerCapturesTheStoreWarning(t *testing.T) {
	cfgPath := writeConfigFile(t, `{
		"servers": {"home": {"url": "http://localhost:9091/transmission/rpc", "username": "user"}},
		"default_server": "home"
	}`)

	mockStoreUnavailable(t)
	resetStoreUnavailableWarning(t)

	cmd := dialCmd(t, cfgPath)

	var stderr bytes.Buffer

	cmd.SetErr(&stderr)

	res, err := Dialer(cmd)("home")
	require.NoError(t, err)

	assert.Contains(t, res.Warning, "secret store could not be reached")
	assert.Empty(t, stderr.String())
	assert.Same(t, &stderr, cmd.ErrOrStderr())
}

// TestServersListsTransmissionAliases checks the picker's list: sorted,
// the default flagged, and Prowlarr servers left out.
func TestServersListsTransmissionAliases(t *testing.T) {
	cfgPath := writeConfigFile(t, `{
		"servers": {
			"seedbox": {"url": "http://sb.example:9091/transmission/rpc"},
			"home": {"url": "http://nas:9091/transmission/rpc"},
			"indexer": {"url": "http://prowlarr:9696", "type": "prowlarr"}
		},
		"default_server": "seedbox",
		"default_prowlarr_server": "indexer"
	}`)

	entries, err := Servers(dialCmd(t, cfgPath))()
	require.NoError(t, err)

	assert.Equal(t, []ServerEntry{
		{Name: "home", URL: "http://nas:9091/transmission/rpc"},
		{Name: "seedbox", URL: "http://sb.example:9091/transmission/rpc", Default: true},
	}, entries)
}

// TestServersUnconfiguredIsEmpty checks a missing config lists nothing rather
// than failing.
func TestServersUnconfiguredIsEmpty(t *testing.T) {
	entries, err := Servers(dialCmd(t, t.TempDir()+"/missing.json"))()
	require.NoError(t, err)

	assert.Empty(t, entries)
}

// TestProwlarrDialerResolvesTheNamedServer checks the Prowlarr seam: a named
// alias dials to a client with its alias and URL, and a Transmission alias is
// refused rather than handed an API key.
func TestProwlarrDialerResolvesTheNamedServer(t *testing.T) {
	cfgPath := writeConfigFile(t, `{
		"servers": {
			"home": {"url": "http://nas:9091/transmission/rpc"},
			"indexer": {"url": "http://prowlarr:9696", "type": "prowlarr"}
		},
		"default_server": "home",
		"default_prowlarr_server": "indexer"
	}`)
	t.Setenv(envAPIKey, "k")

	dial := ProwlarrDialer(dialCmd(t, cfgPath))

	res, err := dial("indexer")
	require.NoError(t, err)

	assert.NotNil(t, res.Client)
	assert.Equal(t, "indexer", res.Name)
	assert.Equal(t, "http://prowlarr:9696", res.URL)
	assert.Empty(t, res.Warning)

	_, err = dial("home")
	require.Error(t, err)
}

// TestProwlarrDialerCapturesTheStoreWarning checks the Prowlarr dial keeps a
// store warning off stderr, and restores the error writer, as Dialer does.
func TestProwlarrDialerCapturesTheStoreWarning(t *testing.T) {
	cfgPath := writeConfigFile(t, `{
		"servers": {"indexer": {"url": "http://prowlarr:9696", "type": "prowlarr"}},
		"default_prowlarr_server": "indexer"
	}`)

	mockStoreUnavailable(t)
	resetStoreUnavailableWarning(t)

	cmd := dialCmd(t, cfgPath)

	var stderr bytes.Buffer

	cmd.SetErr(&stderr)

	res, err := ProwlarrDialer(cmd)("indexer")
	require.NoError(t, err)

	assert.Contains(t, res.Warning, "secret store could not be reached")
	assert.Empty(t, stderr.String())
	assert.Same(t, &stderr, cmd.ErrOrStderr())
}

// TestProwlarrServersListsProwlarrAliases checks the Prowlarr list: sorted, the
// Prowlarr default flagged, and Transmission servers left out.
func TestProwlarrServersListsProwlarrAliases(t *testing.T) {
	cfgPath := writeConfigFile(t, `{
		"servers": {
			"seedbox": {"url": "http://sb.example:9091/transmission/rpc"},
			"public": {"url": "http://p2:9696", "type": "prowlarr"},
			"indexer": {"url": "http://prowlarr:9696", "type": "prowlarr"}
		},
		"default_server": "seedbox",
		"default_prowlarr_server": "public"
	}`)

	entries, err := ProwlarrServers(dialCmd(t, cfgPath))()
	require.NoError(t, err)

	assert.Equal(t, []ServerEntry{
		{Name: "indexer", URL: "http://prowlarr:9696"},
		{Name: "public", URL: "http://p2:9696", Default: true},
	}, entries)
}

// TestProwlarrServersUnconfiguredIsEmpty checks a missing config lists nothing.
func TestProwlarrServersUnconfiguredIsEmpty(t *testing.T) {
	entries, err := ProwlarrServers(dialCmd(t, t.TempDir()+"/missing.json"))()
	require.NoError(t, err)

	assert.Empty(t, entries)
}
