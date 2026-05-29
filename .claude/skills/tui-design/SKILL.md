---
name: tui-design
description: Create distinctive, production-grade terminal user interfaces in Go using Bubble Tea, Lip Gloss, Bubbles, and the broader Charm ecosystem. Use this skill when the user asks to build TUI applications, terminal components, CLI tools with interactive interfaces, or any terminal-based UI. Generates creative, polished Go code that avoids generic default-styled terminal apps while maintaining excellent performance.
---

This skill guides creation of distinctive, production-grade terminal user interfaces in Go that avoid the "default Lip Gloss demo" look. Implement real, working Go code with exceptional attention to aesthetic detail, responsive layout, and the unique strengths of the terminal medium.

The user provides TUI requirements: a component, full application, dashboard, interactive tool, or terminal interface to build. They may include context about the purpose, audience, data sources, or performance constraints.

## The Charm Ecosystem

**Target Charm v2.** As of 2026 the core libraries shipped v2 and moved to the
`charm.land/<lib>/v2` vanity import path (Go 1.25+). Write v2 code by default —
v1 patterns (`github.com/charmbracelet/*` imports, `View() string`,
`lipgloss.AdaptiveColor`, `tea.WithAltScreen()`) **will not compile** against v2.
The detailed v2 API and a v1→v2 migration cheat-sheet live in
[`references/charm-v2.md`](references/charm-v2.md) — read it before writing real code.

Know the tools and reach for the right one (v2 import paths shown):

- **Bubble Tea** (`charm.land/bubbletea/v2`): The Elm-architecture TUI framework. Models, messages, commands. This is the backbone — every interactive TUI starts here. In v2, `View()` returns a declarative `tea.View` struct.
- **Lip Gloss** (`charm.land/lipgloss/v2`): Styling engine. Borders, padding, margins, colors, alignment, bold/italic/faint. Think of it as CSS for the terminal. v2 colors are stdlib `image/color.Color`; `table`/`list`/`tree` are sub-packages.
- **Bubbles** (`charm.land/bubbles/v2`): Pre-built Bubble Tea components — text inputs, spinners, viewports, lists, tables, file pickers, progress bars, paginator, timers, stopwatches, help. Use these as foundations, then restyle aggressively.
- **Huh** (`charm.land/huh/v2`): Declarative forms. When you need structured user input (multi-field forms, surveys, wizards), Huh handles the boilerplate.
- **Log** (`charm.land/log/v2`): Styled, leveled logging that looks good in a terminal context.
- **Wish** (`charm.land/wish/v2`): SSH server for TUIs. When the app needs to be served over SSH — the v2 renderer is a big bandwidth win here.
- **Glamour** (`charm.land/glamour/v2`): Markdown rendering in the terminal.
- **Fang** (`charm.land/fang/v2`): CLI starter kit — wraps Cobra with styled help/errors, `--version`, manpages, and completions. Reach for it when the TUI ships as a CLI.
- **Ultraviolet** (`github.com/charmbracelet/ultraviolet`, pre-v1): the new cell-diffing "Cursed Renderer" engine that powers Bubble Tea v2 and Lip Gloss v2. You rarely import it directly, but it's why v2 is fast.
- **Colorprofile** (`github.com/charmbracelet/colorprofile`, pre-v1): terminal color-profile detection and downsampling, used by Lip Gloss's print functions.
- **Harmonica** (`github.com/charmbracelet/harmonica`, still pre-v1): Spring-based animation. Pure physics math with no Bubble Tea dependency, so it still works under v2. Smooth motion for values that interpolate naturally (progress, scrolling, transitions).
- **VHS** (`github.com/charmbracelet/vhs`, still pre-v1): Tape files for recording terminal GIFs. Useful for demos, not runtime.

**Reach beyond Charm when appropriate**: `golang.org/x/term` for terminal queries, raw size detection. Third-party ANSI art libraries for splash screens. Standard `os/exec` for shelling out. The Charm stack is the core, but don't limit yourself.

