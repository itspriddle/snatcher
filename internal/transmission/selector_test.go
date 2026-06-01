package transmission_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// fortyHex is a valid 40-character SHA1 info-hash used across the selector
// tests.
const fortyHex = "0123456789abcdef0123456789abcdef01234567"

func TestParseSelector(t *testing.T) {
	t.Run("ids only", func(t *testing.T) {
		sel, err := transmission.ParseSelector([]string{"1", "42"}, false)
		require.NoError(t, err)
		assert.Equal(t, []int64{1, 42}, sel.IDs)
		assert.Empty(t, sel.Hashes)
		assert.False(t, sel.All)
	})

	t.Run("hashes only", func(t *testing.T) {
		sel, err := transmission.ParseSelector([]string{fortyHex}, false)
		require.NoError(t, err)
		assert.Empty(t, sel.IDs)
		assert.Equal(t, []string{fortyHex}, sel.Hashes)
		assert.False(t, sel.All)
	})

	t.Run("hashes are lowercased", func(t *testing.T) {
		sel, err := transmission.ParseSelector([]string{strings.ToUpper(fortyHex)}, false)
		require.NoError(t, err)
		assert.Equal(t, []string{fortyHex}, sel.Hashes)
	})

	t.Run("mixed ids and hashes", func(t *testing.T) {
		sel, err := transmission.ParseSelector([]string{"7", fortyHex, "9"}, false)
		require.NoError(t, err)
		assert.Equal(t, []int64{7, 9}, sel.IDs)
		assert.Equal(t, []string{fortyHex}, sel.Hashes)
	})

	t.Run("all", func(t *testing.T) {
		sel, err := transmission.ParseSelector(nil, true)
		require.NoError(t, err)
		assert.True(t, sel.All)
		assert.Empty(t, sel.IDs)
		assert.Empty(t, sel.Hashes)
	})

	t.Run("neither args nor all errors", func(t *testing.T) {
		_, err := transmission.ParseSelector(nil, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--all")
	})

	t.Run("both args and all errors", func(t *testing.T) {
		_, err := transmission.ParseSelector([]string{"1"}, true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot combine")
	})

	t.Run("invalid token errors and names it", func(t *testing.T) {
		_, err := transmission.ParseSelector([]string{"foo"}, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"foo"`)
	})

	t.Run("short hex is not a hash", func(t *testing.T) {
		short := fortyHex[:39]
		_, err := transmission.ParseSelector([]string{short}, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), short)
	})

	t.Run("long hex is not a hash", func(t *testing.T) {
		long := fortyHex + "0"
		_, err := transmission.ParseSelector([]string{long}, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), long)
	})

	t.Run("numeric overflow errors and names it", func(t *testing.T) {
		overflow := "99999999999999999999999999"
		_, err := transmission.ParseSelector([]string{overflow}, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), overflow)
	})
}

func TestSelectorRPCIDs(t *testing.T) {
	t.Run("ids only", func(t *testing.T) {
		sel := transmission.Selector{IDs: []int64{1, 2}}
		assert.Equal(t, []any{int64(1), int64(2)}, sel.RPCIDs())
	})

	t.Run("hashes only", func(t *testing.T) {
		sel := transmission.Selector{Hashes: []string{fortyHex}}
		assert.Equal(t, []any{fortyHex}, sel.RPCIDs())
	})

	t.Run("mixed ids then hashes", func(t *testing.T) {
		sel := transmission.Selector{IDs: []int64{3}, Hashes: []string{fortyHex}}
		assert.Equal(t, []any{int64(3), fortyHex}, sel.RPCIDs())
	})

	t.Run("all returns nil so the param is omitted", func(t *testing.T) {
		sel := transmission.Selector{All: true}
		assert.Nil(t, sel.RPCIDs())
	})

	t.Run("parse then RPCIDs round-trips a mixed selection", func(t *testing.T) {
		sel, err := transmission.ParseSelector([]string{"5", fortyHex}, false)
		require.NoError(t, err)
		assert.Equal(t, []any{int64(5), fortyHex}, sel.RPCIDs())
	})
}
