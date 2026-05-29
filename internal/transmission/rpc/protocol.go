// This file contains the protocol abstraction: a Protocol interface with two
// implementations that own the differences between the two documented
// Transmission RPC schemes.
//
//   - JSON-RPC 2.0 (Transmission >= 4.1.0, rpc-version-semver >= 6.0.0): request
//     envelope {jsonrpc,method,params,id}, response {jsonrpc,result|error,id},
//     snake_case method names (session_get) and snake_case field names.
//   - Bespoke (Transmission <= 4.0.6): request envelope {method,arguments,tag},
//     response {result,arguments,tag}, kebab-case method names (session-get) and
//     a kebab-case/camelCase field mix.
//
// Each implementation owns its wire envelope, method naming, and field-name
// decoding, converting to shared canonical structs (e.g. Session) so the rest of
// the client calls one typed API regardless of Transmission version.
package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Protocol names returned in Session.Protocol and used to label the negotiated
// scheme.
const (
	// ProtocolJSONRPC is the JSON-RPC 2.0 scheme (Transmission >= 4.1.0).
	ProtocolJSONRPC = "jsonrpc-2.0"

	// ProtocolBespoke is the legacy bespoke scheme (Transmission <= 4.0.6).
	ProtocolBespoke = "bespoke"
)

// Method names per protocol. The two schemes spell the same operations
// differently (snake_case vs kebab-case).
const (
	methodSessionGetJSONRPC = "session_get"
	methodSessionGetBespoke = "session-get"

	methodSessionStatsJSONRPC = "session_stats"
	methodSessionStatsBespoke = "session-stats"
)

// ErrRPC signals that the server accepted the request at the transport level but
// returned an application error: a JSON-RPC error object, or a bespoke response
// whose result was not "success". The wrapped message carries the server text.
var ErrRPC = errors.New("rpc: server error")

// bespokeResultSuccess is the value the bespoke protocol's result field carries
// on success; any other value denotes an error.
const bespokeResultSuccess = "success"

// Session is the canonical, protocol-neutral result of a SessionGet call. Each
// Protocol implementation reads the correct per-scheme field names and fills
// this shared shape.
type Session struct {
	// Version is the long Transmission version string, e.g. "4.0.6 (38c164933e)".
	Version string `json:"version"`

	// RPCVersionSemver is the semver-compatible RPC API version, e.g. "6.0.0".
	RPCVersionSemver string `json:"rpc_version_semver"`

	// Protocol is the negotiated protocol name (ProtocolJSONRPC or
	// ProtocolBespoke).
	Protocol string `json:"protocol"`
}

// StatsSnapshot is the canonical, protocol-neutral shape of the cumulative and
// current stats objects nested in a SessionStats result. Each Protocol
// implementation reads the correct per-scheme field names and fills this shared
// shape.
type StatsSnapshot struct {
	// UploadedBytes is the total number of bytes uploaded in the snapshot.
	UploadedBytes int64 `json:"uploaded_bytes"`

	// DownloadedBytes is the total number of bytes downloaded in the snapshot.
	DownloadedBytes int64 `json:"downloaded_bytes"`

	// FilesAdded is the number of files added in the snapshot.
	FilesAdded int64 `json:"files_added"`

	// SessionCount is the number of sessions counted in the snapshot.
	SessionCount int64 `json:"session_count"`

	// SecondsActive is the number of seconds active in the snapshot.
	SecondsActive int64 `json:"seconds_active"`
}

// SessionStats is the canonical, protocol-neutral result of a SessionStats
// call. Each Protocol implementation reads the correct per-scheme field names
// and fills this shared shape.
type SessionStats struct {
	// ActiveTorrentCount is the number of active torrents.
	ActiveTorrentCount int64 `json:"active_torrent_count"`

	// PausedTorrentCount is the number of paused torrents.
	PausedTorrentCount int64 `json:"paused_torrent_count"`

	// TorrentCount is the total number of torrents.
	TorrentCount int64 `json:"torrent_count"`

	// DownloadSpeed is the current aggregate download speed in bytes/second.
	DownloadSpeed int64 `json:"download_speed"`

	// UploadSpeed is the current aggregate upload speed in bytes/second.
	UploadSpeed int64 `json:"upload_speed"`

	// CumulativeStats aggregates statistics across all sessions.
	CumulativeStats StatsSnapshot `json:"cumulative_stats"`

	// CurrentStats covers statistics for the current session only.
	CurrentStats StatsSnapshot `json:"current_stats"`
}

