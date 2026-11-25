# AGENTS.md

Guidance for AI agents and humans working in this repo.

`snatcher` is a modern Go CLI/TUI for the Transmission BitTorrent client,
built on the Charm Land v2 libraries. See `docs/vision.md` for the product
vision and the target CLI surface.

## Development loop

Inner loop: **edit → `make check` → fix → repeat → push.**

`make check` is the local CI gate and must pass green before every push. It
runs `fmt-check`, `vet`, `lint`, `test`, `check-surface`, `check-man`, and
`tidy-check` — the same checks CI enforces.

```sh
make build         # build bin/snatcher with version ldflags
make check         # local CI gate (run before pushing)
make test          # go test ./...
make lint          # golangci-lint run (needs golangci-lint v2.15.0)
make fmt           # gofmt -s -w .
make release-check # check + replace-check + vuln + race-test
```

Requirements: Go 1.27+, `golangci-lint` v2.15.0, `govulncheck`. `brew bundle`
installs the dev toolchain from the `Brewfile`. `direnv` (`.envrc`) puts
`./bin` on `PATH` after `make build`.

## Repository structure

```
cmd/snatcher/             # thin entrypoint: os.Exit(cli.Execute())
internal/cli/             # root command wiring + Execute()
internal/commands/        # one cobra command group per file (server, torrent, …)
internal/transmission/    # domain model (types, Selector, transforms, sentinels) + concrete Client
internal/transmission/rpc # typed Transmission RPC client (Client→Protocol→Transport)
internal/config/          # non-secret config (named servers, client settings, JSON, XDG path)
internal/theme/           # .theme file parser, embedded builtins, user theme registry
internal/secrets/         # server passwords (OS keyring, or file store)
internal/clipboard/       # native clipboard writer (pbcopy/wl-copy/xclip/xsel) for TUI yanks
internal/output/          # JSON / jq / human formatters + terminal sanitizer
internal/tui/             # terminal UI: layout, focus router, keymap registry
internal/surface/         # CLI surface snapshot generator
internal/version/         # ldflags-injected build info
.surface                  # committed CLI surface snapshot (regression guard)
docs/                     # vision, plans, prompts, Transmission RPC specs
```

## Architecture

snatcher talks to exactly one backend: **Transmission RPC**.
`internal/transmission` owns the domain model and the concrete `Client`;
commands talk to that client directly.

Layers, outermost to innermost:

- **`internal/cli`** — builds the root cobra command and wires subcommands.
- **`internal/commands`** — one command group per file. Depends on
  `*transmission.Client` plus `config`/`secrets`/`output`. `commands.dial()`
  (in `server_registry.go`) is where a command *constructs* a client: it
  resolves an alias, reads that server's password from the store, and returns
  the client. That same file holds the named-server registry subcommands
  (`server add`/`list`/`default`/`show`/`ping`/`remove`). The power-user `api`
  escape hatch (`api.go`) calls `client.CallAPI(...)` directly with an
  arbitrary RPC method. `tui.go` holds the seams `internal/cli` hands the TUI:
  `Dialer`, whose `DialResult` carries the resolved alias and any
  unavailable-store warning (captured, never written to stderr under the alt
  screen); `Servers`, the configured aliases for the server picker;
  `ResolveClientSettings`; and `ClientConfigStore`, which reads and writes client settings through the same
  functions `config list` and `config set` use.
- **`internal/tui`** — the terminal UI, launched by `internal/cli` when
  `snatcher` is run bare on a terminal. It depends on `transmission`,
  `output`, and `theme` and **never imports `commands`**: everything it needs
  arrives through `tui.Deps`, built from the `commands.Dialer` /
  `commands.Servers` / `commands.ResolveClientSettings` /
  `commands.ClientConfigStore` seam — the picker's list as `Deps.Servers`,
  the native clipboard as `Deps.Clipboard` (nil over SSH), the config store as
  `Deps.Config`, the theme lookup (`commands.ThemeLookup`) as `Deps.Themes`,
  the `tui.ClientConfig` store the settings window reads and
  writes client settings through (adapted in `internal/cli`, since neither
  package imports the other).
  It is handed the *dialer*, not a dialed client, so switching servers
  mid-session needs no new seam, and `tui.Run` dials before entering the alt
  screen so a configuration error reaches the terminal. Live data rides one
  refresh spine (`poll.go`): a tick-driven poll whose superseded ticks and
  replies are dropped by sequence and generation rather than canceled. Mock
  torrents exist only in tests, where a non-nil `Deps.Torrents` disables
  polling. Mutations ride the same spine: a verb's RPC runs as a `Cmd`, and its
  reply refetches at once (queued behind a poll already in flight) rather than
  editing rows optimistically. The detail pane's `TorrentDetail` rides the same
  spine while the pane is open (`detail_fetch.go`): debounced on cursor moves,
  and dropped by torrent ID when the cursor has moved on. The list's sort,
  direction, and columns are read from and written to `client.list_*` through
  `Deps.Config`, and `columns.go` mirrors `internal/commands/list.go`'s field
  keys under a pinning test rather than importing them. `detail_overview.go` and
  `detail_table.go` mirror `torrent show`'s fields and the
  `files`/`peers`/`trackers` registries the same way, and the files tab consumes
  the same generic selection model (`selection.go`) as the torrent list. The
  keymap registry in `keymap.go` is this package's `.surface`: every key the UI
  responds to is one row of it, components are constructed from those rows
  rather than from their own defaults, and the collision test asserts no key
  resolves twice in any focus context a user can be in. Adding a binding means
  adding a registry row, not a `switch` case. The `:` command palette
  (`palette.go`) is fed by that registry plus `unboundActions`, the table of
  actions no key is bound to, filtered to the live pane stack less
  `paletteHidden`; drift tests fail on an action that is neither listed nor
  hidden. Adding an unbound action means adding a table row. The mouse adds
  no actions: `mouse.go` routes clicks and the wheel in the same precedence
  shape as `handleKey`, and `hit.go` resolves a point from `layout()` plus the
  zero-width row markers (`rowmark.go`) the tables carry, then calls the same
  transitions the keys do.
