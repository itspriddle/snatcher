---
name: tui-library-stack
description: Locked library/dependency decisions for the snatcher Transmission TUI on Charm v2
metadata: 
  node_type: memory
  type: project
  originSessionId: b2009d4a-d2a1-4120-a174-147f6382b349
---

The snatcher TUI targets the **Charm v2 ecosystem** (Feb 2026 release): vanity import paths `charm.land/<lib>/v2`, Go 1.25+. v1 patterns do NOT compile against v2. Researched 2026-05-30; the user picked all the recommended (dependency-light, tagged-release-only) options.

**Core deps (all v2-ready, tagged):** bubbletea v2.0.6, lipgloss v2.0.3, bubbles v2.1.0, huh v2.0.3.

**Decisions:**
- **Torrent list** → core `bubbles/v2/table`, with sort/filter driven by the existing transforms in `internal/transmission`. NOT Evertras/bubble-table (its v2 is untagged `@main`; avoid the pre-release pin).
- **Mouse** → deferred for v1. Keyboard-first. `github.com/lrstanley/bubblezone/v2` is v2-ready and is the fast-follow when we add mouse.
- **Speed graphs** → hand-rolled block-glyph sparkline (`▁▂▃▄▅▆▇█`) from a rolling buffer. NOT ntcharts (v2 branch is untagged AND carries a `replace` directive → heaviest pre-release risk).
- **Overlays/modals/toasts** → Lip Gloss `Compositor` (`NewCompositor(layers...).Render()`, layers via `.X().Y().Z()`). Replaces bubbleup (v1-only) and bubbletea-overlay (v1-only, maintainer says use v2 compositing). No third-party overlay dep.
- **Lists/filter** → hand-rolled, not `bubbles/v2/list`: T1 declined it and T7 declined it again, with its sahilm/fuzzy dependency. The sidebar, the modals, and the command palette draw over `modalBox`; fuzzy matching is `internal/tui/fuzzy.go`, a table-tested scored subsequence. **Viewport** (detail pane) → `bubbles/v2/viewport`. **Help/keys** → `bubbles/v2/help` + `key`. **Forms** (settings/upload/labels) → `huh/v2`. **Spinner/progress** → `bubbles/v2/{spinner,progress}`. **Clipboard** (copy magnet/tracker URLs) → `tea.SetClipboard` (OSC52, built-in) plus a native write-only fallback in `internal/clipboard` (`pbcopy`, `wl-copy`/`xclip`/`xsel` via `os/exec`, off over SSH), because Terminal.app has no OSC 52; no clipboard dep (release builds are `CGO_ENABLED=0`).
- **Hand-roll, no lib:** window splits (lipgloss `JoinH/V`+`Place`+`Compositor`), tabs (no v2 tabs component exists, core PR #811 still open), themes default/dracula/nord (hex const map; `catppuccin/go` is v2-agnostic if Catppuccin is ever added), Nerd Font icons (const glyph map keyed by torrent state + existing `--no-nerd-font` ASCII fallback).

**v2 gotchas verified against source (will bite during implementation):**
- `huh` is NOT a drop-in `tea.Model` in v2: `Form.Update` returns `huh.Model` (assert to `*huh.Form`), `Form.View()` returns `string`. Wrap it inside our `View() tea.View`.
- Neither `bubbles/v2/table` nor `lipgloss/v2/table` sorts — sort the data, rebuild rows via `SetRows`.
- `key.Matches` is generic now (`Matches[Key fmt.Stringer]`) but call sites are unchanged (`KeyPressMsg` implements `String()`).
- `Model.View()` returns `tea.View` (build with `tea.NewView(s)`); set AltScreen/MouseMode/WindowTitle/Cursor as struct fields, not program options.
- Lip Gloss v2 has no root `AdaptiveColor`; detect bg once and use `lipgloss.LightDark(hasDark)`. `compat` subpkg has it if needed.

The `tui-design` skill (`.claude/skills/tui-design/`) and its `references/charm-v2.md` are the implementation guide.
