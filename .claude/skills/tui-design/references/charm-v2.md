# Charm v2 API reference & v1 → v2 migration

Concrete API detail for the Charm v2 ecosystem. SKILL.md links here; load it when
writing real v2 code. Everything below is checked against upstream source
(`go.mod`, package source, and the official upgrade guides) as of May 2026.

When in doubt, defer to the upstream upgrade guides — they are authoritative and
were written to be machine-readable:

- Bubbles: <https://github.com/charmbracelet/bubbles/blob/main/UPGRADE_GUIDE_V2.md>
- Bubble Tea & Lip Gloss: see each repo's `README.md` / migration notes.

## Imports & versions

All three core libraries — and most of the ecosystem — moved off
`github.com/charmbracelet/*` to the **`charm.land/<lib>/v2`** vanity import path.
They require **Go 1.25+**.

| Library | Latest v2 | Import path |
|---------|-----------|-------------|
| Bubble Tea | v2.0.6 | `charm.land/bubbletea/v2` |
| Lip Gloss | v2.0.3 | `charm.land/lipgloss/v2` |
| Bubbles | v2.1.0 | `charm.land/bubbles/v2` |
| Huh | v2.0.3 | `charm.land/huh/v2` |
| Glamour | v2.0.0 | `charm.land/glamour/v2` |
| Log | v2.0.0 | `charm.land/log/v2` |
| Wish | v2.0.1 | `charm.land/wish/v2` |
| Fang | v2.0.1 | `charm.land/fang/v2` |

Still **pre-v1** (use as-is; not part of the v2 path rename):

| Library | Version | Import path | Notes |
|---------|---------|-------------|-------|
| Ultraviolet | v0.x | `github.com/charmbracelet/ultraviolet` | the new renderer engine; powers BT/LG v2 |
| Colorprofile | v0.4.x | `github.com/charmbracelet/colorprofile` | color downsampling |
| Harmonica | v0.2.0 (2022) | `github.com/charmbracelet/harmonica` | pure spring math; still works under v2 |
| VHS | v0.11.x | `github.com/charmbracelet/vhs` | tape/recording tool, not a runtime dep |
| Glow | v2.1.x | `github.com/charmbracelet/glow/v2` | an *application*, kept the github path |

Canonical import block for a v2 app:

```go
import (
    tea "charm.land/bubbletea/v2"
    "charm.land/lipgloss/v2"
    "charm.land/bubbles/v2/list"      // and /textinput, /viewport, /spinner, ...
)
```

---

## Bubble Tea v2

### Model interface — `View()` now returns a struct

```go
type Model interface {
    Init() Cmd
    Update(Msg) (Model, Cmd)
    View() View              // v1 returned string; v2 returns a tea.View
}

type Msg = uv.Event          // ultraviolet event union
type Cmd func() Msg
```

`Update` is unchanged. `View()` returns a **`tea.View`** built with `tea.NewView`.

### The `View` struct — declarative terminal state

This is the single biggest v2 change and the main reason v2 is easier to drive
correctly: terminal state that used to be set via imperative `tea.Cmd`s or
`tea.With*` program options is now **plain fields on the View you return**. Set
the state you want; the renderer reconciles it.

```go
type View struct {
    Content                   string                 // the rendered UI
    OnMouse                   func(msg MouseMsg) Cmd  // view-scoped mouse handler
    Cursor                    *Cursor                 // nil = hidden
    BackgroundColor           color.Color             // request terminal bg
    ForegroundColor           color.Color             // request terminal fg
    WindowTitle               string                  // OSC window/tab title
    ProgressBar               *ProgressBar            // OS/taskbar progress (OSC 9;4)
    AltScreen                 bool                    // was tea.WithAltScreen()
    ReportFocus               bool                    // was tea.WithReportFocus()
    DisableBracketedPasteMode bool                    // bracketed paste is ON by default
    MouseMode                 MouseMode               // was tea.WithMouseCellMotion()
    KeyboardEnhancements      KeyboardEnhancements    // Kitty keyboard protocol opt-in
}

func NewView(s string) View

type Cursor struct {
    Position             // embedded X, Y
    Color color.Color
    Shape CursorShape    // CursorBlock | CursorUnderline | CursorBar
    Blink bool
}
```

