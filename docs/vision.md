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

**Note on `speed`.** That listing is the original alpha sketch, and its
top-level `speed` command was never built and will not be. The capability
shipped as `snatcher server bandwidth show|update|alt` instead, next to the
other server-scoped verbs, since alt-speed and the global limits are server
session settings. **`server bandwidth` is the only surface** - there is no
top-level `speed` command and no alias for one.

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
- [x] `snatcher search ["query"] ...` - search for torrents with Prowlarr. Shipped as a group that both runs a search of its own and carries `grab`, `download`, `indexers`, and `categories`, so those four words are shadowed as bare positional queries - `--query` is the documented escape, called out in `search --help`. Results keep Prowlarr's own order (no client-side sort, filter, or de-duplication) and are numbered from 1 in a `#` column that is also the `index` field in `--json`, so a human reading the table and a script reading JSON name a release the same way.
    - [x] `snatcher server add --type=search|prowlarr ...`  /  - set up prowlarr connection. Shipped as `--type prowlarr` with no `search` synonym, plus `--api-key-stdin` beside `--password-stdin`; the API key goes to the same `keyring`/`file`/`none` store a password does, keyed by server URL. Each type gets its own default (`default_prowlarr_server`), so registering Prowlarr never shadows the Transmission server, and `SNATCHER_API_KEY` is scoped to Prowlarr aliases exactly as `SNATCHER_PASSWORD` is to Transmission ones.
    - [x] `snatcher api --search|--prowlarr ...` - raw API access to prowlarr search. Shipped as `--prowlarr` only, taking a REST *path* rather than an RPC method (a bare `indexer` expands to `/api/v1/indexer`), with `-X`/`--method`, `-f`/`-F`/`--input` as query parameters for GET/DELETE and a JSON body otherwise, and the response body emitted raw rather than wrapped in Transmission's `result`/`arguments`/`tag` envelope.
    - [x] `--pages N` see if we can work needing to 'grab more' from prowlarr. Shipped as N sequential requests stepping the offset by `--limit`, concatenated in order and stopping early on a short page. `--limit` is always sent so the offsets stay deterministic; an indexer that ignores paging is Prowlarr's problem to fix, not snatcher's to paper over.

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

- [x] `snatcher server bandwidth show [download|upload] [--alt]` - show speeds (current and limits)
- [x] `snatcher server bandwidth update <download|upload> <speed> [--alt]` - set up/down speed limits
- [x] `snatcher server bandwidth alt on|off|toggle` - toggle/set alt-speed
- [ ] `snatcher server doctor` - check for common issues with server connection, auth, config, network (server side, port) etc
    - show torrents with errors?
    - show old torrents with low seeding?
    - top X largest torrents?
- [x] `snatcher torrent verify --all-with-errors` - verify all torrents with error status. Shipped with a counted `[y/N]` gate on **both** bulk forms (`--all` too, skippable with `--yes`/`-y`) - a full-session re-hash earns a prompt for its blast radius, not its reversibility. An explicit id/hash selection is never prompted.
- [x] `snatcher torrent label list|add|remove|set [id|hash]... [--all] [--label name]...` - manage labels for torrent(s). Shipped with `ls`/`rm` as aliases of `list`/`remove` (matching every other pair in the CLI), and labels on a repeatable `--label` flag rather than positional - positional labels are ambiguous next to a multi-torrent selector.
- [x] `snatcher torrent ls --top=N` - list top N torrents by seeding size/etc. Shipped as a flag-only truncation of the *final rendered order* (applied after `--filter`/`--search`/`--sort`/`--reverse`), so "the biggest N" is `--sort size --reverse --top N` rather than a second ranking vocabulary. It truncates `--json`/`--jq` too, and has no `client.*` key - it is a per-question flag, not a stored preference.

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

Marks survive a sort change, and are **cleared whenever the visible set is
narrowed**: a filter change (`f`, or `enter` in the filter pane) and a `/`
search both do it. A mark hidden by either would otherwise be acted on
invisibly - and with exactly one hidden mark, an action would skip the cursor
row without any confirmation to say so. The status bar's mark count is the only
other tell, and it is not enough.

