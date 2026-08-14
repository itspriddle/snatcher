package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// NewFreeSpaceCmd builds the "free-space" command, which queries how much free
// (and, on a new-enough server, total) space is available on a download path.
// With no positional path it queries the server's default download directory.
// Plaintext is an aligned "Label: value" block; --json emits the FreeSpace
// contract and --jq filters that same structure. It carries its own local
// --config flag and dials the backend through the shared dial seam.
func NewFreeSpaceCmd() *cobra.Command {
	var (
		configPath string
		server     string
		asJSON     bool
		jqExpr     string
	)

	cmd := &cobra.Command{
		Use:   "free-space [<path>]",
		Short: "Show free space on a download path",
		Long: "Show the free space (and, when the server reports it, the total " +
			"capacity) on a download path.\n\n" +
			"With no path, queries the server's default download directory. The " +
			"total capacity is reported only by Transmission 4.1+ and omitted " +
			"otherwise.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			backend, serverURL, err := dial(cmd, configPath, server)
			if err != nil {
				return err
			}

			path := ""
			if len(args) == 1 {
				path = args[0]
			}

			// Default to the server's configured download directory when no path
			// was given, so a bare "free-space" reports the directory that matters.
			if path == "" {
				session, err := backend.GetSession(cmd.Context())
				if err != nil {
					return fmt.Errorf("connecting to %s: %w", serverURL, err)
				}
				path = session.DownloadDir
			}

			result, err := backend.FreeSpace(cmd.Context(), path)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), result, jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), result)
			}

			writeFreeSpace(cmd, result)

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}

// writeFreeSpace renders a FreeSpace as an aligned "Label: value" plaintext
// block, using the output package's byte formatter for the sizes. The path is a
// server-derived string, so it is passed through output.SanitizeTerminal; the
// total line is omitted when the server did not report a total capacity.
func writeFreeSpace(cmd *cobra.Command, fs transmission.FreeSpace) {
	out := cmd.OutOrStdout()

	_, _ = fmt.Fprintf(out, "Path:  %s\n", output.SanitizeTerminal(fs.Path))
	_, _ = fmt.Fprintf(out, "Free:  %s\n", output.Bytes(fs.SizeBytes))

	if fs.TotalBytes > 0 {
		_, _ = fmt.Fprintf(out, "Total: %s\n", output.Bytes(fs.TotalBytes))
	}
}
