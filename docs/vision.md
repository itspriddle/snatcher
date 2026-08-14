# snatcher - vision

Modern golang based CLI/TUI for Transmission. Built on Charm Land v2
libs.

Transmission only. Support different Transmission RPC API versions.

MacOS/Linux builds only.

Modern go toolchain and test suite.

AI first development flow with good guardrails and careful planning.

Anything TUI can do can be done with the CLI. Smart human outputs, ability to
script easily (i.e. `--jq ...` filters like gh CLI).

Prowlarr integration (torznab spec).

---

CLI from alpha:

```
$ snatcher --help
Transmission BitTorrent CLI client

Usage:
  snatcher [flags]
  snatcher [command]

Available Commands:
  auth        Manage server authentication
  completion  Generate the autocompletion script for the specified shell
  help        Help about any command
  server      Manage server settings, stats, and port
  speed       View or toggle alt-speed limits
  torrent     Manage torrents
  version     Print version information

Flags:
      --config string   config file path
  -h, --help            help for snatcher
      --no-nerd-font    use ASCII icons instead of Nerd Font glyphs
      --theme string    color theme (default, dracula, nord)

Use "snatcher [command] --help" for more information about a command.
```

---

## Unscoped Commands

- [x] `snatcher torrent update ...` - set torrent fields (e.g. label, priority, etc)
- [x] `snatcher torrent reannounce ...` - reannounce torrent to trackers
- [x] `snatcher torrent move ...` - `torrent_set_location`
- [x] `snatcher torrent queue top|up|down|bottom ...` - change queue position
- [x] `snatcher server port get|set|test` - show or change listening port
- [x] `snatcher server free-space ...` - show free space on disk (merge in with stats?)
- [x] `snatcher torrent show <hash|id> ...` - show torrent info
- [x] `snatcher torrent files|peers|trackers <hash|id>` - the detail-pane data
- [x] `snatcher torrent files update ...` - file priority / wanted
- [x] `snatcher torrent rename ...` - `torrent-rename-path`
- [x] `snatcher config get|set|list|unset` - snatcher's own settings
- [ ] `snatcher search ["query"] ...` - search for torrents with Prowlarr
    - [ ] `snatcher server add --type=search|prowlarr ...`  /  - set up prowlarr connection
    - [ ] `snatcher api --search|--prowlarr ...` - raw API access to prowlarr search
    - [ ] `--pages N` see if we can work needing to 'grab more' from prowlarr

Sequenced in `docs/todo.md` (CLI) and `docs/tui-todo.md` (TUI).

### Aliases

- [x] `snatcher torrent mv`
- [x] `snatcher torrent rm`
- [x] `snatcher torrent ls`
- [x] `snatcher torrent pause` => `snatcher torrent stop`
- [x] `snatcher torrent info` => `snatcher torrent show`

### Renames

- [x] `snatcher torrent set` -> `snatcher torrent update` 
- [x] `snatcher torrent upload` -> `snatcher torrent add`
- [x] `snatcher torrent info` -> `snatcher torrent show` 
- [x] `snatcher server settings set` -> `snatcher server update`
- [x] `snatcher server settings reset <key>` -> `snatcher server reset <key>...`
- [x] `snatcher server settings` + `settings get` -> merged into `snatcher server show`

### Helpers?

- [ ] `snatcher server bandwidth show [download|upload] [--alt]` - show speeds (current and limits)
- [ ] `snatcher server bandwidth update <download|upload> [--alt]...` - set up/down speed limits
- [ ] `snatcher server bandwidth alt on|off|toggle` - toggle/set alt-speed
- [ ] `snatcher server doctor` - check for common issues with server connection, auth, config, network (server side, port) etc
    - show torrents with errors?
    - show old torrents with low seeding?
    - top X largest torrents?
- [ ] `snatcher torrent verify --all-with-errors` - verify all torrents with error status
- [ ] `snatcher torrent label add|rm|set|ls ...` - manage labels for torrent(s)
- [ ] `snatcher torrent ls --top=N` - list top N torrents by seeding size/etc

## TUI

