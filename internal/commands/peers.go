package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// peerFields is the peers command's field registry: it maps a selectable
// --fields key to the column that renders it, for both the TTY aligned table and
// the piped tab-separated rows. Only client is truncatable, per the
// one-truncatable-column-per-registry invariant nameColumnLimit relies on.
//
// Every string column here is sanitized. A peer's client string and flag string
// are the most hostile input the CLI renders — they are chosen by an arbitrary
// member of the swarm, not by the operator's own server — and the address comes
// off the same connection.
var peerFields = map[string]column[transmission.Peer]{
	"address": {header: "Address", sanitize: true, value: func(p transmission.Peer) string {
		return p.Address
	}},
	"port": {header: "Port", rightAlign: true, value: func(p transmission.Peer) string {
		return strconv.Itoa(p.Port)
	}},
	"client": {header: "Client", truncate: true, sanitize: true, value: func(p transmission.Peer) string {
		return p.Client
	}},
	"progress": {header: "Progress", rightAlign: true, value: func(p transmission.Peer) string {
		return output.Percent(p.Progress)
	}},
	"down": {header: "Down", rightAlign: true, value: func(p transmission.Peer) string {
		return output.Speed(p.RateToClient)
	}},
	"up": {header: "Up", rightAlign: true, value: func(p transmission.Peer) string {
		return output.Speed(p.RateToPeer)
	}},
	"encrypted": {header: "Encrypted", value: func(p transmission.Peer) string {
		return yesNo(p.IsEncrypted)
	}},
	"status": {header: "Status", sanitize: true, value: func(p transmission.Peer) string {
		return p.FlagStr
	}},
}

// peerFieldKeys is the peers command's default column set, in order. Every key in
// the registry is in it, so it doubles as the canonical ordering for the --fields
// help text.
var peerFieldKeys = []string{
	"address", "port", "client", "progress", "down", "up", "encrypted", "status",
}

// NewPeersCmd builds the "peers" command, which resolves a single torrent by
// numeric id or 40-character info-hash and lists the peers it is currently
// connected to. It is the CLI equivalent of the TUI detail pane's peers tab, and
// the way to inspect swarm health from a script. Output honors the same
// precedence as list: --jq, then --json, then plaintext (an aligned table with
// headers on a TTY, header-less tab-separated rows when piped or when NO_COLOR is
// set).
func NewPeersCmd() *cobra.Command {
	var (
		configPath string
		server     string
		asJSON     bool
		jqExpr     string
		fieldsFlag string
	)

	cmd := &cobra.Command{
		Use:           "peers <id|hash>",
		Short:         "List a torrent's connected peers",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := transmission.ParseSelector(args, false)
			if err != nil {
				return err
			}

			columns, err := parseFields(peerFields, peerFieldKeys, fieldsFlag)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(cmd, configPath, server)
			if err != nil {
				return err
			}

			match, err := resolveTorrent(cmd, backend, serverURL, sel, args[0])
			if err != nil {
				return err
			}

			if jqExpr != "" {
				return output.JQ(cmd.OutOrStdout(), jsonSlice(match.Peers), jqExpr)
			}

			if asJSON {
				return output.JSON(cmd.OutOrStdout(), jsonSlice(match.Peers))
			}

			writeListPlaintext(cmd, match.Peers, columns, "No peers.")

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of plaintext")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter JSON output with a jq expression")
	cmd.Flags().StringVar(&fieldsFlag, "fields", "", fmt.Sprintf("comma-separated columns to show, in order (%s)", strings.Join(peerFieldKeys, ", ")))

	return cmd
}
