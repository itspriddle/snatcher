package commands

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
