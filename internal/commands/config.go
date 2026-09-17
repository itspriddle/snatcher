package commands

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/config"
	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/theme"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// The sources a client setting's effective value can come from, reported by
// "config list" and "config get" so four rungs of precedence are visible
// without opening the file. They are listed here in precedence order: flag >
// env > config > default.
const (
	// sourceFlag means a command-line flag supplied the value for this
	// invocation, outranking everything below it.
	sourceFlag = "flag"

	// sourceEnv means an environment variable supplied it, outranking the file.
	sourceEnv = "env"

	// sourceConfig means the value was read from the client section of
	// config.json.
	sourceConfig = "config"

	// sourceDefault means no value is stored and the registry default applies.
	sourceDefault = "default"
)

// The dotted CLI keys, spelled once here so a command that consumes a setting
// cannot drift from the registry entry that defines it. The registry below
// remains the source of truth for what each key means; these are only its
// addresses.
const (
	keyTheme        = "client.theme"
	keyNerdFont     = "client.nerd_font"
	keyColorDepth   = "client.color_depth"
	keyPollInterval = "client.poll_interval"
	keyListColumns  = "client.list_columns"
	keyListSort     = "client.list_sort"
	keyListReverse  = "client.list_reverse"
	keyMouse        = "client.mouse"
)

// colorDepths lists every valid client.color_depth value. auto keeps the
// profile the terminal is detected as; the rest override it (see
// tui.effectiveProfile, which spells the same four under a pinning test).
var colorDepths = []string{"auto", "truecolor", "256", "16"}

// themesDirName is the directory beside the config file that holds the
// operator's own .theme files.
const themesDirName = "themes"

const (
	// minPollInterval and maxPollInterval bound client.poll_interval. The floor
	// keeps a hand-edit from hammering a server; the ceiling keeps a "paused"
	// poll loop from looking like a hung one.
	minPollInterval = time.Second
	maxPollInterval = time.Hour

	// pollIntervalRange is how those bounds are worded in a rejection.
	// time.Hour.String() renders "1h0m0s", which is not what an operator types.
	pollIntervalRange = "1s and 1h"
)

// clientKeyDef describes one client setting: the dotted CLI key that addresses
// it, the default that applies when nothing is stored, the accessor that reads
// its stored value out of the config struct, and the validator that decides
// whether a stored (or, later, a supplied) value means anything.
//
// This registry lives in internal/commands rather than internal/config on
// purpose: two of the validators reach into the command layer (list_columns
// validates against listFields, list_sort against transmission.ParseSortField),
// and the architecture already puts "valid values and their precedence" here.
// internal/config stays schema and persistence, knowing nothing about meaning.
//
// Values are formatted for plaintext by their natural Go type
// (formatClientValue), shared by every key rather than named per entry.
type clientKeyDef struct {
	// key is the dotted CLI key, e.g. "client.theme". The "client." prefix is
	// deliberate: it leaves room for a future section without renaming these.
	key string

	// def is the default value, in the same natural type the accessor yields.
	// Every default reproduces today's behavior exactly, so an operator with no
	// client section sees no change.
	def any

	// accessor returns the stored value and whether it is set at all. An unset
	// key is distinct from one explicitly set to its zero value.
	accessor func(config.Client) (any, bool)

	// apply writes value into the client section, or clears the key when value
	// is nil. The nil convention mirrors the storage exactly — every client field
	// is a pointer whose nil means "unset" — so "config set" and "config unset"
	// share one write path rather than two that can drift.
	apply func(*config.Client, any) error

	// validate reports whether a value means anything for this key. It is never
	// nil — a key whose JSON type is its only constraint names alwaysValid, so
	// that "nothing to check here" is a decision on the record rather than an
	// omission.
	//
	// configPath is the --config value in force (empty for the default path).
	// Only client.theme reads it: the valid themes include the files in the
	// themes directory beside that config file.
	validate func(configPath string, value any) error

	// override resolves the key from the two rungs above the config file — a
	// command-line flag, then an environment variable — for the invocation cmd
	// belongs to. It yields nothing when neither rung has an opinion, leaving
	// the file (or the default) to settle the key.
	//
	// A nil override means the key has no rungs above the file at all, which is
	// true of every key but the three the TUI's appearance depends on: a setting
	// worth a global flag is one worth overriding for a single invocation, and
	// the rest are changed with "config set".
	override func(*cobra.Command) (*clientOverride, error)
}