// transport is the subset of *Transport a Protocol needs to perform a request.
// Defining it as an interface keeps the protocol implementations testable in
// isolation from the HTTP layer.
type transport interface {
	Do(ctx context.Context, body []byte) ([]byte, error)
}

// Protocol abstracts the wire scheme used to talk to a Transmission server. Each
// implementation owns its request envelope, method naming, and response decoding.
type Protocol interface {
	// Name reports the protocol's canonical name (ProtocolJSONRPC or
	// ProtocolBespoke).
	Name() string

	// SessionGet calls the server's session-get/session_get method and returns a
	// canonical Session.
	SessionGet(ctx context.Context, t transport) (Session, error)

	// SessionStats calls the server's session-stats/session_stats method and
	// returns canonical SessionStats.
	SessionStats(ctx context.Context, t transport) (SessionStats, error)
}

// jsonrpcProtocol implements the JSON-RPC 2.0 scheme.
type jsonrpcProtocol struct{}

// jsonrpcRequest is the JSON-RPC 2.0 request envelope. Transmission requires
// params to be an Object (by-position params are unsupported).
type jsonrpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      int    `json:"id"`
}

// jsonrpcError is the JSON-RPC 2.0 Error object.
type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// jsonrpcResponse is the JSON-RPC 2.0 response envelope. result is captured raw
// so each method decodes its own result shape.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonrpcError   `json:"error"`
	ID      int             `json:"id"`
}

// jsonrpcSessionResult is the snake_case session_get result shape.
type jsonrpcSessionResult struct {
	Version          string `json:"version"`
	RPCVersionSemver string `json:"rpc_version_semver"`
}

// jsonrpcStatsSnapshot is the snake_case nested stats object shape.
type jsonrpcStatsSnapshot struct {
	UploadedBytes   int64 `json:"uploaded_bytes"`
	DownloadedBytes int64 `json:"downloaded_bytes"`
	FilesAdded      int64 `json:"files_added"`
	SessionCount    int64 `json:"session_count"`
	SecondsActive   int64 `json:"seconds_active"`
}

// jsonrpcStatsResult is the snake_case session_stats result shape.
type jsonrpcStatsResult struct {
	ActiveTorrentCount int64                `json:"active_torrent_count"`
	PausedTorrentCount int64                `json:"paused_torrent_count"`
	TorrentCount       int64                `json:"torrent_count"`
	DownloadSpeed      int64                `json:"download_speed"`
	UploadSpeed        int64                `json:"upload_speed"`
	CumulativeStats    jsonrpcStatsSnapshot `json:"cumulative_stats"`
	CurrentStats       jsonrpcStatsSnapshot `json:"current_stats"`
}

func (jsonrpcProtocol) Name() string { return ProtocolJSONRPC }

func (p jsonrpcProtocol) SessionGet(ctx context.Context, t transport) (Session, error) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  methodSessionGetJSONRPC,
		Params:  map[string]any{"fields": []string{"version", "rpc_version_semver"}},
		ID:      1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return Session{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return Session{}, err
	}

	var resp jsonrpcResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return Session{}, fmt.Errorf("rpc: decoding session_get response: %w", err)
	}

	if resp.Error != nil {
		return Session{}, fmt.Errorf("%w: %s", ErrRPC, resp.Error.Message)
	}

	var result jsonrpcSessionResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return Session{}, fmt.Errorf("rpc: decoding session_get result: %w", err)
	}

	return Session{
		Version:          result.Version,
		RPCVersionSemver: result.RPCVersionSemver,
		Protocol:         ProtocolJSONRPC,
	}, nil
}

func (p jsonrpcProtocol) SessionStats(ctx context.Context, t transport) (SessionStats, error) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  methodSessionStatsJSONRPC,
		ID:      1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return SessionStats{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return SessionStats{}, err
	}

	var resp jsonrpcResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return SessionStats{}, fmt.Errorf("rpc: decoding session_stats response: %w", err)
	}

	if resp.Error != nil {
		return SessionStats{}, fmt.Errorf("%w: %s", ErrRPC, resp.Error.Message)
	}

	var result jsonrpcStatsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return SessionStats{}, fmt.Errorf("rpc: decoding session_stats result: %w", err)
	}

	return SessionStats{
		ActiveTorrentCount: result.ActiveTorrentCount,
		PausedTorrentCount: result.PausedTorrentCount,
		TorrentCount:       result.TorrentCount,
		DownloadSpeed:      result.DownloadSpeed,
		UploadSpeed:        result.UploadSpeed,
		CumulativeStats:    StatsSnapshot(result.CumulativeStats),
		CurrentStats:       StatsSnapshot(result.CurrentStats),
	}, nil
}