- Window/pane based layout (like a vim window)
    - Torrents (Default)
    - Tracker Search (Prowlarr)
    - Settings
    - (V2) File manager, view by size and seeding info to determine whether to delete

- https://github.com/daltonsw/bubbleup - popups
- https://github.com/lrstanley/bubblezone - mouse?
- https://github.com/charmbracelet/huh - form fields
- https://github.com/charmbracelet/gum - possible references, but not direct use

## UI

### Status Bar

- Transmission version
- Transmission hostname
- Current server (alias from config)
- Global download
- Global download limit
- Global upload
- Global upload limit
- Free space on disk
- Total space used by torrents
- Marked count (only when marks exist)


### Keybindings

#### Principles

These hold in every window. The per-window lists below are the additions and
exceptions, not a restatement.

**Selection.** Every action key operates on the **marked set** if any marks
exist, otherwise on the **cursor row**. `space` toggles a mark, `esc` clears
them (see the ladder). This is what makes bulk actions explicit instead of
focus-dependent, and it maps 1:1 onto `transmission.Selector`
(ids/hashes/`--all`), so every TUI action has an exact CLI equivalent.

**Verbs mean the same thing everywhere.** `a` adds, `d` destroys, `enter` opens
details, `m` opens the action menu, `y` yanks, `o` sorts, `c` configures
columns, `f` filters, `/` searches, `R` refreshes.

**Confirmation scales with blast radius.** Anything touching more than one
torrent confirms and states the count ("Delete 12 torrents?"). Deletes always
confirm, and always ask about deleting data as a separate question. Filter-pane
group actions confirm even when the group has one member.

**esc ladder.** One ordered stack, evaluated top to bottom. The first
applicable step runs, and only that step:

1. close the topmost overlay (modal, action menu, palette, help)
2. blur the focused text input (keeping its contents)
3. clear the active search
4. clear marks
5. deselect the cursor row (detail pane stays, goes empty)
6. return to the torrents window
7. at rest in the torrents window: do nothing - **esc never quits**

**Terminal constraints.** These are hard limits, not preferences:

- No `alt`/`option` chords anywhere. They need "use Option as Meta" on macOS
  Terminal.app and still misbehave.
- Never bind `ctrl-j`, `ctrl-i`, `ctrl-m`, `ctrl-h` - legacy terminals send
  them as Enter/Tab/Enter/Backspace.
- Never bind `ctrl-s` / `ctrl-q` - XOFF/XON flow control.
- Never bind `ctrl-l` - universal redraw/clear.
- Clipboard is **write-only** (OSC 52). Yanking works; reading the clipboard
  does not. Pasting is the terminal's own paste (bracketed paste enabled).

#### Global

- `q` / `ctrl-c` - quit
- `?` - show help (hide if help open)
- `:` - command palette: fuzzy list of every command, including the long tail
  that gets no binding (switch server, test port, reset a setting, obscure
  `torrent update` flags). Runs against the current selection. This is what
  keeps "anything the TUI can do, the CLI can do" honest without inventing
  thirty more chords.
- `ctrl-t` - open torrents window
- `ctrl-p` - open prowlarr window
- `,` - open settings window
- `S` - server picker (named servers from config)
- `esc` - the ladder above


#### Torrents Window Globals

```
+------------------------------------------------------------------------------+
| All            | Ubuntu 1 ....                                               |
| Active         | Ubuntu 2 ....                                               |
| Inactive       |                                                             |
|                |                                                             |
|                |                                                             |
|                |                                                             |
|                |                                                             |
|                |                                                             |
+------------------------------------------------------------------------------+
| Tab 1 | Tab 2 | Tab 3 |                                                      |
| ____________________________________________________________________________ |
| Info                                                                         |
| Info                                                                         |
|                                                                              |
+------------------------------------------------------------------------------+
```

- `{` - toggle filter pane (left)
- `}` - toggle detail pane (bottom)
- `tab` / `shift-tab` - move focus between open panes (panes only, never tabs)
- `ctrl-w` + `h` / `j` / `k` / `l` - focus a pane directly, vim-style. While a
  text input is focused `ctrl-w` stays readline delete-word; the pane prefix
  only applies when no input has focus.
