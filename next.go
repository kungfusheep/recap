package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/kungfusheep/recap/cursor"
	"github.com/kungfusheep/recap/db"
	"github.com/kungfusheep/recap/snooze"
	"hash/fnv"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kungfusheep/recap/config"
	"github.com/kungfusheep/recap/notify"
	"github.com/kungfusheep/recap/poll"
	"github.com/kungfusheep/recap/todo"
)

const (
	waitInterval = time.Second      // how often `recap next --wait` re-checks the queue
	waitTimeout  = 30 * time.Minute // how long it parks before returning idle
)

// recap next is the protocol form of "what do I work on": it returns the next work
// item across the whole priority order (amends → unread replies → todos) and records
// it as the current in-flight item (driving the flare) — so going in-flight is a
// side-effect of taking work, never a thing the agent has to declare. While an item is
// in flight, a bare `recap next` is non-mutating: it refuses to advance and points at
// `recap current` (re-inspect), so a harmless re-check never skips work. Only
// `recap next --skip "reason"` walks past an unfinished item, recording the reason so
// the reviewer sees it was passed. A parked todo cursor still re-prioritises to new
// amends/replies automatically (that's not a skip).

// WorkItem is one unit of work, of any tier. Ref is its stable cursor id.
type WorkItem struct {
	Kind      string // "amends" | "reply" | "todo"
	Repo      string
	TaskID    int64  // amends/reply
	ReviewID  int64  // amends: the open request_changes review (for `recap review show`)
	CommentID int64  // reply / message id
	From      string // message sender repo (Kind "message") — the reply target
	Title     string // display text (task title / comment body / todo line)
	Ref       string // stable cursor id, e.g. "amends:50" / "reply:73" / "todo:9f2a"
}

// buildQueue assembles the priority-ordered work list for a repo: amends first
// (tasks with an open request_changes review), then unread reviewer replies (on
// tasks not already in amends), then the next incomplete todos. repoPath is the
// repo's filesystem path (for the TODO file); pass "" to skip the todo tier.
func buildQueue(st *db.Store, repo, repoPath string) []WorkItem {
	var q []WorkItem
	amendsTasks := map[int64]bool{}

	// 1. amends — tasks needing rework (derived, like the redo queue), oldest first.
	if tasks, err := st.List("", repo); err == nil {
		for i := len(tasks) - 1; i >= 0; i-- { // List is id DESC; oldest first
			t := tasks[i]
			if st.ReviewState(t.ID) == db.StateRework {
				amendsTasks[t.ID] = true
				q = append(q, WorkItem{Kind: "amends", Repo: t.Repo, TaskID: t.ID,
					ReviewID: st.ReworkReviewID(t.ID),
					Title:    t.Title, Ref: fmt.Sprintf("amends:%d", t.ID)})
			}
		}
	}

	// 2. replies/comments — EVERY unread reviewer comment, except those on a task
	// that's already an amends item (its review's comments ride with it). This must
	// include top-level comments (parent_id == 0), not just threaded replies — else a
	// plain reviewer comment on a non-amends task is silently dropped from the intake.
	if cs, err := st.UnreadByAgent(repo); err == nil {
		for _, c := range cs {
			if amendsTasks[c.TaskID] {
				continue // covered by the amends work order (recap review show lists its comments)
			}
			q = append(q, WorkItem{Kind: "reply", Repo: repo, TaskID: c.TaskID, CommentID: c.ID,
				Title: firstLine(c.Body), Ref: fmt.Sprintf("reply:%d", c.ID)})
		}
	}

	// 3. peer messages — agent→agent notes addressed to THIS repo, FIFO. They sit
	// below human feedback (amends/replies) but above fresh todos, and complete via
	// recap read m<N>. Durable: a message sent while no loop runs here simply waits.
	if ms, err := st.UnreadMessages(repo); err == nil {
		for _, m := range ms {
			q = append(q, WorkItem{Kind: "message", Repo: repo, CommentID: m.ID, From: m.FromRepo,
				Title: fmt.Sprintf("%s@%s: %s", m.FromWho, m.FromRepo, firstLine(m.Body)),
				Ref:   fmt.Sprintf("msg:%d", m.ID)})
		}
	}

	// 3. todos — the next incomplete todo lines from the repo's TODO file, minus any
	// snoozed by a recent --skip (so a blocked todo doesn't keep the queue hot and
	// defeat `recap next --wait`'s park; snoozes expire, so it'll re-surface later).
	if repoPath != "" {
		snoozed := snooze.Load(repo)
		if cfg, err := config.LoadConfig(); err == nil {
			if path, err := todo.PathFor(cfg.TODOTemplate, repoPath); err == nil && path != "" {
				if items, err := todo.Read(path); err == nil {
					for _, it := range items {
						if !it.IsTask || it.Done {
							continue
						}
						text := strings.TrimSpace(it.Text)
						ref := fmt.Sprintf("todo:%08x", fnvHash(text))
						if snoozed[ref] {
							continue
						}
						q = append(q, WorkItem{Kind: "todo", Repo: repo, Title: text, Ref: ref})
					}
				}
			}
		}
	}
	return q
}