## Charm v2 at a Glance

v2 was deliberately reshaped to be easier to drive correctly (the Bubbles upgrade
guide literally says "point your LLM at it and let it go to town"). Lean into these:

- **Declarative `tea.View`.** `View()` returns a `tea.View` struct (built with `tea.NewView(s)`), and terminal state — alt-screen, window title, cursor, mouse mode, focus reporting, OS progress bar — is set as **fields on that struct**, not via imperative commands or `tea.With*` options. Describe the desired state; the renderer reconciles it.
- **Explicit message types.** `KeyMsg` and `MouseMsg` are interfaces; handle the concrete `tea.KeyPressMsg`/`tea.KeyReleaseMsg` and `tea.MouseClickMsg`/`MouseWheelMsg`/`MouseMotionMsg`. No flag-checking on a generic message.
- **Stdlib colors.** `lipgloss.Color("#hex")` returns `image/color.Color`; any `color.Color` works.
- **Light/dark is explicit.** No more `AdaptiveColor` — detect once with `lipgloss.HasDarkBackground(in, out)` and pick with `lipgloss.LightDark(hasDark)`.
- **Styles are pure values.** No global/per-style renderer; `Style.Render()` emits full-fidelity ANSI and downsampling happens at print time via `lipgloss.Println`/`Sprint`.
- **The Cursed Renderer** (Ultraviolet) diffs frames and writes only what changed — fast, and dramatically less I/O over SSH.

Full signatures, the `View` struct fields, removed options, and per-component
Bubbles changes are in [`references/charm-v2.md`](references/charm-v2.md).

## Design Thinking

Before writing code, understand the context and commit to a clear aesthetic direction:

- **Purpose**: What problem does this TUI solve? Who uses it? A developer tool has different needs than a sysadmin dashboard or a creative coding toy.
- **Tone**: Choose deliberately. Possibilities include: hacker-dense (information-rich, monochrome, compact), soft and modern (rounded borders, pastel accents, breathing room), retro terminal (amber/green phosphor, scanline feel, blocky), dashboard/mission-control (panels, charts, status grids), conversational (chat-like flow, generous whitespace), brutalist (raw, undecorated, maximally functional), luxury CLI (restrained palette, elegant type, whisper-quiet animation), playful/whimsical (emoji accents, color bursts, personality in copy). These are starting points — invent your own.
- **Constraints**: Terminal width/height variability, color support (true color vs 256 vs 16), SSH delivery, performance budgets, accessibility (screen reader compatibility, high contrast).
- **Differentiation**: What makes this TUI feel *crafted* rather than default? What's the moment of delight?

**CRITICAL**: The terminal is a constrained canvas — that constraint is a strength. A few well-chosen details (a distinctive border style, a single accent color used precisely, a subtle spinner in exactly the right place) create more impact than overloading every surface. Intentionality over intensity.

Then implement working Go code that is:
- Production-grade: compiles, runs, handles resize, handles edge cases
- Visually cohesive with a clear aesthetic point-of-view
- Performant: snappy input response, efficient rendering, no unnecessary redraws
- Well-structured: clean model/update/view separation, composable components

## TUI Aesthetics Guidelines

### Color & Theme

- **Commit to a palette.** Define your colors as `lipgloss.Color("#hex")` values up front (in v2 these are stdlib `image/color.Color`). 3–5 colors max for most apps: a background tone, a primary text color, an accent, a muted/secondary, and maybe a highlight or error state.
- **Handle light/dark explicitly.** v2 removed `AdaptiveColor` from the root package — detect the background once and pick colors with a closure. Never assume dark mode:
  ```go
  hasDark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
  ld := lipgloss.LightDark(hasDark)
  accent := ld(lipgloss.Color("#D75FEE"), lipgloss.Color("#D7FFAE")) // light, dark
  ```
  Inside a Bubble Tea program, prefer detecting the background through the runtime rather than blocking on `os.Stdin` at startup. `compat.AdaptiveColor` exists for quick migration but is discouraged for new code.
