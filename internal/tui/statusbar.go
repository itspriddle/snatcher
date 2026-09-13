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
// hiding the thing it exists to say.
const (
	rankNever         = 0
	rankVersion       = 2
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

// statusBarView renders the bottom line: what snatcher this is, what it is
// pointed at, what that server is doing, and the transient facts a user needs to
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
	segs := []barSegment{
		{forms: []string{"snatcher " + m.deps.Version, ""}, rank: rankVersion},
		fixed(m.hostLabel()),
	}

	segs = append(segs, m.serverSegments()...)
	segs = append(segs, m.transferSegments()...)
	segs = append(segs, m.diskSegments()...)

	return append(segs, m.stateSegments()...)
}

// serverSegments are the last reply's error and the Transmission version.
func (m Model) serverSegments() []barSegment {
	var segs []barSegment

	if m.live.err != nil {
		segs = append(segs, fixed(m.styles.Toast.Render("! "+errorShort(m.live.err))))
	}

	if v := serverVersion(m.live.info.Version); m.live.hasSession && v != "" {
		segs = append(segs, barSegment{forms: []string{"Transmission " + v, ""}, rank: rankServerVersion})
	}

	return segs
}

// transferSegments are the two rates, each with its sparkline and the limit in
// force, and the alt-speed marker.
func (m Model) transferSegments() []barSegment {
	var segs []barSegment

	if m.live.loaded {
		downLimit, upLimit := m.limits()

		segs = append(segs,
			rateSegment("↓", m.live.stats.DownloadSpeed, m.live.down, downLimit),
			rateSegment("↑", m.live.stats.UploadSpeed, m.live.up, upLimit),
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

	// Unsaved settings are shown from every window: leaving the settings window
	// does not save them, and the count is the reminder that they exist.
	if n := m.settings.dirtyCount(); n > 0 {
		segs = append(segs, fixed(fmt.Sprintf("unsaved: %d", n)))
	}

	if m.chord.pending() {
		segs = append(segs, fixed("pending: "+m.chord.label()))
	}

	return segs
}

// rateSegment is one direction's rate: full, without the sparkline, the rate
// alone, and dropped. Forms that come out identical — no sparkline yet, no limit
// in force — are collapsed, so each degrade step changes what is drawn.
func rateSegment(arrow string, rate int64, ring rateRing, limit string) barSegment {
	head := arrow + " " + output.Speed(rate)
	spark := sparkline(ring.values(), sparkSamples)

	forms := slices.Compact([]string{
		joinNonEmpty(head, spark, limit),
		joinNonEmpty(head, limit),
		head,
		"",
	})

	return barSegment{forms: forms, rank: rankRates}
}

// joinNonEmpty joins the non-empty parts with single spaces.
func joinNonEmpty(parts ...string) string {
	return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), " ")
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

// limitLabel renders a limit raw, in the KB/s Transmission stores it in, exactly
// as `server bandwidth show` does. output.Speed is IEC over bytes and would
// misstate it.
func limitLabel(kbps int64) string {
	return fmt.Sprintf("(%d KB/s)", kbps)
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
// then the least degraded within it — so the two rates lose their sparklines
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

// hostLabel is the server the TUI is pointed at, as alias@host rather than a
// full RPC URL: the path is the same on every Transmission server and spends
// status bar width saying nothing, and the alias is what the picker lists.
func (m Model) hostLabel() string {
	if m.serverURL == "" {
		return noServerLabel
	}

	host := hostOf(m.serverURL)

	if m.serverName != "" {
		host = output.SanitizeTerminal(m.serverName) + "@" + host
	}

	return host
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
