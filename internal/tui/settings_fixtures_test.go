package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// fixtureSession is a session snapshot covering every setting. download-dir
// carries an escape sequence, so the sanitize rule is exercised on the path a
// server tab draws.
func fixtureSession() transmission.SessionSettings {
	return transmission.SessionSettings{
		DownloadDir:             "/data\x1b[2Jdownloads",
		StartAddedTorrents:      true,
		RenamePartialFiles:      true,
		IncompleteDirEnabled:    false,
		IncompleteDir:           "",
		SeedRatioLimited:        true,
		SeedRatioLimit:          2.5,
		IdleSeedingLimitEnabled: false,
		IdleSeedingLimit:        30,
		CacheSizeMiB:            8,
		PeerPort:                31129,
		PeerPortRandomOnStart:   false,
		PortForwardingEnabled:   true,
		Encryption:              "preferred",
		PeerLimitGlobal:         240,
		PeerLimitPerTorrent:     60,
		PexEnabled:              true,
		DHTEnabled:              true,
		LPDEnabled:              false,
		UTPEnabled:              true,
		BlocklistEnabled:        false,
		BlocklistURL:            "http://list.example/blocklist",
		BlocklistSize:           1234,
		SpeedLimitDownEnabled:   false,
		SpeedLimitDown:          100,
		SpeedLimitUpEnabled:     false,
		SpeedLimitUp:            100,
		AltSpeedEnabled:         false,
		AltSpeedDown:            50,
		AltSpeedUp:              50,
		AltSpeedTimeEnabled:     false,
		AltSpeedTimeBegin:       540,
		AltSpeedTimeEnd:         1020,
		AltSpeedTimeDay:         127,
		DownloadQueueEnabled:    true,
		DownloadQueueSize:       5,
		SeedQueueEnabled:        false,
		SeedQueueSize:           10,
		QueueStalledEnabled:     true,
		QueueStalledMinutes:     30,
	}
}

// fakeConfig is an in-memory ClientConfig with the same validation rules the
// command layer applies to the three keys the window edits.
type fakeConfig struct {
	// entries is what Entries reports; Set rewrites the matching entry.
	entries []ClientEntry

	// sets records every successful Set, in order.
	sets []string

	// failKey makes Set fail for that key, after validation.
	failKey string
}

// newFakeConfig reports every key at its default.
func newFakeConfig() *fakeConfig {
	return &fakeConfig{entries: []ClientEntry{
		{Key: clientKeyNerdFont, Value: true, Source: "default"},
		{Key: clientKeyPollInterval, Value: "5s", Source: "default"},
		{Key: clientKeyTheme, Value: "default", Source: "default", Allowed: slices.Clone(builtinThemes)},
	}}
}

// Entries implements ClientConfig.
func (c *fakeConfig) Entries() ([]ClientEntry, error) {
	return slices.Clone(c.entries), nil
}

// Validate implements ClientConfig.
func (c *fakeConfig) Validate(key string, value any) error {
	switch key {
	case clientKeyTheme:
		if name, _ := value.(string); slices.Contains(builtinThemes, name) {
			return nil
		}

		return fmt.Errorf("%v is not a known theme", value)
	case clientKeyNerdFont:
		if _, ok := value.(bool); ok {
			return nil
		}

		return errors.New("expected a bool")
	case clientKeyPollInterval:
		raw, _ := value.(string)

		d, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("%q is not a duration", raw)
		}

		if d < time.Second || d > time.Hour {
			return fmt.Errorf("%q is out of range", raw)
		}

		return nil
	}

	return fmt.Errorf("unknown config key %q", key)
}

// Set implements ClientConfig.
func (c *fakeConfig) Set(key string, value any) error {
	if err := c.Validate(key, value); err != nil {
		return err
	}

	if key == c.failKey {
		return errors.New("disk full")
	}

	for i := range c.entries {
		if c.entries[i].Key == key {
			c.entries[i].Value = value
			c.entries[i].Source = "config"
		}
	}

	c.sets = append(c.sets, key)

	return nil
}

// settingsModel is a model in the settings window with a config store and a
// loaded session, sized to a terminal. The session is installed directly, which
// is all a rendering or key test needs; settings_data_test.go covers the round
// trip that installs it for real.
func settingsModel(w, h int) Model {
	deps := testDeps()
	deps.Config = newFakeConfig()

	return settingsModelWith(deps, w, h)
}

// settingsModelWith is settingsModel with the caller's dependencies.
func settingsModelWith(deps Deps, w, h int) Model {
	m := press(New(deps).applyTheme(true).resize(w, h), ",")

	m.settings.noServer = false
	m.settings = m.settings.loadSession(fixtureSession())

	return m.syncSettings()
}

// Tab indices, for tests that navigate by pressing ] rather than by name.
const (
	tabGeneral = iota
	tabPolling
	tabDownload
	tabNetwork
	tabBandwidth
	tabQueue
)

// gotoTab presses ] until the settings window is on the given tab.
func gotoTab(m Model, tab int) Model {
	for m.settings.tab != tab {
		m = press(m, "]")
	}

	return m
}

// networkTab moves a settings model to the Network tab.
func networkTab(m Model) Model {
	return gotoTab(m, tabNetwork)
}

// typeText types s into whatever has the keyboard, one key per rune.
func typeText(m Model, s string) Model {
	for _, r := range s {
		m = press(m, string(r))
	}

	return m
}

// editField replaces the cursor row's value through the text input: enter,
// clear, type, enter.
func editField(m Model, value string) Model {
	m = press(m, "enter", "ctrl+u")

	return press(typeText(m, value), "enter")
}