// advance picks the next work item given the current cursor. The queue is tier-ordered
// (amends → replies → todos). With no current (or it's been completed and is gone), it
// returns the highest-priority item. Otherwise it walks forward past the current (a
// skip), wrapping at the end — EXCEPT it never lets a parked todo cursor hide
// higher-priority work: if the current is a todo but amends/replies exist, it leads with
// the highest (not a skip — the queue re-prioritised, the agent didn't pass the todo).
func advance(q []WorkItem, currentRef string) (next WorkItem, skipped bool, ok bool) {
	if len(q) == 0 {
		return WorkItem{}, false, false
	}
	idx := -1
	for i, w := range q {
		if w.Ref == currentRef {
			idx = i
			break
		}
	}
	if idx < 0 {
		return q[0], false, true // current gone (completed) or unset → highest priority
	}
	// amends/replies must never wait behind a parked todo cursor (they sort before
	// todos, so anything earlier than a todo current is higher-priority): lead with it.
	if q[idx].Kind == "todo" && idx > 0 {
		return q[0], false, true
	}
	skipped = true // current is still in the queue → we're passing it without completing
	return q[(idx+1)%len(q)], skipped, true
}

// cmdNext is the protocol form of "what do I work on". It builds the repo's priority
// queue (amends → replies → todos), hands out the current/next item, records it as the
// in-flight cursor (which drives the flare), and prints the work order. While an item is
// in flight a bare call is non-mutating (refuses to skip, points at `recap current`);
// only --skip "reason" advances past unfinished work, recording why on the skipped item.
func cmdNext(args []string) error {
	fs := flag.NewFlagSet("next", flag.ExitOnError)
	skipReason := fs.String("skip", "", "reason this item is being skipped (recorded on it)")
	dryRun := fs.Bool("dry-run", false, "preview the next item without advancing the cursor")
	wait := fs.Bool("wait", false, "long-poll: when nothing is queued, block until work appears instead of exiting the loop")
	fs.Parse(args)

	st, err := db.Open()
	if err != nil {
		return err
	}
	defer st.Close()

	repo := currentRepo()
	q := buildQueue(st, repo, currentRepoPath())
	curRef, _ := cursor.Load(repo)
	cur := findRef(q, curRef) // the in-flight item, if its cursor still points at live work
	next, skipped, ok := advance(q, curRef)

	// dry run: show what advancing WOULD hand out, but touch nothing — no cursor
	// move, no skip recorded, no push. Pure preview ("what's next" without claiming it).
	if *dryRun {
		if !ok {
			fmt.Println("(nothing to work on — inbox + todos are clear)")
			return nil
		}
		fmt.Println("(dry run — cursor unchanged)")
		printWorkOrder(next)
		return nil
	}

	// plain `recap next` must NOT move the cursor while the current item is still in
	// flight — a re-run to re-inspect should never mutate queue state. advance() flags
	// `skipped` only when it would walk PAST a live current (not when a parked todo
	// cursor re-prioritises to higher work — that proceeds). So: a would-be skip with no
	// reason is refused; only `recap next --skip "reason"` advances past unfinished work.
	if skipped && *skipReason == "" {
		fmt.Println("blocked: current item is still in flight — cursor unchanged")
		printWorkOrder(cur)
		fmt.Println("  recap current — re-inspect · recap next --skip \"reason\" — skip it · recap revise/done/read — finish it")
		return nil
	}

	if !ok {
		if *wait {
			// hard loop: an empty queue is "wait", not "done". Park until work appears
			// (so a request_changes from the reviewer brings the agent back to life).
			next, ok = waitForWork(st, repo)
			if !ok {
				return nil // timed out or canceled — message already printed
			}
			skipped = false
		} else {
			cursor.Save(repo, "", "")
			notify.Reload()
			fmt.Println("(nothing to work on — inbox + todos are clear)")
			return nil
		}
	}

	// a deliberate --skip past an unfinished item: record the reason on it (when it has a
	// task) so the reviewer sees it was passed, not silently dropped.
	if skipped {
		if cur.TaskID != 0 {
			st.AddComment(cur.TaskID, identityWho(), "⤳ skipped (still open): "+*skipReason)
		} else if strings.HasPrefix(cur.Ref, "todo:") {
			// a todo has no task to comment on — snooze it so it leaves the queue and
			// `recap next --wait` can park instead of re-handing it forever.
			snooze.Record(repo, cur.Ref)
		}
		fmt.Printf("skipped %s — %s\n", cur.Title, *skipReason)
	}

	if err := cursor.Save(repo, next.Ref, next.Title); err != nil {
		return err
	}
	notify.Reload()
	printWorkOrder(next)
	return nil
}

