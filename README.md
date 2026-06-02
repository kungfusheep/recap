# recap

An async review inbox for autonomous (`tododo` / `deadman-todo`) work.

An agent loop does the work and records each finished unit here; you review the queue
later — out of band, out of git. Diffs stay in git (pointed to by sha); recap holds only
the private review layer: the task, its falsifiable check, the result, your verdict, and
the line-anchored comment thread. It's a cross-repo inbox, like a personal PR queue that
never touches the project's history.

```
recap            # launch the reviewer TUI
recap help       # the authoritative command surface
```

## Why

The autonomous loop is fire-forward: it doesn't wait for you. recap is how feedback flows
back asynchronously. The loop records what it did; you review when convenient; your
`request_changes` becomes the next cycle's fix-forward work — without ever rewriting
history or blocking the loop.

## Model

- **Task** — one completed unit of work: title, falsifiable criterion, the command that
  re-proves it, result, status (`pending` / `approved` / `redo`), and the `sha` it points
  at. A fix task can link to the one it fixes via `--parent` (PR-style lineage).
- **Review** — a batch of feedback on a task: a verdict (`request_changes` / `approve` /
  `comment`), an overall summary (the "what to change"), and N comments, some anchored to
  a diff line (file + hunk + line + the snippet captured at review time). Drafts
  accumulate; **submit** publishes the batch atomically.
- **Verdict → status**: `request_changes` flips the task to `redo` and drops a breadcrumb
  in the repo's TODO; `approve` marks it approved; `comment` is a non-blocking note.

## Storage

A single SQLite db at `$RECAP_DB`, else `~/.config/recap/recap.db`. Global and cross-repo
by design — **never** commit it to a project or push it anywhere. It is the reviewer's
private layer.

Optional config at `$RECAP_CONFIG`, else `~/.config/recap/config.toml`:

```toml
# Where to drop a TODO breadcrumb when a review requests changes.
# {relpath} expands to the repo path relative to $HOME.
todo_template = "~/Library/Mobile Documents/iCloud~md~obsidian/Documents/O Notes/reponotes/{relpath}/TODO.md"
```

With no template set, `submit` just prints the breadcrumb line for you to paste.

## The two faces

recap has an **agent CLI** (what the loop drives) and a **reviewer TUI** (what you drive).

### Reviewer TUI (`recap`)

Borderless, mail-style. Two/three focusable panes — `h`/`l`/`Tab` move focus, `j`/`k`
navigate within:

- **Inbox** (left) — tasks grouped pending / needs-rework / approved; `f` filters by repo.
- **Detail + diff** (right) — task metadata and a friendly parsed diff (native-scroll).
  In the diff, `c` drops jump labels to anchor a line comment.
- **Draft review** (conditional, far right) — appears only when the selected task has
  draft comments; shows the pending verdict/summary and every draft comment in one place,
  like a PR's conversation overview.

Review actions: `c` line comment, `S` submit review (verdict + summary), `a` approve,
`r` rework, `q` quit.

### Agent CLI

Run `recap help` for the live, authoritative surface. The loop uses a small subset:

```
recap add --title T --criterion C --check CMD --result R --sha HEAD [--parent ID]
                                   # record a finished task (commit first, then add)
recap review ls --state submitted  # drain reviews waiting to be addressed
recap review show <review-id>       # the work order: verdict + summary + anchored comments
recap review resolve <review-id>    # mark addressed after a fix-forward commit
```

## How the loop uses it

This is driven by the **`recap` skill** (a thin wrapper that lives with the agent config,
not in this repo — it discovers the surface via `recap help`, so it never goes stale). The
skill layers two hooks onto the `tododo` / `deadman-todo` loop:

1. **Record** — after the loop commits a completed task, `recap add --sha HEAD` so the
   entry resolves to the real commit.
2. **Drain** — at the start of each cycle, before fresh TODO items, the loop reads
   submitted reviews (`review ls --state submitted` → `review show`), fixes forward,
   records the fix with `--parent`, and `review resolve`s the review.

Submitting reviews is always the **human's** job (TUI or CLI) — the loop never
self-reviews.

```
loop: do task ──commit──> recap add ──┐
                                       ▼
                              you: review in TUI ──submit request_changes──┐
                                                                            ▼
loop next cycle: recap review show ──fix forward──> recap add --parent ──> recap review resolve
```

## Build & test

```
go build ./...
go test ./...
go install .        # puts `recap` on $GOPATH/bin
```

`skill_contract_test.go` pins the exact CLI surface the loop's skill depends on — if you
rename or drop a flag the loop uses, it fails. Keep it, the `recap` skill, and `recap help`
in sync.
