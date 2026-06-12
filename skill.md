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
  following one. You never block waiting for a review or an answer — and when the queue runs
  dry you `recap next --wait` (long-poll) rather than stopping, so you're never idle and
  never out of the loop.
- **The reviewer stays saturated.** A steady stream of finished items to review at their own
  pace, with replies and emotes carrying feedback back — no synchronous handshake, no
  meeting, no "are you done yet?".
- **Questions don't stall you.** When you need an answer, you don't stop and wait — you
  record it (a `reply`, or a `recap next --skip "reason"`) and move on. The answer comes
  back to you through `recap next` later, as a reply in the queue.

The comments system is what makes feedback flow *asynchronously*: it lets the reviewer
respond whenever they get to it and lets you keep producing in the meantime.

## The loop is hard — never exit it

This is a **hard loop**. Once you're in it you stay in it. You do **not** stop, return,
or end your turn because you think you're finished — the loop ends only when the human
**explicitly** tells you to stop. Nothing else exits it.

**An empty queue is not "done" — it's "wait".** When `recap next` has nothing to hand
you, do not stop and report idle. Call:

```
recap next --wait
```

It long-polls — blocks until review feedback or new work appears, then returns the next
item and you carry straight on. That is how an idle agent comes back to life the moment
the reviewer submits `request_changes`: no restart, no "shall I continue?", no handshake.
On the rare timeout it returns idle; just call `recap next --wait` again. Treat reaching
the end of the queue as the cue to **wait**, never as permission to leave.

**A user message still takes precedence.** If the human says something mid-loop, handle it
and continue the conversation as normal — that is not exiting the loop. The hard-loop rule
is only about where *work* comes from: you pick up tasks from the loop (`recap next`), never
from inventing your own, unless the human explicitly redirects you.

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
This is NOT the commit message — write the commit however the user/project normally does;
the summary is a separate thing for a different audience. The summary is the rich,
contextual narrative for whoever reviews this now — "what I did, why, and what to watch
for" — including relevant context from the working session that would mean nothing to a
future git reader. Make the review fast: surface the decisions, trade-offs, and anything
you're unsure about.

**Format for scanning, not reading.** A wall of prose makes the reviewer dig; structure
makes them fast. The TUI renders a small markup in summaries:

```
newlines        real line breaks (use them — one paragraph per idea)
- bullet        bullet rows with hanging indent
Label:          a short capitalised lead-in at line start renders bold
                (Why: / What changed: / Verify: / Watch:)
`code`          identifiers, commands, paths get the code colour
**bold**        emphasis for the load-bearing phrase
```

Shape that works: a one-line headline first (what landed), then `Why:` if the motivation
isn't obvious, a few `- ` bullets for the substance (one decision/trade-off each), and
ALWAYS a `Verify:` line — the command or steps that re-prove it. Short lines, bold the
one thing per bullet that matters. The goal: the reviewer knows what they're looking at
before the diff scrolls.

Order matters: **you commit first** (yourself — see Boundaries; don't defer it to the
human), **then `recap done --sha HEAD`**, so the entry resolves to the real commit and its
diff. Complete every item — the inbox is the audit trail. recap derives
`--repo`/`--repo-path` from the cwd's git root, so run it inside the repo.

**Trickle, don't flood.** Saturation cuts both ways: keep yourself busy, but don't bury the
reviewer. Complete one item, let it land, move to the next — so the reviewer can review item
N while you work N+1, not face a backlog of nine at once. Don't barrel task→task→task in one
burst dumping a wall of items, especially when the reviewer is actively engaged (replying,
reviewing). A steady stream keeps both sides saturated; a flood saturates only you.

## Name yourself (persistent identity)

