package surface

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestGenerate(t *testing.T) {
	root := &cobra.Command{Use: "snatcher"}
	root.PersistentFlags().String("config", "", "config file path")
	root.PersistentFlags().Bool("json", false, "JSON output")

	// Group command (no RunE).
	torrent := &cobra.Command{Use: "torrent", Short: "Manage torrents"}
	root.AddCommand(torrent)

	// Leaf with local flags.
	list := &cobra.Command{
		Use:  "list",
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	list.Flags().Int("limit", 0, "Max results")
	torrent.AddCommand(list)

	// Command with a positional arg.
	info := &cobra.Command{
		Use:  "info <torrent-id>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	torrent.AddCommand(info)

	// Command with multiple positional args.
	move := &cobra.Command{
		Use:  "move <torrent-id> <location>",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	move.Flags().Bool("force", false, "Skip confirmation")
	torrent.AddCommand(move)

	got := Generate(root)

	expected := `ARG snatcher torrent info 0 torrent-id
ARG snatcher torrent move 0 torrent-id
ARG snatcher torrent move 1 location
CMD snatcher
CMD snatcher torrent
CMD snatcher torrent info
CMD snatcher torrent list
CMD snatcher torrent move
FLAG snatcher --config type=string
FLAG snatcher --json type=bool
FLAG snatcher torrent list --limit type=int
FLAG snatcher torrent move --force type=bool
`

	assert.Equal(t, expected, got)
}

func TestGenerateExcludesCompletion(t *testing.T) {
	root := &cobra.Command{Use: "snatcher"}

	completion := &cobra.Command{Use: "completion", Short: "Generate completions"}
	root.AddCommand(completion)

	realCmd := &cobra.Command{
		Use:  "stats",
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	root.AddCommand(realCmd)

	got := Generate(root)

	assert.Contains(t, got, "CMD snatcher\n")
	assert.Contains(t, got, "CMD snatcher stats\n")
	assert.NotContains(t, got, "completion")
}

func TestGenerateExcludesHelpFlag(t *testing.T) {
	root := &cobra.Command{Use: "snatcher"}
	root.Flags().Bool("version", false, "Show version")

	got := Generate(root)

	assert.NotContains(t, got, "--help")
	assert.Contains(t, got, "--version")
}
