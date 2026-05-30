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
  info        Show torrent details
  list        List torrents
  remove      Remove torrents
  settings    Manage server settings
  speed       View or toggle alt-speed limits
  start       Start or resume torrents
  stats       Show session statistics
  stop        Stop (pause) torrents
  upload      Upload torrents from files, URLs, or magnet links
  verify      Verify local torrent data
  version     Print version information

Flags:
      --config string   config file path
  -h, --help            help for snatcher
      --no-nerd-font    use ASCII icons instead of Nerd Font glyphs
      --theme string    color theme (default, dracula, nord)

Use "snatcher [command] --help" for more information about a command.
```

---

## TUI

- Window based layout (like a vim window)
    - Torrents (Default)
    - Tracker Search (Prowlarr)
    - Settings

### Keybindings

#### Global

- `q` / `Ctrl-c` - quit
- `?` - show help (hide if help open)
- Note: spamming esc should get us back to main torrent page I think, but
  confirm

#### Torrents Window Globals

- `{` - show left pane (filters) (?)
- `}` - show right (bottom) pane (details) (?)
- `o` - sort using filter fields
- `left` / `right` / `h` / `l` - scroll pane visibility
- `up` / `down` / `j` / `k` - scroll pane, select item in table row (i.e. live update of details pane if open)
- `tab` / `shift-tab` - move between open panes
- `f` - filter torrents shown (save state), show in UI somehow
- `/` - open search window
- `u` - upload torrent (file, URL, magnet link)
- `a` - toggle alt-speed
- `r` - force state refresh from server
- `c` - configure visible columns in list pane
- `,` - show settings window

#### Torrents Window > Torrent List Pane (Main)

- `s` - start selected torrent
- `p` - pause selected torrent
- `v` - verify selected torrent
- `d` - delete selected torrent (with confirmation, ask for data too)
    - go with `r` for remove?
- `m` - copy magnet link for selected torrent to clipboard
- `l` - create label for selected torrent
- `enter` - show torrent details pane, focus
- `O` - show details > overview tab for selected torrent
- `F` - show details > files tab
- `T` - show details > trackers tab
- `P` - show details > peers tab
- `L` - show details > labels tab
- `esc` - select no torrent (details stays if visible, but empty)

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

#### Prowlarr Window

#### Settings Window

Tabs: General (Snatcher), Polling, Download, Network, Bandwidth, Queue

- `esc`
    - no field focused, close, ask to save pending changes if any, back to previous window
    - field focused, unfocus, stay in settings window
- `[` / `]` - move between tabs, cycle
- `tab` / `shift-tab` / `up` / `down` - move between fields, cycle on current tab only
- `enter` - edit field, if select dropdown open it, if toggle toggle it, etc
