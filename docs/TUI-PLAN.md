# snatcher TUI — build plan

Working spec for the snatcher TUI. Pairs with `docs/vision.md` (product
vision, window layout, exhaustive keybindings) and the `tui-design` skill
(`.claude/skills/tui-design/`, especially `references/charm-v2.md`). This
document is authoritative for the TUI's **library stack, architecture, and
build order**; `vision.md` remains authoritative for the **UX surface**.

The TUI is pure UI-layer work: the vendor-neutral `torrent.Backend`
(`internal/torrent`) already exposes every method the TUI needs short of
Prowlarr — `Stats`, `GetSession`/`SetSession`, `ListTorrents`, and the full
mutation set (start/stop/verify/reannounce/queue/remove/move/set/add). No
backend changes are required until step 6.

## Library stack

Target the **Charm v2 ecosystem** (Feb 2026 release): vanity import paths
`charm.land/<lib>/v2`, Go 1.25+. v1 patterns do not compile against v2.

Runtime dependencies — the entire set for v1 of the TUI:

| Module | Version | Used for |
|--------|---------|----------|
| `charm.land/bubbletea/v2` | v2.0.6 | framework (Elm architecture, `View() tea.View`) |
| `charm.land/lipgloss/v2` | v2.0.3 | styling + `Compositor` (overlays/modals/toasts) |
| `charm.land/bubbles/v2` | v2.1.0 | table, list, viewport, help, key, spinner, progress, textinput |
| `charm.land/huh/v2` | v2.0.3 | forms (settings, upload, labels) |

Deferred (v2-ready, add when mouse lands):
`github.com/lrstanley/bubblezone/v2@v2.0.0`.

### Component mapping

| Surface | Built with |
|---------|------------|
| Torrent list (main pane) | `bubbles/v2/table` + existing `internal/torrent` sort/filter/search transforms |
| Filter sidebar / in-list search | `bubbles/v2/list` (built-in `sahilm/fuzzy` filter) |
| Detail pane (scrollable) | `bubbles/v2/viewport` |
| Modals / confirmations / toasts | lipgloss `Compositor` layers |
| Settings / upload / labels forms | `huh/v2` (wrapped — not a drop-in `tea.Model`) |
| Verify / port-test progress | `bubbles/v2/spinner` + `progress` |
| Copy magnet/tracker URL | `tea.SetClipboard` (OSC52, built-in) |
| Speed graph | hand-rolled `▁▂▃▄▅▆▇█` sparkline from a rolling buffer |

### Hand-rolled (no v2 lib worth the dependency)

- **Window splits / layout** — lipgloss `JoinHorizontal`/`JoinVertical` +
  `Place` + `Compositor`. (`76creates/stickers` is half-migrated: lipgloss v2
  but still bubbletea v1.)
- **Tabs** (detail tabs, settings tabs) — styled tab strip. No tabs component
  exists in bubbles v2; the core PR for one is still open.
- **Themes** (`default|dracula|nord`) — small hex const map per theme.
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

**Package placement preserves the layering rule.** TUI code lives in a new
`internal/tui/` package (with sub-packages per window/component as it grows).
It depends only on `torrent.Backend` plus `config`/`secrets` — never the
concrete client, the same rule the commands follow. `internal/cli` launches it
and injects the backend.

**Launch model.** Bare `snatcher` on a TTY launches the TUI (replacing today's
`cmd.Help()` fallback in `internal/cli/root.go`). `--help`, an explicit
subcommand, or non-TTY stdout keeps CLI behavior. Add `--theme` and
`--no-nerd-font` as persistent root flags (shown in `vision.md`, not yet
wired). `dial()` is currently unexported in `internal/commands`; expose a seam
(`commands.Dial(...)` or a TUI constructor that takes a `torrent.Backend`) so
`cli` can dial and inject without `internal/tui` importing the concrete
backend.

**Focus / routing is the core architecture.** The keybindings in `vision.md`
are deeply context-dependent — the same key means different things in the
filter pane vs list pane vs detail pane, `tab` cycles panes, `esc` is layered.
The spine is a focus router: which pane owns input → which `key.Map` is active
→ which keymap feeds the `bubbles/help` view. Root model orchestrates focus and
routes messages; each window/pane is a sub-model with its own `Update`/`View`.

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
  outer model's `View() tea.View`. Solve this once in the settings page.
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
- **Clipboard via OSC52** isn't universal across terminals; consider a fallback
  for copy actions.

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

`huh` forms (solve the v2 embedding gotcha here), settings tabs
(General/Polling/Download/Network/Bandwidth/Queue per `vision.md`), and the
first real backend round-trip: `GetSession` load + `SetSession` write with
loading/error UX. Establishes the form pattern (reused by upload/labels) and
the mutation→refresh pattern (reused by torrent actions). Note: General
(snatcher) and Polling tabs map to snatcher's own config; the rest map to
Transmission session settings — so this exercises both config-write and
session-write paths.

### 4. Status bar + data/refresh spine

Generic poll loop over `Stats` (down/up rates, counts), live UI updates, and
the `▁▂▃▄▅▆▇█` speed sparkline from a rolling rate buffer. Deliverable: the
reusable fetch→update→render machine the torrent list will ride.

### 5. Torrent list (live)

Make the torrent window live: `bubbles/v2/table` for the main pane driven by
the existing `internal/torrent` transforms; the filter sidebar
(`bubbles/v2/list`) drives which torrents are visible; per-row actions
(start/stop/verify/remove/move/queue/set, copy magnet) wired to the backend
mutations through the refresh pattern from step 3. Reuses everything above.

**5b. Detail pane tabs.** Overview/files/trackers/peers/labels in a
`viewport`, reusing the tab pattern proven in the settings page.

### 6. Prowlarr

Last — needs a new Prowlarr (torznab) backend that does not yet exist. The
search window reuses the established window/pane/table/detail patterns.

## Open / deferred

- **Mouse** — deferred for v1; keyboard-first. `bubblezone/v2` is the
  fast-follow and pairs with `Compositor` layer hit-testing.
- **Speed graphs beyond a sparkline** — revisit `ntcharts` once it tags a v2
  release and drops the `replace` directive.
