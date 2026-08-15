package prowlarr

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// systemStatusBody is a trimmed GET /api/v1/system/status response, carrying
// fields snatcher does not model so the decode is proven to tolerate them.
const systemStatusBody = `{
  "appName": "Prowlarr",
  "instanceName": "Basement",
  "version": "1.37.0.5076",
  "isProduction": true,
  "runtimeVersion": "6.0.36",
  "urlBase": ""
}`

// newTestClient builds a Client for baseURL, failing the test if the URL is
// rejected.
func newTestClient(t *testing.T, baseURL string, opts ...Option) *Client {
	t.Helper()

	client, err := NewClient(baseURL, opts...)
	require.NoError(t, err)

	return client
}

func TestSystemStatusDecodesTheInstanceDescription(t *testing.T) {
	var (
		gotPath   string
		gotMethod string
		gotKey    string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotKey = r.Header.Get(apiKeyHeader)
		_, _ = w.Write([]byte(systemStatusBody))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	status, err := client.SystemStatus(t.Context())
	require.NoError(t, err)

	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Equal(t, "/api/v1/system/status", gotPath)
	assert.Equal(t, testAPIKey, gotKey)

	assert.Equal(t, SystemStatus{
		AppName:      "Prowlarr",
		InstanceName: "Basement",
		Version:      "1.37.0.5076",
	}, status)
}

func TestSystemStatusUsesThePathPrefixOfTheBaseURL(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(systemStatusBody))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL+"/prowlarr/", WithAPIKey(testAPIKey))

	_, err := client.SystemStatus(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "/prowlarr/api/v1/system/status", gotPath)
}

func TestSystemStatusMapsUnauthorizedToAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.SystemStatus(t.Context())
	require.ErrorIs(t, err, ErrAuth)
	assert.NotContains(t, err.Error(), testAPIKey)
}

func TestSystemStatusMapsAnUndecodableBodyToServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html>not json</html>`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL, WithAPIKey(testAPIKey))

	_, err := client.SystemStatus(t.Context())
	require.ErrorIs(t, err, ErrServer)
	assert.Contains(t, err.Error(), "decoding system status")
}

func TestNewClientRejectsAnUnusableBaseURL(t *testing.T) {
	_, err := NewClient("")
	require.ErrorIs(t, err, ErrUnreachable)
}
