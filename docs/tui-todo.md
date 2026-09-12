# snatcher - TUI TODO

The outstanding **TUI** work, sequenced. Starts after `docs/todo.md` is done.

Three documents, three jobs:

- `docs/vision.md` - authoritative for the **UX surface**: windows, panes,
  keybindings, the selection rule, the esc ladder
- `docs/TUI-PLAN.md` - authoritative for the **library stack and
  architecture**: Charm v2 versions, component mapping, v2 gotchas verified
  against source
- this file - the **milestones and their order**

## Gate

Do not start T1 until `docs/todo.md` M2-M4 have landed. The detail pane, the
files-tab mutations, and the settings window all need CLI capabilities
underneath them; building the TUI first inverts the vision's parity rule and
puts RPC work on the critical path of UI work. M8 (Prowlarr) only gates T8.

## Cross-cutting rules

These are decided; they are not per-milestone choices.

**One selection model.** A single marked-set + cursor structure, owned by the
root model, that renders to a `transmission.Selector`. Every action - list
pane, filter pane, files tab, palette - consumes it. Nothing reaches for "the
current row" on its own.

**The keymap is data, not `switch` statements.** One registry of
(context, key, action, description). It feeds the `bubbles/help` view, the
command palette, and a **collision test**: no two bindings may share a key
within one focus context, and no binding may use `alt`, `ctrl-j/i/m/h`,
`ctrl-s/q`, or `ctrl-l`. That test is the TUI's `.surface` - the guardrail that
catches a keymap regression in review instead of in a user's terminal.

**Confirmation is a shared component,** not per-action prose. It takes a verb,
a count, and an optional secondary question (delete data?), so blast radius is
communicated identically everywhere.

**Errors are already typed.** `ErrAuth`, `ErrUnreachable`, `ErrServer`,
`ErrUnsupported` come out of `internal/transmission`. Each gets a distinct
presentation - auth means "fix your credentials", unreachable means "the
poll will retry", unsupported means "this server is too old" - decided once in
T4 and reused.

**Testing matches the repo.** `Update()` as pure state transitions,
table-tested; golden `View()` snapshots at a fixed size. `make check` stays
green throughout; new flags mean `make surface` and a man page edit.

---

## T1. Shell, focus router, keymap spine — **done**

**The hard 80%.** Layout is a consequence of getting focus and routing right;
the panes fall out of it.

- ~~Window/pane layout and resize reflow (`JoinHorizontal`/`JoinVertical` +
  `Compositor` per TUI-PLAN)~~
- ~~Focus router: which pane owns input → which keymap is active → which keymap
  feeds help. `tab`/`shift-tab` cycles **panes only**; `ctrl-w` + `hjkl`
  targets directly~~
- ~~The **esc ladder** as an explicit ordered stack (vision.md, Principles), not
  scattered local handlers. Implement it once, here, with the overlay stack it
  depends on~~
- ~~The **selection model** and the mark rendering, even before real data~~
- ~~Keymap registry + collision test + `?` help~~
- ~~Theme/styles/icon plumbing threaded from the first component (v2 has no
  global renderer - styles are passed explicitly)~~
- ~~Launch wiring: bare `snatcher` on a TTY starts the TUI, `--help`, an explicit
  subcommand, or non-TTY stdout keeps CLI behavior; client injected through a
  seam over `commands.dial()`~~ — shipped as `commands.Dialer` +
  `commands.ResolveClientSettings`; the TUI keeps the *dialer*, not a dialed
  client, so T4's server picker re-dials without a new seam
- ~~Every `bubbles`/`huh` component constructed with a zeroed keymap; the
  collision test checks the components' active keymaps, not just the registry~~
- ~~Windows scaffolded with mock data: torrents + settings~~

**Done when:** an empty shell navigates correctly - panes focus, `esc`
unwinds in the specified order, help lists the active context's keys, resize
reflows, and no key is bound twice. **Met.**

### Decisions

These were settled while building T1 and are not open in later milestones.

- **One package, `internal/tui`,** split by file. A window gets a sub-package
  when it outgrows that, not before (T5+).
- **The filter sidebar is hand-rolled,** not `bubbles/v2/list`. The list
  component binds `q`, `esc`, `h`, `l`, `u`, `d`, `f`, and `b` — seven keys
  vision.md spends elsewhere — and adds a status line and paginator to style
  away, in exchange for a fuzzy filter a fixed-length sectioned index does not
  want. TUI-PLAN's component table is corrected to match.
