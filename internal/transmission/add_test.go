package transmission_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/itspriddle/snatcher/internal/transmission"
)

func TestClassifySource(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want transmission.Source
	}{
		{name: "magnet link", arg: "magnet:?xt=urn:btih:abc123", want: transmission.SourceMagnet},
		{name: "magnet uppercase scheme", arg: "MAGNET:?xt=urn:btih:abc", want: transmission.SourceMagnet},
		{name: "http url", arg: "http://example.com/a.torrent", want: transmission.SourceURL},
		{name: "https url", arg: "https://example.com/a.torrent", want: transmission.SourceURL},
		{name: "https uppercase scheme", arg: "HTTPS://example.com/a.torrent", want: transmission.SourceURL},
		{name: "relative file path", arg: "a.torrent", want: transmission.SourceFile},
		{name: "absolute file path", arg: "/tmp/a.torrent", want: transmission.SourceFile},
		{name: "file with http in name", arg: "httpfoo.torrent", want: transmission.SourceFile},
		{name: "empty string is a file", arg: "", want: transmission.SourceFile},
		{name: "ftp scheme is a file", arg: "ftp://example.com/a.torrent", want: transmission.SourceFile},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, transmission.ClassifySource(tt.arg))
		})
	}
}

func TestSourceString(t *testing.T) {
	assert.Equal(t, "file", transmission.SourceFile.String())
	assert.Equal(t, "url", transmission.SourceURL.String())
	assert.Equal(t, "magnet", transmission.SourceMagnet.String())
	assert.Equal(t, "unknown", transmission.Source(99).String())
}
