package rpc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONRPCProtocolSessionGet(t *testing.T) {
	var rawBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"result": {"version": "4.1.0 (ae226418eb)", "rpc_version_semver": "6.0.0"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	session, err := jsonrpcProtocol{}.SessionGet(context.Background(), tr)
	require.NoError(t, err)

	assert.Equal(t, "4.1.0 (ae226418eb)", session.Version)
	assert.Equal(t, "6.0.0", session.RPCVersionSemver)
	assert.Equal(t, ProtocolJSONRPC, session.Protocol) //nolint:testifylint // encoded-compare false positive: ProtocolJSONRPC is an identifier name, not a JSON literal

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// JSON-RPC 2.0 envelope: jsonrpc/method/params/id, snake_case method.
	assert.Equal(t, "2.0", gotBody["jsonrpc"])
	assert.Equal(t, "session_get", gotBody["method"])
	assert.Contains(t, gotBody, "params")
	assert.Contains(t, gotBody, "id")
	assert.NotContains(t, gotBody, "arguments")
	assert.NotContains(t, gotBody, "tag")
}

func TestBespokeProtocolSessionGet(t *testing.T) {
	var rawBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{
			"arguments": {"version": "4.0.6 (38c164933e)", "rpc-version-semver": "5.4.0"},
			"result": "success",
			"tag": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	session, err := bespokeProtocol{}.SessionGet(context.Background(), tr)
	require.NoError(t, err)

	assert.Equal(t, "4.0.6 (38c164933e)", session.Version)
	assert.Equal(t, "5.4.0", session.RPCVersionSemver)
	assert.Equal(t, ProtocolBespoke, session.Protocol)

	var gotBody map[string]any
	require.NoError(t, json.Unmarshal(rawBody, &gotBody))

	// Bespoke envelope: method/arguments/tag, kebab-case method.
	assert.Equal(t, "session-get", gotBody["method"])
	assert.Contains(t, gotBody, "arguments")
	assert.Contains(t, gotBody, "tag")
	assert.NotContains(t, gotBody, "jsonrpc")
	assert.NotContains(t, gotBody, "params")
}

func TestJSONRPCProtocolSessionGetMapsErrorObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"jsonrpc": "2.0",
			"error": {"code": -32601, "message": "method not found"},
			"id": 1
		}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	_, err := jsonrpcProtocol{}.SessionGet(context.Background(), tr)
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "method not found")
}

func TestBespokeProtocolSessionGetMapsNonSuccessResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result": "no such session", "tag": 1}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	_, err := bespokeProtocol{}.SessionGet(context.Background(), tr)
	require.ErrorIs(t, err, ErrRPC)
	assert.ErrorContains(t, err, "no such session")
}

func TestProtocolNames(t *testing.T) {
	assert.Equal(t, ProtocolJSONRPC, jsonrpcProtocol{}.Name()) //nolint:testifylint // encoded-compare false positive: ProtocolJSONRPC is an identifier name, not a JSON literal
	assert.Equal(t, ProtocolBespoke, bespokeProtocol{}.Name())
}
