package rpc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSessionID = "abc123sessionid"

func TestDoSendsJSONContentType(t *testing.T) {
	var gotContentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{"result":"success"}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	body, err := tr.Do(context.Background(), []byte(`{}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"result":"success"}`, string(body))
	assert.Equal(t, "application/json", gotContentType)
}

func TestDoRetriesOn409AndCapturesSessionID(t *testing.T) {
	var (
		calls       int
		seenIDs     []string
		seenMethods []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		seenIDs = append(seenIDs, r.Header.Get(sessionIDHeader))
		seenMethods = append(seenMethods, r.Method)

		if calls == 1 {
			w.Header().Set(sessionIDHeader, testSessionID)
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("409 conflict"))
			return
		}

		_, _ = w.Write([]byte(`{"result":"success"}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	body, err := tr.Do(context.Background(), []byte(`{"method":"session-get"}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"result":"success"}`, string(body))

	require.Equal(t, 2, calls, "expected one 409 then a retry")
	assert.Empty(t, seenIDs[0], "first request carries no session id")
	assert.Equal(t, testSessionID, seenIDs[1], "retry carries captured session id")
	assert.Equal(t, []string{http.MethodPost, http.MethodPost}, seenMethods)
	assert.Equal(t, testSessionID, tr.getSessionID(), "session id is cached on the transport")
}

func TestDoReusesCachedSessionID(t *testing.T) {
	var calls atomic.Int32
	var seenIDs []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		seenIDs = append(seenIDs, r.Header.Get(sessionIDHeader))

		if n == 1 {
			w.Header().Set(sessionIDHeader, testSessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}

		_, _ = w.Write([]byte(`{"result":"success"}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	// First call: 409 then retry (2 server hits).
	_, err := tr.Do(context.Background(), []byte(`{}`))
	require.NoError(t, err)

	// Second call: the cached id is sent up front, so no 409 (1 server hit).
	_, err = tr.Do(context.Background(), []byte(`{}`))
	require.NoError(t, err)

	require.Equal(t, int32(3), calls.Load())
	// hits: [first(no id), retry(id), second(id)]
	assert.Equal(t, []string{"", testSessionID, testSessionID}, seenIDs)
}

func TestDoMapsUnauthorizedToAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	_, err := tr.Do(context.Background(), []byte(`{}`))
	require.ErrorIs(t, err, ErrAuth)
	assert.NotErrorIs(t, err, ErrTransport)
}

func TestDoMapsOtherNon2xxToTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	_, err := tr.Do(context.Background(), []byte(`{}`))
	require.ErrorIs(t, err, ErrTransport)
	assert.NotErrorIs(t, err, ErrAuth)
}

func TestDoSendsBasicAuth(t *testing.T) {
	var (
		gotUser string
		gotPass string
		gotOK   bool
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, gotOK = r.BasicAuth()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL, WithBasicAuth("alice", "s3cret"))

	_, err := tr.Do(context.Background(), []byte(`{}`))
	require.NoError(t, err)

	assert.True(t, gotOK)
	assert.Equal(t, "alice", gotUser)
	assert.Equal(t, "s3cret", gotPass)
}

func TestDoWithoutBasicAuthSendsNoAuthHeader(t *testing.T) {
	var hadAuthHeader bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuthHeader = r.Header["Authorization"]
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	_, err := tr.Do(context.Background(), []byte(`{}`))
	require.NoError(t, err)
	assert.False(t, hadAuthHeader)
}

func TestDoHonorsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the request runs

	_, err := tr.Do(ctx, []byte(`{}`))
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, ErrTransport)
}

func TestProbeReturnsResponseHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Transmission-Rpc-Version", "18")
		_, _ = w.Write([]byte(`{"result":"success"}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	body, headers, err := tr.Probe(context.Background(), []byte(`{}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"result":"success"}`, string(body))
	assert.Equal(t, "18", headers.Get("X-Transmission-Rpc-Version"))
}

func TestConcurrentDoIsRaceFree(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			w.Header().Set(sessionIDHeader, testSessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}

		_, _ = w.Write([]byte(`{"result":"success"}`))
	}))
	defer srv.Close()

	tr := NewTransport(srv.URL)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := tr.Do(context.Background(), []byte(`{}`))
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	assert.Equal(t, testSessionID, tr.getSessionID())
}

func TestDoWrapsHTTPClientError(t *testing.T) {
	tr := NewTransport("http://127.0.0.1:9091/transmission/rpc", WithHTTPClient(failingDoer{}))

	_, err := tr.Do(context.Background(), []byte(`{}`))
	require.ErrorIs(t, err, ErrTransport)
	require.ErrorIs(t, err, errDoer)
}

var errDoer = errors.New("boom")

type failingDoer struct{}

func (failingDoer) Do(*http.Request) (*http.Response, error) {
	return nil, errDoer
}
