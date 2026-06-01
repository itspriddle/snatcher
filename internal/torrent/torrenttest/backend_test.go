package torrenttest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/torrent"
	"github.com/itspriddle/snatcher/internal/torrent/torrenttest"
)

func TestBackendSatisfiesInterface(t *testing.T) {
	// A nil *Backend assigned to torrent.Backend is the compile-time check; this
	// runtime call confirms the fake is usable as the full interface.
	var backend torrent.Backend = &torrenttest.Backend{}
	require.NoError(t, backend.Close())
}

func TestBackendListTorrentsAll(t *testing.T) {
	backend := &torrenttest.Backend{
		Torrents: []torrent.Torrent{
			{ID: 1, Hash: "aaaa"},
			{ID: 2, Hash: "bbbb"},
		},
	}

	got, err := backend.ListTorrents(context.Background(), torrent.Selector{All: true})
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestBackendListTorrentsFiltersBySelector(t *testing.T) {
	backend := &torrenttest.Backend{
		Torrents: []torrent.Torrent{
			{ID: 1, Hash: "aaaa"},
			{ID: 2, Hash: "bbbb"},
			{ID: 3, Hash: "cccc"},
		},
	}

	// Match one by id and one by hash; the third is excluded.
	got, err := backend.ListTorrents(
		context.Background(),
		torrent.Selector{IDs: []int64{1}, Hashes: []string{"cccc"}},
	)
	require.NoError(t, err)

	require.Len(t, got, 2)
	assert.Equal(t, int64(1), got[0].ID)
	assert.Equal(t, int64(3), got[1].ID)
}

func TestBackendListTorrentsRecordsFields(t *testing.T) {
	backend := &torrenttest.Backend{}

	_, err := backend.ListTorrents(context.Background(), torrent.Selector{All: true}, torrent.PollFields...)
	require.NoError(t, err)
	assert.Equal(t, torrent.PollFields, backend.ListFields)
}

func TestBackendTorrentDetailNotFound(t *testing.T) {
	backend := &torrenttest.Backend{
		Details: map[int64]torrent.TorrentDetail{
			7: {Torrent: torrent.Torrent{ID: 7}, MagnetLink: "magnet:?xt=urn:btih:7"},
		},
	}

	got, err := backend.TorrentDetail(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, "magnet:?xt=urn:btih:7", got.MagnetLink)

	_, err = backend.TorrentDetail(context.Background(), 99)
	require.ErrorIs(t, err, torrent.ErrServer)
}

func TestBackendFreeSpaceEchoesPath(t *testing.T) {
	backend := &torrenttest.Backend{Free: torrent.FreeSpace{SizeBytes: 100}}

	got, err := backend.FreeSpace(context.Background(), "/data")
	require.NoError(t, err)
	assert.Equal(t, "/data", got.Path)
	assert.Equal(t, int64(100), got.SizeBytes)
}

func TestBackendSimulatesSentinels(t *testing.T) {
	for _, sentinel := range []error{torrent.ErrAuth, torrent.ErrUnreachable, torrent.ErrServer} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			backend := &torrenttest.Backend{Err: sentinel}

			_, err := backend.ServerInfo(context.Background())
			require.ErrorIs(t, err, sentinel)

			_, err = backend.ListTorrents(context.Background(), torrent.Selector{All: true})
			require.ErrorIs(t, err, sentinel)

			err = backend.StartTorrents(context.Background(), torrent.Selector{All: true}, false)
			assert.ErrorIs(t, err, sentinel)
		})
	}
}

func TestBackendRecordsMutations(t *testing.T) {
	backend := &torrenttest.Backend{}
	ctx := context.Background()
	sel := torrent.Selector{IDs: []int64{1}}

	require.NoError(t, backend.StartTorrents(ctx, sel, true))
	require.NoError(t, backend.RemoveTorrents(ctx, sel, true))
	require.NoError(t, backend.MoveTorrents(ctx, sel, "/dest", true))
	require.NoError(t, backend.SetFiles(ctx, sel, torrent.FileChanges{Wanted: []int{0, 1}}))

	require.Len(t, backend.Mutations, 4)

	assert.Equal(t, "start", backend.Mutations[0].Kind)
	assert.True(t, backend.Mutations[0].Now)

	assert.Equal(t, "remove", backend.Mutations[1].Kind)
	assert.True(t, backend.Mutations[1].DeleteData)

	assert.Equal(t, "move", backend.Mutations[2].Kind)
	assert.Equal(t, "/dest", backend.Mutations[2].Location)
	assert.True(t, backend.Mutations[2].Move)

	assert.Equal(t, "set-files", backend.Mutations[3].Kind)
	assert.Equal(t, []int{0, 1}, backend.Mutations[3].Files.Wanted)
}

func TestBackendRecordsMutationBeforeError(t *testing.T) {
	backend := &torrenttest.Backend{Err: torrent.ErrServer}

	err := backend.StopTorrents(context.Background(), torrent.Selector{All: true})
	require.ErrorIs(t, err, torrent.ErrServer)

	// The failed write is still recorded so a test can assert it was attempted.
	require.Len(t, backend.Mutations, 1)
	assert.Equal(t, "stop", backend.Mutations[0].Kind)
}

func TestBackendCloseCounts(t *testing.T) {
	backend := &torrenttest.Backend{}

	require.NoError(t, backend.Close())
	require.NoError(t, backend.Close())
	assert.Equal(t, 2, backend.Closed)
}

func TestBackendAddReturnsCannedResult(t *testing.T) {
	backend := &torrenttest.Backend{Add: torrent.AddResult{ID: 5, Name: "ubuntu"}}

	got, err := backend.AddTorrent(context.Background(), torrent.AddRequest{Magnet: "magnet:?x"})
	require.NoError(t, err)
	assert.Equal(t, int64(5), got.ID)

	require.Len(t, backend.Mutations, 1)
	assert.Equal(t, "add", backend.Mutations[0].Kind)
	assert.Equal(t, "magnet:?x", backend.Mutations[0].Add.Magnet)
}

// errSentinel confirms a non-neutral error also propagates unchanged.
var errSentinel = errors.New("boom")

func TestBackendPropagatesArbitraryError(t *testing.T) {
	backend := &torrenttest.Backend{Err: errSentinel}

	_, err := backend.Stats(context.Background())
	require.ErrorIs(t, err, errSentinel)
}
