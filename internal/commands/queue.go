package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// queueMove applies one of the backend's queue-move operations to a selector.
// It is the only per-verb difference between the four subcommands, which are
// otherwise identical in selector parsing, confirmation lookup, and output.
type queueMove func(context.Context, *transmission.Client, transmission.Selector) error

// NewQueueCmd builds the "queue" parent command and its top/up/down/bottom
// subcommands, which reorder the selected torrents within the download/seed
// queue. With no subcommand it prints help (consistent with port). Each
// subcommand mirrors the other mutation commands (start, stop, reannounce) in
// selectors, output, and the --config/--json/--jq flags, but offers no --all
// flag — moving "all" torrents to the top/bottom is meaningless and up/down
// would be a near no-op. Render precedence mirrors list: --jq → --json →
// plaintext.
func NewQueueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "queue",
		Short:         "Reorder torrents in the queue",
		Long:          "Move one or more torrents within the download/seed queue.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newQueueMoveCmd(
		"top", "Move torrents to the top of the queue",
		"Move one or more torrents to the top of the download/seed queue.",
		"queue-top",
		func(ctx context.Context, b *transmission.Client, sel transmission.Selector) error {
			return b.QueueMoveTop(ctx, sel)
		},
	))
	cmd.AddCommand(newQueueMoveCmd(
		"up", "Move torrents up one position in the queue",
		"Move one or more torrents up one position in the download/seed queue.",
		"queue-up",
		func(ctx context.Context, b *transmission.Client, sel transmission.Selector) error {
			return b.QueueMoveUp(ctx, sel)
		},
	))
	cmd.AddCommand(newQueueMoveCmd(
		"down", "Move torrents down one position in the queue",
		"Move one or more torrents down one position in the download/seed queue.",
		"queue-down",
		func(ctx context.Context, b *transmission.Client, sel transmission.Selector) error {
			return b.QueueMoveDown(ctx, sel)
		},
	))
	cmd.AddCommand(newQueueMoveCmd(
		"bottom", "Move torrents to the bottom of the queue",
		"Move one or more torrents to the bottom of the download/seed queue.",
		"queue-bottom",
		func(ctx context.Context, b *transmission.Client, sel transmission.Selector) error {
			return b.QueueMoveBottom(ctx, sel)
		},
	))

	return cmd
}

// newQueueMoveCmd builds a single "queue <verb>" subcommand. action is the
// mutationResult action key (which selects the confirmation verb and surfaces in
// --json); move applies the matching backend operation. The flow mirrors
// reannounce: parse selectors (no --all is offered, so a bare invocation is an
// error, not a no-op), dial, apply the move, re-fetch to resolve names, render.
func newQueueMoveCmd(verb, short, long, action string, move queueMove) *cobra.Command {
	var (
		configPath string
		server     string
		asJSON     bool
		jqExpr     string
	)

	cmd := &cobra.Command{
		Use:   verb + " [id|hash]...",
		Short: short,
		Long: long + "\n\n" +
			"Select torrents by numeric id or 40-character SHA1 hash. At least " +
			"one selector is required; a bare 'snatcher torrent queue " + verb +
			"' is an error, not a no-op.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				// ParseSelector's no-selector message mentions --all, which these
				// commands do not offer; give a queue-specific hint instead.
				return errors.New("specify torrent ids or hashes")
			}

			sel, err := transmission.ParseSelector(args, false)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(cmd, configPath, server)
			if err != nil {
				return err
			}

			if err := move(cmd.Context(), backend, sel); err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			torrents, err := backend.ListTorrents(cmd.Context(), transmission.Selector{All: true})
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			result, err := resolveMutation(action, sel, torrents)
			if err != nil {
				return err
			}

			return renderMutation(cmd, result, jqExpr, asJSON)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")

	return cmd
}
