package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
)

// sessionGetResponse is a bespoke session-get payload used as the canned answer
// for the api command's real call (the CSRF probe is answered separately by the
// stub's 409). The map key order in --raw output is the server's, so raw
// assertions use assert.JSONEq.
const sessionGetResponse = `{
	"arguments": {"version": "4.0.5", "rpc-version-semver": "5.4.0"},
	"result": "success",
	"tag": 1
}`

// runAPI executes the api command with args against a bytes.Buffer stdout,
// returning stdout, stderr, and the Execute error.
func runAPI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	return runAPIStdin(t, "", args...)
}

// runAPIStdin is runAPI with a custom stdin, exercising the @-/--input - paths.
func runAPIStdin(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewAPICmd()
	AddGlobalFlags(cmd)

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// apiConfigPath stands up a transmission stub serving body for the given method
// and writes a config pointing at it, returning the config path and a closer.
func apiConfigPath(t *testing.T, method, body string) (string, func()) {
	t.Helper()

	keyring.MockInit()

	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // bespoke path
		responses: map[string]string{
			method: body,
		},
	}.handler())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	return cfgPath, srv.Close
}

func TestAPIHappyPath(t *testing.T) {
	cfgPath, closeSrv := apiConfigPath(t, "session-get", sessionGetResponse)
	defer closeSrv()

	out, _, err := runAPI(t, "--config", cfgPath, "session-get")
	require.NoError(t, err)

	// The normalized envelope is rendered, not the raw server bytes.
	var got apiEnvelope
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "success", got.Result)
	assert.Equal(t, 1, got.Tag)
	assert.Zero(t, got.Code)
	assert.JSONEq(t, `{"version": "4.0.5", "rpc-version-semver": "5.4.0"}`, string(got.Arguments))
}

func TestAPIRPCErrorPrintsEnvelopeAndExits(t *testing.T) {
	const body = `{"arguments": {}, "result": "method name not recognized", "tag": 1}`

	cfgPath, closeSrv := apiConfigPath(t, "bogus-method", body)
	defer closeSrv()

	out, _, err := runAPI(t, "--config", cfgPath, "bogus-method")
	require.Error(t, err, "an RPC-level failure exits non-zero")
	assert.Contains(t, err.Error(), "method name not recognized")

	// The envelope is still printed to stdout despite the failure.
	var got apiEnvelope
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "method name not recognized", got.Result)
}

func TestAPIJQ(t *testing.T) {
	cfgPath, closeSrv := apiConfigPath(t, "session-get", sessionGetResponse)
	defer closeSrv()

	out, _, err := runAPI(t, "--config", cfgPath, "session-get", "--jq", ".arguments.version")
	require.NoError(t, err)

	assert.Equal(t, `"4.0.5"`, strings.TrimSpace(out))
}

func TestAPIRaw(t *testing.T) {
	cfgPath, closeSrv := apiConfigPath(t, "session-get", sessionGetResponse)
	defer closeSrv()

	out, _, err := runAPI(t, "--config", cfgPath, "session-get", "--raw")
	require.NoError(t, err)

	// --raw echoes the server's exact bytes, including its top-level shape and key
	// order (asserted structurally, since map order is not guaranteed).
	assert.JSONEq(t, sessionGetResponse, out)
}

