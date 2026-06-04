# recap — agent loop guide

recap is the agent's work loop and its async review layer. `recap next` hands you the next
thing to do — review amends, reviewer replies, and pending todo items, in priority order;
you complete it, which records it to the review inbox to be looked at later (out of band,
out of git), and the reviewer's feedback comes back as fix-forward work. Everything runs
through recap verbs: recap tracks the work and marks it done for you — you never edit
project files by hand to manage the queue.

For the exact command surface and flags, run `recap help`. The verbs below are the stable
contract the loop depends on — this skill is self-contained; you don't need any other.

## Why it's async — keep both sides saturated

The whole design — the inbox, the record-and-move-on flow, the comment threads — exists so
that **neither side ever waits on the other**. That two-sided saturation is the point:
maximum progress, best use of everyone's time.

- **You stay saturated.** Finish an item, complete it, and immediately `recap next` for the
  following one. You never block waiting for a review or an answer.
- **The reviewer stays saturated.** A steady stream of finished items to review at their own
  pace, with replies and emotes carrying feedback back — no synchronous handshake, no
  meeting, no "are you done yet?".
- **Questions don't stall you.** When you need an answer, you don't stop and wait — you
  record it (a `reply`, or a `recap next --skip "reason"`) and move on. The answer comes
  back to you through `recap next` later, as a reply in the queue.

The comments system is what makes feedback flow *asynchronously*: it lets the reviewer
respond whenever they get to it and lets you keep producing in the meantime.

## Completing work (→ inbox)

How you finish an item depends on its kind — `recap next` tells you which:

- **todo** → commit your work, then `recap done <ref>`. This **records it to the review
  inbox AND marks the todo done** in one explicit step (the `<ref>` is what `recap next`
  handed you, e.g. `todo:a66e3a51`; the title is auto-filled from the todo text). recap
  does the bookkeeping — you don't.
  ```
  recap done <ref> \
    --criterion "<the falsifiable success check you wrote>" \
    --check     "<the command that re-proves it, e.g. go test -run X ./...>" \
    --result    "PASS" \
    --summary   "<reviewer briefing — see below>" \
    --sha       "$(git rev-parse --short HEAD)"
  ```
- **amends** → fix forward, commit, then `recap revise <task-id> --summary "…"` (records
  the fix + resolves the review; see "Acting on what `recap next` hands you").
- **reply** → answer with `recap reply <cN>` and/or acknowledge with `recap read <cN>`.

(`recap add` still records **ad-hoc** work that didn't come from a `recap next` todo —
same flags but you supply `--title`. For the normal loop, `recap done` is the path.)

Always pass `--summary`: a **reviewer briefing** shown at the top of the item's preview.
This is NOT the commit message (keep that concise for git). The summary is the rich,
contextual narrative for whoever reviews this now — "what I did, why, and what to watch
for" — including relevant context from the working session that would mean nothing to a
future git reader. Make the review fast: surface the decisions, trade-offs, and anything
you're unsure about.

Order matters: **commit first, then `recap done --sha HEAD`**, so the entry resolves to the
real commit and its diff. Complete every item — the inbox is the audit trail. recap derives
`--repo`/`--repo-path` from the cwd's git root, so run it inside the repo.

**Trickle, don't flood.** Saturation cuts both ways: keep yourself busy, but don't bury the
reviewer. Complete one item, let it land, move to the next — so the reviewer can review item
N while you work N+1, not face a backlog of nine at once. Don't barrel task→task→task in one
burst dumping a wall of items, especially when the reviewer is actively engaged (replying,
reviewing). A steady stream keeps both sides saturated; a flood saturates only you.

## Name yourself (persistent identity)

Run `recap whoami` at the start of a loop session. The identity **persists** beside the
db, so it survives stop/start — only name yourself **if it shows none** (don't rename an
already-named agent; that would churn your voice across restarts):

```
recap whoami                              # already named? keep it — do nothing
recap whoami "<a name>" --color "#RRGGBB" # ONLY if unnamed
```

You choose the name. If the user set a `name_theme` in config.toml, let it guide your
pick; otherwise pick freely — it works either way. From then on your comments/replies/
emotes are attributed to that name in that colour. To deliberately start fresh, the user
clears it (`recap whoami` with an empty name) or sets a new one. This is **recap-only** —
never put a name in git commits.

## Taking work — `recap next` (the single entry point)

Do not hand-roll the priority order or declare what you're working on. **`recap next` is
how you get work**: it returns the one highest-priority item across the whole queue,
records it as the in-flight item (which drives the reviewer's spinner — automatically, no
separate call), and advances on each call.

```
recap next                 # hand me the next item + flare it
recap current              # peek at the in-flight item without advancing
recap next --skip "why"    # can't do it → record the reason on it, move to the next
```

Priority (repo-scoped, computed for you): **amends** (tasks with an open
`request_changes` review) → **unread reviewer replies** → **next incomplete todo**. The
returned item tells you its kind and the verbs to act on it.

