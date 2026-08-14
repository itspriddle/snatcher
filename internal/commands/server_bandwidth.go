package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// directionDownload and directionUpload are the two values a bandwidth
// direction argument may take, and the canonical spelling every helper here
// compares against.
const (
	directionDownload = "download"
	directionUpload   = "upload"
)

// altStateOn, altStateOff, and altStateToggle are the three values the "alt"
// subcommand's state argument may take, and the canonical spelling
// parseBandwidthAltState returns.
const (
	altStateOn     = "on"
	altStateOff    = "off"
	altStateToggle = "toggle"
)

// bandwidthDirectionToken maps a canonical direction to the token Transmission's
// speed-limit and alt-speed session keys spell it with. Every direction reaching
// this map has already been through parseBandwidthDirection.
var bandwidthDirectionToken = map[string]string{
	directionDownload: "down",
	directionUpload:   "up",
}

// bandwidthState is the JSON contract emitted by every "bandwidth" subcommand:
// the live transfer rates, both limit pairs, and the alt (turtle) switches. Its
// json tags are a stable scripting surface — treat them as API.
//
// The JSON is the rendered view: whatever the plaintext block shows, the object
// contains, no more and no less. A direction is omitted entirely when the caller
// narrowed to the other one, while the two alt fields are the invariant header
// and are always present.
type bandwidthState struct {
	Download *bandwidthDirection `json:"download,omitempty"`
	Upload   *bandwidthDirection `json:"upload,omitempty"`

	// AltEnabled reports whether the alt (turtle) profile is active right now.
	AltEnabled bool `json:"alt_enabled"`

	// AltScheduleEnabled reports whether a scheduled alt-speed window is
	// configured. Read-only here; "server update" owns the schedule keys.
	AltScheduleEnabled bool `json:"alt_schedule_enabled"`
}

// bandwidthDirection is one direction's rates and limits. Limit fields are
// pointers so a narrowed view omits what it did not render.
//
// The units deliberately differ, mirroring Transmission's own inconsistency:
// CurrentSpeed is bytes/second while every limit is KB/s. Neither is converted
// into the other's unit.
type bandwidthDirection struct {
	// CurrentSpeed is the live aggregate rate in bytes/second.
	CurrentSpeed int64 `json:"current_speed"`

	// Limit is the normal-profile limit in KB/s; LimitEnabled is its switch.
	Limit        *int64 `json:"limit,omitempty"`
	LimitEnabled *bool  `json:"limit_enabled,omitempty"`

	// AltLimit is the alt-profile limit in KB/s.
	AltLimit *int64 `json:"alt_limit,omitempty"`
}

// NewBandwidthCmd builds the "bandwidth" parent command and its subcommands.
// With no subcommand it prints help (consistent with port and auth). The
// bandwidth group is an ergonomic wrapper over the global speed-limit session
// settings, so a caller never has to know that a limit and its "-enabled"
// companion travel together; each subcommand dials the backend through the
// shared dial seam.
func NewBandwidthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "bandwidth",
		Short:         "View or change global speed limits",
		Long:          "View and change the server's global download and upload speed limits, including the alt (turtle) profile.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(newBandwidthShowCmd())
	cmd.AddCommand(newBandwidthUpdateCmd())
	cmd.AddCommand(newBandwidthAltCmd())

	return cmd
}

