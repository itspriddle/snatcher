# AGENTS.md

Guidance for AI agents and humans working in this repo.

`snatcher` is a modern Go CLI/TUI for BitTorrent clients (Transmission first),
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
make lint          # golangci-lint run (needs golangci-lint v2.12.2)
make fmt           # gofmt -s -w .
make release-check # check + replace-check + vuln + race-test
```

Requirements: Go 1.26+, `golangci-lint` v2.12.2, `govulncheck`. `brew bundle`
installs the dev toolchain from the `Brewfile`. `direnv` (`.envrc`) puts
`./bin` on `PATH` after `make build`.

## Repository structure

```
cmd/snatcher/             # thin entrypoint: os.Exit(cli.Execute())
internal/cli/             # root command wiring + Execute()
internal/commands/        # one cobra command group per file (auth, list, …)
internal/torrent/         # vendor-neutral Backend interface + types + transforms
internal/transmission/    # Transmission adapter (the only importer of rpc)
internal/transmission/rpc # typed Transmission RPC client (Client→Protocol→Transport)
internal/config/          # non-secret config (named servers, JSON, XDG path)
internal/secrets/         # passwords in the OS keyring
internal/output/          # JSON / jq / human formatters + terminal sanitizer
internal/surface/         # CLI surface snapshot generator
internal/version/         # ldflags-injected build info
.surface                  # committed CLI surface snapshot (regression guard)
docs/                     # vision, plans, prompts, Transmission RPC specs
```

## Architecture

One rule drives the layering: **commands depend only on the vendor-neutral
`torrent.Backend` interface, never on a concrete client.** That seam is what
lets a future qbittorrent backend slot in without rewriting commands.

Layers, outermost to innermost:

- **`internal/cli`** — builds the root cobra command and wires subcommands.
- **`internal/commands`** — one command group per file. Depends only on
  `torrent.Backend` plus `config`/`secrets`/`output`. `commands.dial()` (in
  `auth.go`) is the *single* seam where a command crosses into the concrete
  backend: it loads config, reads the keyring password, and returns a
  `torrent.Backend`.
- **`internal/torrent`** — the neutral seam itself: the `Backend` interface, the
  `Torrent`/`Stats`/`ServerInfo` types, the `Selector` (id/hash/`--all` parsing
  for mutation commands), and the in-memory list transforms (filter/search/sort).
  Backend errors are mapped to neutral sentinels `ErrAuth` / `ErrUnreachable`.
- **`internal/transmission`** — adapts `rpc` types to neutral `torrent` types
  and maps rpc errors to the sentinels. It is the **only** package that imports
  `rpc`.
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
`rpc.ErrUnsupported`, mapped to `torrent.ErrUnsupported`. A server that reports no
`rpc-version` at all (pre-1.50, or not Transmission) is left to fail naturally
rather than mislabeled — every real 2.x/3.x server does report it, so the
realistic "too old" case is still caught.
The check is stateless — re-run every process, nothing cached to disk — so a
server up/downgrade is picked up immediately. The integer `rpc-version` (present
since Transmission 1.50) is the gate signal; `rpc-version-semver` only exists from
4.0.0 and the CSRF version header only from 4.1.0, so neither can gate the 4.0
boundary alone.

**Output contract.** Every command renders in one of three modes, fixed
precedence: **`--jq` → `--json` → plaintext** (`internal/output`). The neutral
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
with a `url` and `username`) plus a `default_server` alias; legacy single-server
files are migrated on read. The config package no longer reads env vars. Server
selection precedence (`--server` flag > `SNATCHER_SERVER` env > `default_server`)
is resolved at the command layer by `dial`/`config.Resolve`, not in the config
package. The password lives only in the OS keyring (`internal/secrets`), keyed by
server URL — never on disk.

`config.json` is trusted, operator-owned input. The `validateURL` userinfo guard
(rejecting embedded `user:pass@` credentials) applies only at `auth set` time;
URLs loaded from a legacy or hand-edited config are used as-is, with no
load-time re-validation.

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
