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
      param so the data package doesn't depend on config. **Deferred:** the rest of the
      TODO data layer (`todo_edit.go`: `todoItem` + parse/read/write/toggle) carries
      glyph-coupled UI fields on its struct, so it moves into `todo` as part of slice 5
      (UI-globals decoupling), where those UI fields get separated from the data.
- [ ] **Slice 2 — `store` → `db` package.** The canonical "db package". `store.go` is
      already a zero-coupling leaf and `store_test.go` is already pure-store. The cost is
      blast radius: every `Task`/`Comment`/`Review`/`State*`/`Verdict*`/`Status*` and
      `st.Method()` reference across the tree gets a `db.` qualifier. Big but mechanical
      and compiler-driven. **Open question for review: package name `db` vs `store`?**
      (article example uses `db`; current type is `Store`.)
- [ ] **Slice 3 — remaining pure leaves.** `diff` (parse/model: `DiffFile`, `DiffHunk`,
      `DiffLine`, `parseUnifiedDiff`), `snooze`, and the per-repo `cursor` (current.go).
      Each has a tiny caller set and no UI-global coupling.
- [ ] **Slice 4 — render/theme utilities.** `theme` (`Theme`, palettes, `applyTheme`),
      `contrast`, `highlight`, `links`, `focus_shade` — stateless helpers the TUI calls.
      Group into a small number of flat packages (likely `theme` + `render`).
- [ ] **Slice 5 — the hard one: decouple the TUI globals.** *Design decision needed.*
      ui.go's ~50 globals must become non-global before the TUI can be a package. Two
      candidate shapes:
      1. a single `ui.Model` struct holding all state, methods on it, passed explicitly;
      2. dependency-injected sub-structs per concern (inbox, diff pane, modals).
      Recommendation: **(1)** first — least invasive, mechanical (globals → fields), then
      split ui.go into per-concern files (`inbox.go`, `diffpane.go`, `modals.go`,
      `keybindings.go`) inside the `ui` package. (2) only if a second consumer appears
      (no one-way doors).
- [ ] **Slice 6 — root cleanup.** Rename `main.go` → `recap.go` (service-named entry),
      keep `func main` + command dispatch composing the pipeline; command bodies can move
      to a flat `cli` package if dispatch grows. Per-struct file naming pass.

## Why slice-by-slice and not one big commit

A 40-file reshape committed atomically is the textbook one-way door. Each slice here is
independently revertable and reviewable; the expensive, opinionated steps (db naming,
the TUI-globals shape) are isolated behind their own review so direction can change before
the churn lands. Slices 1–4 are safe to run unattended; **slice 5 wants explicit sign-off
on the chosen shape before execution.**
