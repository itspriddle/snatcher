package commands

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// errPortClosed is the sentinel returned (after the result has already been
// rendered) when a reachable server reports the configured peer port as closed.
// The command writes the {open:false, …} payload to stdout first, then returns
// this so Execute exits non-zero — a probe-style result scripts can gate on.
// This is distinct from a connection/auth failure, which renders no result.
var errPortClosed = errors.New("port is closed")

// portState is the JSON contract emitted by the "port" subcommands: the peer
// port plus the random-on-start and port-forwarding flags read from the live
// session. Its json tags are a stable scripting surface — treat them as API.
type portState struct {
	Port              int  `json:"port"`
	RandomOnStart     bool `json:"random_on_start"`
	ForwardingEnabled bool `json:"forwarding_enabled"`
}

// portStateFromSession projects the peer-port-related fields of a session
// snapshot into the portState contract.
func portStateFromSession(s transmission.SessionSettings) portState {
	return portState{
		Port:              s.PeerPort,
		RandomOnStart:     s.PeerPortRandomOnStart,
		ForwardingEnabled: s.PortForwardingEnabled,
	}
}

// NewPortCmd builds the "port" parent command and its subcommands. With no
// subcommand it prints help (consistent with auth). The port group is a
// discoverable wrapper over the peer/listening-port session settings; each
// subcommand carries its own --config flag and dials the backend through the
// shared dial seam.
func NewPortCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "port",
		Short:         "View or change the listening port",
		Long:          "View, change, and test the BitTorrent peer/listening port.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(newPortGetCmd())
	cmd.AddCommand(newPortSetCmd())
	cmd.AddCommand(newPortTestCmd())

	return cmd
}

// newPortGetCmd builds "port get", which prints the current peer port alongside
// the random-on-start and port-forwarding flags read from the live session.
// Plaintext is an aligned "Label: value" block; --json emits the portState
// contract and --jq filters that same structure.
func newPortGetCmd() *cobra.Command {
	var (
		configPath string
		server     string
		asJSON     bool
		jqExpr     string
	)

	cmd := &cobra.Command{
		Use:           "get",
		Short:         "Show the current peer port and related settings",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			backend, serverURL, err := dial(configPath, server)
			if err != nil {
				return err
			}

			session, err := backend.GetSession(cmd.Context())
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			state := portStateFromSession(session)

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), state, jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), state)
			}

			writePortState(cmd, state)

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// newPortSetCmd builds "port set [<port>]", which writes the peer port and/or
// the random-on-start and port-forwarding flags, then re-reads the session and
// renders the resulting state in the same shape as "port get".
//
// A positional <port> sets peer-port and, unless --random was explicitly
// passed, also disables peer-port-random-on-start so a forwarded port sticks
// across restarts. --random and --forwarding write their flags directly and an
// explicit --random always wins over the auto-disable. With neither a port nor
// any flag, it returns a "nothing to set" error.
func newPortSetCmd() *cobra.Command {
	var (
		configPath string
		server     string
		asJSON     bool
		jqExpr     string
		random     bool
		forwarding bool
	)

	cmd := &cobra.Command{
		Use:   "set [<port>]",
		Short: "Set the peer port and related settings",
		Long: "Set the peer port and related settings.\n\n" +
			"Given a port, sets peer-port and disables random-on-start so the " +
			"port sticks across restarts (pass --random to keep it on). " +
			"--random and --forwarding write those flags directly; an explicit " +
			"--random overrides the auto-disable. After writing, the server's " +
			"actual values are read back and reported.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			randomChanged := cmd.Flags().Changed("random")
			forwardingChanged := cmd.Flags().Changed("forwarding")

			if len(args) == 0 && !randomChanged && !forwardingChanged {
				return fmt.Errorf("nothing to set: provide a port and/or --random/--forwarding")
			}

			changes := make(map[string]any)

			if len(args) == 1 {
				port, err := parsePort(args[0])
				if err != nil {
					return err
				}
				changes["peer-port"] = port

				// Pin the port across restarts unless the user explicitly opted
				// into random-on-start.
				if !randomChanged {
					changes["peer-port-random-on-start"] = false
				}
			}

			if randomChanged {
				changes["peer-port-random-on-start"] = random
			}

			if forwardingChanged {
				changes["port-forwarding-enabled"] = forwarding
			}

			backend, serverURL, err := dial(configPath, server)
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

			state := portStateFromSession(session)

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), state, jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), state)
			}

			writePortState(cmd, state)

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().BoolVar(&random, "random", false, "randomize the peer port on each server start")
	cmd.Flags().BoolVar(&forwarding, "forwarding", false, "enable port forwarding (UPnP/NAT-PMP)")

	return cmd
}

// newPortTestCmd builds "port test", which asks the server whether the
// configured peer port is reachable from outside. The structured result is
// rendered first in all modes; when the server reports the port closed the
// command still exits non-zero (via errPortClosed) so scripts can gate on it.
// A connection/auth failure is distinct: it renders no result and surfaces as
// the dial/transport error.
func newPortTestCmd() *cobra.Command {
	var (
		configPath string
		server     string
		asJSON     bool
		jqExpr     string
	)

	cmd := &cobra.Command{
		Use:   "test",
		Short: "Test whether the configured peer port is reachable",
		Long: "Test whether the configured peer port is reachable from outside.\n\n" +
			"Reports open or closed for the currently configured port (it takes no " +
			"port argument). Exits 0 when the port is open and non-zero when it is " +
			"closed, while still writing the structured result to stdout — a " +
			"connection failure is a distinct non-zero exit that renders no result.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			backend, serverURL, err := dial(configPath, server)
			if err != nil {
				return err
			}

			result, err := backend.TestPort(cmd.Context())
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			switch {
			case jqExpr != "":
				if err := output.JQ(cmd.OutOrStdout(), result, jqExpr); err != nil {
					return err
				}
			case asJSON:
				if err := output.JSON(cmd.OutOrStdout(), result); err != nil {
					return err
				}
			default:
				writePortTest(cmd, result)
			}

			// A reachable-but-closed port is a probe failure: the result is
			// already written, so return the sentinel to flip Execute's exit code.
			if !result.Open {
				return errPortClosed
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// parsePort validates a positional port argument: it must parse to an integer
// in the inclusive [1, 65535] range. Any other value returns a clear error and
// the caller performs no session write.
func parsePort(raw string) (int, error) {
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q: must be 1-65535", raw)
	}

	return port, nil
}

// writePortState renders a portState as an aligned "Label: value" plaintext
// block. The values are server-derived numbers/bools, not free-form strings, so
// no terminal sanitization is required.
func writePortState(cmd *cobra.Command, state portState) {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Port:               %d\n", state.Port)
	_, _ = fmt.Fprintf(out, "Random on start:    %s\n", yesNo(state.RandomOnStart))
	_, _ = fmt.Fprintf(out, "Forwarding enabled: %s\n", yesNo(state.ForwardingEnabled))
}

// writePortTest renders a port-reachability result as a single plaintext line.
// IPProtocol is appended only when the server reported it (JSON-RPC 2.0); it is
// a fixed server-derived enum ("ipv4"/"ipv6"), not free-form text, so no
// terminal sanitization is required.
func writePortTest(cmd *cobra.Command, result transmission.PortTest) {
	out := cmd.OutOrStdout()

	state := "closed"
	if result.Open {
		state = "open"
	}

	if result.IPProtocol != "" {
		_, _ = fmt.Fprintf(out, "Port is %s (%s)\n", state, result.IPProtocol)
		return
	}

	_, _ = fmt.Fprintf(out, "Port is %s\n", state)
}
