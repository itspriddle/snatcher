package torrent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/itspriddle/snatcher/internal/torrent"
)

func TestClassifySource(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want torrent.Source
	}{
		{name: "magnet link", arg: "magnet:?xt=urn:btih:abc123", want: torrent.SourceMagnet},
		{name: "magnet uppercase scheme", arg: "MAGNET:?xt=urn:btih:abc", want: torrent.SourceMagnet},
		{name: "http url", arg: "http://example.com/a.torrent", want: torrent.SourceURL},
		{name: "https url", arg: "https://example.com/a.torrent", want: torrent.SourceURL},
		{name: "https uppercase scheme", arg: "HTTPS://example.com/a.torrent", want: torrent.SourceURL},
		{name: "relative file path", arg: "a.torrent", want: torrent.SourceFile},
		{name: "absolute file path", arg: "/tmp/a.torrent", want: torrent.SourceFile},
		{name: "file with http in name", arg: "httpfoo.torrent", want: torrent.SourceFile},
		{name: "empty string is a file", arg: "", want: torrent.SourceFile},
		{name: "ftp scheme is a file", arg: "ftp://example.com/a.torrent", want: torrent.SourceFile},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, torrent.ClassifySource(tt.arg))
		})
	}
}

func TestSourceString(t *testing.T) {
	assert.Equal(t, "file", torrent.SourceFile.String())
	assert.Equal(t, "url", torrent.SourceURL.String())
	assert.Equal(t, "magnet", torrent.SourceMagnet.String())
	assert.Equal(t, "unknown", torrent.Source(99).String())
}