- **Every binding vision.md specifies is registered from T1,** with a `notYet`
  handler that toasts "not implemented yet" for the verbs a later milestone
  owns. Later milestones replace handlers, never bindings, so the collision
  test sees the whole keyboard from the start instead of discovering a clash in
  T5.
- **Goldens store `ansi.Strip(render())`.** A golden full of escape sequences
  is unreadable in review and pins the palette, which is exactly what T2
  changes. Style correctness gets two narrow assertions instead, plus an
  invariant that every frame is exactly `h` lines of exactly `w` cells.
- **`ctrl+c` escapes even a focused text input,** the one exception to "a
  focused input owns the keyboard". A user must never be trapped by a widget
  that has swallowed the keyboard.
- **The filter sidebar's status vocabulary is `transmission.Filter`'s own** —
  the values `--filter` takes — rather than vision.md's sketch of
  "Completed"/"Waiting", which the CLI cannot express. The parity rule says the
  TUI and the CLI name a selection the same way.
- **List-pane `h`/`l` horizontal scroll is deferred to T5.** `bubbles/v2/table`
  has no horizontal scroll, and a binding that did nothing would lie. The
  columns drop from the right as the pane narrows instead.
- **The help overlay stacks its contexts vertically and scrolls,** rather than
  using `help.FullHelpView`'s side-by-side columns: at 80 columns those
  silently drop two of the four contexts, leaving a help view that does not
  mention `q`.

## T2. Theme + Nerd Font content — **done**

Fills in the substrate stood up in T1.

- ~~`default` / `dracula` / `nord` palettes, light/dark detection via
  `lipgloss.LightDark(hasDark)`~~
- ~~Status→glyph icon map with the `--no-nerd-font` ASCII fallback~~
- ~~Wire flag > `snatcher config` > default (the precedence built in CLI M4)~~ —
  the chain was already built and wired in T1; what T2 added is that the three
  names now render differently
- ~~`NO_COLOR` honored, matching the CLI's plaintext behavior~~

**Done when:** the three theme names render as three distinct palettes, each
legible on a light and a dark terminal; the Nerd Font and ASCII glyph sets both
draw one cell per status; and `NO_COLOR`, `TERM=dumb`, and a piped stdout all
draw a frame whose focus and cursor are still readable. **Met.**

### Decisions