// clientKeyRegistry is the single source of truth for every client setting, in
// the order documented in the PRD's key table (pinned by
// TestClientKeyRegistryMatchesDocumentedKeys). Listings sort by key; see
// sortedClientKeyDefs.
var clientKeyRegistry = []clientKeyDef{
	{
		key:      keyTheme,
		def:      "default",
		accessor: func(c config.Client) (any, bool) { return clientValue(c.Theme) },
		apply:    func(c *config.Client, v any) error { return applyClientValue(&c.Theme, v) },
		validate: validateTheme,
		override: themeOverride,
	},
	{
		key:      keyNerdFont,
		def:      true,
		accessor: func(c config.Client) (any, bool) { return clientValue(c.NerdFont) },
		apply:    func(c *config.Client, v any) error { return applyClientValue(&c.NerdFont, v) },
		validate: alwaysValid,
		override: nerdFontOverride,
	},
	{
		key:      keyColorDepth,
		def:      "auto",
		accessor: func(c config.Client) (any, bool) { return clientValue(c.ColorDepth) },
		apply:    func(c *config.Client, v any) error { return applyClientValue(&c.ColorDepth, v) },
		validate: validateColorDepth,
		override: colorDepthOverride,
	},
	{
		key:      keyPollInterval,
		def:      "5s",
		accessor: func(c config.Client) (any, bool) { return clientValue(c.PollInterval) },
		apply:    func(c *config.Client, v any) error { return applyClientValue(&c.PollInterval, v) },
		validate: validatePollInterval,
	},
	{
		key:      keyListColumns,
		def:      defaultFieldKeys,
		accessor: func(c config.Client) (any, bool) { return clientValue(c.ListColumns) },
		apply:    func(c *config.Client, v any) error { return applyClientValue(&c.ListColumns, v) },
		validate: validateListColumns,
	},
	{
		key:      keyListSort,
		def:      string(transmission.SortQueue),
		accessor: func(c config.Client) (any, bool) { return clientValue(c.ListSort) },
		apply:    func(c *config.Client, v any) error { return applyClientValue(&c.ListSort, v) },
		validate: validateListSort,
	},
	{
		key:      keyListReverse,
		def:      false,
		accessor: func(c config.Client) (any, bool) { return clientValue(c.ListReverse) },
		apply:    func(c *config.Client, v any) error { return applyClientValue(&c.ListReverse, v) },
		validate: alwaysValid,
	},
	{
		key:      keyMouse,
		def:      true,
		accessor: func(c config.Client) (any, bool) { return clientValue(c.Mouse) },
		apply:    func(c *config.Client, v any) error { return applyClientValue(&c.Mouse, v) },
		validate: alwaysValid,
	},
}

// sortedClientKeyDefs is the registry ordered by key. Every listing walks it, so
// the plaintext table and the JSON array agree on order, and the JSON array is
// sorted by key as its contract promises.
var sortedClientKeyDefs = func() []clientKeyDef {
	defs := slices.Clone(clientKeyRegistry)
	slices.SortFunc(defs, func(a, b clientKeyDef) int { return strings.Compare(a.key, b.key) })

	return defs
}()

// clientKeysByKey indexes the registry for O(1) lookup, built once from
// clientKeyRegistry, the source of truth.
var clientKeysByKey = func() map[string]clientKeyDef {
	m := make(map[string]clientKeyDef, len(clientKeyRegistry))
	for _, def := range clientKeyRegistry {
		m[def.key] = def
	}

	return m
}()

// clientValue adapts a Client's optional pointer field to the accessor
// signature: the dereferenced value and whether the key is set at all.
func clientValue[T any](p *T) (any, bool) {
	if p == nil {
		return nil, false
	}

	return *p, true
}

