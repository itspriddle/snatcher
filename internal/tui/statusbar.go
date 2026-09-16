package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"github.com/itspriddle/snatcher/internal/output"
)

// noServerLabel is what the status bar and the list pane show when the TUI was
// constructed without dialing.
const noServerLabel = "no server"

// segmentSeparator joins the status bar's segments.
const segmentSeparator = " │ "

// The degrade ranks. When the bar does not fit, the highest rank gives up a
// form first. Rank zero never degrades: the server, an error, alt-speed, and
// the mark, unsaved, and chord counts are each the answer to "why did that do
// something I did not expect", and a bar that hid one at 80 columns would be
// hiding the thing it exists to say. The server segment degrades only as far as
// its bare name.
const (
	rankNever         = 0
	rankRates         = 3
	rankFree          = 4
	rankUsed          = 5
	rankServerVersion = 6
)

// barSegment is one status bar segment in every form it can take, fullest
// first. An empty form drops the segment altogether.
type barSegment struct {
	forms []string
	rank  int
}

// fixed is a segment with one form that never degrades.
func fixed(s string) barSegment {
	return barSegment{forms: []string{s}, rank: rankNever}
}

// statusBarView renders the bottom line: what snatcher is pointed at, what that server is doing, and the transient facts a user needs to
// understand what the next key will do. It fits the segments to the width left
// of the toast, degrading the least important first.
func (m Model) statusBarView(width int) string {
	if m.toast == "" {
		return m.styles.StatusBar.Render(padLine(fitSegments(m.barSegments(), width), width))
	}

	toast := m.styles.Toast.Render(output.SanitizeTerminal(m.toast))
	left := fitSegments(m.barSegments(), max(0, width-lipgloss.Width(toast)-1))

	return m.styles.StatusBar.Render(pad(left, toast, width))
}

// barSegments are the status bar's segments, in display order.
func (m Model) barSegments() []barSegment {
	segs := m.serverSegments()
	segs = append(segs, m.transferSegments()...)
	segs = append(segs, m.diskSegments()...)

	return append(segs, m.stateSegments()...)
}

// serverSegments are the server's name with its Transmission version, then the
// last reply's error. The version is the first thing to go; the name never is.
func (m Model) serverSegments() []barSegment {
	name := m.serverLabel()
	server := fixed(name)

	if v := serverVersion(m.live.info.Version); m.live.err == nil && m.live.hasSession && v != "" {
		server = barSegment{forms: []string{name + " (v" + v + ")", name}, rank: rankServerVersion}
	}

	segs := []barSegment{server}

	if m.live.err != nil {
		segs = append(segs, fixed(m.styles.Toast.Render("! "+errorShort(m.live.err))))
	}

	return segs
}

// transferSegments are the two rates, each with the limit in force, and the
// alt-speed marker.
func (m Model) transferSegments() []barSegment {
	var segs []barSegment

	if m.live.loaded {
		downLimit, upLimit := m.limits()

		segs = append(segs,
			rateSegment("↓", m.live.stats.DownloadSpeed, downLimit),
			rateSegment("↑", m.live.stats.UploadSpeed, upLimit),
		)
	}

	// Alt-speed is plain text rather than a glyph: Icons is the status column's,
	// and a mode that caps every transfer should read as words.
	if m.live.hasSession && m.live.session.AltSpeedEnabled {
		segs = append(segs, fixed(m.styles.Toast.Render("alt-speed")))
	}

	return segs
}

// diskSegments are the free space in the download directory and the bytes the
// polled torrents have on disk.
func (m Model) diskSegments() []barSegment {
	var segs []barSegment

	if m.live.hasFree {
		segs = append(segs, barSegment{forms: []string{"free " + output.Bytes(m.live.free.SizeBytes), ""}, rank: rankFree})
	}

	if m.live.loaded {
		segs = append(segs, barSegment{forms: []string{"used " + output.Bytes(m.usedBytes()), ""}, rank: rankUsed})
	}

	return segs
}