func TestAPIRawAndJQMutuallyExclusive(t *testing.T) {
	cfgPath, closeSrv := apiConfigPath(t, "session-get", sessionGetResponse)
	defer closeSrv()

	_, _, err := runAPI(t, "--config", cfgPath, "session-get", "--raw", "--jq", ".result")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestAPIRequiresMethodArg(t *testing.T) {
	cfgPath, closeSrv := apiConfigPath(t, "session-get", sessionGetResponse)
	defer closeSrv()

	_, _, err := runAPI(t, "--config", cfgPath)
	require.Error(t, err)
}

func TestAPINotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runAPI(t, "--config", cfgPath, "session-get")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestBuildArgumentsFieldTypes(t *testing.T) {
	dir := t.TempDir()
	strFile := filepath.Join(dir, "name.txt")
	require.NoError(t, os.WriteFile(strFile, []byte("from-file\n"), 0o600))
	jsonFile := filepath.Join(dir, "ids.json")
	require.NoError(t, os.WriteFile(jsonFile, []byte(`[1, 2, 3]`), 0o600))

	tests := []struct {
		name      string
		rawFields []string
		fields    []string
		stdin     string
		want      string // expected arguments JSON; "" means nil
	}{
		{name: "no fields yields nil arguments", want: ""},
		{
			name:      "raw-field is always a string",
			rawFields: []string{"label=42"},
			want:      `{"label": "42"}`,
		},
		{
			name:   "field infers a number, preserving the literal",
			fields: []string{"speed-limit-down=512"},
			want:   `{"speed-limit-down": 512}`,
		},
		{
			name:   "field infers true/false/null literals",
			fields: []string{"a=true", "b=false", "c=null"},
			want:   `{"a": true, "b": false, "c": null}`,
		},
		{
			name:   "field falls back to a string",
			fields: []string{"name=hello world"},
			want:   `{"name": "hello world"}`,
		},
		{
			name:   "field @file reads file contents as a string",
			fields: []string{"name=@" + strFile},
			want:   `{"name": "from-file\n"}`,
		},
		{
			name:   "field @- reads stdin as a string",
			fields: []string{"note=@-"},
			stdin:  "piped value",
			want:   `{"note": "piped value"}`,
		},
		{
			name:   "field := parses raw JSON arrays",
			fields: []string{`ids:=[1,2,3]`},
			want:   `{"ids": [1, 2, 3]}`,
		},
		{
			name:   "field := parses raw JSON objects",
			fields: []string{`labels:={"a":1}`},
			want:   `{"labels": {"a": 1}}`,
		},
		{
			name:   "field :=@file parses file contents as JSON",
			fields: []string{"ids:=@" + jsonFile},
			want:   `{"ids": [1, 2, 3]}`,
		},
		{
			name:      "raw-field and field merge in order",
			rawFields: []string{"who=alice"},
			fields:    []string{"count=3"},
			want:      `{"who": "alice", "count": 3}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildArguments(strings.NewReader(tc.stdin), tc.rawFields, tc.fields, "")
			require.NoError(t, err)

			if tc.want == "" {
				assert.Nil(t, got)
				return
			}

			assert.JSONEq(t, tc.want, string(got))
		})
	}
}

func TestBuildArgumentsPreservesLargeIntegers(t *testing.T) {
	// 9007199254740993 = 2^53 + 1, which round-tripping through float64 corrupts.
	got, err := buildArguments(strings.NewReader(""), nil, []string{"ids:=[9007199254740993]"}, "")
	require.NoError(t, err)
	assert.Contains(t, string(got), "9007199254740993")

	// The inferred-number path must also keep the literal intact.
	got, err = buildArguments(strings.NewReader(""), nil, []string{"id=9007199254740993"}, "")
	require.NoError(t, err)
	assert.Contains(t, string(got), "9007199254740993")
}

func TestBuildArgumentsInputFromStdin(t *testing.T) {
	got, err := buildArguments(strings.NewReader(`{"ids": [1, 2]}`), nil, nil, "-")
	require.NoError(t, err)
	assert.JSONEq(t, `{"ids": [1, 2]}`, string(got))
}

func TestBuildArgumentsInputFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"fields": ["id", "name"]}`), 0o600))

	got, err := buildArguments(strings.NewReader(""), nil, nil, path)
	require.NoError(t, err)
	assert.JSONEq(t, `{"fields": ["id", "name"]}`, string(got))
}

func TestBuildArgumentsInvalidInputJSON(t *testing.T) {
	_, err := buildArguments(strings.NewReader("not json"), nil, nil, "-")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid JSON")
}