- **Dominant + accent beats uniform distribution.** One strong accent color used sparingly (active selection, key status, the one thing the user should look at) outperforms rainbow-ing every element.
- **True color is available but not universal.** Downsampling is automatic when you print through Lip Gloss (`lipgloss.Println`/`Sprint`), so a true-color palette degrades gracefully on its own. For per-profile control, use `lipgloss.Complete(profile)` (the v2 replacement for `CompleteColor`).

NEVER default to the Lip Gloss example purple (`#7571F9`) or the demo pink/magenta palette. These are instantly recognizable as "unthemed Charm app." Choose colors that serve your application's identity.

### Borders, Boxes & Spatial Composition

- **Lip Gloss border styles**: `NormalBorder`, `RoundedBorder`, `ThickBorder`, `DoubleBorder`, `HiddenBorder`, `BlockBorder` — plus you can define fully custom borders character-by-character. Use custom borders for distinctive framing.
- **Use `lipgloss.JoinHorizontal` and `JoinVertical`** for layout composition. Think in terms of composed blocks, not one giant string.
- **Whitespace is your most powerful tool.** Padding and margin in Lip Gloss are cheap. Use them generously. A component with `Padding(1, 3)` instantly feels more considered than `Padding(0, 1)`.
- **Compose asymmetrically when appropriate.** A sidebar at 30% width with a main content area at 70% is a starting point, not a rule. Off-center layouts, weighted proportions, and deliberate imbalance create visual interest.
- **Responsive layout is non-negotiable.** Use `tea.WindowSizeMsg` to adapt. Calculate column widths as ratios, set minimum widths, and degrade gracefully. A TUI that breaks at 80 columns is unfinished.
- **Think about the gutter.** The space between panels, between a list and its detail view, between a header and body — this negative space defines the rhythm of your layout.

### Typography & Text Treatment

Terminal "typography" is limited but far from powerless:

- **Bold, italic, faint, underline, strikethrough, reverse** — these are your type weights. Use them with the same intentionality a web designer uses font weights. Bold for emphasis, faint for de-emphasis, italic for metadata or secondary info.
- **Case and spacing as type design.** `UPPERCASE` with `letterSpacing` (manual space insertion between chars) creates a small-caps header feel. Consistent casing choices across your UI create typographic hierarchy.
- **Align text intentionally.** `lipgloss.Center`, `lipgloss.Right`, `lipgloss.Left` — but also consider when NOT centering. Left-aligned body text with a centered title creates hierarchy.
- **Tab stops and columns.** For data-heavy UIs, consistent column alignment with `lipgloss.PlaceHorizontal` creates order. Use `tabwriter` or manual padding for aligned columnar data.
- **Unicode as display type.** Characters like `│`, `─`, `┌`, `╭`, `▓`, `░`, `█`, `◆`, `●`, `→`, `⣿`, `⠿` and box-drawing sets are your extended character palette. A well-chosen bullet character, a distinctive separator, or a custom progress bar glyph made from block elements can define a TUI's personality.

### Animation & Motion

- **Spinners from Bubbles** are the simplest animation. Choose or customize one that fits the tone — `spinner.Dot` for minimal, `spinner.Globe` for playful, a custom frame set for unique identity.
- **Harmonica for physics-based interpolation.** When a value needs to animate smoothly — a scrolling position, a progress value, a panel width during resize — use `harmonica.NewSpring()` with tuned damping and velocity. This is what separates a polished TUI from a jumpy one. (Harmonica is still pre-v1 and has no Bubble Tea dependency, so it works unchanged under v2.)
- **Frame-rate-aware updates.** Use `tea.Tick` for animation loops, targeting ~30fps for smooth motion without burning CPU. 60fps is rarely necessary in a terminal context and wastes resources. Cap the program's render rate with `tea.WithFPS`.
- **OS-level progress.** For long-running work, surface progress to the terminal/taskbar by setting `view.ProgressBar` on the returned `tea.View` — distinct from an in-content `bubbles/progress` bar.
- **Transitions between states.** When switching views (list → detail, form → confirmation), even a brief fade-through-muted or a slide effect using Harmonica creates continuity. The absence of transition is jarring in an otherwise polished app.
- **Don't animate everything.** One or two well-placed animations (a spinner during loading, a smooth scroll, a gentle highlight pulse on new items) beat decorating every interaction. Motion should communicate state, not perform.