- `up` / `down` / `j` / `k` - move the cursor in the focused pane (live update
  of the detail pane if open)
- `g` / `G` - jump to top / bottom
- `ctrl-d` / `ctrl-u` - half page down / up
- `pgdn` / `pgup`, `home` / `end` - as expected
- `left` / `right` / `h` / `l` - horizontal scroll in the list pane; previous /
  next tab when the detail pane is focused
- `space` - toggle mark on the cursor row (the cursor does not move)
- `f` - filter torrents shown (modal, saves state), shown in the UI
- `/` - search/filter torrents by name; `n` / `N` - next / previous match
- `o` - sort: fields and direction, nothing else
- `c` - configure visible columns in list pane, nothing else
- `a` - add torrent (file, URL, magnet link); `A` - add with options (download
  dir, labels, start paused)
- `t` - toggle alt-speed (Transmission calls it turtle mode)
- `R` - force state refresh from server

#### Torrents Window > Torrent List Pane (Main)

Always visible. Every key here follows the selection rule: the marked set if
any marks exist, otherwise the cursor row.

- `s` - start
- `p` - pause
- `v` - verify
- `r` - reannounce to trackers
- `d` - delete (always confirms, asks about deleting data separately)
- `J` / `K` - move down / up in the queue (top and bottom live in the action
  menu; `q` stays quit, and `ctrl-j`/`ctrl-k` are unusable in legacy terminals)
- `L` - labels editor
- `M` - move / set location
- `y` + a second key - yank to clipboard:
    - `y m` - magnet link
    - `y h` - info hash
    - `y n` - name
    - `y p` - download path
- `enter` - open the detail pane and focus it (overview tab)
- `1`-`5` - open the detail pane on a specific tab (overview, files, trackers,
  peers, pieces)
- `m` - open action menu
    - force start
    - start
    - pause
    - verify
    - reannounce
    - rename torrent
    - queue
        - up
        - down
        - top
        - bottom
    - move / set location
    - set labels
    - yank (magnet / hash / name / path)
    - delete (with confirmation, ask for data too)
    - trackers (add / replace / clear)
    - other
        - honor session limits
        - max download speed (checkbox and input for speed)
        - max upload speed (checkbox and input for speed)
        - peer limit (number input)
        - seed ratio limit (checkbox and number input)
        - stop seeding when inactive for (checkbox and number input for minutes)
        - sequential download
        - bandwidth group
- `esc` - per the ladder: clears marks first, then deselects the row (detail
  pane stays if visible, but empty)

#### Torrents Window > Torrent Filter Pane (Left)

