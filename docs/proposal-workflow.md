# Proposals: formalising cross-repo work agreement

The rule today is convention: an agent wanting work in another repo proposes it
via `recap send`, the owner queues agreed work, and for API changes a proposal
file goes in front of the human (the tui flow). This formalises that into a
first-class recap object — reviewable like everything else, without littering
repos with development artifacts.

## Shape (matching the sketch in todo:779ba797)

- **A proposal is an inbox item whose "diff" is a document.** It lives in the
  recap db (a `proposals` table or a task `kind`), NOT in any repo — the body
  renders in the detail pane through the existing briefing markup (headings,
  bullets, `code`, bold). No repo artifacts until sign-off.
- **`recap propose`** creates it from the proposing repo:

  ```
  recap propose --target tui --title "PreserveBG write mode" \
      --file proposal.md --tag mail,calendar
  ```

  `--target` is the repo that would own the work; `--tag` notifies reviewers.
- **Tags ride the message queue.** Each tagged repo (and the target's loop)
  gets a durable message: "proposal #N awaits your review — recap proposal
  show N · recap proposal comment N --body …". Same delivery, read receipts,
  and parking semantics as every other message — nothing new to learn.
- **Comments fan out to interested parties.** A comment on a proposal is a
  normal threaded comment; on write, recap fans a notification message to all
  CURRENT parties (proposer, target, tagged, prior commenters). `@repo` in a
  comment body adds that repo as a party and delivers it an invite message —
  joining mid-thread is one mention.
- **The human signs off; agents never do.** Proposals appear in the inbox in
  their own section; the verdict verbs are the review ones (`approve` /
  `request_changes` / comment). This keeps the standing guardrail: messages
  and proposals coordinate, only the human approves.
- **Sign-off materialises exactly two artifacts:**
  1. an ADR written into the TARGET repo (`docs/adr/NNN-title.md` — front
     matter: status, proposer, parties, recap proposal id; body: the accepted
     document + a decision line). The one sanctioned repo artifact, created
     only after the decision — the repo records the outcome, recap holds the
     deliberation.
  2. a todo line appended to the MANAGING repo's TODO ("implement ADR NNN:
     title — see docs/adr/NNN-title.md"). This is consistent with the
     cross-repo barrier: sign-off IS the owner+human consent that `recap
     todo --open` otherwise gates.
- **Declined proposals** keep their thread in recap (searchable deliberation
  history), write nothing anywhere.

## What it reuses (almost everything)

| Need | Existing machinery |
|---|---|
| Durable delivery + waking parked loops | messages + notify |
| Threaded discussion, read receipts | review comments |
| Multi-party identity on comments | per-repo identities (`whoami`) |
| Document rendering | the briefing markup renderer |
| Inbox presence, verdicts | task list + review verbs |
| Owner consent for resulting work | sign-off replaces `todo --open` for this path |

New surface: the `proposals` rows, three CLI verbs (`propose`, `proposal
show`, `proposal comment` — sign-off via the existing review verbs), the fan-
out-on-comment hook, the @mention parser, the ADR writer, and an inbox
section + doc-rendering detail mode.

## Open questions for steer

1. **Storage** — ANSWERED (c426): a separate `proposals` table, for room to
   grow. Landed in slice 1.
2. **Who is "the managing agent"?** — ANSWERED (c427): the agent doing the
   work in that repo — i.e. the TARGET repo's loop. Sign-off appends the todo
   to the target repo's TODO; no assignment flag.
3. **ADR numbering**: per-repo sequence scanned from `docs/adr/` at write
   time, or recap-global? Lean: per-repo scan — repos stay self-consistent.
4. **Comment fan-out volume**: every comment to every party could get noisy
   on a hot proposal. Batch per parking-wake (the queue already coalesces),
   or fan every comment? Lean: fan everything, let the queue coalesce.
5. **Does the tui proposal-file flow migrate?** The 001-oscillators pile
   predates this; presumably new proposals use recap and in-flight ones
   finish as files. Glyph Smith should weigh in (tagged in spirit).

## Slices (each lands reviewable, in order)

1. db: proposal rows + parties; `recap propose` + `proposal show` (CLI-only,
   no TUI) — usable via messages immediately. **DONE** (proposals table,
   propose/show/ls verbs, tag delivery via the message queue, DecideProposal
   ready for slice 4).
2. tag delivery + comment fan-out + @mentions. **DONE** (`proposal comment`
   threads on the proposal, fans to every other party via the queue, @repo
   joins + invites; commenting itself makes you a party).
3. TUI: inbox section + document detail rendering.
4. sign-off: ADR writer + managing-repo todo append.
5. skill section + broadcast.