### Status, Feedback & Information Density

- **Status bars are identity.** A bottom-of-screen status bar with mode, context, and key hints is both functional and a design surface. Style it distinctly from the content area — inverted colors, a different background, a subtle border-top.
- **Help text with `bubbles/help`.** Use the help bubble for key bindings, but restyle it to match your theme. Default help styling is recognizable; override it.
- **Progress indication has range.** `bubbles/progress` is a starting point. Customize the filled/empty characters, the color ramp, the percentage display. Or build a custom indicator from block characters for something truly unique.
- **Information density is a spectrum.** Developer tools can be dense (think `htop`, `k9s`). User-facing tools should breathe. Match density to audience and purpose.
- **Error and success states deserve design attention.** A red-tinted border shift on error, a brief green flash on success — these micro-states build trust and polish.

## Performance & Architecture

TUI performance is UX. A laggy terminal app feels broken in a way a slightly slow web page does not.

### Bubble Tea Architecture

- **`View()` returns a `tea.View`, not a string.** Build it with `tea.NewView(content)` and set terminal state declaratively as fields: `v.AltScreen = true`, `v.WindowTitle = "…"`, `v.MouseMode = tea.MouseModeCellMotion`, `v.Cursor = …`. There are no `tea.WithAltScreen()` / `tea.WithMouseCellMotion()` program options and no imperative `EnterAltScreen()`/`SetWindowTitle()` commands in v2 — set the field instead.
- **Keep `Update` fast.** Never block in `Update`. All I/O (file reads, network calls, subprocess execution) goes in `tea.Cmd` functions. `Update` should be pure state transitions.
- **Use `tea.Batch` for concurrent commands**, `tea.Sequence` (renamed from `Sequentially`) for ordered ones. Multiple I/O operations should launch concurrently, not sequentially.
- **Model composition for complex UIs.** Each panel, widget, or mode should be its own sub-model with its own `Update` and `View`. The root model orchestrates focus and message routing.
- **Message routing discipline.** Define clear message types and switch on the concrete v2 types (`tea.KeyPressMsg`, `tea.MouseClickMsg`, `tea.WindowSizeMsg`, `tea.FocusMsg`/`BlurMsg`). Avoid string-matching on whole messages; match keys via `msg.String()` (note `" "` is now `"space"`). Each component handles its own message types and passes others through.

### Rendering Performance

- **`View()` is called often — keep it allocation-light.** Pre-compute styles. Avoid building large strings with repeated concatenation; use `strings.Builder`. The v2 Cursed Renderer already diffs frames and skips writing unchanged cells, so manual sub-component caching is a smaller win than in v1 — still worth caching genuinely expensive string-building, but don't over-engineer it.
- **Lip Gloss styles are immutable value types** — create them once and reuse. Define a `styles` struct or package-level vars initialized at startup, not re-created each frame.
- **Viewport for scrollable content.** Don't render 10,000 lines and let the terminal scroll. Use `bubbles/viewport` to render only the visible window. For lists, `bubbles/list` already handles this.
- **Debounce resize.** `tea.WindowSizeMsg` can fire rapidly during drag-resize. Debounce layout recalculation, or at minimum ensure it's cheap.
- **Profile your render loop** if anything feels sluggish. `go tool pprof` is your friend. Common culprits: re-rendering unchanged panels, excessive string allocation in `View()`, Lip Gloss style re-creation per frame.

### Go Idioms