// waitForWork long-polls until the repo's queue has something, then returns the head.
// On timeout or Ctrl-C it prints why and returns ok=false. This is what keeps an idle
// agent IN the loop (`recap next --wait`) instead of exiting it: the blocked call returns
// the moment the reviewer's request_changes — or any new work — lands.
func waitForWork(st *db.Store, repo string) (WorkItem, bool) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	fmt.Fprintf(os.Stderr, "waiting for work (--wait, up to %s; Ctrl-C to stop)…\n", waitTimeout)
	switch poll.Wait(ctx, waitInterval, waitTimeout, func() bool {
		return len(buildQueue(st, repo, currentRepoPath())) > 0
	}) {
	case poll.TimedOut:
		fmt.Printf("(idle — no work after %s; recap next --wait to keep waiting)\n", waitTimeout)
		return WorkItem{}, false
	case poll.Canceled:
		fmt.Println("(stopped)")
		return WorkItem{}, false
	}
	// Ready: recompute against the fresh queue + cursor and hand out the head.
	q := buildQueue(st, repo, currentRepoPath())
	curRef, _ := cursor.Load(repo)
	next, _, ok := advance(q, curRef)
	return next, ok
}

// cmdDone is the explicit completion for a TODO item: name the item by its ref (from
// recap next), and recap both records it in the review inbox (title auto-filled from
// the todo text, so you review the finished work — the whole point) AND marks the todo
// line done in the file. The agent never opens the TODO file itself. amends/replies
// have their own explicit completions (revise / read), so done errors and points there.
func cmdDone(args []string) error {
	fs := flag.NewFlagSet("done", flag.ExitOnError)
	criterion := fs.String("criterion", "", "falsifiable success check")
	check := fs.String("check", "", "command that re-proves it")
	result := fs.String("result", "", "observed result (e.g. PASS)")
	summary := fs.String("summary", "", "reviewer briefing for the inbox item")
	sha := fs.String("sha", "", "commit sha (default: short HEAD)")
	ref, rest := splitID(args)
	fs.Parse(rest)
	if ref == "" {
		return fmt.Errorf("usage: recap done <ref> --summary \"…\" --sha HEAD   (ref from recap next, e.g. todo:abc12345)")
	}

	st, err := db.Open()
	if err != nil {
		return err
	}
	defer st.Close()

	repo := currentRepo()
	repoPath := currentRepoPath()
	item := findRef(buildQueue(st, repo, repoPath), ref)
	switch {
	case item.Ref == "":
		return fmt.Errorf("no current item with ref %q — run `recap next` to see the queue", ref)
	case item.Kind == "amends":
		return fmt.Errorf("#%d is an amends — complete it with: recap revise %d --summary \"…\"", item.TaskID, item.TaskID)
	case item.Kind == "reply":
		return fmt.Errorf("c%d is a reply — clear it with: recap read c%d", item.CommentID, item.CommentID)
	case item.Kind == "message":
		return fmt.Errorf("m%d is a peer message — clear it with: recap read m%d (reply: recap send %s --reply-to %d --body \"…\")", item.CommentID, item.CommentID, item.From, item.CommentID)
	}

	// todo: record the finished work for review (title = the todo text) ...
	resolved := *sha
	if resolved == "" {
		resolved = "HEAD"
	}
	if h, err := resolveSHA(repoPath, resolved); err == nil {
		resolved = h
	}
	id, err := st.Add(db.Task{
		Repo: repo, RepoPath: repoPath, SHA: resolved, Title: item.Title,
		Criterion: *criterion, CheckCmd: *check, Result: *result,
		Status: db.StatusPending, Summary: *summary,
	})
	if err != nil {
		return err
	}

	// ... and mark the todo line [x] in the file (surgical: only the matching line).
	if cfg, err := config.LoadConfig(); err == nil {
		if path, err := todo.PathFor(cfg.TODOTemplate, repoPath); err == nil && path != "" {
			if err := markTodoLineDone(path, item.Title); err != nil {
				fmt.Fprintf(os.Stderr, "recap: recorded #%d but couldn't mark the TODO line (%v)\n", id, err)
			}
		}
	}
	if cur, _ := cursor.Load(repo); cur == ref {
		cursor.Save(repo, "", "") // drop the flare immediately; next recap next advances
	}
	notify.Reload()
	fmt.Printf("done #%d → inbox: %s\n", id, item.Title)
	return nil
}

