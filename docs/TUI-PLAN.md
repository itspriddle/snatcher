# snatcher TUI — build plan

Working spec for the snatcher TUI. Pairs with `docs/vision.md` (product
vision, window layout, exhaustive keybindings) and the `tui-design` skill
(`.claude/skills/tui-design/`, especially `references/charm-v2.md`). This
document is authoritative for the TUI's **library stack, architecture, and
build order**; `vision.md` remains authoritative for the **UX surface**.

The TUI is pure UI-layer work: the `*transmission.Client`
(`internal/transmission`) already exposes every method the TUI needs —
`Stats`, `GetSession`/`SetSession`, `ListTorrents`, and the full mutation set
(start/stop/verify/reannounce/queue/remove/move/set/add). No client changes
are required.

## Library stack

Target the **Charm v2 ecosystem** (Feb 2026 release): vanity import paths
`charm.land/<lib>/v2`, Go 1.25+. v1 patterns do not compile against v2.

Runtime dependencies — the entire set for v1 of the TUI:

| Module | Version | Used for |
|--------|---------|----------|
| `charm.land/bubbletea/v2` | v2.0.8 | framework (Elm architecture, `View() tea.View`) |
| `charm.land/lipgloss/v2` | v2.0.5 | styling + `Compositor` (overlays/modals/toasts) |
| `charm.land/bubbles/v2` | v2.2.1 | table, viewport, help, key, spinner, progress, textinput |
| `charm.land/huh/v2` | — | **deferred** — T3 hand-rolled the settings form over `textinput` instead; re-evaluate for the upload and labels forms |

The first three landed in T1. `bubbles/v2/textinput`, first used in T3, brings
`github.com/atotto/clipboard` in as an indirect dependency. Two more are direct
dependencies:
`github.com/charmbracelet/x/ansi` for the golden helper's `ansi.Strip`, and
`github.com/charmbracelet/colorprofile` (v0.4.3), added in T2 for
`colorprofile.Profile` — the model stores the one bubbletea reports, and forces
`colorprofile.Ascii` under `NO_COLOR`. No `replace` directives anywhere in the
graph.

`github.com/lrstanley/bubblezone/v2` was decided against when mouse landed:
hit-testing is in-tree (see `docs/tui-todo.md`, T10).

### Component mapping

| Surface | Built with |
|---------|------------|
| Torrent list (main pane) | `bubbles/v2/table` + existing `internal/transmission` sort/filter/search transforms |
| Filter sidebar | **hand-rolled** sectioned cursor list — see the T1 decision in `tui-todo.md` |
| In-list search (`/`) | `bubbles/v2/textinput` over the existing `transmission.SearchByName` |
| Detail pane (scrollable) | `bubbles/v2/viewport` |
| Modals / confirmations / toasts | lipgloss `Compositor` layers |
| Settings form | **hand-rolled** over `bubbles/v2/textinput` — see the T3 decision in `tui-todo.md` |
| Upload / labels forms | re-evaluate `huh/v2`; otherwise reuse the T3 form pattern |
| Verify / port-test progress | `bubbles/v2/spinner` + `progress` |
| Copy magnet/tracker URL | `tea.SetClipboard` (OSC52, built-in) |
| Speed graph | hand-rolled `▁▂▃▄▅▆▇█` sparkline from a rolling buffer |

### Hand-rolled (no v2 lib worth the dependency)

- **Window splits / layout** — lipgloss `JoinHorizontal`/`JoinVertical` +
  `Place` + `Compositor`. (`76creates/stickers` is half-migrated: lipgloss v2
  but still bubbletea v1.)
- **Tabs** (detail tabs, settings tabs) — styled tab strip. No tabs component
  exists in bubbles v2; the core PR for one is still open.
- **Themes** (`default|dracula|nord`) — one function per theme returning the
  named color slots. `default` is the terminal's own ANSI-16 palette (text is
  `lipgloss.NoColor{}`), identical light and dark; `dracula` and `nord` are
  fixed truecolor palettes, each with a light arrangement (Alucard, and Nord's
  bright guidance) picked by `lipgloss.LightDark`.
  (`catppuccin/go` is v2-agnostic if Catppuccin is ever added.)
- **Nerd Font icons** — const glyph map keyed by torrent state, with the
  `--no-nerd-font` ASCII fallback.

### Rejected

- `daltonsw/bubbleup` (toasts) — v1-only, no v2 branch. Replaced by
  `Compositor` layers.
- `Evertras/bubble-table` — v2 only on untagged `@main`; we use core
  `bubbles/v2/table` + our own transforms instead.
