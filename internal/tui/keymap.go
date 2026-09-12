package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
)

// Context is one focus context: a named scope that owns some set of keys. A
// key press is resolved against an ordered stack of contexts, most specific
// first, so the same key means different things in the filter pane and the list
// pane without either knowing about the other.
//
// Contexts are not panes. A pane maps to a stack of contexts (see paneContexts),
// and the shared rungs of that stack — ctxNav, ctxGlobal — are declared once.
type Context int

// The focus contexts, in no particular order: precedence is a property of a
// stack, not of the enum.
const (
	// ctxGlobal holds the keys that mean the same thing everywhere: quit, help,
	// window switching, the esc ladder.
	ctxGlobal Context = iota

	// ctxNav holds the navigation keys shared by every pane that scrolls, plus
	// pane focus movement. Its actions are forwarded to the focused component
	// rather than handled at the root.
	ctxNav

	// ctxTorrents holds the torrents window's own keys, shared by all three of
	// its panes.
	ctxTorrents

	// ctxTorrentList holds the torrent verbs. Per vision.md, action verbs live
	// in a window's main pane and nowhere else in that window.
	ctxTorrentList

	// ctxTorrentFilter holds the filter sidebar's keys, including the group
	// actions whose blast radius is a whole filter group.
	ctxTorrentFilter

	// ctxTorrentDetail holds the detail pane's tab navigation. It deliberately
	// carries no torrent verbs.
	ctxTorrentDetail

	// ctxSettings holds the settings window's keys. Settings owns tab and
	// up/down itself, so it does not sit on top of ctxNav.
	ctxSettings

	// ctxTextInput is the whole stack while a text input has focus: a focused
	// input owns the keyboard, and only esc and enter escape it.
	ctxTextInput

	// ctxHelp is the help overlay's scroll keys.
	ctxHelp

	// ctxConfirm is the whole stack while a confirm overlay is open: its
	// answers, and nothing global.
	ctxConfirm

	// ctxChordYank is the second key of the y chord.
	ctxChordYank

	// ctxChordWindow is the second key of the ctrl+w chord.
	ctxChordWindow
)

// String names a Context for test failures and help column headings.
func (c Context) String() string {
	switch c {
	case ctxGlobal:
		return "global"
	case ctxNav:
		return "navigation"
	case ctxTorrents:
		return "torrents"
	case ctxTorrentList:
		return "torrent list"
	case ctxTorrentFilter:
		return "filter"
	case ctxTorrentDetail:
		return "detail"
	case ctxSettings:
		return "settings"
	case ctxTextInput:
		return "input"
	case ctxHelp:
		return "help"
	case ctxConfirm:
		return "confirm"
	case ctxChordYank:
		return "yank"
	case ctxChordWindow:
		return "window"
	}

	return "unknown"
}

// Action is the stable dotted name of something the TUI can do. It is the join
// between a key press and the code that runs: the registry maps keys to
// actions, the handler table maps actions to code, and T7's command palette
// lists actions by name — including the ones no key is bound to.
//
// These names are a surface. Renaming one is a user-visible change to the
// palette, so treat them the way the JSON tags on the domain types are treated.
type Action string