The cursor is the whole point: **getting work and going in-flight are the same act**, so
the flare can't rot. Calling `recap next` again walks PAST the current item — that's a
skip. Skipping something you didn't complete should carry `--skip "reason"` so the
reviewer sees why it was passed (it's recorded as a comment on the item), not silently
dropped. Completing an item (`recap done` for a todo, `revise`/`resolve` for amends, `read`
for a reply) drops it from the queue, so the next `recap next` naturally lands on what
follows. Everything pushes live — an open TUI tracks the flare without a refresh.

## Project scoping (important)

The loop's intake is auto-scoped to the CURRENT repo (the cwd's git root): `recap review ls`, `recap redo`, and `recap unread` only show THIS project's items, so a loop running in another codebase never drains or answers another project's work. Pass `--all` to cross repos deliberately.

## Don't sweep the channels by hand — `recap next` IS the intake

`recap next` already unifies amends + reviewer replies + todos in priority order from
the db, so **do not** run `recap review ls` / `recap unread` / `recap redo` as a per-cycle
sweep — that's the old pre-`next` workflow. Just call `recap next`; it hands you the next
thing. Those verbs are now **inspection-only** (ad-hoc "show me everything"), not loop
steps. The read-receipt mechanics still matter: when `recap next` hands you a reply,
`recap read <cN>` marks it read (clears the receipt; each comment shows an agent ●/○ and
a user read dot, pushed live). `recap read` is how you ack a reply — not a sweep.

## Acting on what `recap next` hands you

When `recap next` returns an **amends** item, it outranks todos (that's why it's the top
tier) — act on it first:

1. Read the full work order: `recap review show <review-id>`. It carries the verdict, the
   reviewer's summary (the "what to change"), and any line-anchored comments with the code
   captured at review time, plus the task's original criterion.
2. Fix **forward** — never rewrite the reviewed commit. Make the change, satisfy the
   original criterion *and* the reviewer's notes, then commit on the current branch.
3. Attach the fix to the **same** task as a new revision — do NOT create a separate
   task: `recap revise <task-id> --summary "what changed"` (sha defaults to the repo's
   short HEAD). This appends the fix-forward diff to the existing item and resolves the
   open `request_changes` review in one step, returning the item to the inbox for
   re-review. The reviewer cycles its diffs with `o` in the TUI; the original feedback
   stays attached so they can recontextualise.

Do **not** use `recap add --parent` for amend fixes — that spawns a near-duplicate
inbox item per fix. One task accumulates its whole change history via `revise`. (`recap
review resolve <id>` on its own is still fine when there's nothing new to attach.)

### Replying to a comment (threading)

To answer a specific reviewer comment in place — to ask a clarifying question, explain a
decision, or note what you changed — reply to it rather than leaving a loose task comment:

```
recap reply <comment-id> --body "what you did / why"
```

`recap review show <id>` prints every comment with its id as `[cN]`; pass that N. The
reply nests under the comment (and replies can themselves be replied to) and shows the
same way for general and line comments. `--who` defaults to `agent`. This is for
discussion only — it does **not** resolve the review; use `revise` for the fix-forward.

For a lightweight acknowledgement — "seen / agreed / done" without a whole reply —
react to a comment with an emote:

```
recap emote <comment-id> 👍
```

Pass any emoji (👍 seen/agree, 👀 looking, ✅ done). It shows next to the comment in
`recap review show` and the comments pane; one emote per comment (setting it again
overwrites, empty clears). Same `[cN]` ids as reply.

A `request_changes` review surfaces directly from the db as `recap next`'s top
(amends) tier; `recap review show <id>` is the work order and `revise` completes it.

## Attaching files / screenshots to comments

Comments can embed `[[/path/to/file]]` references — e.g. `[[/tmp/shot.png]]` to point at
a screenshot. recap can't render images inline (terminal), so a referenced file is opened
externally with `O` on the selected comment in the TUI. In the comment box, `Ctrl-V`
pastes a clipboard image: it writes a temp PNG ($TMPDIR, OS-tidied — the loop is tight,
so long-term retention isn't needed) and inserts the `[[path]]` link for you. You can also
type any `[[path]]` by hand to attach a log, file, or screenshot to feedback.

## Falsifiable criteria

Before you start an item, state its success check as something that can **fail** if the
work is broken — a concrete command and the result you expect, not "looks right". That
check is exactly what you pass as `--criterion` / `--check` / `--result` when you complete
the item. After the change, run it; if it couldn't fail when the code is wrong, it isn't a
real check — tighten it before recording.

## Boundaries & safety

- The core loop is local and reversible — safe to run unattended: `recap next` (take
  work), `recap done` (complete a todo → inbox), `recap review show` (read feedback),
  `recap revise` (fix-forward diff + resolve).
- Anything **destructive or outward-facing** — deleting data you didn't create, force
  pushes, publishing, network/external calls, or any change that's hard to undo — needs
  explicit human confirmation. Surface it, don't guess.
- Commits: one per item on the current branch, message a concise **single line** — the
  rich detail belongs in the recap `--summary`, not the commit body. Never sign commits or
  leave any agent attribution.
- Submitting reviews (`recap review submit`) is the **human reviewer's** action, never the
  loop's — never self-review.
- The review db (`$RECAP_DB` or `~/.config/recap/recap.db`) is private to the reviewer and
  cross-repo: never commit it, never push it, never surface its contents publicly.
