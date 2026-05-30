package torrent

import "strings"

// Source is the vendor-neutral classification of an upload argument: a local
// .torrent file path, an HTTP(S) URL, or a magnet link. The upload command uses
// it to decide how to populate an AddRequest and to tag each per-source result.
// In output it renders as a stable lowercase token ("file"/"url"/"magnet") via
// String(), mirroring the slug convention used elsewhere in this package.
type Source int

const (
	// SourceFile means the argument is a local .torrent file path. It is the
	// default classification for anything that is not a recognized URL or magnet
	// link.
	SourceFile Source = iota

	// SourceURL means the argument is an HTTP(S) URL the server fetches.
	SourceURL

	// SourceMagnet means the argument is a magnet link.
	SourceMagnet
)

// String returns the canonical lowercase token for the source kind, suitable
// for both human output and the JSON scripting contract. Unknown values render
// as "unknown".
func (s Source) String() string {
	switch s {
	case SourceFile:
		return "file"
	case SourceURL:
		return "url"
	case SourceMagnet:
		return "magnet"
	default:
		return "unknown"
	}
}

// ClassifySource classifies an upload argument into a Source. A "magnet:"
// prefix is a magnet link; an "http://" or "https://" prefix is a URL;
// anything else is treated as a local file path. The match is case-insensitive
// on the scheme, mirroring the small classification helper in selector.go.
func ClassifySource(arg string) Source {
	switch {
	case hasPrefixFold(arg, "magnet:"):
		return SourceMagnet
	case hasPrefixFold(arg, "http://"), hasPrefixFold(arg, "https://"):
		return SourceURL
	default:
		return SourceFile
	}
}

// hasPrefixFold reports whether s begins with prefix, ignoring ASCII case in
// the compared region. It lets ClassifySource accept e.g. "MAGNET:" and
// "HTTPS://" without allocating a lowercased copy of the whole argument.
func hasPrefixFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}

	return strings.EqualFold(s[:len(prefix)], prefix)
}

// AddRequest is the vendor-neutral set of parameters for adding a torrent.
// Exactly one of Metainfo, URL, or Magnet identifies the source: Metainfo is
// the raw .torrent content (the Transmission adapter base64-encodes it on the
// wire), URL is an HTTP(S) .torrent URL the server fetches, and Magnet is a
// magnet link. The remaining fields are options applied to the add.
type AddRequest struct {
	// Metainfo is the raw .torrent file content. When set, URL and Magnet are
	// empty.
	Metainfo []byte

	// URL is an HTTP(S) .torrent URL the server fetches. When set, Metainfo and
	// Magnet are empty.
	URL string

	// Magnet is a magnet link. When set, Metainfo and URL are empty.
	Magnet string

	// DownloadDir is the server-side destination directory; empty uses the
	// server default. The path is passed through verbatim (not resolved
	// client-side).
	DownloadDir string

	// Paused, when true, adds the torrent without starting it.
	Paused bool

	// Labels are the user labels to assign to the torrent.
	Labels []string
}

// AddResult is the vendor-neutral result of adding a torrent. Duplicate reports
// that the torrent was already present on the server (not an error); ID, Hash,
// and Name still describe the existing torrent in that case.
type AddResult struct {
	// ID is the backend's session-scoped numeric identifier for the torrent.
	ID int64 `json:"id"`

	// Hash is the torrent's info-hash string.
	Hash string `json:"hash"`

	// Name is the torrent's display name.
	Name string `json:"name"`

	// Duplicate reports whether the torrent was already present on the server.
	Duplicate bool `json:"duplicate"`
}
