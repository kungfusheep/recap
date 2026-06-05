package main

import (
	"flag"
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"time"

	"github.com/kungfusheep/recap/notify"
)

// recap next is the protocol form of "what do I work on": it returns the next work
// item across the whole priority order (amends → unread replies → todos), records
// it as the current in-flight item (driving the flare), and advances on each call —
// so going in-flight is a side-effect of taking work, never a thing the agent has to
// remember to declare. Calling next again walks past the current (a skip); a skip of
// something not completed can carry a reason so the reviewer sees why it was passed.

// WorkItem is one unit of work, of any tier. Ref is its stable cursor id.
type WorkItem struct {
	Kind      string // "amends" | "reply" | "todo"
	Repo      string
	TaskID    int64  // amends/reply
	CommentID int64  // reply
	Title     string // display text (task title / comment body / todo line)
	Ref       string // stable cursor id, e.g. "amends:50" / "reply:73" / "todo:9f2a"
}

// buildQueue assembles the priority-ordered work list for a repo: amends first
// (tasks with an open request_changes review), then unread reviewer replies (on
// tasks not already in amends), then the next incomplete todos. repoPath is the
// repo's filesystem path (for the TODO file); pass "" to skip the todo tier.
func buildQueue(st *Store, repo, repoPath string) []WorkItem {
	var q []WorkItem
	amendsTasks := map[int64]bool{}

	// 1. amends — tasks needing rework (derived, like the redo queue), oldest first.
	if tasks, err := st.List("", repo); err == nil {
		for i := len(tasks) - 1; i >= 0; i-- { // List is id DESC; oldest first
			t := tasks[i]
			if st.ReviewState(t.ID) == StateRework {
				amendsTasks[t.ID] = true
				q = append(q, WorkItem{Kind: "amends", Repo: t.Repo, TaskID: t.ID,
					Title: t.Title, Ref: fmt.Sprintf("amends:%d", t.ID)})
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

	// 3. todos — the next incomplete todo lines from the repo's TODO file.
	if repoPath != "" {
		if cfg, err := LoadConfig(); err == nil {
			if path, err := cfg.todoPathFor(repoPath); err == nil && path != "" {
				if items, err := readTodo(path); err == nil {
					for _, it := range items {
						if !it.IsTask || it.Done {
							continue
						}
						text := strings.TrimSpace(it.Text)
						q = append(q, WorkItem{Kind: "todo", Repo: repo, Title: text,
							Ref: fmt.Sprintf("todo:%08x", fnvHash(text))})
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
// queue (amends → replies → todos), advances the cursor past the current item, records
// the new current (which drives the flare), and prints the work order. Calling it again
// walks past the current — a skip; --skip "reason" records why on the skipped item so
// the reviewer sees it wasn't silently dropped.
func cmdNext(args []string) error {
	fs := flag.NewFlagSet("next", flag.ExitOnError)
	skipReason := fs.String("skip", "", "reason this item is being skipped (recorded on it)")
	dryRun := fs.Bool("dry-run", false, "preview the next item without advancing the cursor")
	fs.Parse(args)

	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()

	repo := currentRepo()
	q := buildQueue(st, repo, currentRepoPath())
	curRef, curTitle := loadCurrent(repo)
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

	// passing an item that's still in the queue = a skip (not a completion). Capture
	// the reason on it if given; otherwise nudge so the next skip carries one.
	if skipped {
		prev := findRef(q, curRef)
		switch {
		case *skipReason != "" && prev.TaskID != 0:
			st.AddComment(prev.TaskID, identityWho(), "⤳ skipped (still open): "+*skipReason)
			notify.Reload()
			fmt.Printf("skipped %s — %s\n", curTitle, *skipReason)
		case *skipReason != "":
			fmt.Printf("skipped %s — %s\n", curTitle, *skipReason)
		default:
			fmt.Printf("note: advanced past %q without completing it (recorded as a skip).\n", curTitle)
			fmt.Printf("      to re-inspect WITHOUT advancing use `recap current` (or `recap review show <id>`); pass --skip \"why\" to record a reason\n")
		}
	}

	if !ok {
		saveCurrent(repo, "", "")
		notify.Reload()
		fmt.Println("(nothing to work on — inbox + todos are clear)")
		return nil
	}

	if err := saveCurrent(repo, next.Ref, next.Title); err != nil {
		return err
	}
	notify.Reload()
	printWorkOrder(next)
	return nil
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

	st, err := Open()
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
	}

	// todo: record the finished work for review (title = the todo text) ...
	resolved := *sha
	if resolved == "" {
		resolved = "HEAD"
	}
	if h, err := resolveSHA(repoPath, resolved); err == nil {
		resolved = h
	}
	id, err := st.Add(Task{
		Repo: repo, RepoPath: repoPath, SHA: resolved, Title: item.Title,
		Criterion: *criterion, CheckCmd: *check, Result: *result,
		Status: StatusPending, Summary: *summary,
	})
	if err != nil {
		return err
	}

	// ... and mark the todo line [x] in the file (surgical: only the matching line).
	if cfg, err := LoadConfig(); err == nil {
		if path, err := cfg.todoPathFor(repoPath); err == nil && path != "" {
			if err := markTodoLineDone(path, item.Title); err != nil {
				fmt.Fprintf(os.Stderr, "recap: recorded #%d but couldn't mark the TODO line (%v)\n", id, err)
			}
		}
	}
	if cur, _ := loadCurrent(repo); cur == ref {
		saveCurrent(repo, "", "") // drop the flare immediately; next recap next advances
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
		it, ok := parseTodoLine(ln)
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
		fmt.Printf("  recap review show <id> · fix forward · recap revise %d --summary \"…\"\n", w.TaskID)
	case "reply":
		fmt.Printf("▸ reply    c%d  %q  (task #%d)\n", w.CommentID, w.Title, w.TaskID)
		fmt.Printf("  recap reply %d --body \"…\"  ·  recap read c%d\n", w.CommentID, w.CommentID)
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