**A focused text input owns the keyboard.** While a text input has focus (the
search fields, a settings field, a form in a modal) every key goes to the
input: printable characters insert, and the readline chords (`ctrl-a`/`e`,
`ctrl-w`, `ctrl-u`, `ctrl-k`, `ctrl-d`, `ctrl-p`/`n`) keep their readline
meaning. Only two keys escape: `esc` blurs the input (ladder rung 2) and
`enter` submits it. So `q`, `?`, `:`, `,`, `S`, `ctrl-t`, and `ctrl-p` do
nothing special until the input is blurred - switching windows from a search
field is `esc` then the chord. This is the rule that makes typing a `?` into a
query possible; it is not a per-input exception.

**Navigation is the same in every window.** These keys hold wherever there is
a list, a table, or a scrollable pane, and are not restated per window:

- `up` / `down` / `j` / `k` - move the cursor in the focused pane (live update
  of the detail pane if open)
- `g` / `G` - jump to top / bottom
- `ctrl-d` / `ctrl-u` - half page down / up
- `pgdn` / `pgup`, `home` / `end` - as expected
- `tab` / `shift-tab` - move focus between open panes (panes only, never tabs)
- `ctrl-w` + `h` / `j` / `k` / `l` - focus a pane directly, vim-style (a
  focused text input keeps `ctrl-w` as delete-word, per the rule above)
- `}` - toggle the detail pane (bottom) in any window that has one
- `left` / `right` / `h` / `l` - horizontal scroll in a table pane; previous /
  next tab when a tabbed detail pane is focused

**Action verbs live in a window's main pane.** The torrent verbs (`s p v r d`,
`J`/`K`, `L`, `M`, `y`, `m`) are bound in the torrent list pane and nowhere
else in the torrents window. A focused detail pane owns navigation, its tab
keys, and the files tab's own `space`/`m`; it does not also carry torrent
verbs, because the files tab has marks of its own and a `d` that might mean
"delete the torrent" next to a file cursor is exactly the ambiguity marks were
introduced to remove. `esc` (or `shift-tab`) returns to the list pane, where
the verbs are.

**Verbs mean the same thing everywhere.** `a` adds, `d` destroys, `enter` opens
details, `m` opens the action menu, `y` yanks, `o` sorts, `c` configures
columns, `f` filters, `/` searches, `R` refreshes.

**Confirmation scales with blast radius.** Anything touching more than one
torrent confirms and states the count ("Delete 12 torrents?"). Deletes always
confirm, and always ask about deleting data as a separate question. Filter-pane
group actions confirm even when the group has one member.

**esc ladder.** One ordered stack, evaluated top to bottom. The first
applicable step runs, and only that step:

0. cancel a pending chord (`y`, `ctrl-w`) - the status bar shows the pending
   prefix while one is live
1. close the topmost overlay (modal, action menu, palette, help)
2. blur the focused text input (keeping its contents)
3. return focus to the window's main pane (torrent list, search results) from
   a detail or filter pane
4. clear the active search
5. clear marks
6. deselect the cursor row (detail pane stays, goes empty)
7. return to the torrents window. Leaving the settings window with unsaved
   changes asks to save first; that prompt is an overlay, so the next `esc`
   closes it (rung 1) and the window stays open
8. at rest in the torrents window: do nothing - **esc never quits**

`q` runs the same save prompt as rung 7 when the settings window is dirty,
then quits. It never discards edits silently.

**Terminal constraints.** These are hard limits, not preferences:

- No `alt`/`option` chords anywhere. They need "use Option as Meta" on macOS
  Terminal.app and still misbehave.
- Never bind `ctrl-j`, `ctrl-i`, `ctrl-m`, `ctrl-h` - legacy terminals send
  them as Enter/Tab/Enter/Backspace.
