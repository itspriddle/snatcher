// Package torrenttest provides a configurable in-memory torrent.Backend for
// tests. It lets view-logic and command tests run against a real Backend
// implementation instead of a hand-written one-off stub: callers seed canned
// torrents and detail, force any read or write to fail with one of the neutral
// sentinels (torrent.ErrAuth, torrent.ErrUnreachable, torrent.ErrServer), and
// assert on the mutations the code under test performed.
//
// A zero Backend is usable: every read returns empty data and every write
// succeeds and is recorded. Configure it by setting the public fields directly
// (it is not safe for concurrent use, matching its single-test scope).
package torrenttest

import (
	"context"

	"github.com/itspriddle/snatcher/internal/torrent"
)

// Backend is an in-memory torrent.Backend for tests. Seed the read fixtures
// (Server, Stats, Torrents, Details, Session, Port, Free) to control what the
// reads return, set Err to force every method to fail with a chosen sentinel,
// and inspect Mutations (or Closed) to assert on what the code under test did.
//
// The zero value is ready to use: reads return the zero fixtures and writes
// succeed silently. Backend is not safe for concurrent use.
type Backend struct {
	// Server is returned by ServerInfo.
	Server torrent.ServerInfo

	// StatsValue is returned by Stats.
	StatsValue torrent.Stats

	// Torrents is the canned set ListTorrents filters and returns. A selector
	// that is All (or whose RPCIDs is empty) returns every entry; otherwise only
	// the entries whose ID or Hash is named by the selector are returned, in the
	// order they appear here. The fields projection is recorded but does not prune
	// the returned Torrents (tests assert on the requested fields via the
	// recorded ListFields).
	Torrents []torrent.Torrent

	// Details maps a torrent ID to the detail TorrentDetail records by id.
	Details map[int64]torrent.TorrentDetail

	// Session is returned by GetSession.
	Session torrent.SessionSettings

	// Port is returned by TestPort.
	Port torrent.PortTest

	// Free is returned by FreeSpace.
	Free torrent.FreeSpace

	// Add is returned by AddTorrent.
	Add torrent.AddResult

	// Err, when non-nil, is returned by every Backend method (reads and writes),
	// so a test can simulate torrent.ErrAuth, torrent.ErrUnreachable, or
	// torrent.ErrServer. Writes are still recorded before the error is returned,
	// so a test can assert both that the call was attempted and that it failed.
	Err error

	// ListFields records the field projection passed to the most recent
	// ListTorrents call, so a test can assert a polling caller requested only the
	// cheap subset.
	ListFields []torrent.Field

	// Mutations records every write the code under test performed, in order, for
	// assertions. Reads are not recorded here.
	Mutations []Mutation

	// Closed counts the number of times Close has been called.
	Closed int
}

// Mutation is one recorded write against the Backend. Kind names the method
// (e.g. "start", "stop", "remove", "set-files"); Selector is the targeted
// selection; the remaining fields carry the method's extra arguments (only the
// ones relevant to Kind are set).
type Mutation struct {
	// Kind is the mutation method name (e.g. "start", "remove", "move").
	Kind string

	// Selector is the targeted selection (zero for AddTorrent and SetSession,
	// which are not selector-scoped).
	Selector torrent.Selector

	// Now carries StartTorrents' start-now flag.
	Now bool

	// DeleteData carries RemoveTorrents' delete-local-data flag.
	DeleteData bool

	// Location carries MoveTorrents' destination directory.
	Location string

	// Move carries MoveTorrents' relocate flag.
	Move bool

	// Changes carries SetTorrents' or SetSession's per-key changes.
	Changes map[string]any

	// Files carries SetFiles' per-file changes.
	Files torrent.FileChanges

	// Add carries AddTorrent's request.
	Add torrent.AddRequest
}

// Compile-time assertions that Backend satisfies the neutral interfaces.
var (
	_ torrent.Reader  = (*Backend)(nil)
	_ torrent.Mutator = (*Backend)(nil)
	_ torrent.Backend = (*Backend)(nil)
)

// ServerInfo returns the canned Server (or Err).
func (b *Backend) ServerInfo(context.Context) (torrent.ServerInfo, error) {
	if b.Err != nil {
		return torrent.ServerInfo{}, b.Err
	}

	return b.Server, nil
}

// Stats returns the canned StatsValue (or Err).
func (b *Backend) Stats(context.Context) (torrent.Stats, error) {
	if b.Err != nil {
		return torrent.Stats{}, b.Err
	}

	return b.StatsValue, nil
}

// ListTorrents records the field projection and returns the canned Torrents
// filtered by sel (or Err). An All selector returns every torrent; otherwise
// only the torrents named by the selector's IDs/Hashes are returned.
func (b *Backend) ListTorrents(_ context.Context, sel torrent.Selector, fields ...torrent.Field) ([]torrent.Torrent, error) {
	b.ListFields = fields

	if b.Err != nil {
		return nil, b.Err
	}

	if sel.All || len(sel.RPCIDs()) == 0 {
		out := make([]torrent.Torrent, len(b.Torrents))
		copy(out, b.Torrents)

		return out, nil
	}

	wantID := make(map[int64]bool, len(sel.IDs))
	for _, id := range sel.IDs {
		wantID[id] = true
	}

	wantHash := make(map[string]bool, len(sel.Hashes))
	for _, h := range sel.Hashes {
		wantHash[h] = true
	}

	out := make([]torrent.Torrent, 0, len(b.Torrents))
	for _, t := range b.Torrents {
		if wantID[t.ID] || wantHash[t.Hash] {
			out = append(out, t)
		}
	}

	return out, nil
}

