package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/prowlarr"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// errAPIFailure is the sentinel wrapped (after the envelope has already been
// printed) when an RPC call fails at the RPC level. It drives the non-zero exit
// code without re-rendering: the command has already written the envelope to
// stdout, so the returned error only flips Execute's exit code. Execute prints
// it as an extra "Error: …" stderr line, matching gh api.
var errAPIFailure = errors.New("RPC call failed")

// errAPIStatus is errAPIFailure's REST counterpart, wrapped after a Prowlarr
// response body has already been printed. Prowlarr reports failure with an HTTP
// status rather than an envelope, so the two cannot share a message.
var errAPIStatus = errors.New("request failed")

// apiMethods lists the HTTP verbs --method accepts. An escape hatch should not
// second-guess which endpoint takes which verb, but it should catch a typo
// before the request builder turns it into an opaque transport error.
var apiMethods = []string{
	http.MethodGet,
	http.MethodHead,
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
	http.MethodOptions,
}

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
// response. The arguments object is built from gh-style field flags (-f/-F) or a
// raw JSON body (--input).
func NewAPICmd() *cobra.Command {
	var (
		server       string
		jqExpr       string
		raw          bool
		rawFields    []string
		fields       []string
		input        string
		prowlarrMode bool
		method       string
	)

	cmd := &cobra.Command{
		Use:   "api <method|path>",
		Short: "Make an arbitrary Transmission RPC or Prowlarr REST call",
		Long: "Make an arbitrary Transmission RPC call and print the response.\n\n" +
			"Issues the given canonical kebab-case RPC method (e.g. session-get, " +
			"torrent-get, queue-move-top) and prints a normalized response envelope " +
			"that is identical whether the server speaks the bespoke (<=4.0) or " +
			"JSON-RPC 2.0 (>=4.1) dialect.\n\n" +
			"snatcher translates the method name only (bespoke sends it verbatim; " +
			"JSON-RPC replaces '-' with '_'). It never rewrites argument KEYS or " +
			"VALUES (e.g. recently-active vs recently_active, hashString vs " +
			"hash_string); those are your responsibility and are not portable " +
			"across Transmission dialect versions.\n\n" +
			"Build the arguments object with field flags or a raw body:\n" +
			"  -f, --raw-field key=value   add a string field\n" +
			"  -F, --field key=value       add a type-inferred field:\n" +
			"                                true/false/null  -> JSON literal\n" +
			"                                123 / 1.5         -> JSON number\n" +
			"                                @file / @-        -> file/stdin as a string\n" +
			"                                anything else     -> string\n" +
			"                              key:=value parses value as raw JSON;\n" +
			"                              key:=@file parses a file's contents as JSON.\n" +
			"  --input <file|->            supply the whole arguments object as raw\n" +
			"                              JSON; mutually exclusive with -f/-F.\n\n" +
			"At most one stdin source (--input - or a single @-) may be used.\n\n" +
			"An RPC-level failure still prints the envelope, then exits 1.\n\n" +
			"With --prowlarr the positional is a Prowlarr REST PATH instead, called " +
			"on the configured Prowlarr server (--server must name a Prowlarr " +
			"alias). A path starting with '/' is used verbatim against the server's " +
			"base URL; a bare path is expanded to /api/v1/<path>. -X sets the HTTP " +
			"method (default GET).\n\n" +
			"The two modes print different shapes. Transmission prints the " +
			"normalized result/arguments/tag envelope; Prowlarr has no envelope, so " +
			"its response body is printed as indented JSON exactly as the server " +
			"sent it. --raw prints the exact bytes in both modes, and --jq filters " +
			"whatever the mode would have printed.\n\n" +
			"Under --prowlarr the field flags follow the verb: -f/-F/--input become " +
			"QUERY PARAMETERS for GET and DELETE (a repeated key stays repeated, " +
			"which is how Prowlarr binds an array), and a JSON REQUEST BODY for every " +
			"other verb, with --input supplying that body verbatim. A non-2xx " +
			"response prints the server's body, then exits 1.\n\n" +
			"Examples:\n" +
			"  snatcher api session-get\n" +
			"  snatcher api session-get --jq '.arguments.version'\n" +
			"  snatcher api session-get --raw\n" +
			"  snatcher api torrent-get -F ids:=1 -F fields:='[\"id\",\"name\"]' --jq '.arguments.torrents'\n" +
			"  snatcher api session-set -F speed-limit-down=512 -F speed-limit-down-enabled=true\n" +
			"  snatcher api --prowlarr indexer --jq '.[].name'\n" +
			"  snatcher api --prowlarr search -f query=ubuntu -f indexerIds=3\n" +
			"  snatcher api --prowlarr -X DELETE indexer/7",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateAPIMode(prowlarrMode, cmd.Flags().Changed("method")); err != nil {
				return err
			}

			if prowlarrMode {
				return runProwlarrAPI(cmd, prowlarrAPICall{
					path:      args[0],
					method:    method,
					server:    server,
					jqExpr:    jqExpr,
					raw:       raw,
					rawFields: rawFields,
					fields:    fields,
					input:     input,
				})
			}

			rpcMethod := args[0]

			arguments, err := buildArguments(cmd.InOrStdin(), rawFields, fields, input)
			if err != nil {
				return err
			}

			backend, serverURL, err := dial(cmd, server)
			if err != nil {
				return err
			}

			resp, err := backend.CallAPI(cmd.Context(), rpcMethod, arguments)
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

	addServerFlag(cmd, &server)
	cmd.Flags().BoolVar(&prowlarrMode, "prowlarr", false, "call the configured Prowlarr server, treating the positional as a REST path")
	cmd.Flags().StringVarP(&method, "method", "X", http.MethodGet, "HTTP method for --prowlarr (GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS)")
	cmd.Flags().StringVar(&jqExpr, "jq", "", "filter the normalized response with a jq expression")
	cmd.Flags().BoolVar(&raw, "raw", false, "print the server's exact response bytes, bypassing normalization")
	cmd.Flags().StringArrayVarP(&rawFields, "raw-field", "f", nil, "add a string argument field (key=value); repeatable")
	cmd.Flags().StringArrayVarP(&fields, "field", "F", nil, "add a type-inferred argument field (key=value, key:=json); repeatable")
	cmd.Flags().StringVar(&input, "input", "", "read the entire arguments object as raw JSON from a file or '-' for stdin")
	cmd.MarkFlagsMutuallyExclusive("jq", "raw")
	cmd.MarkFlagsMutuallyExclusive("input", "raw-field")
	cmd.MarkFlagsMutuallyExclusive("input", "field")

	return cmd
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

// validateAPIMode rejects a flag that names a concept the selected backend does
// not have, before anything is read or dialed.
//
// --method is an HTTP verb, and Transmission RPC has none: every call is a POST
// of a method name, so a -X there would be silently ignored. Nothing runs the
// other way — the Transmission mode's own flags (-f/-F/--input, --jq, --raw,
// --server) all carry over to the REST mode with the same meaning — so this is
// the single crossed combination there is to reject.
func validateAPIMode(prowlarrMode, methodChanged bool) error {
	if methodChanged && !prowlarrMode {
		return errors.New("--method only applies to --prowlarr: a Transmission RPC call is always a POST of the method name")
	}

	return nil
}

// prowlarrAPICall is everything "api --prowlarr" needs: the REST path, the verb,
// and the shared flags, gathered so the Prowlarr branch reads as one call rather
// than an eight-argument function.
type prowlarrAPICall struct {
	path      string
	method    string
	server    string
	jqExpr    string
	raw       bool
	rawFields []string
	fields    []string
	input     string
}

// runProwlarrAPI issues the raw REST call described by call and renders the
// instance's answer. A non-2xx response is printed and then exits 1, mirroring
// how the Transmission path prints its envelope before failing.
func runProwlarrAPI(cmd *cobra.Command, call prowlarrAPICall) error {
	req, err := buildProwlarrRequest(cmd.InOrStdin(), call)
	if err != nil {
		return err
	}

	client, serverURL, err := dialProwlarr(cmd, call.server)
	if err != nil {
		return err
	}

	resp, err := client.CallAPI(cmd.Context(), req)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", serverURL, err)
	}

	if err := renderProwlarrAPIResponse(cmd, resp, call.jqExpr, call.raw); err != nil {
		return err
	}

	if !resp.OK {
		return fmt.Errorf("%w: HTTP %d", errAPIStatus, resp.StatusCode)
	}

	return nil
}

// buildProwlarrRequest turns the command line into a REST request: the verb, the
// expanded path, and the field sources mapped onto whichever half of the request
// that verb carries them in.
func buildProwlarrRequest(stdin io.Reader, call prowlarrAPICall) (prowlarr.Request, error) {
	method, err := parseAPIMethod(call.method)
	if err != nil {
		return prowlarr.Request{}, err
	}

	if strings.TrimSpace(call.path) == "" {
		return prowlarr.Request{}, errors.New("a Prowlarr REST path is required (e.g. 'indexer', or '/api/v1/indexer')")
	}

	req := prowlarr.Request{Method: method, Path: prowlarr.APIPath(call.path)}

	// GET and DELETE carry no body in Prowlarr's API, so their parameters can
	// only travel in the query string.
	if method == http.MethodGet || method == http.MethodDelete {
		query, err := buildQuery(stdin, call.rawFields, call.fields, call.input)
		if err != nil {
			return prowlarr.Request{}, err
		}

		req.Query = query

		return req, nil
	}

	body, err := buildArguments(stdin, call.rawFields, call.fields, call.input)
	if err != nil {
		return prowlarr.Request{}, err
	}

	req.Body = body

	return req, nil
}

// parseAPIMethod normalizes an operator-written --method to its canonical
// spelling, matching case-insensitively and tolerating surrounding whitespace as
// every other value flag in snatcher does. An unrecognized verb errors naming
// the valid ones rather than becoming an opaque request-building failure.
func parseAPIMethod(raw string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(raw))

	for _, method := range apiMethods {
		if normalized == method {
			return method, nil
		}
	}

	return "", fmt.Errorf("invalid --method %q: valid values are %s", raw, strings.Join(apiMethods, ", "))
}