// applyClientValue is the inverse of clientValue: it points field at a copy of
// value, or clears it when value is nil. Clearing writes the key out of the file
// entirely (see config.Client's MarshalJSON), which is what makes an unset key
// revert to its default rather than persist as a null.
func applyClientValue[T any](field **T, value any) error {
	if value == nil {
		*field = nil

		return nil
	}

	typed, err := typedClientValue[T](value)
	if err != nil {
		return err
	}
	*field = &typed

	return nil
}

// allClientKeys returns every config key sorted, for the valid-keys list in
// error messages.
func allClientKeys() []string {
	keys := make([]string, len(sortedClientKeyDefs))
	for i, def := range sortedClientKeyDefs {
		keys[i] = def.key
	}

	return keys
}

// lookupClientKey resolves a dotted key to its registry entry, matching
// case-insensitively after trimming. An unknown key is an error naming it and
// every valid key.
func lookupClientKey(key string) (clientKeyDef, error) {
	def, ok := clientKeysByKey[strings.ToLower(strings.TrimSpace(key))]
	if !ok {
		return clientKeyDef{}, fmt.Errorf("unknown config key %q: valid keys are %s", key, strings.Join(allClientKeys(), ", "))
	}

	return def, nil
}

// typedClientValue asserts a value's Go type before a validator inspects it. A
// mismatch is unreachable through config.Load — a JSON value whose type does not
// match its field is a hard Load error — so this catches only a registry mistake,
// and reports it instead of panicking in a validator.
func typedClientValue[T any](value any) (T, error) {
	typed, ok := value.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("expected %T, got %T", zero, value)
	}

	return typed, nil
}

// alwaysValid is the validator for a key whose JSON type is its only constraint
// — the two bools, where Load has already rejected everything that is not one.
// It is named explicitly rather than left nil so the registry cannot grow a key
// that silently skips validation.
func alwaysValid(string, any) error { return nil }

// validateTheme accepts a builtin theme or a theme file in the themes directory
// beside the config file at configPath. Names match exactly, as the file
// system's stems do. A selected theme file that does not parse is its parse
// error, naming the file and line, rather than "not a known theme".
func validateTheme(configPath string, value any) error {
	name, err := typedClientValue[string](value)
	if err != nil {
		return err
	}

	reg, err := themeRegistry(configPath)
	if err != nil {
		return err
	}

	_, err = reg.Lookup(name)
	if errors.Is(err, theme.ErrUnknown) {
		return fmt.Errorf("%q is not a known theme: valid themes are %s", name, strings.Join(reg.Names(), ", "))
	}

	return err
}

// validateColorDepth accepts one of the colorDepths, exactly.
func validateColorDepth(_ string, value any) error {
	depth, err := typedClientValue[string](value)
	if err != nil {
		return err
	}

	if slices.Contains(colorDepths, depth) {
		return nil
	}

	return fmt.Errorf("%q is not a color depth: valid depths are %s", depth, strings.Join(colorDepths, ", "))
}

// themesDir is the directory the operator's theme files live in: themes/ beside
// the config file, so --config moves it along with the file.
func themesDir(configPath string) (string, error) {
	path, err := resolveConfigPath(configPath)
	if err != nil {
		return "", err
	}

	return filepath.Join(filepath.Dir(path), themesDirName), nil
}

// themeRegistry loads the builtin themes and the theme files beside the config
// file at configPath. It is read fresh on every call, so a file added
// mid-session is seen by the next lookup.
func themeRegistry(configPath string) (theme.Registry, error) {
	dir, err := themesDir(configPath)
	if err != nil {
		return theme.Registry{}, err
	}

	return theme.Load(dir)
}

// validatePollInterval accepts Go duration syntax within the documented bounds.
func validatePollInterval(_ string, value any) error {
	raw, err := typedClientValue[string](value)
	if err != nil {
		return err
	}

	d, parseErr := time.ParseDuration(strings.TrimSpace(raw))
	if parseErr != nil {
		return fmt.Errorf("%q is not a duration: use Go duration syntax, e.g. 5s or 1m30s", raw)
	}

	if d < minPollInterval || d > maxPollInterval {
		return fmt.Errorf("%q is out of range: the poll interval must be between %s", raw, pollIntervalRange)
	}

	return nil
}

