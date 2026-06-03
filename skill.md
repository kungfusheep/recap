# recap — agent loop guide

recap is the async review layer for the `tododo` / `deadman-todo` loop. The loop does the
work; recap is where each finished unit goes to be reviewed later — out of band, out of
git — and where the reviewer's feedback comes back in as fix-forward work.

For the exact command surface and flags, run `recap help`. The verbs below are the stable
contract the loop depends on.

## Recording finished work (loop → inbox)

After the loop commits a completed task (the normal per-task commit), record it so it
shows up in the review inbox, pointing at that exact commit:

```
recap add \
  --title     "<the TODO item, concise>" \
  --criterion "<the falsifiable success check you wrote for the task>" \
  --check     "<the command that re-proves it, e.g. go test -run X ./...>" \
  --result    "PASS" \
  --summary   "<reviewer briefing — see below>" \
  --sha       "$(git rev-parse --short HEAD)"
```

Always pass `--summary`: a **reviewer briefing** shown at the top of the item's preview.
This is NOT the commit message (keep that concise for git). The summary is the rich,
contextual narrative for whoever reviews this now — "what I did, why, and what to watch
for" — including relevant context from the working session that would mean nothing to a
future git reader. Make the review fast: surface the decisions, trade-offs, and anything
you're unsure about.

Order matters: **commit first, then `recap add --sha HEAD`**, so the entry resolves to the
real commit and its diff. Record every completed task — the inbox is the audit trail.
`recap add` derives `--repo`/`--repo-path` from the cwd's git root, so run it inside the
repo.

**Pace for the reviewer, not yourself.** recap is an *async* queue: finish a task, record
it, and let the reviewer take it at their pace. When the reviewer is actively engaged
(replying, reviewing), do NOT sprint through many tasks back-to-back and dump a wall of
items on them at once — that defeats the steady-trickle point of the loop. Prefer the
loop's wakeup cadence between tasks over barrelling task→task→task in one go. A reviewer
should be able to review item N while you work on N+1, not face a backlog of nine at once.

## Picking up review feedback (inbox → fix-forward work)

At the **start of each loop cycle**, before reading the TODO, check for reviews the human
has submitted and act on them first — they outrank fresh TODO items.

1. List submitted, unresolved reviews for this repo: `recap review ls --state submitted`
   (scoped to the current repo by default; pass `--all` to see every repo).
2. Read the full work order: `recap review show <review-id>`. It carries the verdict, the
   reviewer's summary (the "what to change"), and any line-anchored comments with the code
   captured at review time, plus the task's original criterion.
3. Fix **forward** — never rewrite the reviewed commit. Make the change, satisfy the
   original criterion *and* the reviewer's notes, then commit on the current branch.
4. Record the fix as a new task linked to the one it fixes:
   `recap add --parent <original-task-id> --title "fix: <…>" --sha "$(git rev-parse --short HEAD)" …`
5. Resolve the review: `recap review resolve <review-id>`.

A `request_changes` review also drops a breadcrumb line into the repo's TODO
(`address recap review #<id> (recap review show <id>)`); treat that TODO line and the
`recap review show` work order as two views of the same item — resolving the review
completes it.

## Boundaries

- Recording and reading reviews is local and reversible — safe inside the deadman loop.
- `recap add`, `recap review show`, and `recap review resolve` are the loop's three verbs.
  Submitting reviews (`recap review submit`) is the **human reviewer's** action (via the
  TUI or CLI), never the loop's — never self-review.
- The review db (`$RECAP_DB` or `~/.config/recap/recap.db`) is private to the reviewer and
  cross-repo: never commit it, never push it, never surface its contents publicly.

See the `deadman-todo` skill for the loop's safety boundary, TODO path rules,
falsifiable-criteria rules, and the per-task commit policy that this layers on top of.
