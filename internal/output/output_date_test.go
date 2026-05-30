package output_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/itspriddle/snatcher/internal/output"
)

func TestDateUnknown(t *testing.T) {
	assert.Equal(t, "-", output.Date(0), "zero (unset) renders as a dash")
	assert.Equal(t, "-", output.Date(-1), "negative renders as a dash")
}

func TestDatePositive(t *testing.T) {
	// A positive timestamp formats as a local YYYY-MM-DD date. Compare against
	// the same local conversion to stay timezone-independent.
	const ts int64 = 1_700_000_000
	want := time.Unix(ts, 0).Format("2006-01-02")

	assert.Equal(t, want, output.Date(ts))
}
