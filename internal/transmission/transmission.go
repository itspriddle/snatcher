// Package transmission adapts the typed Transmission RPC client
// (internal/transmission/rpc) to the vendor-neutral torrent.Backend interface.
// It is the only package that imports the rpc package; commands depend solely
// on torrent.Backend, so a future client backend slots in without rewriting
// commands.
package transmission

import (
	"context"
	"errors"
	"fmt"

	"github.com/itspriddle/snatcher/internal/torrent"
	"github.com/itspriddle/snatcher/internal/transmission/rpc"
)

// Backend implements torrent.Backend by wrapping an rpc.Client, converting the
// rpc package's typed results into neutral torrent types and mapping rpc errors
// to the neutral sentinels. A zero Backend is not usable; build one with New.
type Backend struct {
	client *rpc.Client
}

// Compile-time assertion that *Backend satisfies torrent.Backend.
var _ torrent.Backend = (*Backend)(nil)

// Option configures a Backend built by New. It is a neutral wrapper over the
// rpc package's options, so callers (commands) never need to import the rpc
// package to configure the backend. Each Option contributes the rpc.Option it
// wraps via the unexported rpcOption accessor.
type Option interface {
	rpcOption() rpc.Option
}

// optionFunc adapts an rpc.Option into a transmission.Option.
type optionFunc func() rpc.Option

func (f optionFunc) rpcOption() rpc.Option { return f() }

// WithBasicAuth enables HTTP Basic Auth with the given credentials, wrapping
// rpc.WithBasicAuth. When username is empty, requests are sent without an
// Authorization header.
func WithBasicAuth(username, password string) Option {
	return optionFunc(func() rpc.Option {
		return rpc.WithBasicAuth(username, password)
	})
}

// WithHTTPClient overrides the HTTP client used for requests, wrapping
// rpc.WithHTTPClient. Primarily useful in tests.
func WithHTTPClient(client rpc.HTTPDoer) Option {
	return optionFunc(func() rpc.Option {
		return rpc.WithHTTPClient(client)
	})
}

// New returns a Backend that talks to the Transmission server at rpcURL,
// applying the supplied neutral options. Callers configure the backend through
// transmission.Option (e.g. transmission.WithBasicAuth) and never need to touch
// the rpc package directly.
func New(rpcURL string, opts ...Option) *Backend {
	rpcOpts := make([]rpc.Option, len(opts))
	for i, opt := range opts {
		rpcOpts[i] = opt.rpcOption()
	}

	return &Backend{client: rpc.NewClient(rpcURL, rpcOpts...)}
}

// ServerInfo performs a SessionGet round-trip and returns the neutral
// ServerInfo. RPC failures are mapped to the neutral sentinels.
func (b *Backend) ServerInfo(ctx context.Context) (torrent.ServerInfo, error) {
	session, err := b.client.SessionGet(ctx)
	if err != nil {
		return torrent.ServerInfo{}, mapError(err)
	}

	return torrent.ServerInfo{
		Version:          session.Version,
		RPCVersionSemver: session.RPCVersionSemver,
		Protocol:         session.Protocol,
	}, nil
}

// Stats performs a SessionStats round-trip and returns the neutral Stats. RPC
// failures are mapped to the neutral sentinels.
func (b *Backend) Stats(ctx context.Context) (torrent.Stats, error) {
	stats, err := b.client.SessionStats(ctx)
	if err != nil {
		return torrent.Stats{}, mapError(err)
	}

	return torrent.Stats{
		ActiveTorrentCount: stats.ActiveTorrentCount,
		PausedTorrentCount: stats.PausedTorrentCount,
		TorrentCount:       stats.TorrentCount,
		DownloadSpeed:      stats.DownloadSpeed,
		UploadSpeed:        stats.UploadSpeed,
		CumulativeStats:    torrent.StatsSnapshot(stats.CumulativeStats),
		CurrentStats:       torrent.StatsSnapshot(stats.CurrentStats),
	}, nil
}

// mapError translates an rpc-layer error into a neutral sentinel while
// preserving the original error in the wrap chain (so errors.Is against the rpc
// sentinels still works and the server message is retained). An rpc.ErrAuth
// becomes torrent.ErrAuth; rpc.ErrTransport and rpc.ErrRPC become
// torrent.ErrUnreachable. A nil error is returned unchanged.
func mapError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, rpc.ErrAuth) {
		return fmt.Errorf("%w: %w", torrent.ErrAuth, err)
	}

	return fmt.Errorf("%w: %w", torrent.ErrUnreachable, err)
}
