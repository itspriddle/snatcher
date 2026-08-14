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

## T1. Shell, focus router, keymap spine

**The hard 80%.** Layout is a consequence of getting focus and routing right;
the panes fall out of it.

- Window/pane layout and resize reflow (`JoinHorizontal`/`JoinVertical` +
  `Compositor` per TUI-PLAN)
- Focus router: which pane owns input → which keymap is active → which keymap
  feeds help. `tab`/`shift-tab` cycles **panes only**; `ctrl-w` + `hjkl`
  targets directly
- The **esc ladder** as an explicit ordered stack (vision.md, Principles), not
  scattered local handlers. Implement it once, here, with the overlay stack it
  depends on
- The **selection model** and the mark rendering, even before real data
- Keymap registry + collision test + `?` help
- Theme/styles/icon plumbing threaded from the first component (v2 has no
  global renderer - styles are passed explicitly)
- Launch wiring: bare `snatcher` on a TTY starts the TUI, `--help`, an explicit
  subcommand, or non-TTY stdout keeps CLI behavior; client injected through the
  `dial()` seam
- Windows scaffolded with mock data: torrents + settings

**Done when:** an empty shell navigates correctly - panes focus, `esc`
unwinds in the specified order, help lists the active context's keys, resize
reflows, and no key is bound twice.

## T2. Theme + Nerd Font content

Fills in the substrate stood up in T1.

- `default` / `dracula` / `nord` palettes, light/dark detection via
  `lipgloss.LightDark(hasDark)`
- Status→glyph icon map with the `--no-nerd-font` ASCII fallback
- Wire flag > `snatcher config` > default (the precedence built in CLI M4)
- `NO_COLOR` honored, matching the CLI's plaintext behavior

## T3. Settings window

First real backend round-trip, and the place the form pattern gets solved.

- `huh/v2` embedding (it is **not** a drop-in `tea.Model` - see TUI-PLAN)
- Tab strip: General, Polling, Download, Network, Bandwidth, Queue
- General/Polling read and write `snatcher config`; the rest read `GetSession`
  and write `SetSession`, driven by the same category table behind
  `server show --category`
- Field navigation per vision.md: `tab` moves fields, `up`/`down` belong to the
  focused field. Decide it here, before there are forty fields
- Loading / error / dirty states; `esc` prompts to save pending changes

**Done when:** both a client-config write and a session write round-trip with
visible loading and error states, and the form keymap does not fight `huh`.

## T4. Status bar + data/refresh spine

The reusable machine everything live rides on.

- `tea.Tick` → fetch `Cmd` → `DataMsg` → update, with explicit loading, empty,
  and error states; poll interval from config
- Status bar: version, host, current server alias, global down/up and limits,
  free space, total size, mark count
- `▁▂▃▄▅▆▇█` sparkline from a rolling rate buffer
- Server picker (`S`) - switch named servers without leaving the TUI, tearing
  down and re-dialing cleanly
- The typed-error presentation decided once, here

## T5. Torrent list, live

The main event. Reuses everything above.

- `bubbles/v2/table` driven by the existing `internal/transmission`
  filter/search/sort transforms (neither table sorts itself - sort the data,
  rebuild rows)
- Filter sidebar (`bubbles/v2/list`) with live counts; `enter` applies
- Marks (`space`), `/` search with `n`/`N`, `f` filter modal, `o` sort,
  `c` columns
- Actions on marks-or-cursor: `s p v r d`, `J`/`K` queue, `L` labels, `M` move,
  `t` alt-speed, `R` refresh, `y` yank chords, `a`/`A` add
- `m` action menu with the full vision.md list
- Filter-pane group actions with counted confirmation and loud focus styling
- Optimistic update or immediate refetch after each mutation - pick one and
  apply it uniformly

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
- Do marks survive a filter or sort change, or are they cleared? Leaning
  survive-with-a-count, since the status bar shows the number.
- Poll cadence vs. server load: one interval for everything, or a cheap
  `PollFields` tick for the list plus a slower full refresh?
- Does the torrent action menu's "trackers" entry edit trackers in place, or
  defer to the trackers tab?