func TestBuildArgumentsInvalidRawJSONField(t *testing.T) {
	_, err := buildArguments(strings.NewReader(""), nil, []string{"ids:=[1,2"}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid JSON")
}

func TestBuildArgumentsMissingSeparator(t *testing.T) {
	_, err := buildArguments(strings.NewReader(""), []string{"noequals"}, nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected key=value")
}

func TestBuildArgumentsStdinContention(t *testing.T) {
	// Two @- fields both want stdin; the second must error.
	_, err := buildArguments(strings.NewReader("x"), nil, []string{"a=@-", "b=@-"}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stdin requested more than once")
}

func TestAPIInputAndFieldMutuallyExclusive(t *testing.T) {
	cfgPath, closeSrv := apiConfigPath(t, "torrent-get", sessionGetResponse)
	defer closeSrv()

	_, _, err := runAPI(t, "--config", cfgPath, "torrent-get", "--input", "-", "-F", "ids:=1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestAPIFieldsReachTheWire(t *testing.T) {
	keyring.MockInit()

	var gotArgs json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") == "" {
			w.Header().Set("X-Transmission-Session-Id", "session-token")
			w.WriteHeader(http.StatusConflict)
			return
		}

		var envelope struct {
			Method    string          `json:"method"`
			Arguments json.RawMessage `json:"arguments"`
		}
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&envelope))

		// Record the arguments from the real call (not the session-get handshake).
		if envelope.Method == "torrent-get" {
			gotArgs = envelope.Arguments
		}

		_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	_, _, err := runAPI(t, "--config", cfgPath, "torrent-get",
		"-F", "ids:=[9007199254740993]",
		"-F", `fields:=["id","name"]`,
	)
	require.NoError(t, err)

	require.NotNil(t, gotArgs, "the real call must carry the assembled arguments")
	assert.JSONEq(t, `{"ids": [9007199254740993], "fields": ["id", "name"]}`, string(gotArgs))
	// The literal large integer must survive verbatim (not coerced to float64).
	assert.Contains(t, string(gotArgs), "9007199254740993")
}

// prowlarrAPIRecord captures what "api --prowlarr" put on the wire, so a test
// can assert the verb, the expanded path, and which half of the request the
// field flags landed in.
type prowlarrAPIRecord struct {
	method string
	path   string
	query  url.Values
	body   string
	apiKey string
}

// prowlarrAPIStub answers every request with status and body, recording the
// request first.
type prowlarrAPIStub struct {
	status int
	body   string
	got    *prowlarrAPIRecord
}

func (s prowlarrAPIStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		*s.got = prowlarrAPIRecord{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.URL.Query(),
			body:   string(body),
			apiKey: r.Header.Get("X-Api-Key"),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(s.body))
	}
}

// prowlarrAPIConfig stands up a Prowlarr stub answering with status and body,
// and writes a config registering it as the default Prowlarr server. It returns
// the config path and a pointer to the recorded request.
func prowlarrAPIConfig(t *testing.T, status int, body string) (string, *prowlarrAPIRecord) {
	t.Helper()

	keyring.MockInit()

	got := new(prowlarrAPIRecord)
	srv := httptest.NewServer(prowlarrAPIStub{status: status, body: body, got: got}.handler())
	t.Cleanup(srv.Close)

	cfgPath := writeConfigFile(t, fmt.Sprintf(
		`{"servers": {"indexers": {"url": %q, "type": "prowlarr"}}, "default_prowlarr_server": "indexers"}`,
		srv.URL))

	return cfgPath, got
}

// indexerListBody is a canned Prowlarr /api/v1/indexer answer.
const indexerListBody = `[{"id": 3, "name": "Example Tracker"}, {"id": 5, "name": "Example Usenet"}]`

func TestAPIProwlarrExpandsABarePathAndDefaultsToGET(t *testing.T) {
	cfgPath, got := prowlarrAPIConfig(t, http.StatusOK, indexerListBody)

	out, _, err := runAPI(t, "--config", cfgPath, "--prowlarr", "indexer")
	require.NoError(t, err)

	assert.Equal(t, http.MethodGet, got.method)
	assert.Equal(t, "/api/v1/indexer", got.path)
	assert.Empty(t, got.body)

	// The body is printed as indented JSON, with no result/arguments/tag
	// envelope wrapped around it.
	assert.JSONEq(t, indexerListBody, out)
	assert.NotContains(t, out, `"arguments"`)
}

