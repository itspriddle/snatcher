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
- Poll cadence vs. server load: one interval for everything, or a cheap
  `PollFields` tick for the list plus a slower full refresh?
- Where does the status bar (T4) get its global speed limits: poll
  `snatcher server bandwidth show`'s object on the refresh interval, or reuse
  the `stats` call the bar already makes and refresh the limits on a slower
  tick? `bandwidth show` returns the current rates, both limit pairs, and the
  alt-speed switch together, but costs two round-trips (`session-get` +
  `session-stats`); `stats` alone is one round-trip and carries the rates only.
  The CLI contract supports either, so this is a TUI cost decision.
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