// newBandwidthShowCmd builds "bandwidth show [download|upload]", which reports
// the live transfer rates alongside both limit pairs and the alt switches. A
// positional direction narrows the report to that direction, and --alt narrows
// each reported direction to its alt-profile limit. Plaintext is an aligned
// block; --json emits the bandwidthState contract and --jq filters that same
// structure.
func newBandwidthShowCmd() *cobra.Command {
	var (
		server string
		asJSON bool
		jqExpr string
		alt    bool
	)

	cmd := &cobra.Command{
		Use:   "show [download|upload]",
		Short: "Show current speeds and the global limits",
		Long: "Show the server's current transfer rates, both global limit pairs, " +
			"and whether the alt (turtle) profile is active.\n\n" +
			"With no argument both directions are reported; naming a direction " +
			"reports only that one. --alt narrows each reported direction to its " +
			"alt-profile limit. Current speeds are bytes/second; every limit is in " +
			"KB/s, exactly as Transmission stores it. A limit whose switch is off " +
			"still shows its stored value, marked disabled, because the value " +
			"survives being turned off.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate the direction before dialing so a typo never opens a
			// connection.
			direction := ""
			if len(args) == 1 {
				parsed, err := parseBandwidthDirection(args[0])
				if err != nil {
					return err
				}
				direction = parsed
			}

			backend, serverURL, err := dial(cmd, server)
			if err != nil {
				return err
			}

			state, err := readBandwidthState(cmd.Context(), backend)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			return renderBandwidthState(cmd, narrowBandwidthState(state, direction, alt), asJSON, jqExpr)
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVar(&alt, "alt", false, "report the alt (turtle) limits instead of the normal ones")

	return cmd
}

// newBandwidthUpdateCmd builds "bandwidth update <download|upload> <speed>",
// which writes one direction's global limit and then reports the resulting
// state, narrowed to what it wrote. It exists so a caller never has to know that
// a limit value and its "-enabled" companion travel together: a numeric speed
// writes both in one call, and "off" flips only the switch, leaving the stored
// value to come back with the next enable.
//
// --alt writes the alt (turtle) profile's limit instead. That profile has no
// per-direction switch — one alt-speed-enabled governs both directions — so an
// --alt write never touches it, and "off" there is an error rather than a write
// that would silently throttle the other direction.
func newBandwidthUpdateCmd() *cobra.Command {
	var (
		server string
		asJSON bool
		jqExpr string
		alt    bool
	)

	cmd := &cobra.Command{
		Use:   "update <download|upload> <speed>",
		Short: "Set a global speed limit",
		Long: "Set the server's global download or upload speed limit.\n\n" +
			"The speed is a non-negative integer in KB/s, exactly as Transmission " +
			"stores it (suffixed values like 5M are not accepted), or the literal " +
			"'off'. Setting a limit also turns it on; 'off' turns it off while " +
			"leaving the stored value in place, so re-enabling restores it.\n\n" +
			"--alt writes the alt (turtle) profile's limit instead. The alt profile " +
			"has a single switch governing both directions, so an --alt write never " +
			"touches it and 'off' is rejected — use 'snatcher server bandwidth alt " +
			"off' to leave turtle mode. After writing, the server's actual values " +
			"are read back and reported.",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate both arguments before dialing so a typo never opens a
			// connection, let alone performs a partial write.
			direction, err := parseBandwidthDirection(args[0])
			if err != nil {
				return err
			}

			changes, err := bandwidthLimitChanges(direction, args[1], alt)
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

			state, err := readBandwidthState(cmd.Context(), backend)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			return renderBandwidthState(cmd, narrowBandwidthState(state, direction, alt), asJSON, jqExpr)
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVar(&alt, "alt", false, "write the alt (turtle) limit instead of the normal one")

	return cmd
}

// newBandwidthAltCmd builds "bandwidth alt <on|off|toggle>", the one command
// that owns the alt (turtle) switch. It exists so the switch is reachable
// without spelling alt-speed-enabled, and so the TUI's turtle key has an exact
// CLI equivalent.
//
// The switch governs both directions at once, so flipping it changes which
// limits are in force everywhere; the full, unnarrowed state is therefore read
// back and reported rather than a slice of it.
func newBandwidthAltCmd() *cobra.Command {
	var (
		server string
		asJSON bool
		jqExpr string
	)

	cmd := &cobra.Command{
		Use:   "alt <on|off|toggle>",
		Short: "Turn the alt (turtle) profile on, off, or toggle it",
		Long: "Activate or deactivate the server's alt (turtle) speed profile, the " +
			"single switch that puts the alt limits in force for both directions " +
			"at once.\n\n" +
			"'on' and 'off' write the switch directly without reading it first. " +
			"'toggle' reads the current state and writes its inverse — Transmission's " +
			"RPC has no compare-and-set, so another client flipping the switch between " +
			"that read and the write wins, and this command does not guard against " +
			"it.\n\n" +
			"Because the switch decides which limits apply to both directions, the " +
			"complete state is read back and reported afterwards.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate the state before dialing so a typo never opens a connection.
			mode, err := parseBandwidthAltState(args[0])
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(cmd, server)
			if err != nil {
				return err
			}

			enabled := mode == altStateOn

			if mode == altStateToggle {
				session, err := backend.GetSession(cmd.Context())
				if err != nil {
					return fmt.Errorf("connecting to %s: %w", serverURL, err)
				}

				enabled = !session.AltSpeedEnabled
			}

			if err := backend.SetSession(cmd.Context(), map[string]any{"alt-speed-enabled": enabled}); err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			state, err := readBandwidthState(cmd.Context(), backend)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			return renderBandwidthState(cmd, state, asJSON, jqExpr)
		},
	}

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// bandwidthLimitChanges builds the session-set payload for one direction's
// limit. The normal profile has a per-direction switch, so a numeric speed
// writes the value and enables it together while "off" flips only the switch and
// preserves the stored number. The alt profile has no per-direction switch, so a
// numeric speed writes the stored value alone — never alt-speed-enabled, which
// governs both directions — and "off" is an error pointing at the command that
// does own that switch. An invalid speed returns an error and no write is
// performed.
func bandwidthLimitChanges(direction, raw string, alt bool) (map[string]any, error) {
	token := bandwidthDirectionToken[direction]
	value := strings.TrimSpace(raw)
	off := strings.EqualFold(value, "off")

	if alt {
		if off {
			return nil, fmt.Errorf("cannot turn off an alt limit per direction: " +
				"one alt-speed switch governs both directions; " +
				"use 'snatcher server bandwidth alt off' to leave turtle mode")
		}

		kbps, err := parseBandwidthSpeed(value)
		if err != nil {
			return nil, err
		}

		return map[string]any{"alt-speed-" + token: kbps}, nil
	}

	if off {
		return map[string]any{"speed-limit-" + token + "-enabled": false}, nil
	}

	kbps, err := parseBandwidthSpeed(value)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"speed-limit-" + token:              kbps,
		"speed-limit-" + token + "-enabled": true,
	}, nil
}

// parseBandwidthSpeed validates a speed argument as a non-negative integer in
// KB/s. Negative, fractional, suffixed ("5M"), and non-numeric values are all
// rejected: this deliberately mirrors torrent update's --download-limit grammar
// rather than introducing a human-unit parser.
func parseBandwidthSpeed(raw string) (int, error) {
	kbps, err := strconv.Atoi(raw)
	if err != nil || kbps < 0 {
		return 0, fmt.Errorf("invalid speed %q: must be a non-negative integer (KB/s) or 'off'", raw)
	}

	return kbps, nil
}

// parseBandwidthDirection validates a direction argument case-insensitively and
// returns its canonical spelling. Any other value returns an error naming both
// valid values, and the caller performs no network call.
func parseBandwidthDirection(raw string) (string, error) {
	switch strings.ToLower(raw) {
	case directionDownload:
		return directionDownload, nil
	case directionUpload:
		return directionUpload, nil
	}

	return "", fmt.Errorf("invalid direction %q: must be one of: %s, %s", raw, directionDownload, directionUpload)
}

// parseBandwidthAltState validates the alt subcommand's state argument
// case-insensitively and returns its canonical spelling. Any other value returns
// an error naming all three valid values, and the caller performs no network
// call.
func parseBandwidthAltState(raw string) (string, error) {
	switch strings.ToLower(raw) {
	case altStateOn:
		return altStateOn, nil
	case altStateOff:
		return altStateOff, nil
	case altStateToggle:
		return altStateToggle, nil
	}

	return "", fmt.Errorf("invalid state %q: must be one of: %s, %s, %s", raw, altStateOn, altStateOff, altStateToggle)
}

// readBandwidthState performs the GetSession + Stats pair — two round-trips,
// since Transmission offers no combined primitive — and builds the complete,
// unnarrowed state. Errors are returned unwrapped for the caller to decorate
// with the resolved server URL.
func readBandwidthState(ctx context.Context, backend *transmission.Client) (bandwidthState, error) {
	session, err := backend.GetSession(ctx)
	if err != nil {
		return bandwidthState{}, err
	}

	stats, err := backend.Stats(ctx)
	if err != nil {
		return bandwidthState{}, err
	}

	return bandwidthState{
		Download: newBandwidthDirection(
			stats.DownloadSpeed,
			session.SpeedLimitDown,
			session.SpeedLimitDownEnabled,
			session.AltSpeedDown,
		),
		Upload: newBandwidthDirection(
			stats.UploadSpeed,
			session.SpeedLimitUp,
			session.SpeedLimitUpEnabled,
			session.AltSpeedUp,
		),
		AltEnabled:         session.AltSpeedEnabled,
		AltScheduleEnabled: session.AltSpeedTimeEnabled,
	}, nil
}

// newBandwidthDirection builds a fully populated direction. The parameters are
// copies, so taking their addresses is safe and keeps every state independent.
func newBandwidthDirection(current, limit int64, limitEnabled bool, altLimit int64) *bandwidthDirection {
	return &bandwidthDirection{
		CurrentSpeed: current,
		Limit:        &limit,
		LimitEnabled: &limitEnabled,
		AltLimit:     &altLimit,
	}
}

// narrowBandwidthState applies the two view filters: a direction ("" for both)
// drops the other direction entirely, and altOnly keeps each surviving
// direction's alt limit while dropping the normal-profile pair. The returned
// state shares no direction with the input, so narrowing never mutates it.
func narrowBandwidthState(state bandwidthState, direction string, altOnly bool) bandwidthState {
	switch direction {
	case directionDownload:
		state.Upload = nil
	case directionUpload:
		state.Download = nil
	}

	if altOnly {
		state.Download = altOnlyDirection(state.Download)
		state.Upload = altOnlyDirection(state.Upload)
	}

	return state
}

// altOnlyDirection returns a copy of dir carrying only the live rate and the
// alt-profile limit.
func altOnlyDirection(dir *bandwidthDirection) *bandwidthDirection {
	if dir == nil {
		return nil
	}

	return &bandwidthDirection{
		CurrentSpeed: dir.CurrentSpeed,
		AltLimit:     dir.AltLimit,
	}
}

// renderBandwidthState writes state in the mode the flags select, honoring the
// fixed --jq -> --json -> plaintext precedence every command shares.
func renderBandwidthState(cmd *cobra.Command, state bandwidthState, asJSON bool, jqExpr string) error {
	if jqExpr != "" {
		return output.JQ(cmd.OutOrStdout(), state, jqExpr)
	}

	if asJSON {
		return output.JSON(cmd.OutOrStdout(), state)
	}

	writeBandwidthState(cmd, state)

	return nil
}

// writeBandwidthState renders a (possibly narrowed) state as an aligned
// plaintext block, alt switch first because it decides which limits are
// actually in force. The values are server-derived numbers and bools, not
// free-form strings, so no terminal sanitization is required.
func writeBandwidthState(cmd *cobra.Command, state bandwidthState) {
	out := cmd.OutOrStdout()

	_, _ = fmt.Fprintf(out, "Alt-speed:      %s (schedule: %s)\n",
		yesNo(state.AltEnabled), yesNo(state.AltScheduleEnabled))

	writeBandwidthDirection(cmd, "Download", state.Download)
	writeBandwidthDirection(cmd, "Upload", state.Upload)
}

// writeBandwidthDirection renders one direction's block, skipping it entirely
// when the state was narrowed to the other direction. Limit lines are written
// only for the fields the narrowed state carries, so the plaintext and the JSON
// always show the same field set.
func writeBandwidthDirection(cmd *cobra.Command, label string, dir *bandwidthDirection) {
	if dir == nil {
		return
	}

	out := cmd.OutOrStdout()

	_, _ = fmt.Fprintf(out, "%s:\n", label)
	_, _ = fmt.Fprintf(out, "  Current:      %s\n", output.Speed(dir.CurrentSpeed))

	if dir.Limit != nil {
		// A disabled limit keeps its stored value: "update <dir> off" only flips
		// the switch, so the user needs to see the number that will come back.
		disabled := ""
		if dir.LimitEnabled != nil && !*dir.LimitEnabled {
			disabled = " (disabled)"
		}

		_, _ = fmt.Fprintf(out, "  Limit:        %d KB/s%s\n", *dir.Limit, disabled)
	}

	if dir.AltLimit != nil {
		_, _ = fmt.Fprintf(out, "  Alt limit:    %d KB/s\n", *dir.AltLimit)
	}
}