Run `recap whoami` at the start of a loop session. Identity is **per-repo** and persists
beside the db, so it survives stop/start *and* a loop in another repo gets its own name
(it won't inherit this one). Only name yourself **if `recap whoami` shows none for this
repo** (don't rename an already-named agent; that would churn your voice across restarts):

```
recap whoami                              # already named? keep it — do nothing
recap whoami "<a name>" --color "#RRGGBB" # ONLY if unnamed
```

You choose the name. If the user set a `name_theme` in config.toml, let it guide your
pick; otherwise pick freely — it works either way. From then on your comments/replies/
emotes are attributed to that name in that colour. To deliberately start fresh, the user
clears it (`recap whoami` with an empty name) or sets a new one. This is **recap-only** —
never put a name in git commits.

**Names are unique across the fleet.** `recap whoami` refuses a name another
repo's loop already holds. If you genuinely ARE that agent expanding into a new
repo, re-run with `--also`; otherwise pick a fresh name. Never `--also` a name
you didn't originally claim — two unrelated agents sharing a name corrupts the
dashboard, the message ledger, and the review record.

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
inbox item per fix. One task accumulates its whole change history via `revise`.

**A review can be a question, not a change.** request_changes puts the item in *your*
amends queue (your turn). If the reviewer's point was a **question** — nothing to revise,
just answer — reply to it (below) and then `recap review resolve <id>` to hand it back.
Resolving with nothing to attach is fine and correct here: it bounces the item out of your
amends and back into the reviewer's inbox for re-review (where they approve, or re-request
with a real change). **Never leave an answered review sitting in amends** — once you've
addressed it (revised *or* answered), the turn is the reviewer's; the ball must move to
exactly one court, never get stuck in yours.

### Replying to a comment (threading)

To answer a specific reviewer comment in place — to ask a clarifying question, explain a
decision, or note what you changed — reply to it rather than leaving a loose task comment:

```
recap reply <comment-id> --body "what you did / why"
```

`recap review show <id>` prints every comment with its id as `[cN]`; pass that N. The
reply nests under the comment (and replies can themselves be replied to) and shows the
same way for general and line comments. `--who` defaults to `agent`. A reply by itself
does **not** resolve the review — use `revise` for a code fix, or, when the comment was a
question you've now answered, `recap review resolve <id>` to hand it back (see above).

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

## Peer messages (agent→agent)

Loops in different repos can message each other directly — for coordination that
shouldn't need the human as a courier:

```
recap send <repo> --body "…"            # queue a note for that repo's loop
recap send <repo> --reply-to N --body … # thread under a message you received
recap messages [--all]                  # the ledger, both directions, read state
recap read m<N>                         # clear a received message from your queue
```

Messages are **durable and addressed to a repo, not a process**: if no loop is
running there, the note simply waits — its next `recap next` (or parked `--wait`)
picks it up. Received messages surface in your queue between reviewer replies and
todos; the work order shows the sender (`name@repo`) and the reply command.

Use them for: announcing a dependency/API change downstream ("riffkey grew X — bump
your go.mod"), requesting a capability from the repo that owns it (with the contract
you need), handing off a bug whose fix lives in another repo (attach a falsifiable
repro), or reporting back that an integration passes.

**Broadcast — ask the room.** `recap send --listeners --body "…"` delivers to every
repo with a live parked loop (`recap listeners` shows who that is), excluding you.
Use it when you don't know WHO can help: a call for a second pair of eyes, "anyone
seen this glyph behaviour?", a heads-up that affects whoever's awake. It deliberately
targets only loops that can respond *now* — a broadcast is a question to the present,
not an announcement to posterity (durably address a specific repo for that).

Boundaries: messages **coordinate, they never approve** — verdicts stay with the
human, who sees all traffic (the TUI's ✉ badge + `recap messages`). Keep them terse
and actionable; one in-flight question per pair (the ball-in-one-court rule applies
to agent pairs too — don't ping-pong). A message is NOT a place to invent new scope:
work enters a repo through its TODO (see `recap todo` below) or review flow, where
the human can see and reorder it.

## Creating work — `recap todo` (YOUR OWN queue only)

```
recap todo "task text"    # append to YOUR repo's TODO — never another repo's
```

Appends an unchecked task to your repo's TODO file (the same file `recap next`'s
todo tier and the TUI's UPCOMING band read). Use it when work surfaces that isn't
yours to do right now: a follow-up you're deferring, or a discovery you're
queueing instead of self-assigning mid-task. Don't use it to bypass a review
verdict (amends stay amends).

**Never drop todos onto another repo's queue — and the CLI refuses it.**
Cross-repo `recap todo --repo-path` is rejected unless that repo's owner has
explicitly opted in (`recap todo --open`, an owner verb). The flow for
cross-repo work is the COMMS MODEL: `recap send <repo>` proposing the work (with
the contract/repro you'd want received), and the OWNING agent — if it agrees —
raises the todo on its own queue with `recap todo`. Agreement first, then the
owner queues it. The human sees the proposal in the message ledger and the
resulting todo in UPCOMING, with a clear chain of who asked and who accepted.

## Falsifiable criteria

Before you start an item, state its success check as something that can **fail** if the
work is broken — a concrete command and the result you expect, not "looks right". That
check is exactly what you pass as `--criterion` / `--check` / `--result` when you complete
the item. After the change, run it; if it couldn't fail when the code is wrong, it isn't a
real check — tighten it before recording.

## Boundaries & safety

- The core loop is local and reversible — safe to run unattended: `recap next` (take
  work) / `recap next --wait` (park until work appears), `recap done` (complete a todo →
  inbox), `recap review show` (read feedback), `recap revise` (fix-forward diff + resolve).
- Anything **destructive or outward-facing** — deleting data you didn't create, force
  pushes, publishing, network/external calls, or any change that's hard to undo — needs
  explicit human confirmation. Surface it, don't guess.
- Commits are **part of the loop, not a milestone to defer**. Make the commit yourself —
  one per item on the current branch, **in whatever commit-message style the user/project
  prefers** (the rich reviewer briefing lives in the recap `--summary`, so the commit only
  needs to follow their convention) — then `recap done --sha HEAD`. A local commit
  is reversible and never leaves the machine, so it is *not* an outward-facing action that
  needs sign-off: **do not stop to ask the human to commit, and never halt the loop waiting
  for one** — that strands every finished item uncompleted and breaks the async contract. If
  your general instructions say to ask before committing, that's for ad-hoc work; inside the
  loop the commit is how an item is recorded, so you own it. (Pushing, publishing, or
  anything that leaves the repo still needs confirmation.) Never sign commits or leave any
  agent attribution.
- Submitting reviews (`recap review submit`) is the **human reviewer's** action, never the
  loop's — never self-review.
- The review db (`$RECAP_DB` or `~/.config/recap/recap.db`) is private to the reviewer and
  cross-repo: never commit it, never push it, never surface its contents publicly.
