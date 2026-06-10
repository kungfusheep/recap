# recap

An async work loop and review inbox for autonomous agents.

Agent loops do the work and record each finished unit here; you review the queue
later — out of band, out of git. Diffs stay in git (pointed to by sha); recap holds
the private review layer: the task, its falsifiable check, the result, your verdict,
the line-anchored comment threads, and the agent↔agent message traffic. It's a
cross-repo inbox — a personal PR queue that never touches a project's history.

```
recap            # launch the reviewer TUI
recap skill      # the agent loop guide (embedded, versions with the binary)
recap help       # the authoritative command surface
```

## Why

The whole design exists so **neither side ever waits on the other**. The agent stays
saturated: finish an item, record it, take the next; when the queue runs dry it parks
on a long-poll instead of exiting. You stay saturated: a steady stream of finished
items to review at your own pace, with feedback flowing back as the next cycle's
work. Review comments are the steering wheel — they redirect architecture as easily
as they flag bugs.

## Model

- **Task** — one completed unit: title, falsifiable criterion, the command that
  re-proves it, result, status (`pending` / `approved` / `redo`), and the `sha` it
  points at. Recording **refuses a sha the repo can't resolve** (`--force` to
  override) — a dangling sha would render as an empty diff forever.
- **Review** — a batch of feedback: a verdict (`request_changes` / `approve` /
  `comment`), a summary, and N comments, some anchored to a diff line with the
  snippet captured at review time. Drafts accumulate; **submit** publishes
  atomically. Resolving a review marks its comments read.
- **Revision** — a fix-forward diff appended to the *same* task (`recap revise`).
  One item accumulates its whole change history; the reviewer cycles diffs with `o`.
- **Message** — an agent→agent note, addressed to a *repo* and queued durably
  (nothing needs to be listening). See "Agent ↔ agent" below.

## The agent loop

`recap next` is the single intake. It returns the highest-priority item across the
repo's whole queue, marks it in-flight (which drives the TUI's spinner flare), and
advances on completion:

1. **amends** — tasks with an open `request_changes` review (your feedback)
2. **replies** — unread reviewer comments
3. **messages** — peer notes from other agents' loops
4. **todos** — the next incomplete line from the repo's TODO file

```
recap next                # take the next item (and flare it)
recap next --wait         # queue empty → park; wakes the moment work lands
recap next --skip "why"   # can't do it → recorded on the item, move on
recap current             # peek without advancing
```

Completion is per-kind: `recap done <todo-ref>` (records to the inbox + ticks the
TODO), `recap revise <id>` (fix-forward + resolves the review), `recap read c<N>` /
`m<N>` (comments / messages). Every completion carries the **falsifiable criterion**,
the command that re-proves it, and a reviewer briefing (`--summary`) — richer than
the commit message, written for review.

The inbox is **FIFO by arrival**: an item returning from amends re-queues at the
end, it doesn't jump the queue on its old id.

Agents name themselves per repo (`recap whoami "Name" --color "#RRGGBB"`) — the name
appears on comments, messages, and the diff's summary header. recap-only; never in
git.

## The reviewer TUI

Borderless, mail-style, three panes — `h`/`l`/`Tab` move focus, `j`/`k` navigate.
`?` shows the full cheatsheet.

- **Inbox** (left) — FIFO queue grouped pinned / inbox / amends / done, with the
  UPCOMING peek at the repo's next todos. `f` filters by repo, `p` pins, `u` undoes
  the last approve/submit/pin, `o` expands a task's revisions, `↵` opens the diff.
  Completed items older than a day paginate behind a "load more" row.
- **Detail + diff** (middle) — the briefing banner (summary · agent name, or the
  amends work order), then the parsed diff: per-file fold (`z` pick, `Z` all,
  `]`/`[` jump between files), syntax-highlighted added lines, renames as
  `old → new`. `c` drops jump labels to anchor a line comment; `e` opens the picked
  line in `$EDITOR`. A sha the repo can't resolve shows a loud "commit not found"
  banner, never a silent "no changes".
- **Comments** (right, when present) — the conversation: threads, replies (`r`),
  emotes, read receipts, `O` opens `[[file]]` attachments (Ctrl-V in the comment box
  pastes a screenshot as one).

`m` opens the **agent messages ledger** — every agent↔agent conversation across all
repos, with read state; viewing stamps your read receipt. The header badges unread
peer traffic (`✉ N`).

**Themes**: 20 palettes (dark, light, and the mfd pack), switchable live from the
command palette (`Space` / `^P`) — colours are pointer-bound, so no rebuild. Syntax
highlighting follows the theme: mfd palettes use their vim scheme's
monotone-with-decoration model; dark/light use nord / monokailight.

## Agent ↔ agent

Loops in different repos coordinate directly — without the human as courier, but
with the human seeing everything:

```
recap send <repo> --body "…"              # durable note for that repo's loop
recap send <repo> --reply-to N --body "…" # thread a reply
recap send --listeners --body "…"         # broadcast: every LIVE parked loop
recap listeners                           # who's parked right now
recap messages [--all]                    # the two-way ledger
recap read m<N>                           # clear a received message
```

Messages are addressed to a **repo, not a process** — no listener means it waits.
A parked `recap next --wait` registers as a live listener and wakes on arrival.
Guardrails: messages **coordinate, never approve** (verdicts stay human), one
in-flight question per pair, and work still only enters a repo through its
TODO/review flow.

## Storage & privacy

A single SQLite db at `$RECAP_DB`, else `~/.config/recap/recap.db` — plus small
state files beside it (cursor, pins, snoozes, listeners, identity, settings).
Global and cross-repo by design — **never** commit it to a project or push it
anywhere. It is the reviewer's private layer.

Optional config at `$RECAP_CONFIG`, else `~/.config/recap/config.toml`:

```toml
# Where each repo's plain-text TODO lives ({relpath} = repo path relative to $HOME).
# Feeds recap next's todo tier and the TUI's UPCOMING section.
todo_template = "~/notes/{relpath}/TODO.md"

# Optional hint for agent self-naming (e.g. "birds", "greek").
name_theme = "birds"
```

## Build & test

```
go build ./...
go test ./...
go install .        # puts `recap` on $GOPATH/bin
```

The agent guide is embedded in the binary (`recap skill`) so it can never drift from
the installed surface. `skill_contract_test.go` pins the CLI contract the loop
depends on — if a verb or flag the loop uses changes, it fails. Keep the guide,
the tests, and `recap help` in sync.
