// This file contains the domain types the Client decodes Prowlarr's REST
// responses into. Prowlarr's wire fields are camelCase; the domain types carry
// snake_case json tags, matching the Transmission domain model and the stable
// scripting surface snatcher's --json output exposes. Each type therefore has a
// small unexported wire struct that decodes the response and projects into it.
package prowlarr

import "encoding/json"

// SystemStatus is a Prowlarr instance's self-description, returned by
// Client.SystemStatus. It models only the fields snatcher renders; the rest of
// Prowlarr's SystemResource is reachable through the raw api escape hatch.
type SystemStatus struct {
	// AppName is the application's own name, e.g. "Prowlarr".
	AppName string `json:"app_name"`

	// InstanceName is the operator-assigned name for this instance.
	InstanceName string `json:"instance_name"`

	// Version is the running version, e.g. "1.37.0.5076".
	Version string `json:"version"`
}

// systemStatusWire is Prowlarr's SystemResource as it arrives on the wire.
type systemStatusWire struct {
	AppName      string `json:"appName"`
	InstanceName string `json:"instanceName"`
	Version      string `json:"version"`
}

// status projects the wire shape into the domain type. The two differ only in
// their json tags, so the conversion is direct; the compiler enforces that they
// stay field-for-field identical, and a field that stops matching has to be
// projected explicitly here.
func (w systemStatusWire) status() SystemStatus {
	return SystemStatus(w)
}

// Release is one search result: Prowlarr's ReleaseResource reduced to the fields
// snatcher renders, plus the exact bytes it arrived as.
//
// The typed fields exist for rendering, not for round-tripping. Prowlarr's grab
// endpoint wants the whole ReleaseResource back, and re-encoding only the
// modeled fields would silently drop anything a newer Prowlarr adds — so Raw
// keeps the server's original object and is what gets POSTed back.
//
// The json tags are snatcher's stable scripting contract: additive changes only.
type Release struct {
	// Raw is the exact ReleaseResource object the server sent, retained so a grab
	// can hand the release back verbatim. It is excluded from snatcher's own
	// --json output, which would otherwise carry every field twice.
	Raw json.RawMessage `json:"-"`

	// GUID is the indexer-assigned identifier for this release. Together with
	// IndexerID it names a release exactly, with no dependence on result order.
	GUID string `json:"guid"`

	// Title is the release name as the indexer published it. It is untrusted
	// server output.
	Title string `json:"title"`

	// Indexer is the display name of the indexer that returned the release.
	Indexer string `json:"indexer"`

	// IndexerID is the numeric id of that indexer, as accepted by
	// SearchParams.IndexerIDs.
	IndexerID int `json:"indexer_id"`

	// Age is how many days ago the release was published.
	Age int `json:"age"`

	// PublishDate is the publication timestamp as the server formatted it. It is
	// carried verbatim rather than parsed: an indexer with an unexpected format
	// must not fail the whole result set.
	PublishDate string `json:"publish_date"`

	// Size is the release size in bytes.
	Size int64 `json:"size"`

	// Files is the number of files in the release, or nil when the indexer does
	// not report it. Nil is distinct from a reported zero.
	Files *int `json:"files"`

	// Grabs is how many times the release has been grabbed, or nil when the
	// indexer does not report it.
	Grabs *int `json:"grabs"`

	// Seeders is the reported seeder count, or nil when the indexer does not
	// report it (as for a usenet release).
	Seeders *int `json:"seeders"`

	// Leechers is the reported leecher count, or nil when the indexer does not
	// report it.
	Leechers *int `json:"leechers"`

	// Protocol is the release's download protocol: "torrent", "usenet", or
	// "unknown".
	Protocol string `json:"protocol"`

	// Categories are the torznab categories the indexer assigned.
	Categories []Category `json:"categories"`

	// InfoHash is the torrent info-hash, when the indexer publishes one.
	InfoHash string `json:"info_hash"`

	// DownloadURL is where the .torrent (or .nzb) is fetched from. It may point
	// at Prowlarr itself or straight at the indexer.
	DownloadURL string `json:"download_url"`

	// MagnetURL is the magnet link, when the indexer publishes one. It is the
	// fallback when DownloadURL is empty.
	MagnetURL string `json:"magnet_url"`

	// InfoURL is the release's page on the indexer.
	InfoURL string `json:"info_url"`

	// CommentURL is the release's comments page on the indexer.
	CommentURL string `json:"comment_url"`
}