// validateListColumns accepts a non-empty list of distinct list columns. Keys
// are matched case-insensitively after trimming, exactly as --fields matches
// them, so a config value and a flag value accept the same spellings.
func validateListColumns(_ string, value any) error {
	columns, err := typedClientValue[[]string](value)
	if err != nil {
		return err
	}

	if len(columns) == 0 {
		return fmt.Errorf("no columns selected: valid columns are %s", validFieldKeys(listFields))
	}

	seen := make(map[string]bool, len(columns))
	for _, raw := range columns {
		key := strings.ToLower(strings.TrimSpace(raw))
		if _, ok := listFields[key]; !ok {
			return fmt.Errorf("%q is not a column: valid columns are %s", raw, validFieldKeys(listFields))
		}
		if seen[key] {
			return fmt.Errorf("column %q is listed more than once", raw)
		}
		seen[key] = true
	}

	return nil
}

// validateListSort accepts any sort field the list command accepts. The error
// comes from transmission.ParseSortField, which already names the value and the
// valid set.
func validateListSort(_ string, value any) error {
	name, err := typedClientValue[string](value)
	if err != nil {
		return err
	}

	if _, parseErr := transmission.ParseSortField(name); parseErr != nil {
		return parseErr
	}

	return nil
}

// validateClientValue checks a value against its key's validator, naming the key
// and the config file so a rejection points at what to fix. A stored value that
// fails is never quietly swapped for the default: an operator who typed a theme
// wrong should hear about it, not wonder why their setting does nothing.
func validateClientValue(def clientKeyDef, configPath string, value any) error {
	if err := def.validate(configPath, value); err != nil {
		return fmt.Errorf("invalid config value for %s: %w", def.key, err)
	}

	return nil
}

// parseClientValue turns a command-line string into the value "config set"
// stores, converting it to the key's natural type and then running it through
// the same validator a stored value faces. There is deliberately one definition
// of valid: a value typed on the command line and one hand-edited into the file
// are accepted or rejected identically.
//
// Both failures name the key, the offending value, and what is accepted, so a
// typo is a message an operator can act on.
func parseClientValue(def clientKeyDef, configPath, raw string) (any, error) {
	value, err := parseClientLiteral(def, raw)
	if err != nil {
		return nil, fmt.Errorf("invalid config value for %s: %w", def.key, err)
	}

	if err := validateClientValue(def, configPath, value); err != nil {
		return nil, err
	}

	return value, nil
}

// parseClientLiteral converts raw to the natural Go type of the key's default —
// the exact inverse of formatClientValue. Like formatting, parsing is one shared
// type switch rather than a per-key registry field: the value's type settles it,
// and a per-key function would be six more places for the two to drift apart.
//
// Only whitespace is normalized. Which spellings are acceptable — a theme's
// case, a column's case — is the validator's decision alone, so the command line
// cannot accept a spelling the file rejects.
func parseClientLiteral(def clientKeyDef, raw string) (any, error) {
	switch def.def.(type) {
	case bool:
		parsed, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("%q is not a boolean: use true or false", raw)
		}

		return parsed, nil
	case []string:
		return splitClientList(raw), nil
	case string:
		return strings.TrimSpace(raw), nil
	default:
		// Unreachable through the registry as it stands; a key added with a
		// default of some other type is a build-time mistake, reported rather
		// than silently stored as the wrong type.
		return nil, fmt.Errorf("values of type %T cannot be set from the command line", def.def)
	}
}

// splitClientList splits a comma-separated value into its trimmed elements,
// dropping empty ones exactly as --fields does, so "id,name" and "id, name"
// mean the same thing. An input with no elements at all yields an empty slice
// for the validator to reject by name.
func splitClientList(raw string) []string {
	parts := strings.Split(raw, ",")
	list := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			list = append(list, trimmed)
		}
	}

	return list
}

// configEntry is the JSON shape emitted per key by "config list" and "config
// get": the dotted key, its effective value in its natural JSON type (a bool
// stays a bool, list_columns stays an array), and where that value came from. It
// is a stable scripting contract: additive changes only.
type configEntry struct {
	Key    string `json:"key"`
	Value  any    `json:"value"`
	Source string `json:"source"`
}