// The actions, grouped by the context that binds them.
const (
	actionQuit           Action = "app.quit"
	actionHelp           Action = "app.help"
	actionEscape         Action = "app.escape"
	actionRefresh        Action = "app.refresh"
	actionPalette        Action = "palette.open"
	actionWindowTorrents Action = "window.torrents"
	actionWindowProwlarr Action = "window.prowlarr"
	actionWindowSettings Action = "window.settings"
	actionServerPicker   Action = "server.picker"
	actionServerAltSpeed Action = "server.alt-speed"

	actionNavUp           Action = "nav.up"
	actionNavDown         Action = "nav.down"
	actionNavTop          Action = "nav.top"
	actionNavBottom       Action = "nav.bottom"
	actionNavHalfPageUp   Action = "nav.half-page-up"
	actionNavHalfPageDown Action = "nav.half-page-down"
	actionNavPageUp       Action = "nav.page-up"
	actionNavPageDown     Action = "nav.page-down"
	actionFocusNext       Action = "focus.next"
	actionFocusPrev       Action = "focus.prev"
	actionFocusChord      Action = "focus.chord"
	actionToggleDetail    Action = "pane.detail"

	actionToggleFilter   Action = "pane.filter"
	actionTorrentFilter  Action = "torrent.filter"
	actionTorrentSearch  Action = "torrent.search"
	actionSearchNext     Action = "torrent.search-next"
	actionSearchPrev     Action = "torrent.search-prev"
	actionTorrentSort    Action = "torrent.sort"
	actionTorrentColumns Action = "torrent.columns"
	actionTorrentAdd     Action = "torrent.add"
	actionTorrentAddOpts Action = "torrent.add-options"
	actionTabOverview    Action = "detail.tab.overview"
	actionTabFiles       Action = "detail.tab.files"
	actionTabTrackers    Action = "detail.tab.trackers"
	actionTabPeers       Action = "detail.tab.peers"
	actionTabPieces      Action = "detail.tab.pieces"

	actionToggleMark    Action = "selection.toggle-mark"
	actionDetailOpen    Action = "detail.open"
	actionYankChord     Action = "yank.chord"
	actionTorrentStart  Action = "torrent.start"
	actionTorrentStop   Action = "torrent.stop"
	actionTorrentVerify Action = "torrent.verify"
	actionTorrentReann  Action = "torrent.reannounce"
	actionTorrentRemove Action = "torrent.remove"
	actionQueueDown     Action = "torrent.queue-down"
	actionQueueUp       Action = "torrent.queue-up"
	actionTorrentLabels Action = "torrent.labels"
	actionTorrentMove   Action = "torrent.move"
	actionMenuOpen      Action = "menu.open"

	actionFilterApply Action = "filter.apply"
	actionGroupStart  Action = "filter.group-start"
	actionGroupStop   Action = "filter.group-stop"
	actionGroupVerify Action = "filter.group-verify"
	actionGroupRemove Action = "filter.group-remove"

	actionDetailPrevTab Action = "detail.prev-tab"
	actionDetailNextTab Action = "detail.next-tab"

	actionSettingsPrevTab   Action = "settings.prev-tab"
	actionSettingsNextTab   Action = "settings.next-tab"
	actionSettingsNextField Action = "settings.next-field"
	actionSettingsPrevField Action = "settings.prev-field"
	actionSettingsEdit      Action = "settings.edit"
	actionSettingsFieldUp   Action = "settings.field-up"
	actionSettingsFieldDown Action = "settings.field-down"

	actionInputBlur   Action = "input.blur"
	actionInputSubmit Action = "input.submit"

	actionConfirmYes Action = "confirm.yes"
	actionConfirmNo  Action = "confirm.no"

	actionHelpScrollUp   Action = "help.scroll-up"
	actionHelpScrollDown Action = "help.scroll-down"
	actionHelpPageUp     Action = "help.page-up"
	actionHelpPageDown   Action = "help.page-down"

	actionYankHash   Action = "yank.hash"
	actionYankName   Action = "yank.name"
	actionYankPath   Action = "yank.path"
	actionYankMagnet Action = "yank.magnet"

	actionFocusLeft  Action = "focus.left"
	actionFocusDown  Action = "focus.down"
	actionFocusUp    Action = "focus.up"
	actionFocusRight Action = "focus.right"
)

// Binding is one row of the registry: a key, the context it is live in, and the
// action it runs. Nothing else in the TUI declares a keybinding — components are
// constructed from these rows (see bindingFor), the help view reads them, and
// the collision test iterates them.
type Binding struct {
	// Context is the focus context this binding is live in.
	Context Context

	// Key carries the key strings and the help text.
	Key key.Binding

	// Action is what the key runs.
	Action Action
}

// bind is registry shorthand. keys are tea.KeyPressMsg.String() spellings;
// display is how the key is shown in help, which is not always the same string
// (help shows "↑/k" for the pair "up", "k").
func bind(ctx Context, action Action, display, desc string, keys ...string) Binding {
	return Binding{
		Context: ctx,
		Key:     key.NewBinding(key.WithKeys(keys...), key.WithHelp(display, desc)),
		Action:  action,
	}
}

