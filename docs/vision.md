# snatcher - vision

Modern golang based CLI/TUI for BitTorrent clients. Built on Charm Land v2
libs.

Prioritize Transmission for v1. Build modularly to support other clients, such
as qbittorrent, in the future. Support different API versions.

MacOS/Linux builds only.

Modern go toolchain and test suite.

AI first development flow with good guardrails and careful planning.

Anything TUI can do can be done with the CLI. Smart human outputs, ability to
script easily (i.e. `--jq ...` filters like gh CLI).

Prowlarr integration (torznab spec).

---

CLI from alpha:

```
$ snatcher --help
Transmission BitTorrent CLI client

Usage:
  snatcher [flags]
  snatcher [command]

Available Commands:
  auth        Manage server authentication
  completion  Generate the autocompletion script for the specified shell
  help        Help about any command
  info        Show torrent details
  list        List torrents
  remove      Remove torrents
  settings    Manage server settings
  speed       View or toggle alt-speed limits
  start       Start or resume torrents
  stats       Show session statistics
  stop        Stop (pause) torrents
  upload      Upload torrents from files, URLs, or magnet links
  verify      Verify local torrent data
  version     Print version information

Flags:
      --config string   config file path
  -h, --help            help for snatcher
      --no-nerd-font    use ASCII icons instead of Nerd Font glyphs
      --theme string    color theme (default, dracula, nord)

Use "snatcher [command] --help" for more information about a command.
```