- `NimbleMarkets/ntcharts` (charts) — v2 branch is untagged and carries a
  `replace` directive; hand-rolled sparkline instead.
- `rmhubbert/bubbletea-overlay` — v1-only; its maintainer points v2 users at
  lipgloss compositing.

## Architecture decisions

**Package placement preserves the layering rule.** TUI code lives in
`internal/tui/`, one package split by file through T1; a window gets a
sub-package when it outgrows that (T5+). It depends on `internal/transmission`
and `internal/output` and nothing else of snatcher's — not `commands`, not
`config`, not `secrets`. `internal/cli` launches it and injects everything
through `tui.Deps`.

**Launch model** (built in T1). Bare `snatcher` on a terminal launches the TUI;
`--help`, an explicit subcommand, or a redirected stdin or stdout keeps CLI
behavior. `--config`, `--theme`, and `--no-nerd-font` are persistent root flags
already in `.surface` and the man page.

The seam is `commands.Dialer(cmd)`, `commands.Servers(cmd)`, and
`commands.ResolveClientSettings(cmd)`: `internal/cli` adapts them into a
`tui.Deps` and calls `tui.Run`, and `internal/tui` imports neither
`internal/commands` nor `internal/config`. It is handed the *dialer* rather than
a dialed client, so the server picker re-dials without a second seam. A dial
returns a `DialResult` with the resolved alias and any unavailable-store
warning, captured rather than written to stderr under the alt screen.
`tui.Run` dials before `tea.NewProgram`, so an unconfigured machine sees the
CLI's own error on the terminal instead of inside an alt screen.

**Focus / routing is the core architecture.** The keybindings in `vision.md`
are deeply context-dependent — the same key means different things in the
filter pane vs list pane vs detail pane, `tab` cycles panes, `esc` is layered.
The spine is a focus router: which pane owns input → which `key.Map` is active
→ which keymap feeds the `bubbles/help` view. Root model orchestrates focus and
routes messages; each window/pane is a sub-model with its own `Update`/`View`.

A focused input's stack is not always `ctxTextInput` alone. An overlay that
holds the focused input decides it, and may bind keys above the input's two
escapes — the `:` palette binds the arrow and page keys over its query. A
registry miss in that stack must be forwarded to the input, never swallowed, or
`j`, `q`, and `:` stop typing.

**Theme/styles is a substrate, not a phase.** Lip Gloss v2 has no global
renderer and no root `AdaptiveColor`; every component is handed its styles
explicitly. A `Theme`/`Styles` struct, light/dark detection
(`lipgloss.LightDark(hasDark)`), and the icon helper are threaded from the
first component so nothing hardcodes styles. Palette/config content is filled
in at step 2, but the plumbing exists at step 1.

**Data / refresh spine.** Steps 4 and 5 share one machine: `tea.Tick` → fetch
`Cmd` → `DataMsg` → model update, with explicit loading / empty / error states
and `ErrAuth`/`ErrUnreachable` surfaced in the UI. Build it once so the status
bar and the torrent list ride identical rails. The first real round-trip
happens earlier, at the settings page (`GetSession`), which proves the
fetch + error UX before the status bar exists.

**Testing matches the repo.** Model `Update()` as pure state transitions
(table-tested), plus golden `View()` snapshots. Must survive `make check`
(`fmt-check`, `vet`, `lint`, `test`, `check-surface`, `check-man`,
`tidy-check`). New flags/commands require regenerating `.surface` and updating
the man page.

## Charm v2 gotchas (verified against source)

- **`huh` is not a drop-in `tea.Model`.** `Form.Update` returns `huh.Model`
  (assert to `*huh.Form`), `Form.View()` returns `string`. Wrap it inside the
  outer model's `View() tea.View`. T3 did not use `huh` (see its decisions in
  `tui-todo.md`); this still applies if the upload or labels forms do.
- **Neither table sorts.** `bubbles/v2/table` and `lipgloss/v2/table` render
  only — sort the underlying data and rebuild rows via `SetRows`.
- **Overlays use `Compositor`, not just Canvas/Layer.**
  `lipgloss.NewCompositor(layers...).Render()`; position layers with
  `.X().Y().Z()` (higher Z draws on top). `Place*` positions within a box but
  does not composite. There is no "center" helper — compute the offset.
- **`Model.View()` returns `tea.View`** (build with `tea.NewView(s)`).
  Alt-screen, mouse mode, window title, and cursor are struct fields, not
  program options.
- **`key.Matches` is generic** (`Matches[Key fmt.Stringer]`) but call sites are
  unchanged — `tea.KeyPressMsg` implements `String()`.
