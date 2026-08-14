package commands

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAddGlobalFlagsRegistersConfigPersistently pins that --config lands in the
// persistent set, not the local one: that is what makes one declaration on root
// reach every subcommand at every depth.
func TestAddGlobalFlagsRegistersConfigPersistently(t *testing.T) {
	root := &cobra.Command{Use: "snatcher"}
	AddGlobalFlags(root)

	require.NotNil(t, root.PersistentFlags().Lookup(flagConfig), "--config must be persistent")
	assert.Nil(t, root.Flags().Lookup(flagConfig), "--config must not also be a local flag")
}

// TestConfigFlagIsEmptyWithoutTheFlag covers a command built standalone, with no
// root above it — every test in this package does exactly that. Reading a flag
// nothing registered must answer "the default path", the same as an unset flag,
// rather than panicking.
func TestConfigFlagIsEmptyWithoutTheFlag(t *testing.T) {
	assert.Empty(t, configFlag(&cobra.Command{Use: "orphan"}))
}

// TestConfigFlagReadsAnInheritedValue is the promotion's real contract: a leaf
// command declares no --config of its own and still sees the one the root
// carries, from any depth.
func TestConfigFlagReadsAnInheritedValue(t *testing.T) {
	var got string

	leaf := &cobra.Command{
		Use:  "list",
		RunE: func(cmd *cobra.Command, _ []string) error { got = configFlag(cmd); return nil },
	}

	group := &cobra.Command{Use: "torrent"}
	group.AddCommand(leaf)

	root := &cobra.Command{Use: "snatcher"}
	AddGlobalFlags(root)
	root.AddCommand(group)

	// Before the subcommand, where a global flag belongs.
	root.SetArgs([]string{"--config", "/tmp/global.json", "torrent", "list"})
	require.NoError(t, root.Execute())
	assert.Equal(t, "/tmp/global.json", got)

	// And after it, which is where every existing invocation puts it.
	root.SetArgs([]string{"torrent", "list", "--config", "/tmp/trailing.json"})
	require.NoError(t, root.Execute())
	assert.Equal(t, "/tmp/trailing.json", got)
}