// stateSegments are the mark count, the unsaved count, and the pending chord.
//
// The mark count appears only when marks exist, and the chord prefix only while
// one is pending. Both are answers to "why did that key do something other than
// what I expected", and a status bar that always showed "marks: 0" would bury
// the one case that matters.
func (m Model) stateSegments() []barSegment {
	var segs []barSegment

	if n := m.torrents.sel.markCount(); n > 0 && m.window == windowTorrents {
		segs = append(segs, fixed(fmt.Sprintf("marks: %d", n)))
	}

	// File marks are counted apart from torrent marks: they are a different
	// selection, and a file menu acts on them from whichever tab it opens on.
	if n := m.torrents.detail.fileSel.markCount(); n > 0 && m.window == windowTorrents {
		segs = append(segs, fixed(fmt.Sprintf("file marks: %d", n)))
	}

	// Unsaved settings are shown from every window: leaving the settings window
	// does not save them, and the count is the reminder that they exist.
	if n := m.settings.dirtyCount(); n > 0 {
		segs = append(segs, fixed(fmt.Sprintf("unsaved: %d", n)))
	}

	if m.window == windowProwlarr {
		segs = append(segs, m.prowlarrSegments()...)
	}

	if m.chord.pending() {
		segs = append(segs, fixed("pending: "+m.chord.label()))
	}

	return segs
}

// prowlarrSegments are the Prowlarr window's own: the server it is on, or why it
// is on none, and how many results the table holds, with a "+" while + can fetch
// more. Neither degrades: they say what a search will be run against and how
// much of it is on screen.
func (m Model) prowlarrSegments() []barSegment {
	w := m.prowlarr

	var segs []barSegment

	switch {
	case w.dialing:
		segs = append(segs, fixed("prowlarr: "+output.SanitizeTerminal(w.pending)+"…"))
	case w.client == nil && w.dialErr != nil:
		segs = append(segs, fixed(m.styles.Toast.Render("prowlarr: ! "+errorShort(w.dialErr))))
	case w.client == nil:
	case w.err != nil:
		segs = append(segs, fixed(m.styles.Toast.Render("prowlarr: ! "+errorShort(w.err))))
	default:
		name := "prowlarr: " + output.SanitizeTerminal(w.serverName)
		if v := output.SanitizeTerminal(w.status.Version); v != "" {
			name += " (v" + v + ")"
		}

		segs = append(segs, fixed(name))
	}

	if (w.searched && !w.inFlight) || len(w.results) > 0 {
		count := fmt.Sprintf("results: %d", len(w.results))
		if !w.exhausted {
			count += "+"
		}

		segs = append(segs, fixed(count))
	}

	return segs
}

// rateSegment is one direction's rate: with its limit, the rate alone, and
// dropped. With no limit in force the first two forms are identical and
// collapse, so each degrade step changes what is drawn.
func rateSegment(arrow string, rate int64, limit string) barSegment {
	head := arrow + " " + output.Speed(rate)

	full := head
	if limit != "" {
		full += " " + limit
	}

	forms := slices.Compact([]string{full, head, ""})

	return barSegment{forms: forms, rank: rankRates}
}

// limits are the download and upload limits in force, as labels, or empty when
// none is. Alt-speed's limits win while it is on; otherwise a global limit
// counts only while it is enabled.
func (m Model) limits() (down, up string) {
	s := m.live.session

	switch {
	case !m.live.hasSession:
		return "", ""
	case s.AltSpeedEnabled:
		return limitLabel(s.AltSpeedDown), limitLabel(s.AltSpeedUp)
	}

	if s.SpeedLimitDownEnabled {
		down = limitLabel(s.SpeedLimitDown)
	}

	if s.SpeedLimitUpEnabled {
		up = limitLabel(s.SpeedLimitUp)
	}

	return down, up
}

// limitLabel renders a limit, stored by Transmission in KB/s, in the same units
// as the rate beside it, so 5120 KB/s reads as 5.0 MB/s.
func limitLabel(kbps int64) string {
	return "(" + output.Speed(kbps*1024) + ")"
}

// serverVersion is the version number off Transmission's long version string,
// "4.0.6 (38c164933e)" becoming "4.0.6". It is server-derived, so sanitized.
func serverVersion(long string) string {
	fields := strings.Fields(output.SanitizeTerminal(long))
	if len(fields) == 0 {
		return ""
	}

	return fields[0]
}