- **Light/dark is explicit** — detect once, pick with
  `lipgloss.LightDark(hasDark)`. No root `AdaptiveColor` (it's in the `compat`
  subpackage if ever needed).
- **Clipboard via OSC52** isn't universal across terminals. Done: yanks also
  write through a native tool (`internal/clipboard`) when one is on PATH and
  the session is not over SSH.
- **The color profile arrives as a message, like the background color.**
  bubbletea detects it with `colorprofile.Detect` unless
  `tea.WithColorProfile` forced one, then sends `tea.ColorProfileMsg`. A
  truecolor palette is safe on a 16-color terminal: ultraviolet downsamples
  ANSI/ANSI256 automatically. The two profiles that carry no color differ —
  `Ascii` drops fg/bg/underline color but **keeps** bold, reverse, and
  underline; `NoTTY` strips everything — so reverse video is a usable fallback
  under `Ascii`, which is the profile `NO_COLOR` forces.
- **colorprofile's own `NO_COLOR` check is `strconv.ParseBool`,** not
  "non-empty". `NO_COLOR=yes` would not disable color there, while the CLI's
  `isTTY` treats any non-empty value as set, per no-color.org. So the command
  layer decides, with one helper, and hands the answer to the TUI as
  `Deps.NoColor`; the TUI reads no environment of its own.
- **Every component ships a default keymap that fights `vision.md`.** Read
  from the v2.1.0 / v2.0.3 sources:
  - `bubbles/v2/table`: `f`/`b`/`space` page, `u`/`d` half-page, `g`/`G`
  - `bubbles/v2/viewport`: `space`/`f`/`b` page, `u`/`d` half-page, `h`/`l`
    horizontal scroll
  - `bubbles/v2/list`: `q`/`esc` quit, `?` help, `h`/`l`/`u`/`d`/`f`/`b`
    paging, `ctrl+j`/`ctrl+k` accept while filtering
  - `bubbles/v2/textinput` and `textarea`: `ctrl+p`/`ctrl+n` suggestions or
    lines, `ctrl+d`, `ctrl+u`, `ctrl+w`, `ctrl+h`, `ctrl+t` (textarea), and
    seven `alt+` chords for word movement and deletion (textinput v2.2.1)
  - `huh/v2`: `ctrl+j`/`ctrl+k` on Select and MultiSelect, `ctrl+j` newline
    on Text, `enter` = next field, `h`/`l` toggle Confirm, `y`/`Y` accept
    Confirm, `x`/`space` toggle MultiSelect, `ctrl+a` select all

  `f`, `d`, `u`, `space`, `h`/`l`, `q`, `?`, and `enter` all mean something
  else in the vision, and `ctrl+j`/`ctrl+k`/`ctrl+h` are on the banned list.
  So no component keeps its default keymap: each is constructed with a zeroed
  `KeyMap` and only the bindings the registry hands it. The T1 collision test
  must assert the components' *active* keymaps against the registry, not just
  the registry against itself - a default that survives construction is a
  binding the registry never saw. Text inputs are the one exception, and
  deliberately: their readline chords are the "focused input owns the
  keyboard" rule in `vision.md`. They are not "defaults minus `ctrl+h`",
  though — the `alt+` chords are banned too — so each input gets an explicitly
  built readline keymap with no banned key, no suggestion keys, and no
  clipboard-reading paste, and a focused input is handed every key the router
  does not reserve (`esc`, `enter`, `ctrl+c`).

- **`tea.Tick` cannot be canceled.** Its timer starts when `Tick` is called and
  there is no handle to stop it. Retire a superseded tick instead: carry a
  sequence number on the message and drop any tick that no longer matches, and
  a generation for anything that must not outlive a server switch. The timer can
  outlive the program by one interval; it only delivers a message nobody reads.
- **`tea.Batch` delivers a `tea.BatchMsg`** (`[]Cmd`), which the runtime runs
  concurrently — except that a batch of one non-nil command is returned as that
  command. A synchronous test helper that runs a `Cmd` and feeds its message
  back has to flatten a `BatchMsg` itself, or it silently tests nothing.
- **Every backend `Cmd` derives from the program context** (`tea.WithContext`)
  with its own timeout, so quitting cancels a round trip in flight.
- **`bubbles/v2/table` columns have no alignment.** `table.Column` is
  `{Title, Width}`. Right alignment is padding in the cell when rows are built,
  and it holds only while every non-flex width is fixed at that point:
  `fitColumns` changes only the flex (name) width and zeroes dropped columns, so
  cell counts and padded widths never shift under a resize.
- **A tick issued while a poll is in flight is dropped** by the spine's own
  single-flight guard. A refetch requested then — after a mutation — must be
  queued (`pollAgain`, honored when the in-flight reply lands), or it waits a
  whole interval.
