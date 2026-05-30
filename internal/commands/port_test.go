package commands

import (
	"bytes"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
)

// runPort executes the port command tree with args, returning stdout, stderr,
// and the Execute error. The command's output is a *bytes.Buffer, so isTTY
// reports false where it matters.
func runPort(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewPortCmd()

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// portConfigPath stands up a transmission stub serving the shared bespoke
// session-get payload and writes a config pointing at it. The payload's
// peer-port is 31129, with random-on-start false and forwarding true.
func portConfigPath(t *testing.T) (string, func()) {
	t.Helper()

	keyring.MockInit()

	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "", // bespoke path
		responses: map[string]string{
			"session-get": bespokeSessionResponse,
		},
	}.handler())

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, config.Save(config.Config{ServerURL: srv.URL}, cfgPath))

	return cfgPath, srv.Close
}

func TestPortGetPlaintext(t *testing.T) {
	cfgPath, closeSrv := portConfigPath(t)
	defer closeSrv()

	out, _, err := runPort(t, "get", "--config", cfgPath)
	require.NoError(t, err)

	assert.Contains(t, out, "Port:")
	assert.Contains(t, out, "31129")
	assert.Contains(t, out, "Random on start:")
	assert.Contains(t, out, "Forwarding enabled:")

	// random-on-start is false and forwarding is true in the canned payload.
	lines := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		label, value, ok := strings.Cut(line, ":")
		require.True(t, ok, "plaintext is a Label: value block")
		lines[strings.TrimSpace(label)] = strings.TrimSpace(value)
	}
	assert.Equal(t, "31129", lines["Port"])
	assert.Equal(t, "no", lines["Random on start"])
	assert.Equal(t, "yes", lines["Forwarding enabled"])
}

func TestPortGetJSON(t *testing.T) {
	cfgPath, closeSrv := portConfigPath(t)
	defer closeSrv()

	out, _, err := runPort(t, "get", "--config", cfgPath, "--json")
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"port": 31129,
		"random_on_start": false,
		"forwarding_enabled": true
	}`, out)
}

func TestPortGetJQ(t *testing.T) {
	cfgPath, closeSrv := portConfigPath(t)
	defer closeSrv()

	out, _, err := runPort(t, "get", "--config", cfgPath, "--jq", ".port")
	require.NoError(t, err)
	assert.Equal(t, "31129", strings.TrimSpace(out))
}

func TestPortGetNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runPort(t, "get", "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestPortBarePrintsHelp(t *testing.T) {
	out, _, err := runPort(t)
	require.NoError(t, err)
	assert.Contains(t, out, "Usage:")
	assert.Contains(t, out, "port")
}
