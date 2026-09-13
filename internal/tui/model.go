package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/itspriddle/snatcher/internal/transmission"
)

// window names the top-level windows. vision.md specifies three; the Prowlarr
// window is T8's, and its ctrl+p binding is registered now so the collision test
// sees the full global set.
type window int

const (
	// windowTorrents is the default window.
	windowTorrents window = iota

	// windowSettings is the settings window.
	windowSettings
)

// inputOwner names the text input that owns the keyboard while
// Model.inputFocused is set, so a keystroke, a paste, or a cursor blink reaches
// the input that is actually focused, and a blur runs that input's own rule.
type inputOwner int

const (
	// ownerNone is no input, or one the owner was never recorded for.
	ownerNone inputOwner = iota

	// ownerSettings is the settings window's row editor, whose blur discards
	// its draft.
	ownerSettings

	// ownerSearch is the `/` field, whose blur keeps its text.
	ownerSearch

	// ownerPrompt is a prompt overlay's input, released when the prompt closes.
	ownerPrompt

	// ownerForm is a form overlay's row editor, whose blur discards the row's
	// draft and leaves the form open.
	ownerForm
)

// Model is the root model. It owns focus, the overlay stack, the pending chord,
// the styles, and the windows; the windows own their panes.
//
// It is a value type with value-receiver methods throughout, so every transition
// is a pure function of the model and the message — which is what makes the
// focus router, the esc ladder, and the selection model table-testable without a
// running program. The bubbles components inside it have pointer-receiver
// mutators; those are always called on the copy a method returns, never on a
// temporary.
type Model struct {
	// deps is what the launcher injected: the dialer, the resolved client
	// settings, and the version string.
	deps Deps

	// client is the dialed Transmission client, nil when the TUI was
	// constructed without dialing.
	client *transmission.Client

	// live is the refresh spine's state: what the last polls returned, and the
	// guards that keep polls from overlapping or outliving a server switch.
	live liveData

	// serverURL is the server the client is pointed at, and serverName the
	// alias it was dialed as. Both are shown in the status bar.
	serverURL  string
	serverName string

	// ctx is the program's context, which every backend command derives its
	// timeout from, so quitting cancels a round trip still in flight. New
	// starts it as context.Background; Run replaces it with the program's.
	ctx context.Context

	// width and height are the terminal's size in cells.
	width, height int

	// isDark is the terminal's background, defaulted to dark until the
	// terminal answers tea.RequestBackgroundColor. A light terminal therefore
	// renders one frame in dark colors rather than blocking on stdin at
	// startup, which is the trade v2 asks for.
	isDark bool

	// profile is the terminal's color profile, defaulted to TrueColor until
	// bubbletea reports the detected one as a tea.ColorProfileMsg. It is the
	// same trade isDark makes: one frame may render in colors the terminal
	// cannot show rather than blocking on stdin at startup.
	profile colorprofile.Profile

	// styles and icons are the rendering substrate, rebuilt on a theme change.
	styles Styles
	icons  Icons

	// window is the window in front.
	window window

	// torrents and settings are the windows.
	torrents torrentsWindow
	settings settingsWindow

	// overlays is the overlay stack, topmost last.
	overlays []overlay

	// chord is the pending multi-key sequence, if any.
	chord chord

	// inputFocused reports whether a text input owns the keyboard; it is the
	// router's flag. owner says which input it is.
	inputFocused bool
	owner        inputOwner

	// toast is a transient status-bar message, cleared by the next key press.
	toast string

	// help renders both the short-help line and the help overlay's columns.
	help help.Model
}

// New builds a Model from its dependencies without dialing, connecting, or
// touching the terminal. Run dials first and hands the result in; tests
// construct a Model directly.
func New(deps Deps) Model {
	m := Model{
		deps:    deps,
		ctx:     context.Background(),
		isDark:  true,
		profile: colorprofile.TrueColor,
		chord:   chordNone,
		help:    help.New(),
	}

	m = m.applyTheme(true)

	// The list preferences are read once, here. The settings window does not
	// edit them, so the only writers in this process are o and c themselves.
	view, err := listPrefs(deps.Config)
	if err != nil {
		m.toast = "config: " + errorLine(err)
	}

	m.torrents = newTorrentsWindow(m.styles, m.icons, deps.Torrents, view)
	m.settings = newSettingsWindow(m.styles, deps)

	return m
}