// TorrentDetail returns the canned detail for id (or Err). An id with no seeded
// detail returns torrent.ErrServer so a test exercising the not-found path gets
// a neutral sentinel rather than a zero struct.
func (b *Backend) TorrentDetail(_ context.Context, id int64) (torrent.TorrentDetail, error) {
	if b.Err != nil {
		return torrent.TorrentDetail{}, b.Err
	}

	detail, ok := b.Details[id]
	if !ok {
		return torrent.TorrentDetail{}, torrent.ErrServer
	}

	return detail, nil
}

// GetSession returns the canned Session (or Err).
func (b *Backend) GetSession(context.Context) (torrent.SessionSettings, error) {
	if b.Err != nil {
		return torrent.SessionSettings{}, b.Err
	}

	return b.Session, nil
}

// TestPort returns the canned Port (or Err).
func (b *Backend) TestPort(context.Context) (torrent.PortTest, error) {
	if b.Err != nil {
		return torrent.PortTest{}, b.Err
	}

	return b.Port, nil
}

// FreeSpace returns the canned Free with its Path set to the queried path (or
// Err).
func (b *Backend) FreeSpace(_ context.Context, path string) (torrent.FreeSpace, error) {
	if b.Err != nil {
		return torrent.FreeSpace{}, b.Err
	}

	free := b.Free
	if free.Path == "" {
		free.Path = path
	}

	return free, nil
}

// StartTorrents records a "start" mutation and returns Err.
func (b *Backend) StartTorrents(_ context.Context, sel torrent.Selector, now bool) error {
	b.record(Mutation{Kind: "start", Selector: sel, Now: now})

	return b.Err
}

// StopTorrents records a "stop" mutation and returns Err.
func (b *Backend) StopTorrents(_ context.Context, sel torrent.Selector) error {
	b.record(Mutation{Kind: "stop", Selector: sel})

	return b.Err
}

// VerifyTorrents records a "verify" mutation and returns Err.
func (b *Backend) VerifyTorrents(_ context.Context, sel torrent.Selector) error {
	b.record(Mutation{Kind: "verify", Selector: sel})

	return b.Err
}

// ReannounceTorrents records a "reannounce" mutation and returns Err.
func (b *Backend) ReannounceTorrents(_ context.Context, sel torrent.Selector) error {
	b.record(Mutation{Kind: "reannounce", Selector: sel})

	return b.Err
}

// QueueMoveTop records a "queue-top" mutation and returns Err.
func (b *Backend) QueueMoveTop(_ context.Context, sel torrent.Selector) error {
	b.record(Mutation{Kind: "queue-top", Selector: sel})

	return b.Err
}

// QueueMoveUp records a "queue-up" mutation and returns Err.
func (b *Backend) QueueMoveUp(_ context.Context, sel torrent.Selector) error {
	b.record(Mutation{Kind: "queue-up", Selector: sel})

	return b.Err
}

// QueueMoveDown records a "queue-down" mutation and returns Err.
func (b *Backend) QueueMoveDown(_ context.Context, sel torrent.Selector) error {
	b.record(Mutation{Kind: "queue-down", Selector: sel})

	return b.Err
}

// QueueMoveBottom records a "queue-bottom" mutation and returns Err.
func (b *Backend) QueueMoveBottom(_ context.Context, sel torrent.Selector) error {
	b.record(Mutation{Kind: "queue-bottom", Selector: sel})

	return b.Err
}

// RemoveTorrents records a "remove" mutation and returns Err.
func (b *Backend) RemoveTorrents(_ context.Context, sel torrent.Selector, deleteData bool) error {
	b.record(Mutation{Kind: "remove", Selector: sel, DeleteData: deleteData})

	return b.Err
}

// MoveTorrents records a "move" mutation and returns Err.
func (b *Backend) MoveTorrents(_ context.Context, sel torrent.Selector, location string, move bool) error {
	b.record(Mutation{Kind: "move", Selector: sel, Location: location, Move: move})

	return b.Err
}

// SetTorrents records a "set" mutation and returns Err.
func (b *Backend) SetTorrents(_ context.Context, sel torrent.Selector, changes map[string]any) error {
	b.record(Mutation{Kind: "set", Selector: sel, Changes: changes})

	return b.Err
}

// SetFiles records a "set-files" mutation and returns Err.
func (b *Backend) SetFiles(_ context.Context, sel torrent.Selector, changes torrent.FileChanges) error {
	b.record(Mutation{Kind: "set-files", Selector: sel, Files: changes})

	return b.Err
}

// AddTorrent records an "add" mutation and returns the canned Add (or Err).
func (b *Backend) AddTorrent(_ context.Context, req torrent.AddRequest) (torrent.AddResult, error) {
	b.record(Mutation{Kind: "add", Add: req})

	if b.Err != nil {
		return torrent.AddResult{}, b.Err
	}

	return b.Add, nil
}

// SetSession records a "set-session" mutation and returns Err.
func (b *Backend) SetSession(_ context.Context, changes map[string]any) error {
	b.record(Mutation{Kind: "set-session", Changes: changes})

	return b.Err
}

// Close records the call (incrementing Closed) and returns nil. It is safe to
// call more than once.
func (b *Backend) Close() error {
	b.Closed++

	return nil
}

// record appends m to Mutations.
func (b *Backend) record(m Mutation) {
	b.Mutations = append(b.Mutations, m)
}