// buildQuery maps the field sources onto a query string, for the verbs that
// carry no body.
//
// Each field contributes one parameter in the order given, and a repeated key
// stays repeated: a query string is multi-valued, and repeating a key is how
// Prowlarr binds an array parameter (indexerIds=1&indexerIds=2). That is the one
// place this differs from the JSON-object path, where a repeated key cannot
// exist and the last value wins. An --input document contributes its top-level
// object keys, since a whole JSON body has no other query form.
func buildQuery(stdin io.Reader, rawFields, fields []string, input string) (url.Values, error) {
	specs, err := queryFields(stdin, rawFields, fields, input)
	if err != nil {
		return nil, err
	}

	if len(specs) == 0 {
		return nil, nil
	}

	query := url.Values{}
	for _, f := range specs {
		query.Add(f.key, queryValue(f.value))
	}

	return query, nil
}

// queryFields collects the fields a query string is built from, from --input
// (already enforced mutually exclusive with -f/-F by cobra) or from the field
// flags.
func queryFields(stdin io.Reader, rawFields, fields []string, input string) ([]argField, error) {
	if input != "" {
		return inputFields(stdin, input)
	}

	return parseFieldSpecs(stdin, rawFields, fields)
}

// inputFields decodes an --input document into query fields. It must be a JSON
// object: its keys are the parameters, and no other JSON value has a
// query-string form. Order does not matter here — url.Values.Encode sorts.
func inputFields(stdin io.Reader, input string) ([]argField, error) {
	data, err := readSource(stdin, input, new(bool))
	if err != nil {
		return nil, fmt.Errorf("reading --input: %w", err)
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, fmt.Errorf("--input must be a JSON object to become query parameters: %s", input)
	}

	specs := make([]argField, 0, len(object))
	for key, value := range object {
		specs = append(specs, argField{key: key, value: value})
	}

	return specs, nil
}

