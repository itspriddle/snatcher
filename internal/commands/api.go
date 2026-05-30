package commands

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/torrent"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// errAPIFailure is the sentinel wrapped (after the envelope has already been
// printed) when an RPC call fails at the RPC level. It drives the non-zero exit
// code without re-rendering: the command has already written the envelope to
// stdout, so the returned error only flips Execute's exit code. Execute prints
// it as an extra "Error: …" stderr line, matching gh api.
var errAPIFailure = errors.New("RPC call failed")

// apiEnvelope is the normalized response envelope the api command renders by
// default. It mirrors transmission.APIResponse's normalized fields but
// deliberately omits the raw bytes and OK flag (those drive --raw and the exit
// code, not the JSON contract), so the same shape is emitted regardless of which
// Transmission wire dialect the server speaks.
type apiEnvelope struct {
	// Result is the normalized status string: "success", or the server's error
	// text on an RPC-level failure.
	Result string `json:"result"`

	// Arguments is the normalized result payload, or null on an RPC-level failure.
	Arguments json.RawMessage `json:"arguments"`

	// Tag is the normalized request tag/id echoed by the server.
	Tag int `json:"tag"`

	// Code is the error code on an RPC-level failure (JSON-RPC error.code); it is
	// omitted from the JSON when zero.
	Code int `json:"code,omitempty"`
}

// NewAPICmd builds the "api" command, a power-user escape hatch — modeled on
// `gh api` — that issues an arbitrary Transmission RPC call and prints the
// response. This story handles zero-argument methods only; field flags and
// --input land in a later story. The backend must implement the Transmission
// APICaller capability (the generic call is deliberately kept off the
// vendor-neutral torrent.Backend interface).
func NewAPICmd() *cobra.Command {
	var (
		configPath string
		jqExpr     string
		raw        bool
	)

	cmd := &cobra.Command{
		Use:   "api <method>",
		Short: "Make an arbitrary Transmission RPC call",
		Long: "Make an arbitrary Transmission RPC call and print the response.\n\n" +
			"Issues the given canonical kebab-case RPC method (e.g. session-get, " +
			"torrent-get, queue-move-top) and prints a normalized response envelope " +
			"that is identical whether the server speaks the bespoke (<=4.0) or " +
			"JSON-RPC 2.0 (>=4.1) dialect.\n\n" +
			"snatcher translates the method name only (bespoke sends it verbatim; " +
			"JSON-RPC replaces '-' with '_'). It never rewrites argument keys or " +
			"values, which are not portable across Transmission dialect versions.\n\n" +
			"An RPC-level failure still prints the envelope, then exits 1.\n\n" +
			"Examples:\n" +
			"  snatcher api session-get\n" +
			"  snatcher api session-get --jq '.arguments.version'\n" +
			"  snatcher api session-get --raw",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			method := args[0]

			backend, serverURL, err := dial(configPath)
			if err != nil {
				return err
			}

			caller, err := apiCaller(backend)
			if err != nil {
				return err
			}

			resp, err := caller.CallAPI(cmd.Context(), method, nil)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", serverURL, err)
			}

			if err := renderAPIResponse(cmd, resp, jqExpr, raw); err != nil {
				return err
			}

			// On an RPC-level failure the envelope is already printed; return the
			// sentinel (carrying the server's error text) so Execute exits 1.
			if !resp.OK {
				return fmt.Errorf("%w: %s", errAPIFailure, resp.Result)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "config file path")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter the normalized response with a jq expression")
	cmd.Flags().BoolVar(&raw, "raw", false, "print the server's exact response bytes, bypassing normalization")
	cmd.MarkFlagsMutuallyExclusive("jq", "raw")

	return cmd
}

// apiCaller type-asserts a torrent.Backend to the Transmission APICaller
// capability, returning a clear error when the backend lacks it. The generic
// call is a Transmission feature kept off the vendor-neutral torrent.Backend
// interface, so a non-Transmission backend produces a "not supported" error
// rather than a panic.
func apiCaller(backend torrent.Backend) (transmission.APICaller, error) {
	caller, ok := backend.(transmission.APICaller)
	if !ok {
		return nil, errors.New("backend does not support raw API calls")
	}

	return caller, nil
}

// renderAPIResponse writes the API response in the selected output mode. --raw
// echoes the server's exact bytes verbatim; otherwise the normalized envelope is
// rendered (filtered through --jq when given, else as indented JSON). JSON/jq
// output is never passed through output.SanitizeTerminal, consistent with the
// --json/--jq contract elsewhere.
func renderAPIResponse(cmd *cobra.Command, resp transmission.APIResponse, jqExpr string, raw bool) error {
	out := cmd.OutOrStdout()

	if raw {
		if _, err := out.Write(resp.Raw); err != nil {
			return fmt.Errorf("writing raw response: %w", err)
		}

		return nil
	}

	envelope := apiEnvelope{
		Result:    resp.Result,
		Arguments: resp.Arguments,
		Tag:       resp.Tag,
		Code:      resp.Code,
	}

	if jqExpr != "" {
		return output.JQ(out, envelope, jqExpr)
	}

	return output.JSON(out, envelope)
}
