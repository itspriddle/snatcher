# snatcher - vision

Modern golang based CLI/TUI for BitTorrent clients. Built on Charm Land v2
libs.

Prioritize Transmission for v1. Build modularly to support other clients, such
as qbittorrent, in the future. Support different API versions.

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
  settings    Manage server settings
  speed       View or toggle alt-speed limits
  stats       Show session statistics
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

- [ ] `snatcher torrent set ...` - set torrent fields (e.g. label, priority, etc)
- [x] `snatcher torrent reannounce ...` - reannounce torrent to trackers
- [x] `snatcher torrent move ...` - `torrent_set_location`
- [x] `snatcher torrent queue top|up|down|bottom ...` - change queue position
- [ ] `snatcher server blocklist ...` - 
- [x] `snatcher port get|set|test` - show or change listening port
- [ ] `snatcher free-space ...` - show free space on disk (merge in with stats?)
- [x] `snatcher torrent info <hash|id> ...` - show torrent info
- [ ] `snatcher search ...` - search for torrents with Prowlarr

### Aliases

- [ ] `snatcher torrent up` / `snatcher upload`
- [x] `snatcher torrent mv`
- [x] `snatcher torrent rm`
- [x] `snatcher torrent ls`

### Helpers?

- `snatcher server toggle-alt-speed` - toggle alt-speed, show new state

## TUI

- Window based layout (like a vim window)
    - Torrents (Default)
    - Tracker Search (Prowlarr)
    - Settings

- https://github.com/daltonsw/bubbleup - popups
- https://github.com/lrstanley/bubblezone - mouse?
- https://github.com/charmbracelet/huh - form fields
- https://github.com/charmbracelet/gum - possible references, but not direct use

### Keybindings

#### Global

- `q` / `Ctrl-c` - quit
- `?` - show help (hide if help open)
- Note: spamming esc should get us back to main torrent page I think, but
  confirm

#### Torrents Window Globals

+------------------------------------------------------------------------------+
|                |                                                             |
|                |                                                             |
|                |                                                             |
|                |                                                             |
|                |                                                             |
|                |                                                             |
|                |                                                             |
|                |                                                             |
+------------------------------------------------------------------------------+
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
+------------------------------------------------------------------------------+

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
- `r` - force state refresh from server
- `c` - configure visible columns in list pane
- `,` - show settings window

#### Torrents Window > Torrent List Pane (Main)

Always visible.

- `s` - start selected torrent
- `p` - pause selected torrent
- `i` - set prio?
- `v` - verify selected torrent
- `d` - delete selected torrent (with confirmation, ask for data too)
    - go with `r` for remove?
- `y` - copy magnet link for selected torrent to clipboard
- `l` - set labels for selected torrent
- `enter` - show torrent details pane, focus

- `O` - show details > overview tab for selected torrent
- `F` - show details > files tab
- `T` - show details > trackers tab
- `P` - show details > peers tab
- `L` - show details > labels tab
- `esc` - select no torrent (details stays if visible, but empty)

Todo: need something to search in the table, like / in vim, but taken above...

#### Torrents Window > Torrent Filter Pane (Left)

Notes: all, downloading, completed, etc nav list

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

#### Prowlarr Window Global 

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