// queryValue renders a field's JSON fragment as a query parameter value: a JSON
// string contributes its unquoted text, so -f query=ubuntu sends query=ubuntu
// rather than query="ubuntu", and every other fragment (a number, a bool, an
// array, an object) contributes its literal JSON.
func queryValue(value json.RawMessage) string {
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		return text
	}

	return string(value)
}

// renderProwlarrAPIResponse writes the instance's answer in the selected output
// mode.
//
// There is no envelope to normalize — Prowlarr answers with the resource itself
// — so the default rendering is the response body re-indented, --raw echoes the
// exact bytes, and --jq filters the decoded body. A body that is not JSON at all
// (an HTML page from a reverse proxy, or the empty body of a successful DELETE)
// is written verbatim in every mode, there being nothing to indent or filter.
func renderProwlarrAPIResponse(cmd *cobra.Command, resp prowlarr.APIResponse, jqExpr string, raw bool) error {
	out := cmd.OutOrStdout()

	if raw || !json.Valid(resp.Raw) {
		if _, err := out.Write(resp.Raw); err != nil {
			return fmt.Errorf("writing raw response: %w", err)
		}

		return nil
	}

	body := json.RawMessage(resp.Raw)

	if jqExpr != "" {
		return output.JQ(out, body, jqExpr)
	}

	return output.JSON(out, body)
}

