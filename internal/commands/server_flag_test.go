package commands

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/itspriddle/snatcher/internal/config"
)

// statsResponseWithCount renders a JSON-RPC session_stats payload whose
// torrent_count is count, so a test can tell which server answered.
func statsResponseWithCount(count int) string {
	return fmt.Sprintf(`{
		"jsonrpc": "2.0",
		"result": {
			"active_torrent_count": 0,
			"paused_torrent_count": 0,
			"torrent_count": %d,
			"download_speed": 0,
			"upload_speed": 0,
			"cumulative_stats": {"uploaded_bytes": 0, "downloaded_bytes": 0, "files_added": 0, "session_count": 0, "seconds_active": 0},
			"current_stats": {"uploaded_bytes": 0, "downloaded_bytes": 0, "files_added": 0, "session_count": 0, "seconds_active": 0}
		},
		"id": 1
	}`, count)
}

// newCountServer stands up a transmission stub whose session_stats reports the
// given torrent count, registering its cleanup with the test.
func newCountServer(t *testing.T, count int) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(transmissionStub{
		rpcVersionHeader: "6.0.0",
		responses: map[string]string{
			"session_stats": statsResponseWithCount(count),
		},
	}.handler())
	t.Cleanup(srv.Close)

	return srv
}

// twoServerConfig writes a config registering aliases "alpha" and "beta"
// pointing at two distinct servers, defaulting to defaultAlias. It returns the
// config path.
func twoServerConfig(t *testing.T, alphaURL, betaURL, defaultAlias string) string {
	t.Helper()

	keyring.MockInit()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Config{
		Servers: map[string]config.Server{
			"alpha": {URL: alphaURL},
			"beta":  {URL: betaURL},
		},
		DefaultServer: defaultAlias,
	}
	require.NoError(t, config.Save(cfg, cfgPath))

	return cfgPath
}

func TestServerFlagSelectsNamedServer(t *testing.T) {
	alpha := newCountServer(t, 11)
	beta := newCountServer(t, 22)
	cfgPath := twoServerConfig(t, alpha.URL, beta.URL, "alpha")

	// --server beta must reach the beta server (count 22), not the default alpha.
	out, _, err := runStats(t, "--config", cfgPath, "--server", "beta", "--json", "--jq", ".torrent_count")
	require.NoError(t, err)
	assert.Equal(t, "22\n", out)
}

func TestServerFlagOmittedUsesDefault(t *testing.T) {
	alpha := newCountServer(t, 11)
	beta := newCountServer(t, 22)
	cfgPath := twoServerConfig(t, alpha.URL, beta.URL, "beta")

	// No --server: the configured default (beta, count 22) is used.
	out, _, err := runStats(t, "--config", cfgPath, "--json", "--jq", ".torrent_count")
	require.NoError(t, err)
	assert.Equal(t, "22\n", out)
}

func TestServerFlagUnknownAliasErrors(t *testing.T) {
	alpha := newCountServer(t, 11)
	beta := newCountServer(t, 22)
	cfgPath := twoServerConfig(t, alpha.URL, beta.URL, "alpha")

	_, _, err := runStats(t, "--config", cfgPath, "--server", "nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown server")
	assert.Contains(t, err.Error(), "nope")
	// Points the user at how to discover valid aliases.
	assert.Contains(t, err.Error(), "server list")
}

func TestServerEnvVarSelectsServer(t *testing.T) {
	alpha := newCountServer(t, 11)
	beta := newCountServer(t, 22)
	cfgPath := twoServerConfig(t, alpha.URL, beta.URL, "alpha")

	t.Setenv("SNATCHER_SERVER", "beta")

	// SNATCHER_SERVER selects beta when --server is omitted, overriding the
	// configured default (alpha).
	out, _, err := runStats(t, "--config", cfgPath, "--json", "--jq", ".torrent_count")
	require.NoError(t, err)
	assert.Equal(t, "22\n", out)
}

func TestServerFlagOverridesEnvVar(t *testing.T) {
	alpha := newCountServer(t, 11)
	beta := newCountServer(t, 22)
	cfgPath := twoServerConfig(t, alpha.URL, beta.URL, "beta")

	t.Setenv("SNATCHER_SERVER", "beta")

	// --server takes precedence over SNATCHER_SERVER: alpha (count 11) wins.
	out, _, err := runStats(t, "--config", cfgPath, "--server", "alpha", "--json", "--jq", ".torrent_count")
	require.NoError(t, err)
	assert.Equal(t, "11\n", out)
}

func TestServerNoDefaultErrors(t *testing.T) {
	keyring.MockInit()

	alpha := newCountServer(t, 11)
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Config{
		Servers: map[string]config.Server{
			"alpha": {URL: alpha.URL},
		},
		// No default set and no --server passed.
	}
	require.NoError(t, config.Save(cfg, cfgPath))

	_, _, err := runStats(t, "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no default server")
	// Lists the configured aliases and how to set a default.
	assert.Contains(t, err.Error(), "alpha")
	assert.Contains(t, err.Error(), "server default")
}

// TestServerFlagOnlyOnDialingCommands guards the FR-8 invariant: --server is
// registered on commands that select a server by flag, but never on those that
// take a positional [alias] instead — the local-config registry subcommands plus
// ping and show, which dial the alias they are given.
func TestServerFlagOnlyOnDialingCommands(t *testing.T) {
	byAlias := map[string]bool{
		"add": true, "list": true, "default": true, "remove": true,
		"ping": true, "show": true,
	}
	// "port" is omitted: it is a group parent that dials nothing itself, so
	// --server lives on its get/set/test children.
	dialing := map[string]bool{
		"update": true, "reset": true, "stats": true, "free-space": true,
	}

	for _, c := range NewServerCmd().Commands() {
		switch {
		case byAlias[c.Name()]:
			assert.Nilf(t, c.Flags().Lookup("server"), "server %s selects by positional alias and must not have --server", c.Name())
		case dialing[c.Name()]:
			assert.NotNilf(t, c.Flags().Lookup("server"), "server %s dials and must have --server", c.Name())
		}
	}

	// Spot-check a representative dialing command outside the server group.
	assert.NotNil(t, NewListCmd().Flags().Lookup("server"), "list must have --server")
}