// bespokeProtocol implements the legacy bespoke scheme.
type bespokeProtocol struct{}

// bespokeRequest is the bespoke request envelope.
type bespokeRequest struct {
	Method    string `json:"method"`
	Arguments any    `json:"arguments,omitempty"`
	Tag       int    `json:"tag"`
}

// bespokeResponse is the bespoke response envelope. arguments is captured raw so
// each method decodes its own shape.
type bespokeResponse struct {
	Result    string          `json:"result"`
	Arguments json.RawMessage `json:"arguments"`
	Tag       int             `json:"tag"`
}

// bespokeSessionArguments is the kebab-case session-get arguments shape.
type bespokeSessionArguments struct {
	Version          string `json:"version"`
	RPCVersionSemver string `json:"rpc-version-semver"`
}

// bespokeStatsSnapshot is the camelCase nested stats object shape.
type bespokeStatsSnapshot struct {
	UploadedBytes   int64 `json:"uploadedBytes"`
	DownloadedBytes int64 `json:"downloadedBytes"`
	FilesAdded      int64 `json:"filesAdded"`
	SessionCount    int64 `json:"sessionCount"`
	SecondsActive   int64 `json:"secondsActive"`
}

// bespokeStatsArguments is the camelCase/kebab-case session-stats arguments
// shape. The top-level counts and speeds are camelCase; the nested snapshot
// objects are kebab-case keys.
type bespokeStatsArguments struct {
	ActiveTorrentCount int64                `json:"activeTorrentCount"`
	PausedTorrentCount int64                `json:"pausedTorrentCount"`
	TorrentCount       int64                `json:"torrentCount"`
	DownloadSpeed      int64                `json:"downloadSpeed"`
	UploadSpeed        int64                `json:"uploadSpeed"`
	CumulativeStats    bespokeStatsSnapshot `json:"cumulative-stats"`
	CurrentStats       bespokeStatsSnapshot `json:"current-stats"`
}

func (bespokeProtocol) Name() string { return ProtocolBespoke }

func (p bespokeProtocol) SessionGet(ctx context.Context, t transport) (Session, error) {
	req := bespokeRequest{
		Method:    methodSessionGetBespoke,
		Arguments: map[string]any{"fields": []string{"version", "rpc-version-semver"}},
		Tag:       1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return Session{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return Session{}, err
	}

	var resp bespokeResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return Session{}, fmt.Errorf("rpc: decoding session-get response: %w", err)
	}

	if resp.Result != bespokeResultSuccess {
		return Session{}, fmt.Errorf("%w: %s", ErrRPC, resp.Result)
	}

	var args bespokeSessionArguments
	if err := json.Unmarshal(resp.Arguments, &args); err != nil {
		return Session{}, fmt.Errorf("rpc: decoding session-get arguments: %w", err)
	}

	return Session{
		Version:          args.Version,
		RPCVersionSemver: args.RPCVersionSemver,
		Protocol:         ProtocolBespoke,
	}, nil
}

func (p bespokeProtocol) SessionStats(ctx context.Context, t transport) (SessionStats, error) {
	req := bespokeRequest{
		Method: methodSessionStatsBespoke,
		Tag:    1,
	}

	body, err := marshalRequest(req)
	if err != nil {
		return SessionStats{}, err
	}

	respBody, err := t.Do(ctx, body)
	if err != nil {
		return SessionStats{}, err
	}

	var resp bespokeResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return SessionStats{}, fmt.Errorf("rpc: decoding session-stats response: %w", err)
	}

	if resp.Result != bespokeResultSuccess {
		return SessionStats{}, fmt.Errorf("%w: %s", ErrRPC, resp.Result)
	}

	var args bespokeStatsArguments
	if err := json.Unmarshal(resp.Arguments, &args); err != nil {
		return SessionStats{}, fmt.Errorf("rpc: decoding session-stats arguments: %w", err)
	}

	return SessionStats{
		ActiveTorrentCount: args.ActiveTorrentCount,
		PausedTorrentCount: args.PausedTorrentCount,
		TorrentCount:       args.TorrentCount,
		DownloadSpeed:      args.DownloadSpeed,
		UploadSpeed:        args.UploadSpeed,
		CumulativeStats:    StatsSnapshot(args.CumulativeStats),
		CurrentStats:       StatsSnapshot(args.CurrentStats),
	}, nil
}

// marshalRequest encodes a request envelope, wrapping any error in a clear,
// protocol-neutral message.
func marshalRequest(req any) ([]byte, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("rpc: encoding request: %w", err)
	}

	return body, nil
}