// argField is a single key/value pair of the assembled arguments object. value
// is a pre-validated JSON fragment (a quoted string, number, bool, null, array,
// or object), kept as raw bytes to preserve integer fidelity — round-tripping
// through map[string]any would coerce large ids to float64.
type argField struct {
	key   string
	value json.RawMessage
}

// buildArguments assembles the RPC arguments object from the field flags or a
// raw --input body. --input (already enforced mutually exclusive with -f/-F by
// cobra) supplies the whole object as validated JSON from a file or stdin.
// Otherwise the -f (string) and -F (type-inferred) fields are merged in order
// into a JSON object built from validated json.RawMessage fragments. With no
// fields and no input, nil is returned (a zero-argument method). At most one
// stdin source (--input - or a single @-) may be consumed per invocation.
func buildArguments(stdin io.Reader, rawFields, fields []string, input string) (json.RawMessage, error) {
	if input != "" {
		data, err := readSource(stdin, input, new(bool))
		if err != nil {
			return nil, fmt.Errorf("reading --input: %w", err)
		}

		if !json.Valid(data) {
			return nil, fmt.Errorf("--input is not valid JSON: %s", input)
		}

		return json.RawMessage(data), nil
	}

	if len(rawFields) == 0 && len(fields) == 0 {
		return nil, nil
	}

	specs, err := parseFieldSpecs(stdin, rawFields, fields)
	if err != nil {
		return nil, err
	}

	var assembled []argField
	for _, f := range specs {
		assembled = upsertField(assembled, f.key, f.value)
	}

	return encodeFields(assembled), nil
}

// parseFieldSpecs parses the -f (string) and -F (type-inferred) flags into
// ordered key/value fields, -f first, which is the order they merge in.
//
// No de-duplication happens here: a JSON object cannot hold a repeated key, but
// a query string can, so each consumer decides what a repeat means (see
// buildArguments and buildQuery).
func parseFieldSpecs(stdin io.Reader, rawFields, fields []string) ([]argField, error) {
	// stdinUsed guards against multiple @- (or @- alongside --input -) consumers:
	// stdin can only be read once.
	stdinUsed := new(bool)

	assembled := make([]argField, 0, len(rawFields)+len(fields))

	for _, f := range rawFields {
		key, value, err := parseRawField(f)
		if err != nil {
			return nil, err
		}

		assembled = append(assembled, argField{key: key, value: value})
	}

	for _, f := range fields {
		key, value, err := parseTypedField(stdin, f, stdinUsed)
		if err != nil {
			return nil, err
		}

		assembled = append(assembled, argField{key: key, value: value})
	}

	return assembled, nil
}

// upsertField appends key/value to fields, or overwrites the value in place when
// key already exists (last value wins, original position preserved), mirroring
// gh api's behavior for repeated keys.
func upsertField(fields []argField, key string, value json.RawMessage) []argField {
	for i := range fields {
		if fields[i].key == key {
			fields[i].value = value
			return fields
		}
	}

	return append(fields, argField{key: key, value: value})
}

// encodeFields renders the assembled fields as a JSON object, splicing each
// pre-validated value fragment in verbatim so integer fidelity is preserved.
func encodeFields(fields []argField) json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('{')

	for i, f := range fields {
		if i > 0 {
			buf.WriteByte(',')
		}

		// json.Marshal a string always produces a valid, escaped JSON key.
		quotedKey, _ := json.Marshal(f.key)
		buf.Write(quotedKey)
		buf.WriteByte(':')
		buf.Write(f.value)
	}

	buf.WriteByte('}')

	return json.RawMessage(buf.Bytes())
}