// releaseWire is Prowlarr's ReleaseResource as it arrives on the wire. Only the
// modeled fields are listed; the rest survive on Release.Raw.
type releaseWire struct {
	GUID        string         `json:"guid"`
	Title       string         `json:"title"`
	Indexer     string         `json:"indexer"`
	IndexerID   int            `json:"indexerId"`
	Age         int            `json:"age"`
	PublishDate string         `json:"publishDate"`
	Size        int64          `json:"size"`
	Files       *int           `json:"files"`
	Grabs       *int           `json:"grabs"`
	Seeders     *int           `json:"seeders"`
	Leechers    *int           `json:"leechers"`
	Protocol    string         `json:"protocol"`
	Categories  []categoryWire `json:"categories"`
	InfoHash    string         `json:"infoHash"`
	DownloadURL string         `json:"downloadUrl"`
	MagnetURL   string         `json:"magnetUrl"`
	InfoURL     string         `json:"infoUrl"`
	CommentURL  string         `json:"commentUrl"`
}

// release projects the wire shape into the domain type. Raw is filled in by the
// decoder, which is the only layer holding the original bytes.
func (w releaseWire) release() Release {
	return Release{
		GUID:        w.GUID,
		Title:       w.Title,
		Indexer:     w.Indexer,
		IndexerID:   w.IndexerID,
		Age:         w.Age,
		PublishDate: w.PublishDate,
		Size:        w.Size,
		Files:       w.Files,
		Grabs:       w.Grabs,
		Seeders:     w.Seeders,
		Leechers:    w.Leechers,
		Protocol:    w.Protocol,
		Categories:  categories(w.Categories),
		InfoHash:    w.InfoHash,
		DownloadURL: w.DownloadURL,
		MagnetURL:   w.MagnetURL,
		InfoURL:     w.InfoURL,
		CommentURL:  w.CommentURL,
	}
}

// Category is one torznab category, as Prowlarr's IndexerCategory. Categories
// nest one level in Prowlarr's default tree (a parent such as "TV" holding
// "TV/HD"), but the wire shape is recursive and so is this one.
type Category struct {
	// ID is the torznab category id, as accepted by SearchParams.Categories.
	ID int `json:"id"`

	// Name is the category's display name. It is untrusted server output.
	Name string `json:"name"`

	// Description is the category's longer description, often empty.
	Description string `json:"description"`

	// SubCategories are the categories nested under this one.
	SubCategories []Category `json:"sub_categories"`
}

// categoryWire is Prowlarr's IndexerCategory as it arrives on the wire.
type categoryWire struct {
	ID            int            `json:"id"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	SubCategories []categoryWire `json:"subCategories"`
}

// category projects the wire shape into the domain type, recursing into the
// nested categories.
func (w categoryWire) category() Category {
	return Category{
		ID:            w.ID,
		Name:          w.Name,
		Description:   w.Description,
		SubCategories: categories(w.SubCategories),
	}
}

// categories projects a slice of wire categories, preserving server order. A nil
// or empty input stays nil so an absent subCategories key does not become an
// empty array in snatcher's output.
func categories(wires []categoryWire) []Category {
	if len(wires) == 0 {
		return nil
	}

	out := make([]Category, 0, len(wires))
	for _, wire := range wires {
		out = append(out, wire.category())
	}

	return out
}

// Indexer is one configured indexer, as Prowlarr's IndexerResource reduced to
// the fields `search indexers` renders and the TUI's indexer dropdown needs. The
// rest of the resource — including its credential-bearing fields list — is
// deliberately not modeled; it is reachable through the raw api escape hatch.
type Indexer struct {
	// ID is the indexer's numeric id, as accepted by SearchParams.IndexerIDs.
	ID int `json:"id"`

	// Name is the indexer's display name. It is untrusted server output.
	Name string `json:"name"`

	// Protocol is the indexer's download protocol: "torrent", "usenet", or
	// "unknown".
	Protocol string `json:"protocol"`

	// Privacy is the indexer's privacy level: "public", "semiPrivate", or
	// "private".
	Privacy string `json:"privacy"`

	// Enable reports whether the indexer is enabled and so participates in a
	// search that does not name indexers explicitly.
	Enable bool `json:"enable"`

	// Priority is the indexer's search priority, lower being higher priority.
	Priority int `json:"priority"`

	// SupportsSearch reports whether the indexer can be searched at all.
	SupportsSearch bool `json:"supports_search"`

	// SupportsPagination reports whether the indexer honors limit/offset, which
	// is what makes `search --pages` meaningful for it.
	SupportsPagination bool `json:"supports_pagination"`

	// Language is the indexer's content language tag, e.g. "en-US".
	Language string `json:"language"`
}

// indexerWire is Prowlarr's IndexerResource as it arrives on the wire.
type indexerWire struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	Protocol           string `json:"protocol"`
	Privacy            string `json:"privacy"`
	Enable             bool   `json:"enable"`
	Priority           int    `json:"priority"`
	SupportsSearch     bool   `json:"supportsSearch"`
	SupportsPagination bool   `json:"supportsPagination"`
	Language           string `json:"language"`
}

// indexer projects the wire shape into the domain type. The two differ only in
// their json tags, so the conversion is direct; a field that stops matching has
// to be projected explicitly here.
func (w indexerWire) indexer() Indexer {
	return Indexer(w)
}