- Never bind `ctrl-s` / `ctrl-q` - XOFF/XON flow control.
- Never bind `ctrl-l` - universal redraw/clear.
- Clipboard is **write-only** (OSC 52, plus `pbcopy`/`wl-copy`/`xclip`/`xsel`
  when present and not over SSH). Yanking works; reading the clipboard does
  not. Pasting is the terminal's own paste (bracketed paste enabled).

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
- `S` - server picker (named servers from config). The picker lists servers of
  the **type the current window uses**: Transmission aliases from the torrents
  and settings windows, Prowlarr aliases from the prowlarr window. Switching
  one never changes the other, exactly as `default_server` and
  `default_prowlarr_server` are independent in the CLI.
- `t` - toggle alt-speed (Transmission calls it turtle mode). A server session
  setting, so it is not scoped to the torrents window
- `R` - force a refresh: re-poll the server in the torrents window, resubmit
  the current search in the prowlarr window, re-read settings in the settings
  window
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

Navigation (`j`/`k`, `g`/`G`, paging, `tab`/`shift-tab`, `ctrl-w`, `}`,
`h`/`l`) is per the Principles. Additions here:

- `{` - toggle filter pane (left). The filter pane is the torrents window's
  own; `}` (detail pane) is global
- `space` - toggle mark on the cursor row (the cursor does not move)
- `f` - filter torrents shown (modal, saves state), shown in the UI. Clears
  marks (Selection principle)
- `/` - filter the torrents shown to those whose name matches, live as you
  type. It narrows whatever the sidebar filter left, so the two compose; `enter`
  blurs the field keeping the filter in force, and `esc` clears it (ladder rung
  4). There is no `n`/`N`: with only matches on screen, "next match" is `j`
- `o` - sort: fields and direction, nothing else
- `c` - configure visible columns in list pane, nothing else
- `a` - add torrent (file, URL, magnet link); `A` - add with options (download
  dir, labels, start paused)

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

- `enter` - apply the filter to the list pane (clears marks, per the
  Selection principle)
- `esc` - focus torrent list pane (ladder rung 3)
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
- `esc` - focus torrent list pane (ladder rung 3)
- No torrent verbs. `s p v r d`, `J`/`K`, `L`, `M`, `y`, and the torrent `m`
  menu are list-pane keys (Principles, "Action verbs live in a window's main
  pane"). The files tab's `space` and `m` below are file-scoped and are the
  only action keys in this pane.

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

Panes: search (top), results (main), detail (bottom, toggled with `}`).
Navigation and pane focus are per the Principles; there are no marks in this
window in v1, so every action acts on the cursor row.

#### Prowlarr Window > Search Pane (Top)

Text area to enter a search, set categories/indexers

- `enter` - submit search, show results in main listing pane
- `esc` - if search field focused, unfocus, stay in prowlarr window/search
  pane (rung 2). if not focused, back to torrent window (rung 7)

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
- `esc` - back to torrent window (rung 7; a live search is not "an active
  search" in the ladder's sense - rung 4 is the torrents window's `/` filter)

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
- `esc` - focus the results list (rung 3)

#### Settings Window

Tabs: General (Snatcher), Polling, Download, Network, Bandwidth, Queue

General and Polling write snatcher's own config (`snatcher config`); the rest
write Transmission session settings and map onto the same category table that
powers `snatcher server show --category`.

- `esc`
    - no field focused, close, ask to save pending changes if any, back to
      previous window (rung 7)
    - field focused, unfocus, stay in settings window (rung 2). A settings
      field's text is a draft, so unfocusing discards it; only `enter` commits
- `q` - the same save prompt if there are pending changes, then quit
- `[` / `]` - move between tabs, cycle
- `tab` / `shift-tab` - move between fields, cycle on current tab only
- `up` / `down` - belong to the **focused field** (moving through select
  options, lines in a multi-line input), never to field navigation. This is the
  rule that stops a widget's keymap from fighting the form's own navigation;
  decide it here, not after the fields are built.
- `enter` - edit field, if select dropdown open it, if toggle toggle it, etc
- `R` - re-read the settings from the config file and the server, asking first
  if that would discard pending changes
- Saving is a `[ Save (N) ]  [ Revert ]` button row at the bottom of every tab,
  reached with `tab` and pressed with `enter`. Save writes every pending change
  on every tab; Revert discards them. The `esc` and `q` prompts above save the
  same way. `ctrl-s` is deliberately unbound - it is XOFF on many terminals.