func TestAPIProwlarrUsesAnAbsolutePathVerbatim(t *testing.T) {
	cfgPath, got := prowlarrAPIConfig(t, http.StatusOK, `{"appName": "Prowlarr"}`)

	_, _, err := runAPI(t, "--config", cfgPath, "--prowlarr", "/ping")
	require.NoError(t, err)

	assert.Equal(t, "/ping", got.path)
}

func TestAPIProwlarrFieldsBecomeQueryParametersOnGET(t *testing.T) {
	cfgPath, got := prowlarrAPIConfig(t, http.StatusOK, "[]")

	_, _, err := runAPI(t, "--config", cfgPath, "--prowlarr", "search",
		"-f", "query=ubuntu 24.04",
		"-F", "limit=25",
		"-F", "indexerIds=3",
		"-F", "indexerIds=5",
	)
	require.NoError(t, err)

	assert.Empty(t, got.body, "a GET carries no body")
	assert.Equal(t, "ubuntu 24.04", got.query.Get("query"), "a JSON string is unquoted for the query")
	assert.Equal(t, "25", got.query.Get("limit"))
	// A repeated key stays repeated: that is how Prowlarr binds an array.
	assert.Equal(t, []string{"3", "5"}, got.query["indexerIds"])
}

func TestAPIProwlarrFieldsBecomeAJSONBodyOnPOST(t *testing.T) {
	cfgPath, got := prowlarrAPIConfig(t, http.StatusOK, `{}`)

	_, _, err := runAPI(t, "--config", cfgPath, "--prowlarr", "-X", "post", "indexer/test",
		"-f", "name=Example",
		"-F", "id:=7",
	)
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, got.method, "the verb is normalized to its canonical spelling")
	assert.Empty(t, got.query)
	assert.JSONEq(t, `{"name": "Example", "id": 7}`, got.body)
}

func TestAPIProwlarrInputIsTheBodyVerbatim(t *testing.T) {
	cfgPath, got := prowlarrAPIConfig(t, http.StatusOK, `{}`)

	_, _, err := runAPIStdin(t, `{"guid": "abc", "indexerId": 3}`,
		"--config", cfgPath, "--prowlarr", "-X", "POST", "search", "--input", "-")
	require.NoError(t, err)

	assert.JSONEq(t, `{"guid": "abc", "indexerId": 3}`, got.body)
}

func TestAPIProwlarrInputBecomesQueryParametersOnDELETE(t *testing.T) {
	cfgPath, got := prowlarrAPIConfig(t, http.StatusOK, `{}`)

	_, _, err := runAPIStdin(t, `{"deleteFiles": true, "name": "old"}`,
		"--config", cfgPath, "--prowlarr", "-X", "DELETE", "indexer/7", "--input", "-")
	require.NoError(t, err)

	assert.Equal(t, http.MethodDelete, got.method)
	assert.Empty(t, got.body)
	assert.Equal(t, "true", got.query.Get("deleteFiles"))
	assert.Equal(t, "old", got.query.Get("name"))
}

func TestAPIProwlarrRejectsANonObjectInputForAQuery(t *testing.T) {
	cfgPath, got := prowlarrAPIConfig(t, http.StatusOK, "[]")

	_, _, err := runAPIStdin(t, `[1, 2]`, "--config", cfgPath, "--prowlarr", "search", "--input", "-")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a JSON object")
	assert.Empty(t, got.method, "the request is refused before anything is sent")
}

func TestAPIProwlarrRaw(t *testing.T) {
	const body = `{"appName":"Prowlarr","version":"1.30.2"}`

	cfgPath, _ := prowlarrAPIConfig(t, http.StatusOK, body)

	out, _, err := runAPI(t, "--config", cfgPath, "--prowlarr", "system/status", "--raw")
	require.NoError(t, err)

	// --raw echoes the server's exact bytes, so this is deliberately a
	// byte-for-byte comparison: JSONEq would pass on a re-indented body too.
	assert.Equal(t, body, out) //nolint:testifylint // encoded-compare: byte-exactness is the assertion
}

