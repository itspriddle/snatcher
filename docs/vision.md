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
- [ ] `snatcher server free-space ...` - show free space on disk (merge in with stats?)
- [x] `snatcher torrent info <hash|id> ...` - show torrent info
- [ ] `snatcher search ["query"] ...` - search for torrents with Prowlarr
    - [ ] `snatcher server add --type=search|prowlarr ...`  /  - set up prowlarr connection
    - [ ] `snatcher api --search|--prowlarr ...` - raw API access to prowlarr search
    - [ ] `--pages N` see if we can work needing to 'grab more' from prowlarr

### Aliases

- [x] `snatcher torrent mv`
- [x] `snatcher torrent rm`
- [x] `snatcher torrent ls`
- [x] `snatcher torrent pause` => `snatcher torrent stop`

### Renames

- [x] `snatcher torrent set` -> `snatcher torrent update` 
- [x] `snatcher torrent upload` -> `snatcher torrent add`
- [ ] `snatcher torrent info` -> `snatcher torrent show` 
- [ ] `snatcher server settings set` -> `snatcher server settings update` 

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
- Global download
- Global download limit
- Global upload
- Global upload limit
- Free space on disk
- Total space used by torrents


### Keybindings

#### Global

- `q` / `Ctrl-c` - quit
- `?` - show help (hide if help open)
- `ctrl-t` - open torrents window
- `ctrl-p` - open prowlarr window
- `,` - open settings window
- Note: spamming esc should get us back to main torrent page I think, but
  confirm


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

- `{` - show left pane (filters) (?)
- `}` - show right (bottom) pane (details) (?)
- `o` - sort using torrent fields/manage col sort order and display
- `left` / `right` / `h` / `l` - scroll focused pane/table visibility
- `up` / `down` / `j` / `k` - scroll focused pane, select item in table row (i.e. live update of details pane if open)
- `tab` / `shift-tab` - move between open panes
- `f` - filter torrents shown (modal, save state), show in UI somehow
- `/` - open search window
- `u` - upload torrent (file, URL, magnet link)
- `a` - toggle alt-speed
- `R` - force state refresh from server
- `c` - configure visible columns in list pane

#### Torrents Window > Torrent List Pane (Main)

Always visible.

- `s` - start selected torrent
- `p` - pause selected torrent
- `v` - verify selected torrent
- `r` - reannounce selected torrent to trackers
- `q` - move in queue
- `d` - delete selected torrent (with confirmation, ask for data too)
- `y` - copy magnet link for selected torrent to clipboard
- `l` - set labels for selected torrent
- `space` - open action menu
    - force start
    - start
    - pause
    - verify
    - reannounce
    - copy magnet link
    - queue
        - up
        - down
        - top
        - bottom
    - set labels
    - delete (with confirmation, ask for data too)
    - trackers
    - other
        - honor session upload limit
        - max download speed (checkbox and input for speed)
        - max upload speed (checkbox and input for speed)
        - peer limit (number input)
        - seed ratio limit (checkbox? and number input)
        - stop seeding when inactive for (checkbox? and number input for minutes)

- `enter` / `O` - show details > overview tab for selected torrent
- `F` - show details > files tab
- `C` - show details > pieces tab ??
- `P` - show details > peers tab ??
- `T` - show details > trackers tab
- `esc` - select no torrent (details stays if visible, but empty)

Todo: need something to search in the table, like / in vim, but taken above...

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

- `s` - start all (with confirmation)
- `p` - pause all (with confirmation)
- `v` - verify all (with confirmation)
- `d` - delete all (with confirmation, double confirm, ask for data too)

#### Torrents Window > Torrent Detail Pane (Bottom)

Tabs: overview, files, trackers, peers, labels

- `tab` / `shift-tab` - move between tabs, if last tab move to the next pane
  (i.e. spamming tab cycles through filter -> torrents -> overview -> files ->
  trackers -> peers -> labels -> filter...)
- `[` / `]` - move between tabs, cycle, keep focus in this pane
- `esc` - focus torrent list pane

Overview:

- transfer info
- torrent info

Files:

- table/list: name, size, done size, percent, priority
- `space` - action menu for file
    - high prio
    - normal prio
    - low prio
    - set wanted
    - set unwanted

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
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
+------------------------------------------------------------------------------+
|                                                                              |
|                                                                              |
|                                                                              |
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

- `/` / `Ctrl-l` - focus search field
    - include a few readline helpers here too? `ctrl-w`, etc?
- `f` - set categories/indexers for search query (ask to resubmit if live results already)
- `enter` - in search, submit and show results. on selection, open in tracker
- `O` - open in tracker
- `o` - sort using torznab fields/manage col sort order and display
- `y` - copy tracker URL
- `Y` - copy magnet URL
- `m` - fetch more (append to table? resort could get tricky)
- `d` - download to Transmission
- `D` - download torrent file locally
- `P` - open Prowlarr
- `esc` - back to torrent window

#### Prowlarr Window > Detail Pane

Similar to torrent details, but with fields relevant to prowlarr results.

fields: age, title, indexer, size, grabs, peers, seeders

- `enter` - open in tracker
- `O` - open in tracker
- `y` - copy tracker URL
- `Y` - copy magnet URL
- `d` - download to Transmission
- `D` - download torrent file locally
- copy others from search?

#### Settings Window

Tabs: General (Snatcher), Polling, Download, Network, Bandwidth, Queue

- `esc`
    - no field focused, close, ask to save pending changes if any, back to previous window
    - field focused, unfocus, stay in settings window
- `[` / `]` - move between tabs, cycle
- `tab` / `shift-tab` / `up` / `down` - move between fields, cycle on current tab only
- `enter` - edit field, if select dropdown open it, if toggle toggle it, etc

---

## Bugs

Auth issue:

```
scarif:~/snatcher $ ./snatcher server add --name default --url 'https://torrents.priddle.network/transmission/rpc'
Password:
Error: secrets: deleting password for "https://torrents.priddle.network/transmission/rpc": exec: "dbus-launch": executable file not found in $PATH
```