Typical `View()`:

```go
func (m model) View() tea.View {
    v := tea.NewView(m.render())   // build your styled string however you like
    v.AltScreen = true
    v.WindowTitle = "My App"
    v.MouseMode = tea.MouseModeCellMotion
    return v
}
```

```go
type MouseMode int
const (
    MouseModeNone MouseMode = iota
    MouseModeCellMotion   // motion events while a button is pressed
    MouseModeAllMotion    // all motion events
)
```

### Program creation & surviving options

```go
p := tea.NewProgram(initialModel /*, opts... */)
finalModel, err := p.Run()
```

Options that **still exist** (all `func(...) ProgramOption`):
`WithContext`, `WithOutput`, `WithInput`, `WithEnvironment`,
`WithoutSignalHandler`, `WithoutCatchPanics`, `WithoutSignals`,
`WithoutRenderer`, `WithFilter`, `WithFPS(int)`, `WithColorProfile`,
`WithWindowSize`.

Options/commands that were **removed** — set the corresponding `View` field instead:

| v1 | v2 |
|----|----|
| `tea.WithAltScreen()` | `view.AltScreen = true` |
| `tea.WithMouseCellMotion()` / `WithMouseAllMotion()` | `view.MouseMode = tea.MouseModeCellMotion` / `...AllMotion` |
| `tea.WithReportFocus()` | `view.ReportFocus = true` |
| `tea.EnterAltScreen()` / `ExitAltScreen()` | toggle `view.AltScreen` |
| `tea.ShowCursor()` / `HideCursor()` | set/clear `view.Cursor` |
| `tea.SetWindowTitle(s)` | `view.WindowTitle = s` |
| `tea.EnableMouseCellMotion()` etc. | `view.MouseMode` |

Still-current commands: `tea.Quit`, `tea.Batch`, `tea.Sequence` (renamed from
`Sequentially`), `tea.Tick`, `tea.Every`, `tea.Printf`.

### Keyboard — `KeyMsg` is now an interface

```go
// KeyMsg is an interface; the concrete types are:
//   tea.KeyPressMsg   tea.KeyReleaseMsg
type Key struct {
    Text        string  // printable text produced, if any
    Mod         KeyMod  // bitmask of modifiers
    Code        rune    // logical key (rune or Key* constant)
    ShiftedCode rune
    BaseCode    rune
    IsRepeat    bool
}
```

Match keys with `msg.String()` (note string forms changed — `" "` is now
`"space"`):

```go
case tea.KeyPressMsg:
    switch msg.String() {
    case "ctrl+c", "q":      return m, tea.Quit
    case "up", "k":          /* ... */
    case "enter", "space":   /* ... */
    }
```

Check modifiers via the bitmask: `msg.Mod.Contains(tea.ModCtrl)` (also
`tea.ModAlt`, `tea.ModShift`, …). Special-key constants: `tea.KeyEnter`,
`tea.KeyTab`, `tea.KeyEscape`, `tea.KeyUp/Down/Left/Right`, `tea.KeyHome`,
`tea.KeyEnd`, `tea.KeyF1`–`KeyF63`, etc.

Opt into the Kitty keyboard protocol (disambiguated keys, press/release events)
via `view.KeyboardEnhancements`; terminals report support with
`tea.KeyboardEnhancementsMsg`.

### Mouse — also interface-based