// markTodoLineDone flips the single open todo line whose text matches, in place —
// it rewrites only that line (not the whole file) so nothing else gets reformatted.
func markTodoLineDone(path, text string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	ts := time.Now().Format("2006-01-02 15:04:05 MST")
	for i, ln := range lines {
		it, ok := todo.ParseLine(ln)
		if ok && it.IsTask && !it.Done && strings.TrimSpace(it.Text) == text {
			indent := ln[:len(ln)-len(strings.TrimLeft(ln, " \t"))]
			lines[i] = indent + "- [x] " + it.Text + "  done " + ts
			return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
		}
	}
	return fmt.Errorf("no matching open todo line for %q", text)
}

// printWorkOrder shows the item plus the verbs to act on it, by tier.
func printWorkOrder(w WorkItem) {
	switch w.Kind {
	case "amends":
		fmt.Printf("▸ amends   #%d  %s\n", w.TaskID, w.Title)
		show := "recap review show <id>"
		if w.ReviewID != 0 {
			show = fmt.Sprintf("recap review show %d", w.ReviewID)
		}
		fmt.Printf("  %s · fix forward · recap revise %d --summary \"…\"\n", show, w.TaskID)
	case "reply":
		fmt.Printf("▸ reply    c%d  %q  (task #%d)\n", w.CommentID, w.Title, w.TaskID)
		fmt.Printf("  recap reply %d --body \"…\"  ·  recap read c%d\n", w.CommentID, w.CommentID)
	case "message":
		fmt.Printf("▸ message  m%d  %s\n", w.CommentID, w.Title)
		fmt.Printf("  recap read m%d  ·  reply: recap send %s --reply-to %d --body \"…\"\n", w.CommentID, w.From, w.CommentID)
	case "todo":
		fmt.Printf("▸ todo   %s  %s\n", w.Ref, w.Title)
		fmt.Printf("  when finished: recap done %s --criterion \"…\" --check \"…\" --result PASS --summary \"…\" --sha HEAD\n", w.Ref)
	}
}

func findRef(q []WorkItem, ref string) WorkItem {
	for _, w := range q {
		if w.Ref == ref {
			return w
		}
	}
	return WorkItem{}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func fnvHash(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}