func TestAPIProwlarrJQ(t *testing.T) {
	cfgPath, _ := prowlarrAPIConfig(t, http.StatusOK, indexerListBody)

	out, _, err := runAPI(t, "--config", cfgPath, "--prowlarr", "indexer", "--jq", ".[0].name")
	require.NoError(t, err)

	assert.Equal(t, `"Example Tracker"`, strings.TrimSpace(out))
}

func TestAPIProwlarrNonSuccessPrintsTheBodyAndExits(t *testing.T) {
	const body = `{"message": "NotFound"}`

	cfgPath, _ := prowlarrAPIConfig(t, http.StatusNotFound, body)

	out, _, err := runAPI(t, "--config", cfgPath, "--prowlarr", "indexer/999")
	require.Error(t, err, "a non-2xx response exits non-zero")
	assert.Contains(t, err.Error(), "HTTP 404")

	// The server's own body is still printed, as the Transmission path prints
	// its envelope before failing.
	assert.JSONEq(t, body, out)
}

func TestAPIProwlarrPrintsANonJSONBodyVerbatim(t *testing.T) {
	const body = "<html><body>502 Bad Gateway</body></html>"

	cfgPath, _ := prowlarrAPIConfig(t, http.StatusBadGateway, body)

	out, _, err := runAPI(t, "--config", cfgPath, "--prowlarr", "indexer")
	require.Error(t, err)
	assert.Equal(t, body, out)
}

func TestAPIMethodRequiresProwlarr(t *testing.T) {
	cfgPath, closeSrv := apiConfigPath(t, "session-get", sessionGetResponse)
	defer closeSrv()

	_, _, err := runAPI(t, "--config", cfgPath, "session-get", "-X", "POST")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--method only applies to --prowlarr")
}

func TestAPIProwlarrRejectsAnUnknownMethod(t *testing.T) {
	cfgPath, got := prowlarrAPIConfig(t, http.StatusOK, "[]")

	_, _, err := runAPI(t, "--config", cfgPath, "--prowlarr", "indexer", "-X", "FETCH")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid --method "FETCH"`)
	assert.Empty(t, got.method, "an unknown verb is refused before dialing")
}

func TestAPIProwlarrRequiresAPath(t *testing.T) {
	cfgPath, _ := prowlarrAPIConfig(t, http.StatusOK, "[]")

	_, _, err := runAPI(t, "--config", cfgPath, "--prowlarr", "  ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "REST path is required")
}

func TestAPIProwlarrRefusesATransmissionServer(t *testing.T) {
	keyring.MockInit()

	cfgPath := writeConfigFile(t,
		`{"servers": {"local": {"url": "http://local:9091/transmission/rpc"}}, "default_server": "local"}`)

	_, _, err := runAPI(t, "--config", cfgPath, "--prowlarr", "--server", "local", "indexer")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `server "local" is a Transmission server`)
}

// TestAPIProwlarrNeverPrintsTheAPIKey covers every render mode: the key reaches
// the wire as a header and nothing else.
func TestAPIProwlarrNeverPrintsTheAPIKey(t *testing.T) {
	const apiKey = "super-secret-api-key"

	cfgPath, got := prowlarrAPIConfig(t, http.StatusOK, indexerListBody)
	t.Setenv(envAPIKey, apiKey)

	for _, args := range [][]string{
		{"--prowlarr", "indexer"},
		{"--prowlarr", "indexer", "--raw"},
		{"--prowlarr", "indexer", "--jq", "."},
	} {
		out, errOut, err := runAPI(t, append([]string{"--config", cfgPath}, args...)...)
		require.NoErrorf(t, err, "%v", args)
		assert.NotContainsf(t, out+errOut, apiKey, "%v", args)
		assert.Equalf(t, apiKey, got.apiKey, "%v", args)
	}
}
