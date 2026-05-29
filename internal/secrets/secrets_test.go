package secrets

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const testURL = "http://localhost:9091/transmission/rpc"

func TestSetGetRoundTrip(t *testing.T) {
	keyring.MockInit()

	require.NoError(t, Set(testURL, "s3cret"))

	got, err := Get(testURL)
	require.NoError(t, err)
	assert.Equal(t, "s3cret", got)
}

func TestSetOverwritesExisting(t *testing.T) {
	keyring.MockInit()

	require.NoError(t, Set(testURL, "old"))
	require.NoError(t, Set(testURL, "new"))

	got, err := Get(testURL)
	require.NoError(t, err)
	assert.Equal(t, "new", got)
}

func TestGetMissingReturnsNotFound(t *testing.T) {
	keyring.MockInit()

	got, err := Get(testURL)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, err, keyring.ErrNotFound)
	assert.Empty(t, got)
}

func TestDeleteRemovesEntry(t *testing.T) {
	keyring.MockInit()

	require.NoError(t, Set(testURL, "s3cret"))
	require.NoError(t, Delete(testURL))

	_, err := Get(testURL)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestDeleteMissingReturnsNotFound(t *testing.T) {
	keyring.MockInit()

	err := Delete(testURL)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, err, keyring.ErrNotFound)
}

func TestSecretsKeyedByServerURL(t *testing.T) {
	keyring.MockInit()

	const otherURL = "http://other:9091/transmission/rpc"

	require.NoError(t, Set(testURL, "first"))
	require.NoError(t, Set(otherURL, "second"))

	first, err := Get(testURL)
	require.NoError(t, err)
	assert.Equal(t, "first", first)

	second, err := Get(otherURL)
	require.NoError(t, err)
	assert.Equal(t, "second", second)
}
