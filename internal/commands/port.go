package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/torrent"
)

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
func portStateFromSession(s torrent.SessionSettings) portState {
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

	return cmd
}

// newPortGetCmd builds "port get", which prints the current peer port alongside
// the random-on-start and port-forwarding flags read from the live session.
// Plaintext is an aligned "Label: value" block; --json emits the portState
// contract and --jq filters that same structure.
func newPortGetCmd() *cobra.Command {
	var (
		configPath string
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
			backend, serverURL, err := dial(configPath)
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
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
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
