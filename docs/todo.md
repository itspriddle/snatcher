# snatcher - CLI TODO (pre-TUI)

The outstanding **non-TUI** work, sequenced. `docs/tui-todo.md` picks up after
this; `docs/vision.md` is the product vision and `docs/TUI-PLAN.md` is the TUI
architecture.

Everything here exists for one reason: the vision's rule is **anything the TUI
can do, the CLI can do**. Each TUI pane needs a CLI capability underneath it, so
the TUI is a view over a shipped capability rather than the only way to reach
one. The "unblocks" line on each milestone says which pane is waiting.

## Definition of done (every milestone)

- `make check` green - `fmt-check`, `vet`, `lint`, `test`, `check-surface`,
  `check-man`, `tidy-check`
- `.surface` regenerated (`make surface`) and committed with the code
- `man/man1/snatcher.1` documents every new command and flag
- Output contract honored: `--jq` → `--json` → plaintext, server-derived
  strings through `output.SanitizeTerminal` on human paths **only**
- New wire fields decoded in **both** protocols (JSON-RPC 2.0 and bespoke) with
  tests for each

## Sequence

1. ~~**M1** keyring bug - blocks real use on headless Linux today~~ **done**
2. **M2**, **M3** detail-pane surface + rename - unblocks TUI detail pane
3. ~~**M4** client config - unblocks TUI settings window, needed before the
   settings window is built, not after~~ **done**
4. **M5**-**M7** small helpers - cheap, each unblocks a keybinding
5. **M8** Prowlarr - the only genuinely new backend; largest by far
6. **M9** hygiene pass before the TUI starts

---

## M1. Keyring failure on headless Linux - **done**

**Bug.** Reported against a headless Linux box:

```
$ snatcher server add --name default --url 'https://.../transmission/rpc'
Password:
Error: secrets: deleting password for "https://..." : exec: "dbus-launch": executable file not found in $PATH
```

`internal/secrets` is `zalando/go-keyring`, which on Linux talks to the D-Bus
Secret Service. With no D-Bus session, **every** keyring call fails with an
`exec` error, not `keyring.ErrNotFound` - so the `secrets.Delete` pre-clear in
`server add` (`server_registry.go:97`) aborts the command before it ever stores
anything. `server add` is unusable on a headless box.

Work:

- Detect "keyring unavailable" as a distinct condition from "entry not found",
  and make the pre-clear `Delete` in `server add`/`server remove` non-fatal
  when the keyring simply is not there
- Decide the fallback and implement one: an explicit opt-in file store
  (0600, XDG state dir, documented as less safe), a `SNATCHER_PASSWORD` env
  path, or a documented "no stored password" mode driven by `--password-stdin`
  per invocation
- Actionable error text naming the fix (install/start a secret service, or the
  fallback flag) rather than leaking `dbus-launch`
- Tests covering: keyring present, keyring absent, entry missing

**Done when:** `server add`, `server ping`, and `dial()` all work on a box with
no D-Bus, and the failure mode is documented in the man page.

**Shipped.** `internal/secrets` classifies an unreachable store as
`ErrUnavailable`, distinct from `ErrNotFound`, and never leaks `dbus-launch`
into a message. Every read path (`dial`, `server list`, `server show`,
`server remove`) tolerates it and warns once with an actionable remedy;
`server add` refuses to half-save a typed password and writes nothing. The
fallback is an explicit opt-in: `--store=keyring|file|none` (saved as
`secret_store` per server, overridden by `SNATCHER_SECRET_STORE`), where `file`
is a plaintext 0600 store at `$XDG_STATE_HOME/snatcher/secrets.json` that
`keyring` never falls back to on its own, plus a `SNATCHER_PASSWORD` read
override for fully unattended runs.

---

## M2. Torrent detail CLI surface

**Unblocks:** the TUI detail pane (files / peers / trackers / pieces tabs) and
the `y m` yank-magnet key.

No RPC work. `rpc.TorrentDetail` already decodes files, peers, trackers,
pieces, magnet link, and the overview scalars in both protocols, and
`Client.SetFiles` already writes `FileChanges`. This is command-layer only.

New commands (shape decided: sibling plural nouns, matching `list`/`queue`):