- **`default` is the terminal's own ANSI-16 palette,** identical for light and
  dark: text is `lipgloss.NoColor{}` (the terminal's configured foreground) and
  every other slot is one of the sixteen colors the terminal already resolves
  for every other program. The terminal has already made both decisions, so a
  light/dark pick for this theme would only second-guess it. `dracula` and
  `nord` are the opt-in fixed truecolor looks.
- **Every theme ships light and dark.** `dracula`'s light variant is Alucard,
  Dracula's own official light palette; `nord`'s follows Nord's guidance for
  bright interfaces (Polar Night for text, Snow Storm for surfaces). All three
  go through one `lipgloss.LightDark` pick, so no theme can render invisible
  text on a background it did not expect.
- **Focus is encoded in the border shape, always** — rounded when unfocused,
  thick when focused, double for the focused filter pane whose group actions hit
  every torrent in a group. Color stays too, but shape is what survives
  `NO_COLOR`, a 16-color terminal, and a colorblind user, and it is why the
  ANSI-stripped goldens now record which pane has focus.
- **Monochrome is a color-profile question, decided once in the model.** The
  model keeps the `colorprofile.Profile` from `tea.ColorProfileMsg` exactly as
  it keeps `isDark` from `tea.BackgroundColorMsg`, and `newStyles` takes a
  `mono` flag: no `Foreground`/`Background` call at all, with reverse video for
  the cursor row, the table's selected row, and the status bar. `NO_COLOR`,
  `TERM=dumb`, a pipe, and a forced profile are all one branch.
- **`NO_COLOR` is resolved at the command layer,** by the same non-empty
  predicate `isTTY` uses, and forced into the program with
  `tea.WithColorProfile(colorprofile.Ascii)`. colorprofile's own check is
  `strconv.ParseBool`, so left to it `NO_COLOR=yes` would strip color from
  `snatcher list` and not from the TUI. The TUI still reads no environment of
  its own.
- **Nerd Font glyphs are written as `\uXXXX` escapes,** never as literal
  private-use characters. T1 shipped the map as eight empty strings — the
  glyphs were lost between editor and commit, and the golden faithfully pinned a
  zero-width column — and the same thing happened once more while planning T2.
  The invariants (one rune, in U+E000–U+F8FF, one cell wide) are asserted on the
  glyphs rather than on a frame, because a golden cannot tell a private-use
  glyph from a missing one.
- **The `Mark` color slot is removed.** `bubbles/v2/table` cannot style one
  cell, so a marked row is a glyph, not a color. T5 adds a slot back if it finds
  a use.

## T3. Settings window — **done**

First real backend round-trip, and the place the form pattern gets solved.

- ~~`huh/v2` embedding (it is **not** a drop-in `tea.Model` - see TUI-PLAN)~~ —
  not embedded: the form is hand-rolled over `bubbles/v2/textinput` (see
  Decisions)
- ~~Tab strip: General, Polling, Download, Network, Bandwidth, Queue~~
- ~~General/Polling read and write `snatcher config`; the rest read `GetSession`
  and write `SetSession`, driven by the same category table behind
  `server show --category`~~ — the table now lives in `internal/transmission`,
  and the config writes go through `Deps.Config`
- ~~Field navigation per vision.md: `tab` moves fields, `up`/`down` belong to the
  focused field. Decide it here, before there are forty fields~~
- ~~Loading / error / dirty states; `esc` prompts to save pending changes~~

**Done when:** both a client-config write and a session write round-trip with
visible loading and error states, and the form keymap does not fight the
registry. **Met.**

### Decisions

- **No `huh/v2`.** The form is hand-rolled over `bubbles/v2/textinput`, the same
  call T1 made for the filter sidebar. Source review found five frictions:
  `huh.Input` cannot strip the banned `ctrl+h` from its embedded textinput; its
  field accessors write on every keystroke, so a draft cannot be discarded;
  `NewForm` reads `TERM`, and the TUI reads no environment; a submitted form's
  `View()` goes blank; and it pulls in about nine more modules. The window needs
  three widget kinds (toggle, text, single select) plus a button row. `huh` is
  re-evaluated when the upload and labels forms are built.
- **The session settings registry moved to `internal/transmission`** as exported
  domain metadata (`SettingDefs`, `LookupSetting`, `FilterSettings`, …), because
  the TUI may import `transmission` but never `commands`. It gained
  `ParseSettingValue` (so the window and a `server update` flag accept the same
  spellings), a display-only `Unit`, and `ServerDerived`, which replaced the
  command layer's private sanitize map. The CLI surface did not change.
- **Client settings reach the TUI through `Deps.Config`,** a `tui.ClientConfig`
  interface implemented by `commands.ClientConfigStore` and adapted in
  `internal/cli`. It wraps the same resolve/validate/write functions as
  `config list` and `config set`, so the window cannot accept a value the CLI
  rejects. A key overridden by a flag or environment variable for this run is
  read-only and names the override (`(set by --theme for this run)`), since
  writing the file would change nothing the user can see. With no store the
  client rows show this run's values read-only.
- **`client.list_columns`, `list_sort`, and `list_reverse` are not in the window.**
  T5 decides where they live, next to the `c` and `o` keys that change them.
- **`up`/`down` belong to the row:** a toggle flips, a select cycles its option,
  an open dropdown moves its cursor, and a text row or button ignores them.
  `enter` flips a toggle, opens or commits a dropdown, starts editing a text row,
  or presses a button. `tab`/`shift+tab` move rows, buttons included.
- **esc in a settings input discards the draft.** vision.md's "blur, keeping
  contents" was written for a search field, whose text is its state; a settings
  row's state is its last committed value. A submitted value that does not parse
  keeps the input open with the reason on the hint line.
- **Read-only rows are skipped by `tab` and never hold the cursor.** The Save and
  Revert buttons are on every tab and act on every tab. The focused button is
  marked in its text (`[>Save (2)<]`) as well as its style, so focus survives
  `NO_COLOR` and a stripped golden.
- **Units are raw.** `KB/s`, minutes, and MiB values are shown and edited exactly
  as Transmission stores them, with a muted unit hint. `output.Speed` (IEC over
  bytes) is never used for the KB/s keys.
- **Client config is read synchronously; the session is fetched asynchronously.**
  The config store is a local file read on every open; `GetSession` is the first
  `Cmd`/msg pair, with a 15s timeout derived from the program context. A loaded
  window is not re-fetched on re-entry, so edits survive leaving it; `R`
  reloads, confirming first when it would discard changes. A save blocks every
  settings key; a load blocks only Save and `R`, because the server rows are not
  selectable until it lands and the client tabs have nothing to race. Save
  writes client keys first (all validated before any is written), then one
  `SetSession` and a `GetSession` to show what the server kept.
- **The confirm overlay is modal.** Its stack is `ctxConfirm` alone, so a stray
  `q` cannot quit past "Save 2 changes before quitting?". `q` prompts from any
  window while settings are unsaved, since leaving the window does not save
  them; `ctrl+c` never prompts. The status bar shows `unsaved: N` everywhere.
- **The ladder gained two rungs:** `close dropdown` (the inline dropdown is not an
  overlay, but it is on top) below `close overlay`, and `prompt to save` above
  `return to torrents`. Eleven rungs.
- **The settings input's keymap is built explicitly,** not taken as "defaults
  minus `ctrl+h`": `textinput.DefaultKeyMap` also binds seven `alt+` chords, and
  the registry bans the prefix. Paste is unbound too — textinput's paste reads
  the system clipboard, which vision.md rules out — and the terminal's bracketed
  paste still arrives as a forwarded `tea.PasteMsg`. The input keeps the virtual
  cursor; `tea.View.Cursor` stays nil.
- **The help line advertises `?` and `q` only where they resolve.** Under a
  focused input, a confirm, or a pending chord they are swallowed, and a line
  offering them would be advertising dead keys.
- **`errorLine` is the first typed-error presentation** — auth, unreachable,
  unsupported, server — used by the settings window. T4 relocates it and makes
  it the one presentation.
- `bubbles/v2/textinput` brings `github.com/atotto/clipboard` in as an indirect
  dependency; nothing in snatcher calls it.

## T4. Status bar + data/refresh spine — **done**

The reusable machine everything live rides on.

- ~~`tea.Tick` → fetch `Cmd` → `DataMsg` → update, with explicit loading, empty,
  and error states; poll interval from config~~
- ~~Status bar: version, host, current server alias, global down/up and limits,
  free space, total size, mark count~~
- ~~`▁▂▃▄▅▆▇█` sparkline from a rolling rate buffer~~
- ~~Server picker (`S`) - switch named servers without leaving the TUI, tearing
  down and re-dialing cleanly~~
- ~~The typed-error presentation decided once, here~~
- ~~`t` alt-speed~~ — moved here from T5 as the spine's first mutation→refresh

**Done when:** the torrent list and status bar fill from a live poll with
visible loading, empty, and error states; `R`, `t`, and `S` each round-trip;
and every frame still fills the terminal exactly at 60, 80, 120, and 200
columns. **Met.**

### Decisions

- **Split cadence.** Every tick polls `ListTorrents` (every torrent,
  `AllFields`) and `Stats`. The first poll, and every sixth after it, also runs
  the slow refresh: one `Session` round-trip (`ServerInfo` + `SessionSettings`)
  and `FreeSpace` on the session's download directory, skipped when it has none.
  `R`, `t`, a server switch, and a settings save that wrote session keys run it
  early. Limits another client changes can lag by up to six ticks.
- **The interval runs from the reply.** The next tick is scheduled when a poll
  answers, so a slow server stretches the cadence rather than stacking polls,
  and polls never overlap. `Deps.PollInterval` is read at scheduling time, so a
  saved `client.poll_interval` applies from the next tick.
- **Stale work is retired, not canceled.** `tea.Tick` cannot be stopped, so
  every tick carries a sequence number and every reply a generation: `R` bumps
  the sequence, a server switch the generation, and whatever no longer matches
  is dropped. Every backend `Cmd` derives its 15s timeout from the program
  context.
- **The poll projects `AllFields`.** The list pane, the sidebar (labels,
  errors), and T6's overview need nearly all of them, and none is nested.
  `PollFields` stays for a caller that wants the light set.
- **Rows are in queue order,** the CLI's default `list_sort`. A non-default
  `client.list_sort` is not honored until T5 wires `o`, so until then the TUI
  and `snatcher list` can disagree for a user who changed it.
- **Mock data is test-only.** `mock.go` became `mock_test.go`. A non-nil
  `Deps.Torrents` is a static set that disables polling (tests); nil with a
  client polls; nil without one is the `no server` state.
- **The cursor follows its torrent by ID** across a poll, and marks on torrents
  that disappeared are dropped silently — the status bar's count is the only
  tell, the stance vision.md takes on hidden marks. The sidebar is rebuilt on
  every poll, keeping its cursor and applied filter by what they name rather
  than by index.
- **Used space is Σ (`SizeWhenDone` − `LeftUntilDone`)** over the polled rows.
  T5's filter must keep summing the unfiltered set.
- **Limits render raw,** `(5000 KB/s)`, exactly as `server bandwidth show`
  prints them, never through `output.Speed` (IEC over bytes). The limit shown is
  the one in force: alt-speed's while it is on, otherwise a global limit only
  while it is enabled. Rates use `output.Speed`.
- **Sparklines are hand-rolled:** eight samples per direction, scaled to that
  ring's own maximum, blank until there are two, and left-padded to eight cells
  so the segment does not jitter. No `ntcharts`.
- **The status bar degrades by rank** when it does not fit: the Transmission
  version first, then used, free, the two rates (sparkline, then limit, then
  dropped, evenly between them), then snatcher's version. The server, an error
  (`! unreachable`), `alt-speed`, and the mark/unsaved/chord counts are never
  dropped. Alt-speed is plain text rather than a glyph, so `Icons` stays
  status-only.
- **One typed-error presentation, in `errors.go`.** Each sentinel has a short
  form for the status bar (`auth failed`, `unsupported server`, `unreachable`,
  `server error`), the sentence T3 introduced, and a pane hint that always ends
  "press R to retry". Any other error is sanitized, and bounded in the bar.
- **`t` flips alt-speed from the last slow refresh** with one `SetSession`,
  shows the change at once, then re-reads the session. It does not confirm, and
  before the first slow refresh it says it is waiting rather than guessing. The
  settings window's loaded Bandwidth tab is not touched; `R` there reloads it.
- **The picker is session-only,** like `--server`. It lists Transmission aliases
  only, never writes `default_server`, and asks before discarding unsaved
  settings. The dial runs as a `Cmd`, because resolving a secret can raise a
  Keychain prompt, and a second `S` is refused until it lands. A failed dial
  keeps the old server and resumes its polling; a successful one empties the
  rows, rebuilds the settings window (reloading it if it is in front), and polls
  at once.
- **Two seams changed.** `commands.Dialer` returns a `DialResult` carrying the
  resolved alias and any unavailable-store warning, captured by swapping the
  command's error writer around the dial so it becomes the first toast instead
  of a line under the alt screen; the single-flight picker is what keeps the
  swap safe. `commands.Servers` lists the picker's aliases without touching a
  secret store. `internal/cli` adapts both to the TUI's own types. The status
  bar's version is `v` + `version.Version`; `FullVersion()` had printed
  "snatcher" twice.

## T5. Torrent list, live

The main event. Reuses everything above.

- `bubbles/v2/table` driven by the existing `internal/transmission`
  filter/search/sort transforms (neither table sorts itself - sort the data,
  rebuild rows)
- Filter sidebar (`bubbles/v2/list`) with live counts; `enter` applies
- Marks (`space`), `/` search with `n`/`N`, `f` filter modal, `o` sort,
  `c` columns
- Actions on marks-or-cursor: `s p v r d`, `J`/`K` queue, `L` labels, `M` move,
  `R` refresh, `y` yank chords, `a`/`A` add (`t` alt-speed shipped in T4)
- `m` action menu with the full vision.md list
- Filter-pane group actions with counted confirmation and loud focus styling
- Optimistic update or immediate refetch after each mutation - pick one and
  apply it uniformly

**Label discovery is client-side.** The sidebar's `Labels (dynamic)` group needs
the distinct labels in use on the server. CLI M6 (`torrent label`) shipped
`list|add|remove|set` and **no** server-side discovery verb, because
Transmission's RPC has none to wrap - the distinct set is a union over every
torrent's `labels`, and any verb snatcher added would still be a full read.
It is derivable from the CLI today:

```
snatcher torrent label list --all --jq '[.[].labels[]] | unique'
```

The TUI gets it cheaper still: project `FieldLabels` in the poll T4 already
makes and compute the union - and the per-label counts the group renders - from
the model the list pane already holds, with no extra round-trip.

**Done when:** every list-pane binding in vision.md works against a real
server, and no action can hit more torrents than the confirmation stated.

## T6. Detail pane

- Tabs: overview, files, trackers, peers, pieces, in a `viewport`, reusing the
  T3 tab strip
- `[`/`]`, `h`/`l`, `1`-`5`; `tab` leaves the pane rather than cycling tabs
- Files tab: marks, priority high/normal/low, wanted/unwanted, rename - over
  CLI M2/M3
- Pieces tab: filled/unfilled block rendering from the decoded bitfield
- Peers and trackers read-only tables

## T7. Command palette

- `:` opens a fuzzy list of every registered action, including the long tail
  with no binding (switch server, test port, reset a setting, obscure
  `torrent update` flags)
- Runs against the current selection, so palette actions obey the same
  marks-or-cursor rule
- Fed by the T1 keymap registry plus a table of unbound actions, so it cannot
  drift out of sync with what the TUI can actually do

## T8. Prowlarr window

Gated on CLI M8. Reuses the established window/pane/table/detail patterns.

- Search pane: query field, category and indexer dropdowns (fed by
  `search indexers` / `search categories`)
- Results table: age, title, indexer, size, grabs, seeders, leechers; `+`
  fetches more
- Detail pane with the tracker URL
- `a` grab (with the "this goes to Prowlarr's download client" wording from
  vision.md), `w` write `.torrent` locally, `O` open in browser, `y` yanks
- `P` opens the Prowlarr web UI

## T9. Hardening and release

- Help screen audited against the keymap registry - every context complete
- Golden `View()` snapshots for each window at a fixed size
- A pass for narrow terminals and small heights (what collapses first)
- Docs: man page, README, vision checklists; a recording for the README
- `make release-check` green

---

## Not in v1

- **Mouse.** Keyboard-first. `bubblezone/v2` is the fast-follow and pairs with
  `Compositor` layer hit-testing.
- **Charts beyond the sparkline.** Revisit `ntcharts` once it tags a v2 release
  without a `replace` directive.
- **File manager window** (vision "V2").

## Open questions

- Does `space` advance the cursor after marking? Spec says no (k9s-style);
  revisit if bulk marking feels slow in practice.
- ~~Do marks survive a filter or sort change, or are they cleared?~~
  **Decided** in `docs/vision.md` (Selection principle): marks survive sort
  and `/` search, and clear on a filter change - a single hidden mark would
  otherwise silently displace the cursor row with no confirmation.
- ~~Poll cadence vs. server load: one interval for everything, or a cheap
  `PollFields` tick for the list plus a slower full refresh?~~
  **Decided** in T4: the full record and stats every tick, the session and
  free space every sixth.
- ~~Where does the status bar (T4) get its global speed limits: poll
  `snatcher server bandwidth show`'s object on the refresh interval, or reuse
  the `stats` call the bar already makes and refresh the limits on a slower
  tick? `bandwidth show` returns the current rates, both limit pairs, and the
  alt-speed switch together, but costs two round-trips (`session-get` +
  `session-stats`); `stats` alone is one round-trip and carries the rates only.
  The CLI contract supports either, so this is a TUI cost decision.~~
  **Decided** in T4: rates from `Stats` every tick, limits from `Session` on
  the slow refresh.
- Does the torrent action menu's "trackers" entry edit trackers in place, or
  defer to the trackers tab?
- Does the `Labels (dynamic)` filter group derive its labels client-side from
  the polled torrent list (see T5), or does CLI M6 grow a server-side discovery
  verb first? Leaning client-side: the union is already in the poll payload once
  `labels` is projected, the counts the group renders need the per-torrent list
  anyway, and a discovery verb would be the same full read wearing a different
  name. Revisit only if recomputing the union every tick costs too much on a
  large library - the fix there is a cached union invalidated on label writes,
  not a new RPC call.