- **`internal/transmission`** — the domain model and the client. It owns the
  `Torrent`/`Stats`/`ServerInfo` types (aliased to the `rpc` wire structs), the
  `Selector` (id/hash/`--all` parsing for mutation commands), the in-memory list
  transforms (filter/search/sort), the session settings registry
  (`SettingDefs`: every setting's key, type, category, default, and accessor,
  shared by `server show`/`update`/`reset` and the TUI settings window), the
  error sentinels (`ErrAuth` /
  `ErrUnreachable` / `ErrServer` / `ErrUnsupported`), and the concrete `Client`
  that wraps `*rpc.Client` and maps rpc errors to those sentinels. It is the
  **only** package that imports `rpc`.
- **`internal/transmission/rpc`** — typed Transmission client. `Transport` POSTs
  JSON and transparently handles the `X-Transmission-Session-Id` CSRF 409 retry
  and Basic Auth. `Client` probes once, then dispatches through a `Protocol`.

**RPC protocol auto-detection.** Transmission speaks two wire schemes. On first
use `rpc.Client` runs the CSRF handshake, reads `rpc-version-semver` off the 409,
and picks JSON-RPC 2.0 (servers ≥ 4.1.0, semver major ≥ 6) or the legacy bespoke
scheme (≤ 4.0.6). Each is a `Protocol` implementation owning its envelope, method
names (snake_case vs kebab-case), and field decoding, converting to shared
canonical structs. RPC specs live in `docs/transmission/`.

**Minimum server version.** The same one-shot detection enforces a
**Transmission 4.0+** floor (`rpc-version` ≥ 17). A JSON-RPC server is necessarily
≥ 4.1.0 and clears it for free; a bespoke server is checked against the integer
`rpc-version` in the probe's own `session-get` body (no extra round-trip). A
server that positively reports an `rpc-version` below 17 is refused with
`rpc.ErrUnsupported`, mapped to `transmission.ErrUnsupported`. A server that reports no
`rpc-version` at all (pre-1.50, or not Transmission) is left to fail naturally
rather than mislabeled — every real 2.x/3.x server does report it, so the
realistic "too old" case is still caught.
The check is stateless — re-run every process, nothing cached to disk — so a
server up/downgrade is picked up immediately. The integer `rpc-version` (present
since Transmission 1.50) is the gate signal; `rpc-version-semver` only exists from
4.0.0 and the CSRF version header only from 4.1.0, so neither can gate the 4.0
boundary alone.

**Output contract.** Every command renders in one of three modes, fixed
precedence: **`--jq` → `--json` → plaintext** (`internal/output`). The domain
types' `json` tags are a stable scripting surface — treat them as an API.
Server-derived strings (names, labels, errors, download dirs) are untrusted and
MUST pass through `output.SanitizeTerminal` on human/plaintext paths, but NEVER
on `--json`/`--jq` output (a structured contract consumers handle themselves).
`list` plaintext is a TTY-aligned table on a terminal, and tab-separated,
header-less rows when piped or when `NO_COLOR` is set.

**Config vs secrets.** Non-secret config is JSON at
`$XDG_CONFIG_HOME/snatcher/config.json`, read and written directly by
`internal/config` (no viper — it lowercases map keys, which would corrupt
case-sensitive aliases). The file holds a `servers` map of named aliases (each
with a `url`, a `username`, and an optional `secret_store`), a
`default_server`, and a `client` block of snatcher's own settings — the only
schema the config package knows. The config package does not read env vars.
Server selection precedence (`--server` flag > `SNATCHER_SERVER` env >
`default_server`) is resolved at the command layer by `dial` over
`config.Resolve`, not in the config package; `server show` and `server remove`
resolve their default alias the same way.

The secret — the server's Transmission password, one per server — lives only
in a secret store (`internal/secrets`: the OS keyring by default, or an opt-in
plaintext file), keyed by server URL, never in `config.json`.
`SNATCHER_PASSWORD` is a per-invocation override for the server being dialed.

`config.json` is trusted, operator-owned input, and so are the `themes/*.theme`
files beside it (`internal/theme`, a leaf package both `commands` and `tui`
import): a broken one is skipped with a warning unless it is the selected
theme. The `validateURL` userinfo guard (rejecting embedded `user:pass@`
credentials) applies only at `server add` time; URLs loaded from a hand-edited
config are used as-is, with no load-time re-validation.

Adding a command: create a file in `internal/commands`, register it in
`internal/cli/root.go`, then regenerate `.surface` (see below).

## CLI surface snapshot

`.surface` is a deterministic, sorted snapshot of every command, argument, and
flag, generated by `internal/surface`. CI fails (`make check-surface`) if the
code's surface drifts from the committed file.

**After any change to a command, argument, or flag, regenerate and commit it:**

```sh
make surface       # rewrites .surface from the current command tree
git add .surface   # commit alongside the code change
```

This makes every CLI change visible in code review.

## Man page

The man page `man/man1/snatcher.1` is hand-written (bundled into release archives
by `.goreleaser.yaml`) and must document every command and flag. CI fails
(`make check-man`) when a command or flag exists in the code but is not mentioned
in the man page — a forward coverage guard, not a generator.

**After adding or renaming a command or flag, also document it in
`man/man1/snatcher.1`.** If something is intentionally left undocumented, add it
to `manExceptions` in `internal/cli/man_test.go` with a comment explaining why.