```
snatcher torrent files <id|hash>
snatcher torrent files update <id|hash> <index...> \
    --priority high|normal|low --wanted --unwanted
snatcher torrent peers <id|hash>
snatcher torrent trackers <id|hash>
```

- Each gets `--json` / `--jq` / `--fields` / `--server` / `--config`, and the
  same TTY-table vs tab-separated plaintext split as `list`
- `files update` maps its flags 1:1 onto `transmission.FileChanges`; indices
  are the positions in `TorrentDetail.Files`
- Columns: **files** name, size, done, %, priority, wanted · **peers** address,
  port, client, have %, down, up, encrypted, status · **trackers** announce
  URL, tier, status, next announce, seeders, leechers, downloads
- Peer and tracker strings are server-derived and untrusted - sanitize on the
  plaintext path

Also in this milestone, `torrent show` plaintext is incomplete: it already
fetches `TorrentDetail` but renders only the `list`-level columns, so magnet
link, comment, creator, private flag, piece count/size, downloaded/uploaded
ever, and seeding time are reachable in `--json` but invisible in plaintext.
The TUI's overview tab is exactly that field set.

- Extend `show`'s field table to the detail scalars, keeping today's default
  field order as the default (no surprise for existing scripts)
- Add `magnet_link` as a selectable field so `show --fields magnet_link` is the
  CLI equivalent of `y m`

**Done when:** every field the TUI detail pane will render is reachable from
the CLI, and file priority/wanted can be changed without the TUI.

---

## M3. Rename (`torrent-rename-path`)

**Unblocks:** rename in the TUI files-tab action menu and the torrent action
menu.

The one item here that needs new RPC-layer work.

- `Protocol.TorrentRenamePath` in **both** implementations (JSON-RPC 2.0
  `torrent_rename_path`, bespoke `torrent-rename-path`), with the usual
  per-scheme argument spelling and response decode
- `rpc.Client.TorrentRenamePath` → `transmission.Client.RenamePath`
- `snatcher torrent rename <id|hash> <path> <new-name>`, single-torrent only
  (the RPC is single-torrent by design), `--json`/`--jq`
- Note the trap: `path` is the torrent-relative path of the file or directory,
  and renaming the torrent's root path renames the torrent

**Done when:** both wire schemes are covered by tests and `torrent rename`
round-trips against a real server.

**Shipped.** `Protocol.TorrentRenamePath` speaks `torrent_rename_path` on
JSON-RPC 2.0 and `torrent-rename-path` on the bespoke scheme, always sending
`ids` - an omitted `ids` would mean every torrent - and decoding the server's
`id`/`path`/`name` echo into the canonical `RenameResult`.
`rpc.Client.TorrentRenamePath` refuses anything but exactly one id, and
`transmission.Client.RenamePath` takes a `Selector`, rejects `--all` and any
multi-torrent selection before the wire, and maps failures onto the package
sentinels. `snatcher torrent rename <id|hash> <path> <new-name>` reads the
torrent's detail before and after the call, so the name it reports is
post-rename truth, and gates a root-path rename - the one that renames the
torrent itself - behind a `[y/N]` confirmation with `--yes` to skip, refusing
outright rather than prompting when stdin is not a terminal.

---

## M4. Client config (`snatcher config`) - **done**

**Unblocks:** the TUI settings window's General and Polling tabs, plus the
`--theme` / `--no-nerd-font` root flags from the vision's alpha CLI.

Today `internal/config` knows exactly one schema: `servers` +
`default_server`. The TUI needs client-side settings, and the parity rule says
they must be reachable from the CLI - not written only by the settings window.

- New client section in `config.json` alongside `servers` (keep the file's
  hand-editable, operator-owned character; still no secrets)
- Keys, at minimum: `theme` (default/dracula/nord), `nerd_font` (bool),
  poll interval, default list columns, default sort
- `snatcher config list|get <key>|set <key> <value>|unset <key>`, with
  `--json`/`--jq` and validation per key (unknown key is an error, not a
  silent write)
- Persistent root flags `--theme` and `--no-nerd-font`; precedence is
  **flag > config > default**, resolved at the command layer the same way
  `--server` > `SNATCHER_SERVER` > `default_server` already is
- Config writes stay atomic and 0600, as `config.Save` already does

