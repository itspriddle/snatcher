package transmission

import "context"

// Reader is the read-only half of Backend.
type Reader interface {
	ServerInfo(ctx context.Context) (ServerInfo, error)
	Stats(ctx context.Context) (Stats, error)
	ListTorrents(ctx context.Context, sel Selector, fields ...Field) ([]Torrent, error)
	TorrentDetail(ctx context.Context, id int64) (TorrentDetail, error)
	GetSession(ctx context.Context) (SessionSettings, error)
	TestPort(ctx context.Context) (PortTest, error)
	FreeSpace(ctx context.Context, path string) (FreeSpace, error)
}

// Mutator is the write half of Backend.
type Mutator interface {
	StartTorrents(ctx context.Context, sel Selector, now bool) error
	StopTorrents(ctx context.Context, sel Selector) error
	VerifyTorrents(ctx context.Context, sel Selector) error
	ReannounceTorrents(ctx context.Context, sel Selector) error
	QueueMoveTop(ctx context.Context, sel Selector) error
	QueueMoveUp(ctx context.Context, sel Selector) error
	QueueMoveDown(ctx context.Context, sel Selector) error
	QueueMoveBottom(ctx context.Context, sel Selector) error
	RemoveTorrents(ctx context.Context, sel Selector, deleteData bool) error
	MoveTorrents(ctx context.Context, sel Selector, location string, move bool) error
	SetTorrents(ctx context.Context, sel Selector, changes map[string]any) error
	SetFiles(ctx context.Context, sel Selector, changes FileChanges) error
	AddTorrent(ctx context.Context, req AddRequest) (AddResult, error)
	SetSession(ctx context.Context, changes map[string]any) error
}

// Backend is the interface commands depend on. It composes Reader and Mutator
// and adds Close. The concrete implementation is *Client.
type Backend interface {
	Reader
	Mutator
	Close() error
}

// Compile-time assertions that *Client satisfies the interfaces.
var (
	_ Reader  = (*Client)(nil)
	_ Mutator = (*Client)(nil)
	_ Backend = (*Client)(nil)
)