// registry is every keybinding in the TUI, in help display order within each
// context. It is the TUI's equivalent of .surface: a change here is a
// user-visible change to the keyboard, visible as a diff in review, and pinned
// by the collision test in keymap_test.go.
//
// Bindings for actions that are not implemented yet are registered anyway, with
// a handler that reports "not implemented yet". A later milestone replaces the
// handler; it must not have to invent the binding, and the collision test sees
// the full keyboard from the start rather than discovering a clash in T5.
var registry = []Binding{
	// Global — the same in every window.
	bind(ctxGlobal, actionQuit, "q", "quit", "q", "ctrl+c"),
	bind(ctxGlobal, actionHelp, "?", "help", "?"),
	bind(ctxGlobal, actionEscape, "esc", "back", "esc"),
	bind(ctxGlobal, actionPalette, ":", "command palette", ":"),
	bind(ctxGlobal, actionWindowTorrents, "ctrl+t", "torrents window", "ctrl+t"),
	bind(ctxGlobal, actionWindowProwlarr, "ctrl+p", "prowlarr window", "ctrl+p"),
	bind(ctxGlobal, actionWindowSettings, ",", "settings window", ","),
	bind(ctxGlobal, actionServerPicker, "S", "switch server", "S"),
	bind(ctxGlobal, actionServerAltSpeed, "t", "toggle alt-speed", "t"),
	bind(ctxGlobal, actionRefresh, "R", "refresh", "R"),

	// Navigation — forwarded to whichever pane has focus.
	bind(ctxNav, actionNavUp, "↑/k", "up", "up", "k"),
	bind(ctxNav, actionNavDown, "↓/j", "down", "down", "j"),
	bind(ctxNav, actionNavTop, "g/home", "top", "g", "home"),
	bind(ctxNav, actionNavBottom, "G/end", "bottom", "G", "end"),
	bind(ctxNav, actionNavHalfPageUp, "ctrl+u", "½ page up", "ctrl+u"),
	bind(ctxNav, actionNavHalfPageDown, "ctrl+d", "½ page down", "ctrl+d"),
	bind(ctxNav, actionNavPageUp, "pgup", "page up", "pgup"),
	bind(ctxNav, actionNavPageDown, "pgdn", "page down", "pgdown"),
	bind(ctxNav, actionFocusNext, "tab", "next pane", "tab"),
	bind(ctxNav, actionFocusPrev, "shift+tab", "previous pane", "shift+tab"),
	bind(ctxNav, actionFocusChord, "ctrl+w", "focus pane…", "ctrl+w"),
	bind(ctxNav, actionToggleDetail, "}", "toggle detail pane", "}"),

	// Torrents window — shared by its three panes.
	bind(ctxTorrents, actionToggleFilter, "{", "toggle filter pane", "{"),
	bind(ctxTorrents, actionTabOverview, "1", "detail: overview", "1"),
	bind(ctxTorrents, actionTabFiles, "2", "detail: files", "2"),
	bind(ctxTorrents, actionTabTrackers, "3", "detail: trackers", "3"),
	bind(ctxTorrents, actionTabPeers, "4", "detail: peers", "4"),
	bind(ctxTorrents, actionTabPieces, "5", "detail: pieces", "5"),
	bind(ctxTorrents, actionTorrentFilter, "f", "filter", "f"),
	bind(ctxTorrents, actionTorrentSearch, "/", "search", "/"),
	bind(ctxTorrents, actionSearchNext, "n", "next match", "n"),
	bind(ctxTorrents, actionSearchPrev, "N", "previous match", "N"),
	bind(ctxTorrents, actionTorrentSort, "o", "sort", "o"),
	bind(ctxTorrents, actionTorrentColumns, "c", "columns", "c"),
	bind(ctxTorrents, actionTorrentAdd, "a", "add torrent", "a"),
	bind(ctxTorrents, actionTorrentAddOpts, "A", "add with options", "A"),

	// Torrent list pane — the torrent verbs, and nowhere else in the window.
	bind(ctxTorrentList, actionToggleMark, "space", "mark", "space"),
	bind(ctxTorrentList, actionDetailOpen, "enter", "open detail", "enter"),
	bind(ctxTorrentList, actionYankChord, "y", "yank…", "y"),
	bind(ctxTorrentList, actionTorrentStart, "s", "start", "s"),
	bind(ctxTorrentList, actionTorrentStop, "p", "pause", "p"),
	bind(ctxTorrentList, actionTorrentVerify, "v", "verify", "v"),
	bind(ctxTorrentList, actionTorrentReann, "r", "reannounce", "r"),
	bind(ctxTorrentList, actionTorrentRemove, "d", "delete", "d"),
	bind(ctxTorrentList, actionQueueDown, "J", "queue down", "J"),
	bind(ctxTorrentList, actionQueueUp, "K", "queue up", "K"),
	bind(ctxTorrentList, actionTorrentLabels, "L", "labels", "L"),
	bind(ctxTorrentList, actionTorrentMove, "M", "move", "M"),
	bind(ctxTorrentList, actionMenuOpen, "m", "action menu", "m"),

	// Filter sidebar — apply, plus the group actions.
	bind(ctxTorrentFilter, actionFilterApply, "enter", "apply filter", "enter"),
	bind(ctxTorrentFilter, actionGroupStart, "s", "start group", "s"),
	bind(ctxTorrentFilter, actionGroupStop, "p", "pause group", "p"),
	bind(ctxTorrentFilter, actionGroupVerify, "v", "verify group", "v"),
	bind(ctxTorrentFilter, actionGroupRemove, "d", "delete group", "d"),

	// Detail pane — tabs only.
	bind(ctxTorrentDetail, actionDetailPrevTab, "[/h", "previous tab", "[", "h", "left"),
	bind(ctxTorrentDetail, actionDetailNextTab, "]/l", "next tab", "]", "l", "right"),

	// Settings window — owns tab and up/down itself, per vision.md.
	bind(ctxSettings, actionSettingsPrevTab, "[", "previous tab", "["),
	bind(ctxSettings, actionSettingsNextTab, "]", "next tab", "]"),
	bind(ctxSettings, actionSettingsNextField, "tab", "next field", "tab"),
	bind(ctxSettings, actionSettingsPrevField, "shift+tab", "previous field", "shift+tab"),
	bind(ctxSettings, actionSettingsEdit, "enter", "edit field", "enter"),
	bind(ctxSettings, actionSettingsFieldUp, "↑", "field up", "up"),
	bind(ctxSettings, actionSettingsFieldDown, "↓", "field down", "down"),

	// Text input — the only two keys that escape a focused input.
	bind(ctxTextInput, actionInputBlur, "esc", "blur", "esc"),
	bind(ctxTextInput, actionInputSubmit, "enter", "submit", "enter"),

	// Confirm overlay — modal: nothing global resolves under it. esc is listed
	// so help shows it, but the router sends it to the ladder, whose "close
	// overlay" rung is what cancels.
	bind(ctxConfirm, actionConfirmYes, "y/enter", "yes", "y", "enter"),
	bind(ctxConfirm, actionConfirmNo, "n", "no", "n"),
	bind(ctxConfirm, actionEscape, "esc", "cancel", "esc"),

	// Help overlay.
	bind(ctxHelp, actionHelpScrollUp, "↑/k", "scroll up", "up", "k"),
	bind(ctxHelp, actionHelpScrollDown, "↓/j", "scroll down", "down", "j"),
	bind(ctxHelp, actionHelpPageUp, "pgup", "page up", "pgup"),
	bind(ctxHelp, actionHelpPageDown, "pgdn", "page down", "pgdown"),

	// The y chord's second key.
	bind(ctxChordYank, actionYankMagnet, "m", "magnet link", "m"),
	bind(ctxChordYank, actionYankHash, "h", "info hash", "h"),
	bind(ctxChordYank, actionYankName, "n", "name", "n"),
	bind(ctxChordYank, actionYankPath, "p", "download path", "p"),

	// The ctrl+w chord's second key.
	bind(ctxChordWindow, actionFocusLeft, "h", "focus left", "h"),
	bind(ctxChordWindow, actionFocusDown, "j", "focus down", "j"),
	bind(ctxChordWindow, actionFocusUp, "k", "focus up", "k"),
	bind(ctxChordWindow, actionFocusRight, "l", "focus right", "l"),
}