**Done when:** every field the settings window's General/Polling tabs will
render has a `config` key, and the flag/config/default precedence is tested.

**Shipped.** `config.json` grew a top-level `client` section holding six keys -
`theme`, `nerd_font`, `poll_interval`, `list_columns`, `list_sort`, and
`list_reverse` - each an optional field, so an explicitly stored `false` stays
distinct from an absent key. `config.Save` now writes atomically (temp file at
0600 in the target's own directory, then a rename) and preserves keys it does
not model, both at the top level and inside `client`, so the file stays the
hand-editable, operator-owned artifact it was. `snatcher config
list|get|set|unset` addresses those keys by dotted name (`client.theme`)
through one registry in `internal/commands/config.go` that owns each key's
default, accessor, and validator: an unknown key or an invalid value is an
error naming what is accepted and leaves the file untouched, and `set`/`unset`
resolve only the key they are asked to touch, so a bad hand-edit is always
repairable from the CLI. `--config` moved from ~30 local declarations into
`commands.AddGlobalFlags`, joined there by persistent `--theme` and
`--no-nerd-font` with `SNATCHER_THEME` and `SNATCHER_NO_NERD_FONT` as their
environment companions; resolution is **flag > env > config > default**, a bad
value at any rung is an error naming that rung, and `config list` reports which
rung each value came from. The three list keys are load-bearing: `torrent list`
uses them whenever `--fields`, `--sort`, or `--reverse` is not passed, and an
explicitly passed flag wins even when it equals the flag's own default.
`theme`, `nerd_font`, and `poll_interval` are stored and validated but
store-only - nothing renders them until the TUI does.

---

## M5. `server bandwidth`

**Unblocks:** the TUI `t` (turtle) key and the status bar's limit readouts.

Ergonomic wrapper over session keys `server update` already exposes raw. No new
RPC work.

```
snatcher server bandwidth show [download|upload] [--alt]
snatcher server bandwidth update <download|upload> <speed> [--alt]
snatcher server bandwidth alt on|off|toggle
```

- `show` reports current rate, limit, whether the limit is enabled, and whether
  alt-speed is currently active
- `update` writes the matching `speed-limit-*` / `alt-speed-*` keys and their
  `*-enabled` companions together, so setting a limit turns it on
- `alt toggle` is read-then-write on `alt-speed-enabled`
- Speeds accept the same human units as `torrent update --download-limit`

**Done when:** alt-speed can be flipped and both limit pairs read/written
without touching `server update`'s raw flags.

---

## M6. `torrent label`

**Unblocks:** the TUI `L` labels editor.

Wrapper over `torrent update --label` / `--clear-labels`, with the
read-modify-write that add/remove need.

```
snatcher torrent label ls  <id|hash>
snatcher torrent label add <id|hash> <label...>
snatcher torrent label rm  <id|hash> <label...>
snatcher torrent label set <id|hash> <label...>
```

- `add`/`rm` read current labels, merge, write - and must accept a multi-torrent
  selector plus `--all`, like the other mutation commands
- `set` replaces wholesale; `set` with no labels is `--clear-labels`
- Labels are server-derived - sanitize on the `ls` plaintext path

**Done when:** labels can be edited incrementally without a read-modify-write
in the caller's shell.

---

## M7. Small list/verify flags

**Unblocks:** TUI filter groups ("error" bulk verify) and `list --top`-style
views.

- `snatcher torrent verify --all-with-errors` - client-side filter over the
  existing `Status`/`Error` fields, then one `VerifyTorrents` call. Confirm and
  name the count, like `remove` does.
- `snatcher torrent list --top=N` - truncate after sort; interacts with
  `--sort`/`--reverse`, so define it as "first N rows of the rendered order"

Both are pure transforms over `internal/transmission`; the cheapest items here.

---

## M8. Prowlarr / search

**Unblocks:** the entire TUI Prowlarr window (TUI M8). The only genuinely new
backend in this list - budget accordingly.

### 8a. Connection + config

- `Server` gains a type (`transmission` | `prowlarr`), defaulting to
  `transmission` so existing config files load unchanged
- `snatcher server add --type=prowlarr --url ... --api-key-stdin`; the API key
  lives in the keyring keyed by URL, exactly like passwords (so M1's fallback
  covers it too)
- `server list` shows type; `server ping` works for both; `dial()` splits by
  type and refuses a Prowlarr URL where a Transmission client is expected, with
  a clear error
- Resolution precedence mirrors the Transmission side (`--server` flag >
  `SNATCHER_SERVER` > default), but search commands resolve the default
  *Prowlarr* server

### 8b. `internal/prowlarr` package

Mirror the layering that already works for Transmission: transport (auth
header, timeouts) → typed client → domain types, with `ErrAuth` /
`ErrUnreachable` / `ErrServer` sentinels mapped the same way so the UI can tell
"can't reach it" from "it said no".

- Typed `Release` off Prowlarr's `ReleaseResource`: guid, title, indexer,
  indexerId, age, publishDate, size, files, grabs, seeders, leechers, protocol,
  categories, infoHash, downloadUrl, magnetUrl, infoUrl, commentUrl
- `Search(query, indexerIDs, categories, limit, offset)` → `GET /api/v1/search`
- `Grab(release)` → `POST /api/v1/search` **with the full ReleaseResource body**
  (this is why grab needs the release object, not just an id)
- `Indexers()` → `GET /api/v1/indexer`, `Categories()` →
  `GET /api/v1/indexer/categories` - the TUI's dropdowns need both
- `Download(release)` - fetch `downloadUrl` bytes for local `.torrent` writes
- Auth is `X-Api-Key`. Never log it, never let it reach `--json` output
- `docs/prowlarr.openapi.json` is the reference; `docs/torznab.xsd` for the
  underlying spec

### 8c. Commands

```
snatcher search <query> [--indexer ...] [--category ...] [--limit N] [--offset N] [--type search|tvsearch|moviesearch]
snatcher search grab <query> <n>          # or --guid <guid> --indexer <id>
snatcher search download <query> <n> [-o file]
snatcher search indexers
snatcher search categories
snatcher api --prowlarr <path> ...        # raw escape hatch, mirrors `api`
```

- Result columns: age, title, indexer, size, grabs, seeders, leechers. Titles
  and indexer names are hostile input - sanitize on plaintext, never on JSON
- **Result references are stateless**: `search grab "query" 3` re-runs the
  query and resolves index 3 from that result set. No cache file. This also
  satisfies the grab API, which wants the whole release object anyway. `--guid`
  + `--indexer` is the exact, no-re-run form for scripts
- `--limit`/`--offset` are the paging surface; the vision's `--pages N` becomes
  "fetch N pages and concatenate" on top of offset
- `search download` writes the `.torrent` locally - this is the CLI equivalent
  of the TUI's `w` key

**Decision on record:** `search grab` delegates to Prowlarr's grab endpoint,
which routes the release to *Prowlarr's* configured download client. That is
not necessarily the server snatcher is pointed at - `grab` means "tell Prowlarr
to grab it", not "add it to this server". Both the CLI help and the TUI's `a`
key must say so plainly. Adding directly from `downloadUrl` (snatcher hands it
to `torrent add` itself) stays available as a follow-up: `search download`
already needs that code path, so it is cheap to add later if the delegation
lands in the wrong place.

**Done when:** a search can be run, paged, inspected as JSON, grabbed, and
saved locally, with indexers and categories enumerable for the TUI dropdowns.

---

## M9. Pre-TUI hygiene

- `make release-check` green (`check` + `replace-check` + `vuln` + `race-test`)
- Man page reviewed end to end, not just coverage-gated
- README/help text reflects the new commands
- One pass over `docs/vision.md`'s command checklists so they match `.surface`
- Confirm `commands.dial()` has an exported seam for the TUI to inject a
  client (`docs/TUI-PLAN.md` "Launch model")

---

## Backlog (explicitly not pre-TUI)

- **`server doctor`** - connectivity/auth/config/port diagnostics, plus torrent
  health summaries (error states, low-ratio seeds, largest torrents). Wanted,
  but the least defined item in the vision and needs its own design pass.
- **Direct add from search** - `search add` that hands `downloadUrl` to
  `torrent add` on the snatcher server, bypassing Prowlarr's download client.
  See the M8 decision note.
- **Tracker mutation commands** - `torrent update --tracker/--clear-trackers`
  covers it today; a `torrent trackers add|remove` verb pair only if the TUI
  trackers tab wants direct editing.