- **Embed `tea.Model` interfaces** — compose models, don't inherit.
- **Enable full-screen and mouse on the `View`**, not via program options: set `v.AltScreen = true` for full-screen apps and `v.MouseMode = tea.MouseModeCellMotion` (or `MouseModeAllMotion`) for mouse support.
- **Graceful degradation.** Check terminal capabilities. Fall back to simpler rendering on constrained terminals.
- **Context and cancellation.** Long-running commands should respect `context.Context` for clean shutdown.
- **Error handling in commands.** Every `tea.Cmd` that does I/O should return an error message type. The model should handle error states visually — not just `log.Fatal`.

## Anti-Patterns

NEVER produce a TUI that:
- Uses stale v1 APIs against v2: `github.com/charmbracelet/*` imports, a string-returning `View()`, `lipgloss.AdaptiveColor`/`CompleteColor` in new code, or `tea.WithAltScreen()`/`tea.WithMouseCellMotion()` — none of these compile under v2
- Uses the default Lip Gloss demo color palette (the purple/pink/magenta combination is the "AI-generated TUI" fingerprint — equivalent to a purple gradient on white in web design)
- Leaves Bubbles components unstyled (default list, default text input, default spinner without color/style customization)
- Ignores terminal resize (hardcoded widths, layouts that clip or overflow)
- Blocks in `Update` (synchronous file I/O, network calls, sleeps)
- Renders the full dataset when only a window is visible
- Uses `fmt.Println` instead of the Bubble Tea render cycle (or for one-off styled output, `lipgloss.Println` so colors downsample)
- Mixes raw ANSI escape codes with Lip Gloss styling (let Lip Gloss own the styling layer)
- Ignores light/dark terminal detection (use `lipgloss.LightDark`, not assumptions)

## Fit & Finish Checklist

Before considering a TUI complete:

- [ ] **Resize**: Drag the terminal window. Does everything reflow? Any clipping?
- [ ] **Color fallback**: Test with `TERM=xterm-256color` and `TERM=xterm`. Does it degrade? (Printing through `lipgloss.Print*` downsamples automatically.)
- [ ] **Light/dark**: Test on both background colors. Colors chosen via `lipgloss.LightDark` rather than assuming dark mode?
- [ ] **Empty states**: What does the UI show with zero data? Loading state? Error state?
- [ ] **Key bindings**: Are they discoverable? Help view available? Consistent with conventions (q/esc to quit, ↑↓ or j/k to navigate, enter to select)?
- [ ] **Focus management**: When there are multiple interactive regions, is it clear which is active? Visual focus indicator?
- [ ] **Startup time**: Does the app launch instantly and populate data async? Or does it block on first load?
- [ ] **Quit cleanly**: `tea.Quit` properly, restore terminal state, no garbage on exit
- [ ] **Edge text**: Very long strings, very short strings, Unicode, emoji — does the layout hold?
- [ ] **One detail of delight**: A custom spinner, a branded header, a subtle color transition, a thoughtful empty-state message — something that says "someone cared"

## Example Structure

A well-organized Charm TUI project:

```
myapp/
├── go.mod               # charm.land/*/v2 deps; go 1.25+
├── main.go              # tea.NewProgram, options, launch
├── model.go             # Root model; Update + View() tea.View
├── styles.go            # All Lip Gloss styles, colors, theme constants
├── keys.go              # Key bindings (key.Binding definitions)
├── components/
│   ├── sidebar/         # Sub-model with own Update/View
│   ├── detail/
│   ├── statusbar/
│   └── header/
├── messages/             # Shared message types
└── commands/             # tea.Cmd functions (I/O, async work)
```

Separate concerns. Styles in one place means theme changes are trivial. Components as sub-models means testability and reuse. Note the v2 module paths and Go 1.25 requirement in `go.mod`.

Remember: The terminal is an intimate medium. Users are close to the metal, they notice details, and they appreciate craft. A TUI that feels *designed* — not just functional — earns trust and loyalty in a way that generic tools never will.
