package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// sessionIDHeader is Transmission's CSRF header.
const sessionIDHeader = "X-Transmission-Session-Id"

// bespokeStatsResponse is a session-stats payload on the bespoke scheme, copied
// from internal/commands' fixture.
const bespokeStatsResponse = `{
	"arguments": {
		"activeTorrentCount": 1,
		"pausedTorrentCount": 4,
		"torrentCount": 5,
		"downloadSpeed": 2097152,
		"uploadSpeed": 1048576,
		"cumulative-stats": {"uploadedBytes": 5368709120, "downloadedBytes": 10737418240, "filesAdded": 21, "sessionCount": 3, "secondsActive": 180000},
		"current-stats": {"uploadedBytes": 536870912, "downloadedBytes": 1073741824, "filesAdded": 2, "sessionCount": 1, "secondsActive": 1800}
	},
	"result": "success",
	"tag": 1
}`

// bespokeTorrentsResponse is a torrent-get payload of three torrents whose queue
// positions (2, 0, 1) differ from their IDs, so queue ordering is observable.
// One carries a label and one a tracker error.
const bespokeTorrentsResponse = `{
	"arguments": {"torrents": [
		{
			"id": 1, "hashString": "aaa111", "name": "ubuntu-24.04.iso", "status": 4,
			"percentDone": 0.5, "totalSize": 2147483648, "sizeWhenDone": 2147483648,
			"leftUntilDone": 1073741824, "rateDownload": 1572864, "rateUpload": 524288,
			"uploadRatio": 0.5, "eta": 3600, "queuePosition": 2, "labels": ["linux"]
		},
		{
			"id": 2, "hashString": "bbb222", "name": "debian.iso", "status": 6,
			"percentDone": 1.0, "totalSize": 1073741824, "sizeWhenDone": 1073741824,
			"leftUntilDone": 0, "rateDownload": 0, "rateUpload": 131072,
			"uploadRatio": 2.5, "eta": -1, "queuePosition": 0, "labels": []
		},
		{
			"id": 3, "hashString": "ccc333", "name": "alpine.iso", "status": 0,
			"percentDone": 0.5, "totalSize": 209715200, "sizeWhenDone": 209715200,
			"leftUntilDone": 104857600, "eta": -1, "queuePosition": 1, "labels": [],
			"error": 2, "errorString": "Tracker gave HTTP response code 404 (Not Found)"
		}
	]},
	"result": "success",
	"tag": 1
}`

// bespokeFreeSpaceResponse is a free-space payload on the bespoke scheme.
const bespokeFreeSpaceResponse = `{
	"arguments": {"path": "/data", "size-bytes": 128849018880, "total_size": 1099511627776},
	"result": "success",
	"tag": 1
}`

// probeSessionGets is the session-get the rpc client sends once, on first use,
// to detect the wire scheme and check the server's version. It is not a poll, so
// tests counting the spine's session-gets add it.
const probeSessionGets = 1

// stubTorrent is the minimum of a torrent a test reorders or removes.
type stubTorrent struct {
	id    int64
	name  string
	queue int
}

// torrentsBody builds a torrent-get payload from minimal torrents.
func torrentsBody(ts ...stubTorrent) string {
	rows := make([]string, 0, len(ts))

	for _, t := range ts {
		rows = append(rows, fmt.Sprintf(`{"id": %d, "hashString": "h%d", "name": %q, "status": 0, "queuePosition": %d}`,
			t.id, t.id, t.name, t.queue))
	}

	return `{"arguments": {"torrents": [` + strings.Join(rows, ",") + `]}, "result": "success", "tag": 1}`
}

// rpcStub is a Transmission server on the bespoke path. It answers each method
// from a table a test can change while it runs, records every request body, and
// can fail one method or every request.
type rpcStub struct {
	mu        sync.Mutex
	bodies    []string
	responses map[string]string
	fail      map[string]bool

	// status, when non-zero, is returned for every request instead of an
	// answer.
	status atomic.Int32
}

// newRPCStub starts a stub server for the test.
func newRPCStub(t *testing.T) (*rpcStub, *httptest.Server) {
	t.Helper()

	stub := &rpcStub{
		responses: map[string]string{
			"session-get":   bespokeSessionResponse,
			"session-stats": bespokeStatsResponse,
			"torrent-get":   bespokeTorrentsResponse,
			"free-space":    bespokeFreeSpaceResponse,
		},
		fail: map[string]bool{},
	}

	srv := httptest.NewServer(http.HandlerFunc(stub.serve))
	t.Cleanup(srv.Close)

	return stub, srv
}

// respond sets the answer to a method.
func (s *rpcStub) respond(method, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.responses[method] = body
}

// failMethod makes a method answer with an RPC error.
func (s *rpcStub) failMethod(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.fail[method] = true
}

// calls is how many requests for a method the stub has received.
func (s *rpcStub) calls(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := 0

	for _, body := range s.bodies {
		if methodOf(body) == method {
			n++
		}
	}

	return n
}

// methodOf reads the method out of a request body.
func methodOf(body string) string {
	var envelope struct {
		Method string `json:"method"`
	}

	_ = json.Unmarshal([]byte(body), &envelope)

	return envelope.Method
}

// serve answers one request: the 409 handshake, then the method's answer, with
// success for a method the table does not name.
func (s *rpcStub) serve(w http.ResponseWriter, r *http.Request) {
	if code := s.status.Load(); code != 0 {
		w.WriteHeader(int(code))

		return
	}

	if r.Header.Get(sessionIDHeader) == "" {
		w.Header().Set(sessionIDHeader, "session-token")
		w.WriteHeader(http.StatusConflict)

		return
	}

	body, _ := io.ReadAll(r.Body)
	method := methodOf(string(body))

	s.mu.Lock()
	s.bodies = append(s.bodies, string(body))
	resp, ok := s.responses[method]
	failing := s.fail[method]
	s.mu.Unlock()

	switch {
	case failing:
		_, _ = w.Write([]byte(`{"result": "permission denied", "arguments": {}, "tag": 1}`))
	case ok:
		_, _ = w.Write([]byte(resp))
	default:
		_, _ = w.Write([]byte(`{"result": "success", "arguments": {}, "tag": 1}`))
	}
}

// settingSetArgs returns the arguments of every session-set the stub received,
// merged — or nil when none was sent.
func (s *rpcStub) settingSetArgs(t *testing.T) map[string]any {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	var merged map[string]any

	for _, body := range s.bodies {
		if methodOf(body) != "session-set" {
			continue
		}

		var envelope struct {
			Arguments map[string]any `json:"arguments"`
		}

		require.NoError(t, json.Unmarshal([]byte(body), &envelope))

		if merged == nil {
			merged = map[string]any{}
		}

		for k, v := range envelope.Arguments {
			merged[k] = v
		}
	}

	return merged
}