// configListColumns renders the "config list" table and its piped rows.
//
// No column is marked sanitize, and that is deliberate rather than an oversight:
// output.SanitizeTerminal exists for server-derived, untrusted text (torrent
// names, tracker strings, labels). Config values are local, operator-owned
// input, the same category as the server URLs "server list" prints unsanitized.
// Sanitizing here would imply the config file is untrusted, contradicting the
// model documented in AGENTS.md.
var configListColumns = []column[configEntry]{
	{header: "KEY", value: func(e configEntry) string { return e.Key }},
	{header: "VALUE", value: func(e configEntry) string { return formatClientValue(e.Value) }},
	{header: "SOURCE", value: func(e configEntry) string { return e.Source }},
}

// formatClientValue renders a client value for the plaintext paths: a string
// list as its comma-separated form (the same spelling "config set" accepts
// back), and every scalar through the shared setting formatter. The JSON and jq
// paths never call it — they carry the natural JSON type.
func formatClientValue(v any) string {
	if list, ok := v.([]string); ok {
		return strings.Join(list, ",")
	}

	return transmission.FormatSettingValue(v)
}

// resolveStoredClientKey returns what the config file says one key is, and its
// source: the stored value, or the registry default when nothing is stored. A
// stored value is validated; a default is not re-validated
// (TestClientKeyRegistryDefaultsAreValid pins that at build time instead).
//
// This is the bottom two rungs only. The write commands report through it
// deliberately: "config set" describes the file it just wrote, so a --theme in
// force for that invocation cannot be mistaken for something that was persisted.
func resolveStoredClientKey(def clientKeyDef, cfg config.Config, configPath string) (configEntry, error) {
	value, ok := def.accessor(cfg.Client)
	if !ok {
		return configEntry{Key: def.key, Value: def.def, Source: sourceDefault}, nil
	}

	if err := validateClientValue(def, configPath, value); err != nil {
		return configEntry{}, err
	}

	return configEntry{Key: def.key, Value: value, Source: sourceConfig}, nil
}

// resolveClientSetting returns one key's effective value for this invocation and
// the rung that supplied it, applying the full precedence chain: flag > env >
// config > default. It is what the read commands report, so "config list"
// answers "what would this process use" rather than only "what is in the file".
func resolveClientSetting(cmd *cobra.Command, def clientKeyDef, cfg config.Config) (configEntry, error) {
	if def.override != nil {
		override, err := def.override(cmd)
		if err != nil {
			return configEntry{}, err
		}

		if override != nil {
			return configEntry{Key: def.key, Value: override.value, Source: override.source}, nil
		}
	}

	return resolveStoredClientKey(def, cfg, configFlag(cmd))
}

// clientSetting resolves one registry key for this invocation and returns its
// effective value in the key's natural Go type. It is the seam a command uses to
// *consume* a setting, as opposed to reporting one: the same flag > env > config
// > default chain applies, and an invalid stored value is the same error
// "config get" gives, naming the key so an operator knows to fix the file rather
// than the command line.
//
// key is one of the constants at the top of this file, so an unknown key is a
// compile-time impossibility rather than a runtime error.
func clientSetting[T any](cmd *cobra.Command, cfg config.Config, key string) (T, error) {
	var zero T

	def, err := lookupClientKey(key)
	if err != nil {
		return zero, err
	}

	entry, err := resolveClientSetting(cmd, def, cfg)
	if err != nil {
		return zero, err
	}

	value, err := typedClientValue[T](entry.Value)
	if err != nil {
		return zero, fmt.Errorf("config key %s: %w", key, err)
	}

	return value, nil
}