// bannedKeys are the key spellings no binding may use, whatever the context.
// These are terminal constraints, not preferences (vision.md, "Terminal
// constraints"): legacy terminals send ctrl+j/i/m/h as Enter/Tab/Enter/
// Backspace, ctrl+s and ctrl+q are XOFF/XON flow control, ctrl+l is the
// universal redraw, and alt chords need "use Option as Meta" on macOS and
// misbehave even then.
var bannedKeys = []string{
	"ctrl+j", "ctrl+i", "ctrl+m", "ctrl+h",
	"ctrl+s", "ctrl+q",
	"ctrl+l",
}

// bannedPrefix is the modifier no binding may use at all. tea spells modifiers
// in a fixed ctrl+alt+shift order, so a prefix scan of the whole string catches
// "alt+x" and "ctrl+alt+x" alike.
const bannedPrefix = "alt+"

// isBannedKey reports whether k is a key no binding may use.
func isBannedKey(k string) bool {
	if strings.Contains(k, bannedPrefix) {
		return true
	}

	for _, banned := range bannedKeys {
		if k == banned {
			return true
		}
	}

	return false
}

// bindingFor returns the binding registered for action in ctx. It is how a
// bubbles component's KeyMap is built: the component is handed the registry's
// own key.Binding, so a component's live keymap cannot drift from the registry
// the help view and the collision test read.
//
// A missing row is a programming error — the action constants and the registry
// are both in this file — so the zero Binding is returned rather than panicking,
// and keymap_test.go asserts every wired field is non-empty.
func bindingFor(ctx Context, action Action) key.Binding {
	for _, b := range registry {
		if b.Context == ctx && b.Action == action {
			return b.Key
		}
	}

	return key.Binding{}
}

