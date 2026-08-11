# snatcher

A modern, scriptable CLI for the Transmission BitTorrent client.

`snatcher` talks to a Transmission server over its RPC interface and exposes
every operation as a clean, composable command. Output is built for both humans
(aligned tables on a terminal) and scripts (`--json` and `gh`-style `--jq`
filters), and your server password never touches disk — it lives in the OS
keyring.

> **Status:** alpha. The CLI is functional and stable; the TUI and Prowlarr
> search integration described in [`docs/vision.md`](docs/vision.md) are on the
> roadmap.

## Highlights

- **Transmission protocol auto-detection.** Requires **Transmission 4.0+** and
  speaks both the legacy bespoke RPC dialect (Transmission 4.0) and JSON-RPC 2.0
  (≥ 4.1.0), chosen automatically from a CSRF handshake. Older servers are
  refused with a clear unsupported-version error.
- **Three output modes, fixed precedence:** `--jq` → `--json` → plaintext. JSON
  field names are a stable scripting contract.
- **Safe by default.** Non-secret config is JSON under XDG; the password is
  stored only in the OS keyring (macOS Keychain / Linux Secret Service), keyed
  by server URL. Server-derived strings are sanitized of terminal escape
  sequences on human output.
- **Power-user escape hatch.** `snatcher api` issues arbitrary RPC methods,
  modeled on `gh api`.

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
| `server add\|list\|default\|show\|ping\|remove` | Register, list, and verify named servers |
| `server stats` | Session statistics: counts, speeds, transfer totals |
| `server settings [get\|set\|reset]` | View and change server (session) settings |
| `server port get\|set\|test` | View, change, or test the peer/listening port |
| `torrent add` | Add torrents from files, URLs, or magnet links |
| `torrent list` (`ls`) | List torrents, with filter/search/sort/columns |
| `torrent info <id\|hash>` | Show every field for one torrent |
| `torrent start\|stop\|verify\|reannounce` | Act on torrents by id, hash, or `--all` (`stop` is also `pause`) |
| `torrent queue top\|up\|down\|bottom` | Reorder torrents in the queue |
| `torrent move` (`mv`) `<id\|hash> <location>` | Change a torrent's download location |
| `torrent remove` (`rm`) | Remove torrents (and optionally their data) |
| `api <method>` | Issue an arbitrary Transmission RPC method |
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
  named aliases (each with a `url` and `username`) plus a `default_server`
  alias. Register servers with `server add --name <alias>`; an omitted `--name`
  defaults to the URL host. Legacy single-server configs are migrated on read.
  The config file is trusted, operator-owned input.
- **Password (secret):** stored only in the OS keyring under the service name
  `snatcher`, keyed by server URL. Never written to disk.

Every command that connects to a server accepts `--server <alias>` to pick one
of your configured servers; `snatcher server default <alias>` sets the one used
when `--server` is omitted. Selection precedence is `--server` flag >
`SNATCHER_SERVER` env var > `default_server`.

Useful environment variables: `SNATCHER_SERVER`, `NO_COLOR`, `XDG_CONFIG_HOME`.

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

# Read one setting (bare value, script-friendly)
snatcher server settings get peer-port

# Set the download speed limit to 500 KB/s and enable it
snatcher server settings set speed-limit-down 500 speed-limit-down-enabled true

# Raw RPC: read the whole session, pick one field with jq
snatcher api session-get --jq '.arguments.version'
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

- TUI (windowed layout: torrents, settings, search)
- Prowlarr / Torznab search integration

See [`docs/vision.md`](docs/vision.md) for the full picture.

## License

[MIT](LICENSE)