// clientEntries resolves every registry key for this invocation, sorted by key.
// A single invalid stored value fails the whole listing rather than being
// reported as a default: "config list" answers "what would this process use",
// and it cannot answer that for a key it does not understand.
func clientEntries(cmd *cobra.Command, cfg config.Config) ([]configEntry, error) {
	entries := make([]configEntry, 0, len(sortedClientKeyDefs))
	for _, def := range sortedClientKeyDefs {
		entry, err := resolveClientSetting(cmd, def, cfg)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// writeClientKey applies one key's new value to the config file and reports what
// that key resolves to afterwards. A nil value clears the key, so "config set"
// and "config unset" share a single write path.
//
// Only the key being written is resolved. A nonsense value hand-edited into some
// *other* key must never make the file unrepairable from the CLI — repairing it
// is exactly what "config set" is for — so nothing here reads a key it was not
// asked to change.
//
// A missing config file is created rather than refused (config.Save creates the
// parent at 0700 and the file at 0600), so settings can be configured on a first
// run, before any server is added. Everything else in the file — servers, the
// default server, and any key snatcher does not recognize — is carried through
// untouched by the load/save round trip.
//
// The entry reported back describes the file, not the invocation: a --theme or a
// SNATCHER_THEME in force for this command is an override for this process and
// has no business either being written or being reported as what was written.
func writeClientKey(def clientKeyDef, configPath string, value any) (configEntry, error) {
	cfg, _, err := loadConfig(configPath)
	if err != nil {
		return configEntry{}, err
	}

	if err := def.apply(&cfg.Client, value); err != nil {
		return configEntry{}, err
	}

	if err := config.Save(cfg, configPath); err != nil {
		return configEntry{}, err
	}

	return resolveStoredClientKey(def, cfg, configPath)
}

// reportClientKey renders one written key in the mode the flags ask for, with
// the group's fixed --jq -> --json -> plaintext precedence. The structured paths
// emit the same {key, value, source} object "config get" emits, so a script that
// reads a setting and a script that writes one parse the same shape; plaintext
// gets the caller's sentence.
func reportClientKey(cmd *cobra.Command, entry configEntry, jqExpr string, asJSON bool, line string) error {
	if jqExpr != "" {
		return output.JQ(cmd.OutOrStdout(), entry, jqExpr)
	}

	if asJSON {
		return output.JSON(cmd.OutOrStdout(), entry)
	}

	// Unsanitized: see configListColumns for why config values are not treated
	// as hostile input.
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), line)

	return nil
}

// NewConfigCmd builds the "config" command group, the namespace for snatcher's
// own settings — how the CLI and TUI behave, as opposed to which server they
// talk to. Those settings live in the client section of config.json and are
// addressed by dotted keys (client.theme, client.list_columns, …). It owns no
// behavior of its own: a bare "snatcher config" prints subcommand help.
//
// The server registry keeps its own "snatcher server" subcommands; this group
// covers the client section only.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage snatcher's own settings",
		Long: "Manage snatcher's own settings.\n\n" +
			"These are the settings that decide how snatcher itself behaves — theme, " +
			"icon set, poll cadence, and the default columns and sort order for " +
			"'torrent list' — as opposed to the servers it talks to, which are managed " +
			"with 'snatcher server'. They live in the client section of the config " +
			"file and are addressed by dotted keys such as client.theme.\n\n" +
			"Run a subcommand (list, get) to read them, or (set, unset) to change " +
			"them.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newConfigListCmd())
	cmd.AddCommand(newConfigGetCmd())
	cmd.AddCommand(newConfigSetCmd())
	cmd.AddCommand(newConfigUnsetCmd())

	return cmd
}

