# Refactor: align recap with the service-principles layout

Target guidance: <https://kungfusheep.com/articles/service-principles>

> - Entry points (`func main`, command dispatch) stay at the **root** in a
>   service-named file; the root composes the pipeline in sequence.
> - Everything else moves into **logically-named, flat (non-nested) packages**.
> - Each package starts as a **single file named after the package**, exposing the
>   bare minimum on its public interface.
> - Per-struct files named after the struct, lowercase (`buffer.go` → `type Buffer`),
>   with the struct's methods + constructor in that same file.
> - A `db`/store package for database calls; package names match the service they call.

## Where we are now

Almost the entire codebase is `package main` at the root (~40 files), sharing a large
pool of package-level mutable globals. Only `notify/` and `poll/` are already packages.
The data layer (`store.go`) is a clean leaf; the CLI dispatch (`main.go`) is clean of UI
globals; the TUI (`ui.go` + its modal/handler satellites) is tightly coupled through
~50 package globals (`uiStore`, `vmRows`, `sel`, `tasks`, `omni`, `uiApp`, theme colours,
modal state, …). That global pool is the one hard problem; everything else is mechanical.

## Slice plan (incremental, each compiles + all tests green)

Ordered low-risk → high-risk so the reviewer can stop/redirect at any boundary.

- [x] **Slice 1 — `config` package.** Extract `config.go` → `config/config.go`
      (`Config`, `LoadConfig`, `Config.TODOPathFor`, `AppendTODO`). Pure file-I/O leaf,
      no UI coupling. Test moved to `config/config_test.go`.
- [x] **Slice 1b — split `todo` out of `config`** (reviewer steer, #170 c205: "config is
      application *config*, TODO is application *data* — split them"). New `todo` package
      owns the TODO data ops: `todo.PathFor(template, repoPath)` (was `Config.TODOPathFor`)
      and `todo.Append` (was `AppendTODO`). `config` is now pure app config
      (`Config{TODOTemplate, NameTheme}`, `LoadConfig`). `todo` takes the template as a
      param so the data package doesn't depend on config.
- [x] **Slice 1c — TODO data/UI split** (reviewer steer, #170 c219: prefer data and UI
      separate — glyph makes it easy). The whole TODO data layer now lives in `todo`:
      `todo.Item` (pure data: IsTask/Done/Text/Raw) + `ParseLine`/`Line`/`Read`/`Write`/
      `Toggle`/`Add`, glyph-free. recap keeps `todoData []todo.Item` (source of truth) and
      builds `todoVM` (UI-only: Selected/Display/FGColor + the two bools the checkbox binds)
      via `todoPrep` — exactly the inbox's `tasks`→`vmRows` pattern. NO deep data/UI
      crossover was needed; the VM projection cleans it up. `todo_edit.go` deleted; its data
      tests moved to `todo/item_test.go`.
- [x] **Slice 2 — data layer → `db` package** (name chosen: `db`, #170 c263). `store.go`
      → `db/store.go` (`package db`); every `db.Task`/`db.Comment`/`db.Review`/`db.Store`/
      `db.State*`/`db.Verdict*`/`db.Status*` + `db.Open`/`db.OpenAt`/`db.Path`/`db.NowStamp`
      qualified across the tree (compiler-driven). Exported the 3 internals used outside:
      `dbPath`→`Path`, `nowStamp`→`NowStamp`, `resolveOpenRequestChanges`→
      `ResolveOpenRequestChanges`. The file-beside-db helpers (snooze/cursor/pins/identity/
      settings) now call `db.Path()`. Tests stay in `package main` (qualified) for now —
      `testStore` returns `*db.Store`; relocating the pure-store tests into `db/` is optional
      polish. Build + full suite green.
- [x] **Slice 3a — `diff` package.** `diff.go` → `diff/diff.go` (`package diff`), clean
      API: `diff.File`/`diff.Hunk`/`diff.Line`/`diff.LineKind`, `diff.Parse`,
      `diff.LineAdd`/`LineDel`/`LineContext`, `diff.TotalLines`. Qualified only the
      diff-type users (ui.go, the diff/editor tests) — NOT the theme files, whose
      `Theme.DiffHunk`/`DiffAdd`/`DiffDel` *fields* collide in name with the old types
      (the rename to `Hunk` sidesteps the collision). Build + suite green.
- [ ] **Slice 3b — remaining leaves.** `snooze` (snooze.go) and per-repo `cursor`
      (current.go) — both tiny file-beside-db helpers (now calling `db.Path()`), used
      mainly by next.go/upcoming.go. No UI coupling.
- [ ] **Slice 4 — render/theme utilities.** `theme` (`Theme`, palettes, `applyTheme`),
      `contrast`, `highlight`, `links`, `focus_shade` — stateless helpers the TUI calls.
      Group into a small number of flat packages (likely `theme` + `render`).
- [ ] **Slice 5 — decouple the TUI globals into cohesive concrete structs.** *Shape agreed
      (#170 c257/c260): NO dependency injection, NO interfaces — concrete types only; and
      NOT one big `ui.Model` (a god-struct only grows). Instead group ui.go's ~50 globals
      into SEVERAL cohesive concrete structs by concern, each owned by the right place:
      - diff pane (layer/meta/fold/scroll/focus) · comments-draft (list/sel/scroll/focus)
      - omnibox · todo view (already half-done: `todoData` is `todo.Item`) · theme/palette
      - inbox model (tasks/vmRows/sel/filter)
      Where a group is really data it lives in / comes from its own package. Success
      criterion is "no loose package globals; state lives in cohesive concrete structs owned
      by the right place" — not "one struct". Lands as smaller sub-slices (one struct group
      at a time), each its own review cycle, matching how we've been going. The grouping
      (deciding the seams) is the actual work.
- [ ] **Slice 6 — root cleanup.** Rename `main.go` → `recap.go` (service-named entry),
      keep `func main` + command dispatch composing the pipeline; command bodies can move
      to a flat `cli` package if dispatch grows. Per-struct file naming pass.

## Why slice-by-slice and not one big commit

A 40-file reshape committed atomically is the textbook one-way door. Each slice here is
independently revertable and reviewable; the expensive, opinionated steps (db naming,
the TUI-globals shape) are isolated behind their own review so direction can change before
the churn lands. Slices 1–4 are safe to run unattended; **slice 5 wants explicit sign-off
on the chosen shape before execution.**