// lookup resolves a key press against an ordered stack of contexts and returns
// the action it runs. The first context that binds the key wins, which is what
// makes a stack "most specific first" meaningful: the filter pane's enter
// shadows nothing, but the list pane's d would shadow a global d if one existed.
func lookup(stack []Context, msg interface{ String() string }) (Action, bool) {
	for _, ctx := range stack {
		for _, b := range registry {
			if b.Context == ctx && key.Matches(msg, b.Key) {
				return b.Action, true
			}
		}
	}

	return "", false
}

// Pane names one focusable region of the UI. A window decides which panes are
// open (see openPanes); this enum is the whole set across all windows.
type Pane int

// The panes, in the order they are cycled within their window.
const (
	// paneFilter is the torrents window's filter sidebar (left).
	paneFilter Pane = iota

	// paneList is the torrents window's torrent table (main).
	paneList

	// paneDetail is the torrents window's detail pane (bottom).
	paneDetail

	// paneSettings is the settings window's single pane.
	paneSettings
)

// String names a Pane for test failures and the ctrl+w chord's status line.
func (p Pane) String() string {
	switch p {
	case paneFilter:
		return "filter"
	case paneList:
		return "list"
	case paneDetail:
		return "detail"
	case paneSettings:
		return "settings"
	}

	return "unknown"
}

// allPanes is every Pane, for tests that must iterate the whole set rather than
// a list that can silently fall behind the enum.
var allPanes = []Pane{paneFilter, paneList, paneDetail, paneSettings}

// paneContexts is the static context stack for each pane, most specific first.
// It is the table the collision test iterates: every stack a key press can be
// resolved against is either one of these or one of the overlay and chord
// stacks below, and no key may resolve twice within any of them.
func paneContexts(p Pane) []Context {
	switch p {
	case paneFilter:
		return []Context{ctxTorrentFilter, ctxTorrents, ctxNav, ctxGlobal}
	case paneList:
		return []Context{ctxTorrentList, ctxTorrents, ctxNav, ctxGlobal}
	case paneDetail:
		return []Context{ctxTorrentDetail, ctxTorrents, ctxNav, ctxGlobal}
	case paneSettings:
		// Settings owns tab, enter, and up/down itself (vision.md: "up/down
		// belong to the focused field"), so it does not sit on top of ctxNav.
		return []Context{ctxSettings, ctxGlobal}
	}

	return []Context{ctxGlobal}
}

// modalStacks are the context stacks that replace a pane's stack rather than
// extending it: an open overlay, a pending chord, and a focused text input each
// swallow everything they do not bind. They are listed here so the collision
// test iterates every reachable stack, not only the per-pane ones.
var modalStacks = [][]Context{
	{ctxHelp, ctxGlobal},
	{ctxConfirm},
	{ctxChordYank},
	{ctxChordWindow},
	{ctxTextInput},
}