// newConfigListCmd builds "config list", which prints every client setting with
// its effective value and where that value came from: a command-line flag, an
// environment variable, the config file, or the built-in default, in that order
// of precedence. Reporting the whole chain is the point — a setting overridden
// for this invocation is shown as overridden, so "why is my theme not what I
// set" is answerable without opening the file.
//
// Plaintext is an aligned table on a terminal and tab-separated, header-less
// rows when piped or when NO_COLOR is set (the same contract as "torrent list");
// --json and --jq emit the stable {key, value, source} shape, sorted by key.
func newConfigListCmd() *cobra.Command {
	var (
		asJSON bool
		jqExpr string
	)

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List every client setting with its value and source",
		Long: "List every client setting, its effective value, and where that value " +
			"came from, so a deliberate setting is distinguishable from a default " +
			"without opening the config file.\n\n" +
			"The source is the rung of the precedence chain that supplied the value: " +
			"flag, env, config, or default, in that order. A setting overridden for " +
			"this invocation is reported as overridden, not as what the file holds.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := loadConfig(configFlag(cmd))
			if err != nil {
				return err
			}

			entries, err := clientEntries(cmd, cfg)
			if err != nil {
				return err
			}

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), entries, jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), entries)
			}

			writeListPlaintext(cmd, entries, configListColumns, "No settings.")

			return nil
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// newConfigGetCmd builds "config get <key>", which prints one setting's
// effective value for this invocation — the same flag > env > config > default
// chain "config list" reports. The plaintext path prints the bare value with no
// key name, padding, or decoration, so $(snatcher config get client.theme) is
// directly usable in a shell; --json and --jq emit the same {key, value, source}
// object "config list" uses.
func newConfigGetCmd() *cobra.Command {
	var (
		asJSON bool
		jqExpr string
	)

	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Print one client setting's value",
		Long: "Print the effective value of one client setting.\n\n" +
			"The plaintext output is the bare value and nothing else, so it can be " +
			"captured directly in a shell. Use --json to see the value's source " +
			"alongside it, or 'snatcher config list' to see every key at once.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			def, err := lookupClientKey(args[0])
			if err != nil {
				return err
			}

			cfg, _, err := loadConfig(configFlag(cmd))
			if err != nil {
				return err
			}

			entry, err := resolveClientSetting(cmd, def, cfg)
			if err != nil {
				return err
			}

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), entry, jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), entry)
			}

			// The bare value, unsanitized: see configListColumns for why config
			// values are not treated as hostile input.
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), formatClientValue(entry.Value))

			return nil
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// newConfigSetCmd builds "config set <key> <value>", which stores one client
// setting in the config file.
//
// The value is parsed and validated before the file is opened, so a typo is an
// error rather than a config to hand-repair later: an unknown key or an
// unacceptable value leaves config.json exactly as it was. Everything else in
// the file survives the write, including keys snatcher does not recognize.
func newConfigSetCmd() *cobra.Command {
	var (
		asJSON bool
		jqExpr string
	)

	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Change one client setting",
		Long: "Change one client setting and persist it to the config file.\n\n" +
			"The value is checked before anything is written, so an unknown key or " +
			"an invalid value is an error that leaves the config file untouched. " +
			"A list-valued key such as client.list_columns takes its elements " +
			"comma-separated, in order.\n\n" +
			"Use 'snatcher config unset' to drop a setting back to its default, and " +
			"'snatcher config list' to see every key with its current value.",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			def, err := lookupClientKey(args[0])
			if err != nil {
				return err
			}

			value, err := parseClientValue(def, configFlag(cmd), args[1])
			if err != nil {
				return err
			}

			entry, err := writeClientKey(def, configFlag(cmd), value)
			if err != nil {
				return err
			}

			return reportClientKey(cmd, entry, jqExpr, asJSON,
				fmt.Sprintf("Set %s to %s", entry.Key, formatClientValue(entry.Value)))
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// newConfigUnsetCmd builds "config unset <key>", which removes one client
// setting from the config file so the key reverts to its default.
//
// Unsetting a key that is not set is a success, not an error: the command
// describes the state it leaves behind ("this key uses its default"), which is
// already true, so re-running it is safe in a script.
func newConfigUnsetCmd() *cobra.Command {
	var (
		asJSON bool
		jqExpr string
	)

	cmd := &cobra.Command{
		Use:   "unset <key>",
		Short: "Remove one client setting, reverting it to its default",
		Long: "Remove one client setting from the config file, so the key reverts to " +
			"its built-in default.\n\n" +
			"Unsetting a key that is not set succeeds and changes nothing, so it is " +
			"safe to re-run. The reported value is the default the key falls back " +
			"to.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			def, err := lookupClientKey(args[0])
			if err != nil {
				return err
			}

			// A nil value clears the key; see writeClientKey.
			entry, err := writeClientKey(def, configFlag(cmd), nil)
			if err != nil {
				return err
			}

			return reportClientKey(cmd, entry, jqExpr, asJSON,
				fmt.Sprintf("Unset %s, now %s (its default)", entry.Key, formatClientValue(entry.Value)))
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}