// applyTheme rebuilds the styles and icons for a light/dark answer and pushes
// them into the components that keep styles of their own.
//
// Panes hold no styles: they are handed them at view time. The table is the one
// exception, because it renders its rows into its own internal viewport, so it
// is re-styled here rather than at view time.
//
// It reads the color profile off the model rather than taking it as a second
// parameter, because the background-color reply and the color-profile message
// arrive independently and in either order: whichever lands funnels through
// here, and the other's answer is already on the model.
func (m Model) applyTheme(isDark bool) Model {
	m.isDark = isDark
	m.styles = newStyles(themeFor(m.deps.Theme, isDark), monochrome(m.profile))
	m.icons = newIcons(m.deps.NerdFont)
	m.help.Styles = m.styles.Help
	m.torrents.list = m.torrents.list.setStyles(m.styles)
	m.torrents.searchInput.SetStyles(m.styles.Input)
	m.settings = m.settings.setStyles(m.styles)

	return m
}

// Init asks the terminal for its background color and starts the refresh spine.
// Neither blocks: the color reply arrives as a tea.BackgroundColorMsg, and until
// it does the model renders with the dark palette. The first tick is immediate,
// and is dropped by tick when there is nothing to poll.
func (m Model) Init() tea.Cmd {
	first := tickMsg{gen: m.live.gen, seq: m.live.tickSeq}

	return tea.Batch(tea.RequestBackgroundColor, func() tea.Msg { return first })
}

// Update is the tea.Model entry point. It is a thin wrapper over update, which
// returns the concrete type so tests can drive transitions without asserting on
// a tea.Model interface at every step.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m.update(msg)
}

// update handles the message kinds the model names. Anything else goes to the
// focused text input — a bracketed paste, the cursor's blink ticks — and is
// dropped otherwise: a message nothing on screen asked for has nowhere to go.
func (m Model) update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.resize(msg.Width, msg.Height), nil
	case tea.BackgroundColorMsg:
		return m.applyTheme(msg.IsDark()), nil
	case tea.ColorProfileMsg:
		m.profile = msg.Profile

		return m.applyTheme(m.isDark), nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case settingsLoadedMsg:
		return m.settingsLoaded(msg)
	case settingsSavedMsg:
		return m.settingsSaved(msg)
	case tickMsg:
		return m.tick(msg)
	case pollMsg:
		return m.polled(msg)
	case sessionMsg:
		return m.sessionPolled(msg)
	case dialedMsg:
		return m.dialed(msg)
	case altSpeedMsg:
		return m.altSpeedSet(msg)
	case mutationMsg:
		return m.mutated(msg)
	case addedMsg:
		return m.added(msg)
	}

	return m.forwardToInput(msg)
}

// resize recomputes the layout and reflows the windows.
func (m Model) resize(w, h int) Model {
	m.width, m.height = w, h

	l := m.layout()
	if l.tooSmall {
		return m
	}

	m.help.SetWidth(w)
	m.torrents = m.torrents.resize(l)

	return m.syncSettings()
}

// layout is the current frame's geometry.
func (m Model) layout() layout {
	return computeLayout(m.width, m.height, m.torrents.filterOpen, m.torrents.detailOpen)
}

// View renders the frame. Alt-screen and the window title are fields of the
// view in bubbletea v2, not program options.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "snatcher"

	return v
}

// render draws the whole frame: the window, the overlay stack over it, and the
// two chrome lines beneath.
//
// Below the size floor it draws nothing but a notice. A pane's border alone
// costs two rows and two columns, so under 60x16 the UI would be borders framing
// nothing; saying so is more useful than drawing it.
func (m Model) render() string {
	l := m.layout()
	if l.tooSmall {
		return m.tooSmallView()
	}

	var body string

	switch m.window {
	case windowSettings:
		body = m.settings.view(m.styles, l)
	case windowTorrents:
		body = m.torrents.view(m.styles, l, m.listPlaceholder())
	}

	frame := m.composite(padFrame(body, l.body))

	return strings.Join([]string{frame, m.helpLineView(l.helpLine.w), m.statusBarView(l.statusBar.w)}, "\n")
}

// padFrame squares a window's rendered body off to exactly the body region, so
// the frame is the terminal's size whatever a pane rounded to.
func padFrame(body string, r rect) string {
	lines := clipLines(strings.Split(body, "\n"), r.h)

	for i, line := range lines {
		lines[i] = padLine(line, r.w)
	}

	return strings.Join(lines, "\n")
}

// tooSmallView is what is drawn below the size floor: the current size, the
// required size, and nothing else. Only quit is honored in this state.
func (m Model) tooSmallView() string {
	msg := fmt.Sprintf("terminal too small: %dx%d (need %dx%d)", m.width, m.height, minWidth, minHeight)

	lines := make([]string, 0, m.height)

	for i := range m.height {
		if i == m.height/2 {
			lines = append(lines, padLine(centered(msg, m.width), m.width))

			continue
		}

		lines = append(lines, padLine("", m.width))
	}

	return strings.Join(lines, "\n")
}

// centered puts s in the middle of width cells.
func centered(s string, width int) string {
	if lipgloss.Width(s) >= width {
		return truncate(s, width)
	}

	return strings.Repeat(" ", (width-lipgloss.Width(s))/2) + s
}
