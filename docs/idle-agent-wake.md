# Waking an idle agent on `request_changes`

## Problem

An agent working the loop goes idle when `recap next` returns empty (queue drained).
If the reviewer later submits `request_changes` from the TUI, that creates an amends
item — but the idle agent doesn't know. Today it only finds out by being manually
re-run, or by polling `recap next` on a timer. We want the agent **brought back to
life automatically** the moment review feedback lands.

## The constraint that rules out the obvious answer

An LLM agent (Claude Code) is **not a daemon**. It can't catch a signal mid-idle and
resume reasoning from a handler. It only makes progress by *running a command, reading
its output, and continuing*. So the wake mechanism has to fit that shape: **the agent
must be parked inside a command that returns when work appears** — not waiting to be
poked from outside.

That immediately rejects the seductive option: registering the agent's pid and having
`recap review submit` send it `SIGUSR1` (the way it already pokes the TUI). The signal
would arrive, but there's no loop handler to turn it into "resume work." Pid-signalling
works for the TUI because the TUI *is* a daemon with a render loop; the agent isn't.

## Options

### A — Blocking intake: `recap next --wait`  ← recommended

The agent stops idling and instead parks in `recap next --wait`:

- queue non-empty → returns the head immediately (today's behaviour).
- queue empty → **blocks** until work appears, then returns the new item.

The reviewer submitting `request_changes` makes the queue non-empty; the blocked call
returns the amends; the agent resumes naturally — no special handler, just a command
that took a while to return.

Mechanism (realises the deferred *FIFO RPC: file for state, pipe for events* plan):

- a per-repo named pipe (FIFO), e.g. `~/.config/recap/wake-<repo>`.
- the waiter blocks reading the FIFO; on each wake it re-runs `buildQueue` and either
  returns the head or re-blocks (guards spurious/legacy wakes).
- the writers — `recap review submit --verdict request_changes`, `recap revise`, any
  path that adds agent-facing work — call `notify.Wake(repo)`: open the FIFO
  non-blocking, write one byte, drop it if no reader (idempotent, never blocks the
  writer). This sits right beside the existing `notify.Reload()` that pokes the TUI.
- a bounded timeout (`--wait=30m`, say): on expiry, return a distinct "idle, no work"
  status so the agent can re-arm or hand back to the user rather than block forever.
- fallback when a FIFO can't be created (odd FS, sandbox): poll `buildQueue` every few
  seconds. Less elegant, trivially robust, same external contract.

Pros: the only **push-based** option that matches how an agent actually runs; instant;
fully contained in recap (no external process tracking). Cons: a long-lived blocked
process (fine — it's the agent's turn, parked) and one new IPC primitive to maintain.

### B — Scheduled re-poll (agent-side, no recap change)

The idle agent arms a `ScheduleWakeup`/cron to re-run `recap next` every N minutes.
Pure polling. Pros: zero recap work; it's already how the deadman loop behaves. Cons:
up to N minutes of latency, not a real push, burns wake cycles for nothing most ticks.

### C — Signal the agent pid

Rejected — see the constraint above. An LLM agent has no resume-from-signal handler.

## Recommendation

**A.** It's the only mechanism that is both push-based and compatible with the agent's
run-a-command execution model, and it directly builds the FIFO RPC we already earmarked
as the next comms primitive. Shape it as: `recap next --wait[=timeout]` on the read
side, `notify.Wake(repo)` (FIFO poke, poll fallback) on the write side, called wherever
agent-facing work is created. Keep B in the back pocket as the zero-dependency fallback
the `--wait` timeout degrades into.

Implementation is a deliberate next step (it introduces blocking IPC), so this records
the *way*; the build is its own item once the approach is agreed.
