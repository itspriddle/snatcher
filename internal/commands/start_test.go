package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
)

// runStart executes the start command with args, returning stdout, stderr, and
// the Execute error. The command's output is a *bytes.Buffer, so the
// piped/plaintext path is exercised.
func runStart(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewStartCmd()
	AddGlobalFlags(cmd)

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

func TestStartByID(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runStart(t, "--config", cfgPath, "1")
	require.NoError(t, err)

	assert.Equal(t, "Started 1 torrent: ubuntu-24.04.iso\n", out)
}

func TestStartByHash(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runStart(t, "--config", cfgPath, fullHashTwo)
	require.NoError(t, err)

	assert.Equal(t, "Started 1 torrent: debian.iso\n", out)
}

func TestStartAll(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runStart(t, "--config", cfgPath, "--all")
	require.NoError(t, err)

	assert.Equal(t, "Started all 2 torrents.\n", out)
}

func TestStartNoSelectionErrors(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	_, _, err := runStart(t, "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "specify torrent ids or hashes, or use --all")
}

// TestStartNowSendsStartNowMethod asserts that --now routes to the
// torrent-start-now RPC method, while a plain start uses torrent-start. The stub
// records the action method (the non-torrent-get request) by reading the request
// body.
func TestStartNowSendsStartNowMethod(t *testing.T) {
	t.Run("with --now uses torrent-start-now", func(t *testing.T) {
		cfgPath, closeSrv, method := methodRecordingConfigPath(t)
		defer closeSrv()

		_, _, err := runStart(t, "--config", cfgPath, "--now", "1")
		require.NoError(t, err)
		assert.Equal(t, "torrent-start-now", method())
	})

	t.Run("without --now uses torrent-start", func(t *testing.T) {
		cfgPath, closeSrv, method := methodRecordingConfigPath(t)
		defer closeSrv()

		_, _, err := runStart(t, "--config", cfgPath, "1")
		require.NoError(t, err)
		assert.Equal(t, "torrent-start", method())
	})
}

func TestStartJSON(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runStart(t, "--config", cfgPath, "--json", "--now", "1")
	require.NoError(t, err)

	var got struct {
		Action   string `json:"action"`
		Count    int    `json:"count"`
		Torrents []struct {
			ID   int64  `json:"id"`
			Hash string `json:"hash"`
			Name string `json:"name"`
		} `json:"torrents"`
		Now       *bool    `json:"now"`
		Unmatched []string `json:"unmatched"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	assert.Equal(t, "start", got.Action)
	assert.Equal(t, 1, got.Count)
	require.Len(t, got.Torrents, 1)
	assert.Equal(t, int64(1), got.Torrents[0].ID)
	assert.Equal(t, fullHashOne, got.Torrents[0].Hash)
	assert.Equal(t, "ubuntu-24.04.iso", got.Torrents[0].Name)
	require.NotNil(t, got.Now, "start includes the now field")
	assert.True(t, *got.Now, "--now sets now=true in the JSON contract")
	assert.Empty(t, got.Unmatched, "no unmatched tokens when every selector matched")
}

func TestStartJSONNowFalse(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runStart(t, "--config", cfgPath, "--json", "1")
	require.NoError(t, err)

	var got struct {
		Now *bool `json:"now"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.NotNil(t, got.Now, "start always includes the now field")
	assert.False(t, *got.Now, "without --now the JSON contract reports now=false")
}

func TestStartJQ(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runStart(t, "--config", cfgPath, "--jq", ".torrents[].name", "--all")
	require.NoError(t, err)

	assert.Equal(t, `"ubuntu-24.04.iso"`+"\n"+`"debian.iso"`, strings.TrimSpace(out))
}

// methodRecordingConfigPath stands up a transmission stub that serves the
// confirmation torrent-get lookup and records the method name of the action
// request (the non-torrent-get call), so a test can assert which RPC method the
// command dispatched. It returns the config path, a cleanup func, and an accessor
// for the recorded action method.
func methodRecordingConfigPath(t *testing.T) (string, func(), func() string) {
	t.Helper()

	keyring.MockInit()

	var actionMethod string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(sessionIDHeader) == "" {
			w.Header().Set(sessionIDHeader, "session-token")
			w.WriteHeader(http.StatusConflict)

			return
		}

		body, _ := io.ReadAll(r.Body)

		var envelope struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &envelope)

		if envelope.Method == "torrent-get" {
			_, _ = w.Write([]byte(mutationListResponse))

			return
		}

		actionMethod = envelope.Method
		_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "id": 1}`))
	}))

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{Servers: map[string]config.Server{"test": {URL: srv.URL}}, DefaultServer: "test"}, cfgPath))

	return cfgPath, srv.Close, func() string { return actionMethod }
}