`MouseMsg` is an interface; concrete types are `tea.MouseClickMsg`,
`tea.MouseReleaseMsg`, `tea.MouseWheelMsg`, `tea.MouseMotionMsg`. Buttons:
`tea.MouseLeft/Middle/Right`, `tea.MouseWheelUp/Down/Left/Right`,
`tea.MouseBackward/Forward`, `tea.MouseButton10/11`, `tea.MouseNone`. Enable mouse
tracking with `view.MouseMode`.

### Other messages & commands

- **Focus:** `tea.FocusMsg{}` / `tea.BlurMsg{}` (set `view.ReportFocus = true`).
- **Resize:** `tea.WindowSizeMsg{Width, Height int}` — sent once on start and on every resize.
- **Clipboard (OSC 52):** `tea.SetClipboard(s) Cmd`, `tea.ReadClipboard() Msg` →
  `tea.ClipboardMsg{Content string, Selection byte}`; plus
  `tea.SetPrimaryClipboard` / `tea.ReadPrimaryClipboard` (X11/Wayland). Works over
  SSH where the terminal supports OSC 52.
- **Paste:** bracketed paste is on by default; pasted text arrives through key
  events. Disable with `view.DisableBracketedPasteMode = true`. (There are no
  `PasteMsg`/`PasteStartMsg` types — don't reach for them.)

### The "Cursed Renderer" (Ultraviolet)

v2 renders through `github.com/charmbracelet/ultraviolet`, a cell-based diffing
renderer modeled on ncurses. It compares each frame to the last and writes only
what changed, with synchronized-output mode for tear-free updates — orders of
magnitude less terminal I/O, which is a large bandwidth win for SSH apps. You
don't call it directly; just return Views. Practical consequence: redrawing an
unchanged frame is cheap, so heavy manual sub-view caching is a smaller win than
in v1 (still fine to cache expensive string building). Cap the frame rate with
`tea.WithFPS` for animation loops.

### Minimal compiling v2 program

```go
package main

import (
    "fmt"
    "os"

    tea "charm.land/bubbletea/v2"
    "charm.land/lipgloss/v2"
)

type model struct {
    choices  []string
    cursor   int
    selected map[int]struct{}
    title    lipgloss.Style
}

func initialModel() model {
    return model{
        choices:  []string{"Carrots", "Celery", "Kohlrabi"},
        selected: make(map[int]struct{}),
        title:    lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575")).Bold(true),
    }
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyPressMsg:
        switch msg.String() {
        case "ctrl+c", "q":
            return m, tea.Quit
        case "up", "k":
            if m.cursor > 0 {
                m.cursor--
            }
        case "down", "j":
            if m.cursor < len(m.choices)-1 {
                m.cursor++
            }
        case "enter", "space":
            if _, ok := m.selected[m.cursor]; ok {
                delete(m.selected, m.cursor)
            } else {
                m.selected[m.cursor] = struct{}{}
            }
        }
    }
    return m, nil
}

func (m model) View() tea.View {
    s := m.title.Render("What should we buy?") + "\n\n"
    for i, choice := range m.choices {
        cursor := " "
        if m.cursor == i {
            cursor = ">"
        }
        checked := " "
        if _, ok := m.selected[i]; ok {
            checked = "x"
        }
        s += fmt.Sprintf("%s [%s] %s\n", cursor, checked, choice)
    }
    s += "\n(q to quit)\n"

    v := tea.NewView(s)
    v.WindowTitle = "Shopping List"
    return v
}

func main() {
    if _, err := tea.NewProgram(initialModel()).Run(); err != nil {
        fmt.Printf("error: %v\n", err)
        os.Exit(1)
    }
}
```

---

## Lip Gloss v2

### Colors are now `image/color.Color`

`lipgloss.Color("#hex")` and ANSI index strings still work, but they now return a
stdlib `image/color.Color`. Style construction is unchanged:

```go
style := lipgloss.NewStyle().
    Foreground(lipgloss.Color("#FAFAFA")).
    Background(lipgloss.Color("#7D56F4")).
    Padding(1, 3)
out := style.Render("hello")
```

Any `image/color.Color` is accepted, and there are named ANSI constants
(`lipgloss.Red`, `lipgloss.BrightBlue`, …) and `lipgloss.RGBColor{R,G,B}`.

### Light/dark adaptation — `AdaptiveColor` is gone from root

v2 removes the implicit global renderer, so adaptive colors are now explicit.
Detect the background once, then pick colors with a closure:

```go
hasDark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
ld := lipgloss.LightDark(hasDark)               // func(light, dark color.Color) color.Color
accent := ld(lipgloss.Color("#D75FEE"), lipgloss.Color("#D7FFAE"))
```

Profile-aware fallbacks (true-color / 256 / 16) replace `CompleteColor`:

```go
profile := colorprofile.Detect(os.Stdout, os.Environ())
complete := lipgloss.Complete(profile)          // func(ansi, ansi256, truecolor color.Color) color.Color
c := complete(lipgloss.Color("5"), lipgloss.Color("200"), lipgloss.Color("#ff00ff"))
```

Inside a Bubble Tea program, prefer reading the background through the runtime
(the `View.BackgroundColor` round-trip / terminal query) rather than blocking on
`os.Stdin` at startup.

`AdaptiveColor`, `CompleteColor`, and `CompleteAdaptiveColor` still exist for
quick migration in **`charm.land/lipgloss/v2/compat`**, but upstream explicitly
does **not recommend them for new code**.

### No renderer; print through Lip Gloss for downsampling

`Style` is now a plain value type — there is no `lipgloss.NewRenderer`,
`SetColorProfile`, or per-style renderer. `Render()` always emits full-fidelity
ANSI; downsampling to the terminal's actual profile happens at print time:

```go
lipgloss.Println(style.Render("hi"))      // drop-in for fmt.Println, downsamples
lipgloss.Printf("%s\n", rendered)
s := lipgloss.Sprint(rendered)            // string variant
lipgloss.Fprintln(os.Stderr, rendered)
```

So: render with Lip Gloss, **print with `lipgloss.Print*` instead of `fmt.Print*`**.

### Unchanged

Borders (`NormalBorder()`, `RoundedBorder()`, `ThickBorder()`, `DoubleBorder()`,
`BlockBorder()`, custom `Border{}`), layout (`JoinHorizontal`, `JoinVertical`,
`Place`, `PlaceHorizontal`, `PlaceVertical`), and `Position` constants
(`Top/Bottom/Center/Left/Right`) behave as in v1.

### New / moved

- Sub-package widgets: `charm.land/lipgloss/v2/table`, `.../list`, `.../tree`
  (fluent builders: `table.New().Headers(...).Row(...)`, etc.).
- `Canvas` + `Layer` (built on ultraviolet) for absolute cell-positioned
  composition when `Join*`/`Place` aren't enough. Check the package docs for the
  current `Canvas`/`Layer` API before using.

---

## Bubbles v2

Import from `charm.land/bubbles/v2/<component>`. Cross-cutting patterns
(from the official `UPGRADE_GUIDE_V2.md`, which is "written for both humans and
LLM-assisted migration tools"):

- **`tea.KeyMsg` → `tea.KeyPressMsg`** in every component's `Update`.
- **Exported `Width`/`Height`/`YOffset` fields → getter/setter methods**:
  `m.Width = 80` becomes `m.SetWidth(80)`, read with `m.Width()`. Applies to
  textinput, viewport, progress, help, table (via `SetWidth/SetHeight`),
  filepicker.
- **`DefaultKeyMap` variable → `DefaultKeyMap()` function** (textinput, textarea,
  paginator).
- **Styles take `isDark bool`** since Lip Gloss no longer auto-adapts:
  `DefaultStyles(isDark)`, plus `DefaultDarkStyles()` / `DefaultLightStyles()`
  (help, list, textinput, textarea). Get `isDark` from
  `tea.WindowSizeMsg`-time background detection or `lipgloss.HasDarkBackground`.
- **`NewModel(...)` aliases removed — use `New(...)`** (help, list, paginator, spinner).
- `runeutil` and `memoization` are now internal (not importable).

Per-component highlights:

| Component | Key changes |
|-----------|-------------|
| **viewport** | Constructor uses functional options: `viewport.New(viewport.WithWidth(80), viewport.WithHeight(24))`. `Width/Height/YOffset` → methods. **`HighPerformanceRendering` removed.** New: soft-wrap, left gutter (line numbers), regex highlighting (`SetHighlights`), `SetContentLines`, `GetContent`, per-line styling, horizontal scroll. |
| **textinput** | `SetWidth`/`Width()`. Styles consolidated into a `Styles` struct (`s.Focused.Prompt`, `.Text`, `.Placeholder`, `.Suggestion`). `Cursor` field → `Cursor()` returning `*tea.Cursor`. New `VirtualCursor()`/`SetVirtualCursor(bool)`. |
| **textarea** | `Styles.Focused` / `Styles.Blurred` (was `FocusedStyle`/`BlurredStyle`); type `Style` renamed `StyleState`. `SetCursor` → `SetCursorColumn`. `Cursor()` → `*tea.Cursor`. New `Column()`, `ScrollYOffset()`, `MoveToBeginning()`, `MoveToEnd()`. v2.1 added dynamic height (`MinHeight`/`MaxHeight`). |
| **list** | `DefaultStyles(isDark)`, `NewDefaultItemStyles(isDark)`. Filter styling consolidated into `Styles.Filter` (a `textinput.Styles`). |
| **progress** | Color API rewritten: `WithGradient(a,b)` → `WithColors(...)`; `WithDefaultGradient()` → `WithDefaultBlend()`; `WithSolidFill(s)` → `WithColors(c)`; add `WithScaled(bool)`, `WithColorFunc(...)`. `WithColorProfile` removed. `FullColor`/`EmptyColor` are now `color.Color`. `Update` returns `(Model, tea.Cmd)`. |
| **spinner** | `NewModel()` → `New()`. Package `spinner.Tick()` → method `m.Tick()`. |
| **timer / stopwatch** | Functional options: `timer.New(timeout, timer.WithInterval(...))`, `stopwatch.New(stopwatch.WithInterval(...))`. `NewWithInterval` removed. |
| **table** | `SetWidth`/`SetHeight` (were exported fields). |
| **paginator** | `DefaultKeyMap()` function; the `Use*Keys` toggle fields removed — set `KeyMap` directly. |
| **cursor** | `Blink` field → `IsBlinked`; `BlinkCmd()` → `Blink()`. |
| **key** | Unchanged; `key.Binding` / `key.Matches` work as before. |

---

## v1 → v2 quick checklist

1. **Imports:** `github.com/charmbracelet/<lib>` → `charm.land/<lib>/v2`; ensure `go 1.25`.
2. **`View()`:** return `tea.View` via `tea.NewView(...)`, not a `string`.
3. **Program state:** move `WithAltScreen()`, `WithMouseCellMotion()`,
   `WithReportFocus()`, `SetWindowTitle()` onto `View` fields.
4. **Keys:** handle `tea.KeyPressMsg` (not `tea.KeyMsg`); `" "` → `"space"`.
5. **Color:** keep `lipgloss.Color("#hex")`; replace `AdaptiveColor` with
   `lipgloss.LightDark(hasDark)`; replace `CompleteColor` with `lipgloss.Complete(profile)`.
6. **Printing:** `fmt.Println(style.Render(x))` → `lipgloss.Println(style.Render(x))`.
7. **Bubbles:** swap exported `Width/Height` for `SetWidth/SetHeight`; call
   `DefaultKeyMap()` and `DefaultStyles(isDark)` as functions; viewport via
   functional options.
