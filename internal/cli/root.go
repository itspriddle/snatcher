package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/commands"
	"github.com/itspriddle/snatcher/internal/version"
)

// NewRootCmd creates and returns the root cobra command.
func NewRootCmd() *cobra.Command {
	var showVersion bool

	cmd := &cobra.Command{
		Use:   "snatcher",
		Short: "Transmission BitTorrent CLI client",
		Long:  "Transmission BitTorrent CLI client\n\nA modern CLI/TUI for managing BitTorrent clients, Transmission first.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), version.FullVersion())
				return nil
			}
			return cmd.Help()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.Flags().BoolVar(&showVersion, "version", false, "Print version information and exit")

	cmd.AddCommand(commands.NewAuthCmd())
	cmd.AddCommand(commands.NewListCmd())
	cmd.AddCommand(commands.NewStatsCmd())

	return cmd
}
