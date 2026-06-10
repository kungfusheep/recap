# recap

recap makes the best use of your time *and* your agents' time: agents work
continuously and record each finished piece here; you review the whole stream
later, at your own pace, from one inbox.

Diffs stay in git (each item points at a sha). recap holds the review layer:
the task, what would prove it works, your verdict, the comment threads, and any
agent↔agent messages. It is cross-repo and private: a personal PR queue, stored
outside every project.

```
recap            # the reviewer TUI
recap skill      # the agent loop guide (embedded in the binary)
recap help       # the full command surface
```

## How it flows

1. An agent finishes a piece of work, commits it, and records it
   (`recap add --sha HEAD`) with a short briefing for you.
2. It immediately takes the next item with `recap next`. When the queue is
   empty it parks (`recap next --wait`) and wakes when work lands.
3. You open the TUI when it suits you, read the diffs, and leave verdicts and
   comments.
4. Your feedback becomes the agent's highest-priority work on its next cycle.

Comments are the steering wheel: they redirect architecture as easily as they
flag bugs.

## The pieces

| Piece | What it is |
|---|---|
| **Task** | One finished unit: title, a check that could fail if it's broken, the command that re-proves it, and the sha. Recording refuses a sha the repo can't resolve (`--force` to override); a bad sha would show an empty diff forever. |
| **Review** | Your verdict (`request_changes` / `approve` / `comment`) plus comments, some anchored to diff lines. Drafts accumulate; submit publishes them all at once. |
| **Revision** | A fix-forward diff added to the *same* task (`recap revise`), so one item carries its whole history. Cycle the diffs with `o`. |
| **Message** | A note from one agent's loop to another repo's. It queues in the db; delivery waits until a loop runs there. |

## The agent loop

`recap next` is the single intake. It returns the most important item first:

1. **amends**: tasks you sent back with `request_changes`
2. **replies**: your unread comments
3. **messages**: notes from other agents
4. **todos**: the next line of the repo's TODO file

```
recap next                # take the next item
recap next --wait         # queue empty → park; wakes when work lands
recap next --skip "why"   # can't do it → noted on the item, move on
recap current             # peek without advancing
```

**Completion is per-kind.** `recap done` ticks a todo, `recap revise` answers an
amend, `recap read c<N>` / `m<N>` clears comments and messages. The queue is
first-come-first-served by arrival; an item coming back from amends rejoins at
the end.

**Agents name themselves per repo** (`recap whoami "Name" --color "#RRGGBB"`).
The name appears on comments and messages; it never reaches git.

## The reviewer TUI

Three panes: inbox (left), diff (middle), comments (right). `?` shows the full
cheatsheet; the essentials:

| Key | Does |
|---|---|
| `h` / `l` / `Tab` | move focus between panes |
| `j` / `k` | navigate within a pane |
| `↵` | open the selected item's diff |
| `f` / `p` / `u` | filter by repo / pin / undo the last action |
| `o` | expand a task's revisions, cycle its diffs |
| `z` / `Z` · `]` / `[` | fold a file / all files · jump between files |
| `c` / `e` | comment on a line / open that line in `$EDITOR` |
| `r` | reply in a thread |
| `O` / Ctrl-V | open a `[[file]]` attachment / paste a screenshot as one |
| `m` | the agent message ledger |
| `Space` / `^P` | command palette (20 themes, switchable live) |

The diff pane shows the agent's briefing first, then the parsed diff: syntax
highlighting that follows the theme, renames as `old → new`, and a loud
"commit not found" banner for an unresolvable sha.

In the message ledger, `r` comments on the selected message; your note goes
straight into that agent's queue. The header badges unread peer traffic
(`✉ N`).

## Agents talking to each other

Loops in different repos coordinate directly, and you see all of it:

```
recap send <repo> --body "…"              # durable note for that repo's loop
recap send <repo> --reply-to N --body "…" # thread a reply
recap send --listeners --body "…"         # broadcast to every parked loop
recap listeners                           # who's parked right now
recap messages [--all]                    # the two-way ledger
recap read m<N>                           # clear a received message
```

Messages address a **repo, not a process**; one sent with nobody listening
waits in the queue. **Messages coordinate; they never approve.** Verdicts stay
yours.

## Storage & privacy

One SQLite db at `$RECAP_DB`, else `~/.config/recap/recap.db`, plus small state
files beside it. Cross-repo by design. **Never commit or push it.** It is your
private layer.

Optional config at `$RECAP_CONFIG`, else `~/.config/recap/config.toml`:

```toml
# where each repo's TODO lives ({relpath} = repo path relative to $HOME);
# feeds recap next's todo tier and the TUI's upcoming section
todo_template = "~/notes/{relpath}/TODO.md"

# optional hint for agent self-naming (e.g. "birds", "greek")
name_theme = "birds"
```

## Build & test

```
go build ./...
go test ./...
go install .
```

The agent guide is embedded in the binary (`recap skill`) so it can't drift
from the installed surface, and `skill_contract_test.go` pins the CLI contract
the loop depends on. Keep the guide, the tests, and `recap help` in sync.
