package tui

import (
	"fmt"
	"strconv"

	"github.com/itspriddle/snatcher/internal/output"
	"github.com/itspriddle/snatcher/internal/transmission"
)

// overviewLabelWidth is the overview's label column: the widest label with its
// colon ("Downloading Time:", 17 cells) plus the two cells of padding
// `torrent show`'s tabwriter puts after it, so the pane and the command line up
// the same way.
const overviewLabelWidth = 19

// overviewDetailRow is one detail-only line of the overview: a field a list
// Torrent does not carry.
type overviewDetailRow struct {
	header string
	value  func(d transmission.TorrentDetail) string
}

// overviewDetailRows mirror the detail-only half of `torrent show`'s field list
// (detailFieldKeys in internal/commands/show.go): the same headers, order,
// renderers, and sanitization. The list-field half is columnDefs, which already
// mirrors internal/commands/list.go. internal/tui does not import
// internal/commands, so this is a copy pinned by TestOverviewMirrorsShow.
// magnet_link is left out, as show leaves it out of its default set; y m yanks
// it.
var overviewDetailRows = []overviewDetailRow{
	{"Comment", func(d transmission.TorrentDetail) string { return output.SanitizeTerminal(d.Comment) }},
	{"Creator", func(d transmission.TorrentDetail) string { return output.SanitizeTerminal(d.Creator) }},
	{"Created", func(d transmission.TorrentDetail) string { return output.Date(d.DateCreated) }},
	{"Private", func(d transmission.TorrentDetail) string { return yesNo(d.IsPrivate) }},
	{"Pieces", func(d transmission.TorrentDetail) string { return strconv.FormatInt(d.PieceCount, 10) }},
	{"Piece Size", func(d transmission.TorrentDetail) string { return output.Bytes(d.PieceSize) }},
	{"Downloaded Ever", func(d transmission.TorrentDetail) string { return output.Bytes(d.DownloadedEver) }},
	{"Uploaded Ever", func(d transmission.TorrentDetail) string { return output.Bytes(d.UploadedEver) }},
	{"Corrupt Ever", func(d transmission.TorrentDetail) string { return output.Bytes(d.CorruptEver) }},
	{"Have Valid", func(d transmission.TorrentDetail) string { return output.Bytes(d.HaveValid) }},
	{"Downloading Time", func(d transmission.TorrentDetail) string { return output.Duration(d.SecondsDownloading) }},
	{"Seeding Time", func(d transmission.TorrentDetail) string { return output.Duration(d.SecondsSeeding) }},
	{"Recheck", func(d transmission.TorrentDetail) string { return output.Percent(d.RecheckProgress) }},
	{"Metadata", func(d transmission.TorrentDetail) string { return output.Percent(d.MetadataPercentComplete) }},
}

// yesNo renders a boolean as the CLI does (internal/commands' yesNo).
func yesNo(b bool) string {
	if b {
		return "yes"
	}

	return "no"
}

// overviewLine formats one "Header: value" line.
func overviewLine(header, value string) string {
	return fmt.Sprintf("%-*s%s", overviewLabelWidth, header+":", value)
}

// overviewLines is the overview tab: `torrent show` in a pane. The list-field
// lines draw from the polled torrent at once; the detail-only lines wait for the
// detail, saying so while it loads. A fetch error leads the tab, so it is seen
// without scrolling, and the last detail stays under it.
func (p detailPane) overviewLines(st Styles) []string {
	t := *p.torrent

	var lines []string

	if p.err != nil {
		lines = append(lines, st.FieldError.Render(errorLine(p.err)), st.Muted.Render(errorHint(p.err)), "")
	}

	for _, key := range allColumnKeys {
		c, _ := columnDef(key)
		lines = append(lines, overviewLine(c.title, c.render(t, Icons{})))
	}

	for _, row := range overviewDetailRows {
		switch {
		case p.hasDetail:
			lines = append(lines, overviewLine(row.header, row.value(p.detail)))
		case p.fetches && p.err == nil:
			lines = append(lines, overviewLine(row.header, st.Muted.Render("loading…")))
		}
	}

	return lines
}