// usedBytes is the bytes the polled torrents have on disk. It sums the
// unfiltered set: a filter narrows the table, not the disk.
func (m Model) usedBytes() int64 {
	var n int64

	for _, t := range m.torrents.all {
		n += t.SizeWhenDone - t.LeftUntilDone
	}

	return n
}

// overlayHelp is the topmost overlay's own help bindings, when it words its keys
// itself and its stack is the one live.
func (m Model) overlayHelp(stack []Context) ([]key.Binding, bool) {
	top, ok := m.topOverlay()
	if !ok || !slices.Equal(stack, top.contexts()) {
		return nil, false
	}

	own, ok := top.(interface{ helpBindings() []key.Binding })
	if !ok {
		return nil, false
	}

	return own.helpBindings(), true
}

// fitSegments joins the segments into at most width cells, degrading one step at
// a time until the line fits or nothing is left to degrade. A line of rank-zero
// segments that still does not fit is returned as is, for the caller to
// truncate.
func fitSegments(segs []barSegment, width int) string {
	level := make([]int, len(segs))

	for {
		line := joinSegments(segs, level)
		if lipgloss.Width(line) <= width {
			return line
		}

		i := nextToDegrade(segs, level)
		if i < 0 {
			return line
		}

		level[i]++
	}
}

// joinSegments joins each segment's current form, skipping dropped ones.
func joinSegments(segs []barSegment, level []int) string {
	parts := make([]string, 0, len(segs))

	for i, s := range segs {
		if form := s.forms[level[i]]; form != "" {
			parts = append(parts, form)
		}
	}

	return strings.Join(parts, segmentSeparator)
}

// nextToDegrade picks the segment to take one step terser: the highest rank,
// then the least degraded within it — so the two rates lose their limits
// together before either is dropped — then the rightmost. It returns -1 when no
// segment can degrade further.
func nextToDegrade(segs []barSegment, level []int) int {
	best := -1

	for i, s := range segs {
		if s.rank == rankNever || level[i] >= len(s.forms)-1 {
			continue
		}

		if best < 0 || s.rank > segs[best].rank || (s.rank == segs[best].rank && level[i] <= level[best]) {
			best = i
		}
	}

	return best
}

// serverLabel is the server the TUI is pointed at: its alias, which is what the
// picker lists, or the URL's host when it has none. Never the full RPC URL: the
// path is the same on every Transmission server and spends width saying nothing.
func (m Model) serverLabel() string {
	switch {
	case m.serverURL == "":
		return noServerLabel
	case m.serverName != "":
		return output.SanitizeTerminal(m.serverName)
	}

	return hostOf(m.serverURL)
}

// helpLineView renders the one-line short help above the status bar, listing the
// keys the current context actually responds to.
func (m Model) helpLineView(width int) string {
	return padLine(m.help.ShortHelpView(m.shortBindings()), width)
}

// shortBindings are the handful of keys the short-help line advertises: the most
// specific context's bindings, then the always-true ones.
//
// It reads the same registry the router does, so the line cannot advertise a key
// that does not work — which is the reason the keymap is data in the first
// place.
func (m Model) shortBindings() []key.Binding {
	stack := m.contextStack()

	var out []key.Binding

	if own, ok := m.overlayHelp(stack); ok {
		// An overlay that words its own keys — a strict confirm, a prompt whose
		// esc cancels rather than blurs — says the same on this line as in its
		// box, so the line never offers a key the box refuses.
		out = own
	} else {
		for _, b := range registry {
			if len(stack) > 0 && b.Context == stack[0] {
				out = append(out, b.Key)
			}
		}
	}

	// The global pair is only advertised where it resolves. A modal stack — a
	// focused input, a confirm prompt, a pending chord — swallows ? and q, and a
	// help line offering them there would be advertising dead keys.
	if slices.Contains(stack, ctxGlobal) {
		for _, action := range []Action{actionHelp, actionQuit} {
			out = append(out, bindingFor(ctxGlobal, action))
		}
	}

	return out
}
