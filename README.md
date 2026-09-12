# snatcher

A modern, scriptable CLI for the Transmission BitTorrent client.

`snatcher` talks to a Transmission server over its RPC interface and exposes
every operation as a clean, composable command. It also searches
[Prowlarr](https://prowlarr.com)'s indexers, so finding a torrent and managing
it happen in the same tool. Output is built for both humans (aligned tables on
a terminal) and scripts (`--json` and `gh`-style `--jq` filters), and your
server password never goes in the config file — by default it lives in the OS
keyring.

> **Status:** alpha. The CLI is functional and stable. The TUI described in
> [`docs/vision.md`](docs/vision.md) is under construction: running `snatcher`
> with no arguments on a terminal starts it today, but it is a shell — it lays
> out the torrents and settings windows, routes focus between panes, and shows
> placeholder rows. Live server data arrives in a later release.

## The TUI

```sh
# Bare invocation on a terminal starts the TUI
snatcher

# Anything else keeps the command line: a subcommand, --help, or a pipe
snatcher | cat
```

Inside it, `?` lists every key the focused pane responds to, `tab` and
`ctrl-w hjkl` move between panes, `{` and `}` toggle the filter sidebar and
the detail pane, `,` opens settings, and `esc` unwinds one step at a time
without ever quitting — `q` or `ctrl-c` does that.

## Highlights

- **Transmission protocol auto-detection.** Requires **Transmission 4.0+** and
  speaks both the legacy bespoke RPC dialect (Transmission 4.0) and JSON-RPC 2.0
  (≥ 4.1.0), chosen automatically from a CSRF handshake. Older servers are
  refused with a clear unsupported-version error.
- **Three output modes, fixed precedence:** `--jq` → `--json` → plaintext. JSON
  field names are a stable scripting contract.
- **Safe by default.** Non-secret config is JSON under XDG; the password is
  kept in a secret store keyed by server URL — the OS keyring (macOS Keychain /
  Linux Secret Service) unless you explicitly opt in to the plaintext file
  store. Server-derived strings are sanitized of terminal escape sequences on
  human output.
- **Prowlarr search.** Register a Prowlarr instance beside your Transmission
  servers (`server add --type prowlarr`) and search its indexers from the
  command line — filtered by indexer and category, paged, rendered as a table
  or as JSON. Hand a release back to Prowlarr to grab, or write its `.torrent`
  locally. The API key lives in the same secret store as a password, and never
  reaches stdout, a log line, or an error message.
- **Power-user escape hatch.** `snatcher api` issues arbitrary RPC methods,
  modeled on `gh api`; `snatcher api --prowlarr <path>` does the same against
  Prowlarr's REST API.

## Install

### From source

Requires **Go 1.26+**.

```sh
go install github.com/itspriddle/snatcher/cmd/snatcher@latest
```

Or clone and build with version metadata baked in:

```sh
git clone https://github.com/itspriddle/snatcher.git
cd snatcher
make build        # produces ./bin/snatcher
```

### Release binaries

Prebuilt `tar.gz` archives for macOS and Linux (amd64/arm64) are attached to
[GitHub Releases](https://github.com/itspriddle/snatcher/releases). Each archive
bundles the binary, `LICENSE`, and the `snatcher(1)` man page.

## Quick start

```sh
# Point snatcher at your Transmission RPC endpoint (prompts for a password)
snatcher server add --url http://localhost:9091/transmission/rpc --username admin

# Confirm the connection works
snatcher server ping

# List your torrents
snatcher torrent list
```

## Commands

| Command | Description |
| --- | --- |
| `server add\|list\|default\|ping\|remove` | Register, list, and verify named servers |
| `server show` `[alias]` | Connection settings, server version, and every session setting |
| `server update` | Change session settings, one typed flag per setting |
| `server reset <key>...` | Restore settings to their Transmission defaults |
| `server stats` | Session statistics: counts, speeds, transfer totals |
| `server port get\|set\|test` | View, change, or test the peer/listening port |
| `server bandwidth show\|update\|alt` | View or change speed limits and the alt-speed switch |
| `server free-space [path]` | Free space on the server's download disk |
| `torrent add` | Add torrents from files, URLs, or magnet links |
| `torrent list` (`ls`) | List torrents, with filter/search/sort/columns |
| `torrent show` (`info`) `<id\|hash>` | Show every field for one torrent |
| `torrent files\|peers\|trackers <id\|hash>` | One torrent's files, connected peers, or trackers |
| `torrent files update <id\|hash> <index>...` | Set file priority and wanted/unwanted |
| `torrent start\|stop\|verify\|reannounce` | Act on torrents by id, hash, or `--all` (`stop` is also `pause`) |
| `torrent update` | Set torrent fields: limits, priority, labels, trackers, and more |
| `torrent label list\|add\|remove\|set` | Manage torrent labels |
| `torrent queue top\|up\|down\|bottom` | Reorder torrents in the queue |
| `torrent move` (`mv`) `<id\|hash> <location>` | Change a torrent's download location |
| `torrent rename <id\|hash> <path> <new-name>` | Rename a file, directory, or the torrent itself |
| `torrent remove` (`rm`) | Remove torrents (and optionally their data) |
| `config list\|get\|set\|unset` | snatcher's own client settings (theme, icons, list defaults) |
| `search <query>` | Search a Prowlarr instance's indexers |
| `search grab\|download` | Hand a result to Prowlarr's download client, or write its `.torrent` here |
| `search indexers\|categories` | List the indexer and torznab category ids the filters take |
| `api <method>` | Issue an arbitrary Transmission RPC method |
| `api --prowlarr <path>` | Issue an arbitrary Prowlarr REST call |
| `version` | Print version information |
| `completion` | Generate shell completion (bash, zsh, fish, PowerShell) |

Run `snatcher <command> --help` for full details, or read the bundled man page:
`man snatcher`.

## Output modes

Every command that produces structured output renders in one of three modes,
with fixed precedence:

```sh
# Plaintext: aligned table on a terminal; tab-separated and header-less when
# piped or when NO_COLOR is set (a stable scripting contract).
snatcher torrent list

# JSON: the full structured object.
snatcher torrent list --json

# jq: filter the JSON through a jq expression (implies --json, highest priority).
snatcher torrent list --jq '.[] | select(.done < 100) | .name'
```

JSON / jq output is never run through the terminal sanitizer — it's a machine
contract consumers handle themselves.

## Configuration & secrets

snatcher can hold an inventory of named servers and target one per invocation.

- **Config (non-secret):** JSON at `$XDG_CONFIG_HOME/snatcher/config.json`
  (falls back to `~/.config/snatcher/config.json`), holding a `servers` map of
  named aliases (each with a `url`, a `username`, and an optional `type` of
  `transmission` or `prowlarr`) plus a `default_server` and a
  `default_prowlarr_server` — one default per type, so a Prowlarr instance
  never shadows your Transmission server. Register servers with
  `server add --name <alias>`; an omitted `--name` defaults to the URL host.
  Legacy single-server configs are migrated on read. The config file is
  trusted, operator-owned input.
- **Password (secret):** kept in a secret store chosen per server with
  `server add --store`. The default is `keyring` — the OS keyring under the
  service name `snatcher`, keyed by server URL, never written to disk. `file`
  is an opt-in **plaintext** store at `$XDG_STATE_HOME/snatcher/secrets.json`
  (mode 0600, 0700 directory) for machines with no OS keyring; it is less safe,
  and `keyring` never falls back to it. `none` stores nothing at all.
- **Prowlarr API key (secret):** the same three stores, keyed by the same
  server URL, written by `server add --type prowlarr` from a hidden prompt or
  `--api-key-stdin`.

Every command that connects to a server accepts `--server <alias>` to pick one
of your configured servers; `snatcher server default <alias>` sets the one used
when `--server` is omitted, inferring from the alias which of the two defaults
to move. Selection precedence is `--server` flag > `SNATCHER_SERVER` env var >
`default_server` (or `default_prowlarr_server` for the `search` commands).

On a headless box with no OS secret store reachable (no D-Bus session, so
nothing answers the Secret Service), read commands keep working and warn on
stderr; `server add` refuses to register a server whose typed password has
nowhere to go, and names the remedies — install or start a secret service, or
re-run with `--store=file` or `--store=none`. See `SECRETS` in
`man 1 snatcher`.

Useful environment variables: `SNATCHER_SERVER`, `SNATCHER_SECRET_STORE`
(`keyring` | `file` | `none`, overriding the per-server `secret_store`),
`SNATCHER_PASSWORD` and `SNATCHER_API_KEY` (read-only per-invocation overrides
that beat every store — the first applies to Transmission servers only, the
second to Prowlarr instances only), `NO_COLOR`, `XDG_CONFIG_HOME`,
`XDG_STATE_HOME`.

## Examples

```sh
# Seeding torrents, highest ratio first
snatcher torrent list --filter seeding --sort ratio --reverse

# Start three torrents by id, jumping the queue
snatcher torrent start 1 2 3 --now

# Pause everything
snatcher torrent stop --all

# Add a file and a magnet link, label them, and start immediately
snatcher torrent add ubuntu.iso.torrent 'magnet:?xt=urn:btih:...' \
    --label linux --start

# Show the connection, server version, and every session setting
snatcher server show

# Read one setting (script-friendly)
snatcher server show --jq '.settings[]|select(.key=="peer-port").value'

# Set the download speed limit to 500 KB/s and enable it
snatcher server update --speed-limit-down 500 --speed-limit-down-enabled

# Restore settings to their defaults
snatcher server reset peer-port encryption

# Raw RPC: read the whole session, pick one field with jq
snatcher api session-get --jq '.arguments.version'
```

### Search with Prowlarr

```sh
# Register a Prowlarr instance beside your Transmission server
# (prompts once, hidden, for the API key)
snatcher server add --name prowlarr --type prowlarr --url http://localhost:9696

# The indexer and category ids the filters take
snatcher search indexers
snatcher search categories

# Search two indexers and one category, three pages deep
snatcher search 'ubuntu 24.04' --indexer 3 --indexer 7 --category 2000 --pages 3

# Every result's number, guid, and indexer id
snatcher search 'ubuntu 24.04' --jq '.[] | {index, guid, indexer_id}'

# Hand one release to Prowlarr's own download client, named exactly by guid
# (grab means "tell Prowlarr to grab it", not "add it to this server")
snatcher search grab --guid 'abc123' --indexer 3

# Or write result 2's .torrent here and add it to this server yourself
snatcher search download 'ubuntu 24.04' 2 -o ubuntu.torrent
snatcher torrent add ubuntu.torrent
```

## Development

The inner loop is **edit → `make check` → fix → repeat → push.** `make check`
is the local CI gate (formatting, vet, lint, tests, CLI-surface and man-page
coverage, and `go mod tidy`) and must pass before every push.

```sh
make build         # build ./bin/snatcher with version ldflags
make check         # local CI gate
make test          # go test ./...
make lint          # golangci-lint run
make fmt           # gofmt -s -w .
make surface       # regenerate the committed .surface snapshot
make release-check # check + replace-check + govulncheck + race tests
```

Requires Go 1.26+, `golangci-lint`, and `govulncheck`; `brew bundle` installs
the toolchain from the `Brewfile`. See [`AGENTS.md`](AGENTS.md) for the
architecture and contribution guide, and [`docs/`](docs/) for the product
vision and Transmission RPC references.

## Roadmap

- TUI (windowed layout: torrents, Prowlarr search, settings)

See [`docs/vision.md`](docs/vision.md) for the full picture.

## License

[MIT](LICENSE)
