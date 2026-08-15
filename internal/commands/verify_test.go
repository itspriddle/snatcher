package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// verifyErrorListResponse is a torrent-get payload where two of three torrents
// carry a server-reported error — one via the numeric "error" code, one via
// "errorString" alone — so --all-with-errors selects a strict subset and both
// halves of the StatusError derivation are exercised.
const verifyErrorListResponse = `{
	"arguments": {"torrents": [
		{"id": 1, "hashString": "0123456789abcdef0123456789abcdef01234567", "name": "ubuntu-24.04.iso", "status": 6},
		{"id": 2, "hashString": "fedcba9876543210fedcba9876543210fedcba98", "name": "debian.iso", "status": 4, "error": 3, "errorString": "unregistered torrent"},
		{"id": 3, "hashString": "abcdef0123456789abcdef0123456789abcdef01", "name": "arch.iso", "status": 0, "errorString": "no data found"}
	]},
	"result": "success",
	"tag": 1
}`

// runVerify executes the verify command with args, returning stdout, stderr, and
// the Execute error. The command's output is a *bytes.Buffer, so the
// piped/plaintext path is exercised.
func runVerify(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	return runVerifyWithStdin(t, strings.NewReader(""), args...)
}

// runVerifyWithStdin is runVerify with a caller-supplied stdin, so the
// confirmation gate's interactive branch can be driven with a fakeTerminal.
func runVerifyWithStdin(t *testing.T, in io.Reader, args ...string) (string, string, error) {
	t.Helper()

	cmd := NewVerifyCmd()
	AddGlobalFlags(cmd)

	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(in)
	cmd.SetArgs(args)

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

func TestVerifyByID(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runVerify(t, "--config", cfgPath, "1")
	require.NoError(t, err)

	assert.Equal(t, "Queued verification of 1 torrent: ubuntu-24.04.iso\n", out)
}

func TestVerifyByHash(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runVerify(t, "--config", cfgPath, fullHashTwo)
	require.NoError(t, err)

	assert.Equal(t, "Queued verification of 1 torrent: debian.iso\n", out)
}

func TestVerifyAll(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runVerify(t, "--config", cfgPath, "--all", "--yes")
	require.NoError(t, err)

	assert.Equal(t, "Queued verification of all 2 torrents.\n", out)
}

func TestVerifyNoSelectionErrors(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	_, _, err := runVerify(t, "--config", cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "specify torrent ids or hashes, or use --all")
}

func TestVerifyNoMatchErrors(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	_, _, err := runVerify(t, "--config", cfgPath, "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no torrents matched: 99")
}

func TestVerifyPartialMatchSurfacesUnmatched(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runVerify(t, "--config", cfgPath, "1", "99")
	require.NoError(t, err)
	assert.Contains(t, out, "Queued verification of 1 torrent: ubuntu-24.04.iso")
	assert.Contains(t, out, "No torrents matched: 99")
}

func TestVerifyJSON(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runVerify(t, "--config", cfgPath, "--json", "1")
	require.NoError(t, err)

	var got struct {
		Action   string `json:"action"`
		Count    int    `json:"count"`
		Torrents []struct {
			ID   int64  `json:"id"`
			Hash string `json:"hash"`
			Name string `json:"name"`
		} `json:"torrents"`
		Now        *bool    `json:"now"`
		DeleteData *bool    `json:"delete_data"`
		Unmatched  []string `json:"unmatched"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	assert.Equal(t, "verify", got.Action)
	assert.Equal(t, 1, got.Count)
	require.Len(t, got.Torrents, 1)
	assert.Equal(t, int64(1), got.Torrents[0].ID)
	assert.Equal(t, fullHashOne, got.Torrents[0].Hash)
	assert.Equal(t, "ubuntu-24.04.iso", got.Torrents[0].Name)
	assert.Nil(t, got.Now, "verify omits the now field")
	assert.Nil(t, got.DeleteData, "verify omits the delete_data field")
	assert.Empty(t, got.Unmatched, "no unmatched tokens when every selector matched")
}

func TestVerifyJSONIncludesUnmatched(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runVerify(t, "--config", cfgPath, "--json", "1", "99")
	require.NoError(t, err)

	var got struct {
		Count     int      `json:"count"`
		Unmatched []string `json:"unmatched"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, 1, got.Count)
	assert.Equal(t, []string{"99"}, got.Unmatched)
}

func TestVerifyJQ(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
	defer closeSrv()

	out, _, err := runVerify(t, "--config", cfgPath, "--jq", ".torrents[].name", "--all", "--yes")
	require.NoError(t, err)

	assert.Equal(t, `"ubuntu-24.04.iso"`+"\n"+`"debian.iso"`, strings.TrimSpace(out))
}

func TestVerifyNotConfigured(t *testing.T) {
	keyring.MockInit()
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	_, _, err := runVerify(t, "--config", cfgPath, "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

// TestVerifySanitizesNameInHumanButNotJSON guards the security boundary: a
// torrent name carrying an ANSI escape is neutralized in the human confirmation
// but preserved verbatim in the structured --json contract.
func TestVerifySanitizesNameInHumanButNotJSON(t *testing.T) {
	const body = `{
		"arguments": {"torrents": [
			{"id": 1, "hashString": "0123456789abcdef0123456789abcdef01234567", "name": "\u001b[2Jmalicious", "status": 0}
		]},
		"result": "success",
		"tag": 1
	}`

	cfgPath, closeSrv := mutationConfigPath(t, body)
	defer closeSrv()

	human, _, err := runVerify(t, "--config", cfgPath, "1")
	require.NoError(t, err)
	assert.NotContains(t, human, "\x1b", "raw ESC must not reach the human confirmation")
	assert.Contains(t, human, "[2Jmalicious", "visible text survives sanitization")

	jsonOut, _, err := runVerify(t, "--config", cfgPath, "--json", "1")
	require.NoError(t, err)
	assert.Contains(t, jsonOut, "\\u001b", "the raw escape is preserved in the JSON contract")
}

// TestVerifyAllWithErrors covers the happy path of the errored selection: only
// the torrents the server reported an error for are verified, and they are named
// on the plaintext confirmation.
func TestVerifyAllWithErrors(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", verifyErrorListResponse)
	defer closeSrv()

	out, _, err := runVerify(t, "--config", cfgPath, "--all-with-errors", "--yes")
	require.NoError(t, err)

	assert.Equal(t, "Queued verification of 2 torrents: debian.iso, arch.iso\n", out)

	body, ok := stub.request("torrent-verify")
	require.True(t, ok, "the verify method was called")
	assert.Contains(t, body, `"ids":[2,3]`, "exactly the errored torrents are named by id")
}

// TestVerifyAllWithErrorsJSON asserts the errored selection renders through the
// shared mutationResult contract, naming the matched torrents rather than
// reporting an --all-style count.
func TestVerifyAllWithErrorsJSON(t *testing.T) {
	cfgPath, closeSrv := mutationConfigPath(t, verifyErrorListResponse)
	defer closeSrv()

	out, _, err := runVerify(t, "--config", cfgPath, "--all-with-errors", "--yes", "--json")
	require.NoError(t, err)

	var got struct {
		Action   string `json:"action"`
		Count    int    `json:"count"`
		Torrents []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"torrents"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))

	assert.Equal(t, "verify", got.Action)
	assert.Equal(t, 2, got.Count)
	require.Len(t, got.Torrents, 2)
	assert.Equal(t, int64(2), got.Torrents[0].ID)
	assert.Equal(t, "debian.iso", got.Torrents[0].Name)
	assert.Equal(t, int64(3), got.Torrents[1].ID)
	assert.Equal(t, "arch.iso", got.Torrents[1].Name)
}

// TestVerifyAllWithErrorsEmpty guards the "nothing matched" contract: it is not
// an error, no verify call is issued, and each output mode reports an empty
// result rather than a dangling confirmation line.
func TestVerifyAllWithErrorsEmpty(t *testing.T) {
	t.Run("plaintext", func(t *testing.T) {
		cfgPath, stub, closeSrv := removeConfigPath(t, "", mutationListResponse)
		defer closeSrv()

		out, _, err := runVerify(t, "--config", cfgPath, "--all-with-errors", "--yes")
		require.NoError(t, err)
		assert.Equal(t, "No torrents with errors.\n", out)

		_, ok := stub.request("torrent-verify")
		assert.False(t, ok, "no verify call is issued when nothing matched")
	})

	t.Run("json", func(t *testing.T) {
		cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
		defer closeSrv()

		out, _, err := runVerify(t, "--config", cfgPath, "--all-with-errors", "--yes", "--json")
		require.NoError(t, err)

		assert.Contains(t, out, `"torrents": []`, "an empty array, never null")

		var got struct {
			Action   string            `json:"action"`
			Count    int               `json:"count"`
			Torrents []mutationTorrent `json:"torrents"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &got))
		assert.Equal(t, "verify", got.Action)
		assert.Equal(t, 0, got.Count)
		assert.Empty(t, got.Torrents)
	})

	t.Run("jq", func(t *testing.T) {
		cfgPath, closeSrv := mutationConfigPath(t, mutationListResponse)
		defer closeSrv()

		out, _, err := runVerify(t, "--config", cfgPath, "--all-with-errors", "--yes", "--jq", ".count")
		require.NoError(t, err)
		assert.Equal(t, "0", strings.TrimSpace(out))
	})
}

// TestVerifyBulkWithoutYesRefuses guards the confirmation gate on both bulk
// forms: piped stdin without --yes is refused by name and count, and nothing is
// verified.
func TestVerifyBulkWithoutYesRefuses(t *testing.T) {
	cases := []struct {
		name string
		flag string
		want string
	}{
		{
			name: "all",
			flag: "--all",
			want: "refusing to verify 3 torrents without confirmation; pass --yes to proceed",
		},
		{
			name: "all-with-errors",
			flag: "--all-with-errors",
			want: "refusing to verify 2 torrents without confirmation; pass --yes to proceed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := removeConfigPath(t, "", verifyErrorListResponse)
			defer closeSrv()

			_, _, err := runVerify(t, "--config", cfgPath, tc.flag)
			require.EqualError(t, err, tc.want)

			_, ok := stub.request("torrent-verify")
			assert.False(t, ok, "the verify method must not be called without confirmation")
		})
	}
}

// TestVerifyExplicitSelectionIsNotGated is the counterpart: an enumerated
// selection verifies without a prompt even on piped stdin, because the caller
// already named every torrent.
func TestVerifyExplicitSelectionIsNotGated(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", verifyErrorListResponse)
	defer closeSrv()

	out, _, err := runVerify(t, "--config", cfgPath, "2", "3")
	require.NoError(t, err)
	assert.NotContains(t, out, "[y/N]", "an explicit selection is never prompted")
	assert.Equal(t, "Queued verification of 2 torrents: debian.iso, arch.iso\n", out)

	_, ok := stub.request("torrent-verify")
	assert.True(t, ok, "the verify method runs for an explicit selection")
}

// TestVerifyBulkPromptsInteractively covers the gate's interactive branch: the
// prompt names the count, an affirmative answer verifies, and a declined answer
// cancels without issuing the call.
func TestVerifyBulkPromptsInteractively(t *testing.T) {
	cases := []struct {
		name   string
		flag   string
		answer string
		prompt string
		verify bool
	}{
		{
			name:   "all confirmed",
			flag:   "--all",
			answer: "y\n",
			prompt: "Verify all 3 torrents? [y/N] ",
			verify: true,
		},
		{
			name:   "all declined",
			flag:   "--all",
			answer: "n\n",
			prompt: "Verify all 3 torrents? [y/N] ",
		},
		{
			name:   "errored confirmed",
			flag:   "--all-with-errors",
			answer: "y\n",
			prompt: "Verify 2 torrents with errors? [y/N] ",
			verify: true,
		},
		{
			name:   "errored declined",
			flag:   "--all-with-errors",
			answer: "\n",
			prompt: "Verify 2 torrents with errors? [y/N] ",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, stub, closeSrv := removeConfigPath(t, "", verifyErrorListResponse)
			defer closeSrv()

			in := fakeTerminal{Reader: strings.NewReader(tc.answer)}
			out, _, err := runVerifyWithStdin(t, in, "--config", cfgPath, tc.flag)
			require.NoError(t, err)
			assert.Contains(t, out, tc.prompt)

			_, ok := stub.request("torrent-verify")
			assert.Equal(t, tc.verify, ok, "the verify call follows the answer")

			if !tc.verify {
				assert.Contains(t, out, "Canceled.")
			}
		})
	}
}

// TestVerifyYesBypassesPrompt is a regression guard: with --yes neither bulk form
// reads stdin or prompts.
func TestVerifyYesBypassesPrompt(t *testing.T) {
	cfgPath, stub, closeSrv := removeConfigPath(t, "", verifyErrorListResponse)
	defer closeSrv()

	out, _, err := runVerifyWithStdin(t, fakeTerminal{Reader: strings.NewReader("n\n")}, "--config", cfgPath, "--all", "-y")
	require.NoError(t, err)
	assert.NotContains(t, out, "[y/N]", "no prompt is shown when --yes is given")
	assert.Equal(t, "Queued verification of all 3 torrents.\n", out)

	_, ok := stub.request("torrent-verify")
	assert.True(t, ok, "the verify method runs with --yes")
}

// TestVerifySelectorCombinationsError asserts the mutually exclusive selection
// modes are rejected before the server is dialed: the config here names no
// server, so reaching dial would surface a "not configured" error instead.
func TestVerifySelectorCombinationsError(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "positional and all-with-errors",
			args: []string{"--all-with-errors", "1"},
			want: "cannot combine torrent selectors with --all-with-errors",
		},
		{
			name: "all and all-with-errors",
			args: []string{"--all-with-errors", "--all"},
			want: "cannot combine --all with --all-with-errors",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			keyring.MockInit()
			cfgPath := filepath.Join(t.TempDir(), "config.json")

			args := append([]string{"--config", cfgPath, "--yes"}, tc.args...)
			_, _, err := runVerify(t, args...)
			require.EqualError(t, err, tc.want)
		})
	}
}

// TestVerifyAllWithErrorsSanitizesNameInHumanButNotJSON mirrors the explicit
// selection's boundary guard for the errored selection: an ANSI escape in a
// server-supplied name is neutralized on the human path and preserved verbatim
// in the JSON contract.
func TestVerifyAllWithErrorsSanitizesNameInHumanButNotJSON(t *testing.T) {
	const body = `{
		"arguments": {"torrents": [
			{"id": 1, "hashString": "0123456789abcdef0123456789abcdef01234567", "name": "\u001b[2Jmalicious", "status": 0, "errorString": "no data found"}
		]},
		"result": "success",
		"tag": 1
	}`

	cfgPath, closeSrv := mutationConfigPath(t, body)
	defer closeSrv()

	human, _, err := runVerify(t, "--config", cfgPath, "--all-with-errors", "--yes")
	require.NoError(t, err)
	assert.NotContains(t, human, "\x1b", "raw ESC must not reach the human confirmation")
	assert.Contains(t, human, "[2Jmalicious", "visible text survives sanitization")

	jsonOut, _, err := runVerify(t, "--config", cfgPath, "--all-with-errors", "--yes", "--json")
	require.NoError(t, err)
	assert.Contains(t, jsonOut, "\\u001b", "the raw escape is preserved in the JSON contract")
}

func TestVerifyPrompt(t *testing.T) {
	cases := []struct {
		name          string
		count         int
		allWithErrors bool
		want          string
	}{
		{name: "all singular", count: 1, want: "Verify all 1 torrent? [y/N] "},
		{name: "all plural", count: 3, want: "Verify all 3 torrents? [y/N] "},
		{name: "errored singular", count: 1, allWithErrors: true, want: "Verify 1 torrent with errors? [y/N] "},
		{name: "errored plural", count: 4, allWithErrors: true, want: "Verify 4 torrents with errors? [y/N] "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, verifyPrompt(tc.count, tc.allWithErrors))
		})
	}
}