// parseRawField splits a -f/--raw-field "key=value" into a key and a JSON string
// value. The value is always treated as a literal string (never type-inferred),
// matching gh api's --raw-field.
func parseRawField(spec string) (string, json.RawMessage, error) {
	key, value, found := strings.Cut(spec, "=")
	if !found {
		return "", nil, fmt.Errorf("invalid field %q: expected key=value", spec)
	}

	return key, jsonString(value), nil
}

// parseTypedField parses a -F/--field spec into a key and a JSON value fragment.
// The "key:=value" form parses value as raw JSON (with "key:=@file" reading JSON
// from a file). The "key=value" form infers the type: true/false/null become
// JSON literals, an integer/number becomes a JSON number, @file/@- read
// file/stdin contents as a string, and anything else is a string.
func parseTypedField(stdin io.Reader, spec string, stdinUsed *bool) (string, json.RawMessage, error) {
	// The raw-JSON form (key:=value) takes precedence; check it before the plain
	// "=" split so a "key:=..." spec is not mis-parsed as key "key:".
	if key, value, found := strings.Cut(spec, ":="); found {
		raw, err := resolveJSONValue(stdin, value, stdinUsed)
		if err != nil {
			return "", nil, fmt.Errorf("field %q: %w", key, err)
		}

		return key, raw, nil
	}

	key, value, found := strings.Cut(spec, "=")
	if !found {
		return "", nil, fmt.Errorf("invalid field %q: expected key=value or key:=json", spec)
	}

	raw, err := inferValue(stdin, value, stdinUsed)
	if err != nil {
		return "", nil, fmt.Errorf("field %q: %w", key, err)
	}

	return key, raw, nil
}

// resolveJSONValue handles the value side of a "key:=" field: "@file"/"@-" reads
// JSON from a file or stdin, otherwise the literal text is the JSON. The result
// is validated as JSON.
func resolveJSONValue(stdin io.Reader, value string, stdinUsed *bool) (json.RawMessage, error) {
	data := []byte(value)

	if after, ok := strings.CutPrefix(value, "@"); ok {
		read, err := readSource(stdin, after, stdinUsed)
		if err != nil {
			return nil, err
		}
		data = read
	}

	if !json.Valid(data) {
		return nil, fmt.Errorf("value is not valid JSON: %s", value)
	}

	return json.RawMessage(data), nil
}

// inferValue maps a plain -F value to a JSON fragment: true/false/null literals,
// numeric literals (preserved verbatim for integer fidelity), @file/@- file or
// stdin contents as a string, otherwise the value as a JSON string.
func inferValue(stdin io.Reader, value string, stdinUsed *bool) (json.RawMessage, error) {
	switch value {
	case "true", "false", "null":
		return json.RawMessage(value), nil
	}

	if after, ok := strings.CutPrefix(value, "@"); ok {
		data, err := readSource(stdin, after, stdinUsed)
		if err != nil {
			return nil, err
		}

		return jsonString(string(data)), nil
	}

	if isJSONNumber(value) {
		// Splice the numeric literal in verbatim so a large integer id is not
		// degraded to float64.
		return json.RawMessage(value), nil
	}

	return jsonString(value), nil
}

// readSource returns the contents of a -F/--input source: "-" reads stdin (once
// per invocation, guarded by stdinUsed), any other value is a file path.
func readSource(stdin io.Reader, source string, stdinUsed *bool) ([]byte, error) {
	if source == "-" {
		if *stdinUsed {
			return nil, errors.New("stdin requested more than once (only one of --input - or a single @- is allowed)")
		}
		*stdinUsed = true

		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}

		return data, nil
	}

	data, err := os.ReadFile(source)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	return data, nil
}

// jsonString encodes s as a JSON string literal. json.Marshal of a string never
// fails, so the error is discarded.
func jsonString(s string) json.RawMessage {
	quoted, _ := json.Marshal(s)
	return json.RawMessage(quoted)
}

// isJSONNumber reports whether s is a valid JSON number literal. It rejects
// leading-plus, leading zeros, and other forms json.Valid would also reject, so
// a bare numeric value is emitted as a number and anything else falls back to a
// string.
func isJSONNumber(s string) bool {
	if s == "" {
		return false
	}

	// strconv accepts forms JSON does not (e.g. "0x1", "1_000", "Inf"); require
	// json.Valid too so only canonical JSON numbers are treated as numbers.
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return false
	}

	return json.Valid([]byte(s))
}