- **A `textinput` held by an overlay is a value in the overlay stack.** Its
  `Update` returns a new model; write the overlay back with
  `replaceTopOverlay`, or keystrokes land on a copy nothing draws.
- **The router has to know which input owns the keyboard.** `inputFocused`
  says one does; `Model.owner` says which, so a key, a paste, or a blink tick
  reaches the right input, and a blur runs that input's own rule: a search keeps
  its text, a settings or form row discards its draft, a prompt closes.
- **`help.ShortHelpView` does not reliably truncate** (bubbles v2.2.1). Once an
  item overflows and the ellipsis no longer fits either, it appends every
  remaining item. A help line inside a fixed-width box is truncated by the
  caller with `ansi.Truncate`.

- **Repointing a viewport every poll must not reset its offset.**
  `viewport.SetContent` leaves the offset alone, and a `SetYOffset(0)` after it
  throws the user back to the top of a tab each tick. Clamp on a refresh
  (`SetYOffset(YOffset())`) and reset only on a change of subject: another
  torrent, another tab.
- **A `table` in a pane with several is focused only while its tab is
  current.** `table.Update` no-ops on an unfocused table, and a focused one on a
  hidden tab eats the nav keys silently, so focus is pushed on a tab switch as
  well as on a pane focus.
- **`table.SetHeight` takes its own header out of the height it is given,** so
  a pane passes the region the header and rows share.
- **The test stub's `torrent-get` is both the poll and a detail read.** Tell
  them apart by argument — only a detail read names `files` in `fields` — with
  `respondWhen`, which is checked before a method's plain answer.
- **`reflect.DeepEqual` never equates two non-nil funcs,** so a func field on a
  value the tests compare with `assert.Equal` breaks the comparison. Carry
  behavior on a zero-size type parameter instead.

## Build sequence

Each step states its goal and what it de-risks. Steps 1–2 establish the shell;
3–4 prove the form/mutation and data/refresh patterns on simpler surfaces; 5 is
the real work; 6 is gated on backend.

### 1. Shell + focus architecture

Windowing/panes/layout, the focus router, the keymap + `bubbles/help`, resize
reflow, **theme/styles/icon plumbing**, and TUI launch wiring (bare `snatcher`
→ TUI, `--theme`/`--no-nerd-font` flags, backend injection seam). Build the
torrent-window and settings-window scaffolds with **mock data**. Deliverable:
navigable empty shell with working focus, help, and resize. This is the hard
80% — the boxes fall out of getting focus/routing/layout right.

### 2. Theming + nerd-font content

Finalize the `default`/`dracula`/`nord` palettes, the state→glyph icon map and
ASCII fallback, and decide the user-configurable surface (flag > config >
default). Fills in the substrate stood up in step 1.

### 3. Settings page

A hand-rolled form over `bubbles/v2/textinput` (`huh` was evaluated and
deferred — see the T3 decisions in `tui-todo.md`), settings tabs
(General/Polling/Download/Network/Bandwidth/Queue per `vision.md`), and the
first real backend round-trip: `GetSession` load + `SetSession` write with
loading/error UX. Establishes the form pattern (reused by upload/labels) and
the mutation→refresh pattern (reused by torrent actions). Note: General
(snatcher) and Polling tabs map to snatcher's own config; the rest map to
Transmission session settings — so this exercises both config-write and
session-write paths.

### 4. Status bar + data/refresh spine

A tick-driven poll over `ListTorrents` + `Stats`, with a slower refresh of the
session and free space, feeding the torrent list and a status bar of live
rates, limits, and `▁▂▃▄▅▆▇█` sparklines; explicit loading, empty, and error
states; the `S` server picker and `t` alt-speed. Deliverable: the reusable
fetch→update→render machine the torrent list rides.

### 5. Torrent list (live)

Make the torrent window live: `bubbles/v2/table` for the main pane driven by
the existing `internal/transmission` transforms; the filter sidebar
(`bubbles/v2/list`) drives which torrents are visible; per-row actions
(start/stop/verify/remove/move/queue/set, copy magnet) wired to the backend
mutations through the refresh pattern from step 3. Reuses everything above.

**5b. Detail pane tabs.** Overview/files/trackers/peers/labels in a
`viewport`, reusing the tab pattern proven in the settings page.

## Open / deferred

- **Mouse** — landed after v1 as T10, with in-tree hit-testing rather than
  `bubblezone/v2`.
- **Speed graphs beyond a sparkline** — revisit `ntcharts` once it tags a v2
  release and drops the `replace` directive.