- Status
    - All (#)
    - Downloading (#)
    - Completed (#)
    - Active (#)
    - Inactive (#)
    - Stopped (#)
    - Error (#)
    - Waiting (#)
- Labels (dynamic)
    - Label 1 (#)
    - No Label (#)
- Trackers
    - Tracker 1 (#)
    - Tracker 2 (#)

With an item selected:

- `enter` - apply the filter to the list pane
- `s` / `p` / `v` / `d` - start / pause / verify / delete **every torrent in
  the group**. Always confirms and names the count ("Pause 412 torrents?"); `d`
  double-confirms and asks about data. The focused pane is styled loudly enough
  that the blast radius is never a surprise - focus alone must not be the only
  thing standing between one torrent and four hundred.

#### Torrents Window > Torrent Detail Pane (Bottom)

Tabs: overview, files, trackers, peers, pieces

Labels are not a tab - they show in overview and are edited with `L` or the
action menu.

- `[` / `]` - previous / next tab, cycles, keeps focus in this pane
- `h` / `l` - same as `[` / `]` while this pane is focused
- `1`-`5` - jump straight to a tab
- `tab` / `shift-tab` - move to the next / previous **pane**. Tabs are not in
  the pane cycle; `[` / `]` already own them, and seven presses to leave the
  detail pane is not navigation.
- `esc` - focus torrent list pane

Overview:

- transfer info
- torrent info

Files:

- table/list: name, size, done size, percent, priority, wanted
- `space` - toggle mark on the cursor file (same selection rule as torrents)
- `m` - action menu, acting on the marked files or the cursor file
    - high prio
    - normal prio
    - low prio
    - set wanted
    - set unwanted
    - rename file

Pieces: 

- filled/unfilled squares?

Peers:

- table/list: ip, port, client, progress("have"?), down speed, up speed, encrypted, status
- no actions?

Trackers:

- table/list: announce url, status, next update, seeds, peers, downloads

#### Prowlarr Window Global 

```
+------------------------------------------------------------------------------+
|                                                                              |
| [search query]  [category dropdown]  [indexer dropdown]                      |
|                                                                              |
+------------------------------------------------------------------------------+
| Title    Size  ...                                                           |
| Foobar   1gb                                                                 |
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
+------------------------------------------------------------------------------+
| Name   Foobar                                                                |
| Size   1gb                                                                   |
| ...    ...                                                                   |
|                                                                              |
+------------------------------------------------------------------------------+
```

#### Prowlarr Window > Search Pane (Top)

Text area to enter a search, set categories/indexers

- `enter` - submit search, show results in main listing pane
- `esc` - if search field focused, unfocus, stay in prowlarr window/search
  pane. if not focused, back to torrent window

#### Prowlarr Window > Main Listing

Empty by default

cols: age, title, indexer, size, grabs, peers, seeders

- `/` - focus search field (`ctrl-l` is off limits - it is the universal
  redraw. Readline helpers like `ctrl-w` inside the field are fine.)
- `f` - set categories/indexers for search query (ask to resubmit if live results already)
- `enter` - in search, submit and show results. on a result, open the detail pane
- `O` - open in tracker (browser)
- `o` - sort using torznab fields
- `c` - configure visible columns
- `y` + a second key - yank to clipboard:
    - `y u` - tracker / info URL
    - `y m` - magnet URL
    - `y d` - download URL
- `+` - fetch more results (append to the table)
- `a` - grab (see the note below)
- `w` - write the .torrent file locally
- `m` - action menu (grab, write locally, open in tracker, yanks)
- `P` - open Prowlarr
- `esc` - back to torrent window

**Note on `a` (grab).** Prowlarr's grab endpoint hands the release to the
download client **Prowlarr** is configured with, which is not necessarily the
server snatcher is pointed at. So `a` here means "tell Prowlarr to grab it",
not "add it to this server". Adding directly from the release's download URL is
a possible follow-up, not v1. `enter` never launches a browser - `O` does.

#### Prowlarr Window > Detail Pane

Similar to torrent details, but with fields relevant to prowlarr results.
include URL to tracker displayed here (not exposed in list pane)

fields: age, title, indexer, size, grabs, peers, seeders

- `O` - open in tracker
- `y` + `u` / `m` / `d` - yank tracker URL / magnet URL / download URL
- `a` - grab (same semantics as the list pane)
- `w` - write the .torrent file locally
- `m` - action menu
- `esc` - focus the results list
- copy others from search?

#### Settings Window

Tabs: General (Snatcher), Polling, Download, Network, Bandwidth, Queue

General and Polling write snatcher's own config (`snatcher config`); the rest
write Transmission session settings and map onto the same category table that
powers `snatcher server show --category`.

- `esc`
    - no field focused, close, ask to save pending changes if any, back to previous window
    - field focused, unfocus, stay in settings window
- `[` / `]` - move between tabs, cycle
- `tab` / `shift-tab` - move between fields, cycle on current tab only
- `up` / `down` - belong to the **focused field** (moving through select
  options, lines in a multi-line input), never to field navigation. This is the
  rule that stops `huh` widget keymaps from fighting the form's own navigation;
  decide it here, not after the fields are built.
- `enter` - edit field, if select dropdown open it, if toggle toggle it, etc
- Saving is an explicit action in the form, plus the `esc` prompt above.
  `ctrl-s` is deliberately unbound - it is XOFF on many terminals.
