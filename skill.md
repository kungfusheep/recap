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

## Signalling what you're working on (the in-flight marker)

When you START work on a recap item, point the marker at it — it takes a **work-item id,
not free text** ("what I'm on", not a status feed):

```
recap working #<task-id>     # e.g. recap working #50
recap working --clear        # when you move off / go idle
```

The reviewer's panel shows a spinner with that item's "#n title", resolved live. Update
it as you move between items. It's a cue only — it doesn't record work (`recap add`) or
change state — and it pushes, so an open TUI tracks it without a refresh.

## Reviewer replies — the read-receipt inbox

Reviewer **replies** (via `recap reply` or the TUI `r`) are thread comments, NOT new
submitted reviews — so they don't show in `recap review ls`. Check for them every cycle:

```
recap unread       # reviewer comments the agent hasn't read yet, with [cN] ids
recap read <cN> …  # mark read once you've acted (clears the receipt)
```

Each comment carries two read dots in the TUI — agent (●/○) and user — so both sides
see receipt; marking read pushes live (no refresh). Treat `recap unread` as part of the
loop's feedback intake alongside `recap review ls`.

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
4. Attach the fix to the **same** task as a new revision — do NOT create a separate
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

A `request_changes` review also drops a breadcrumb line into the repo's TODO
(`address recap review #<id> (recap review show <id>)`); treat that TODO line and the
`recap review show` work order as two views of the same item — `revise` completes it.

## Attaching files / screenshots to comments

Comments can embed `[[/path/to/file]]` references — e.g. `[[/tmp/shot.png]]` to point at
a screenshot. recap can't render images inline (terminal), so a referenced file is opened
externally with `O` on the selected comment in the TUI. In the comment box, `Ctrl-V`
pastes a clipboard image: it writes a temp PNG ($TMPDIR, OS-tidied — the loop is tight,
so long-term retention isn't needed) and inserts the `[[path]]` link for you. You can also
type any `[[path]]` by hand to attach a log, file, or screenshot to feedback.

## Boundaries

- Recording and reading reviews is local and reversible — safe inside the deadman loop.
- `recap add` (new work), `recap review show` (read feedback), and `recap revise`
  (attach a fix-forward diff + resolve the review) are the loop's core verbs.
  Submitting reviews (`recap review submit`) is the **human reviewer's** action (via the
  TUI or CLI), never the loop's — never self-review.
- The review db (`$RECAP_DB` or `~/.config/recap/recap.db`) is private to the reviewer and
  cross-repo: never commit it, never push it, never surface its contents publicly.

See the `deadman-todo` skill for the loop's safety boundary, TODO path rules,
falsifiable-criteria rules, and the per-task commit policy that this layers on top of.
