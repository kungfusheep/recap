// Command recap is the async review inbox for autonomous (tododo/deadman) work.
// The agent records each completed task; you review the queue later — out of
// band, out of git. Diffs live in git (pointed to by sha); this tool holds the
// private review layer (task, falsifiable check, result, verdict, thread).
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kungfusheep/recap/notify"
)

// skillGuide is the agent loop guide, embedded so it ships (and versions) with
// the binary. `recap skill` prints it; the on-disk skill is a thin wrapper that
// loads this, so the instructions can never drift from the installed version.
//
//go:embed skill.md
var skillGuide string

func main() {
	if len(os.Args) < 2 {
		// bare `recap` launches the reviewer TUI
		if err := runUI(); err != nil {
			fmt.Fprintln(os.Stderr, "recap:", err)
			os.Exit(1)
		}
		return
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "ui":
		err = runUI()
	case "add":
		err = cmdAdd(args)
	case "ls", "list":
		err = cmdLs(args)
	case "show":
		err = cmdShow(args)
	case "redo":
		err = cmdRedo(args)
	case "comment":
		err = cmdComment(args)
	case "reply":
		err = cmdReply(args)
	case "emote":
		err = cmdEmote(args)
	case "next":
		err = cmdNext(args)
	case "current":
		err = cmdCurrent(args)
	case "whoami":
		err = cmdWhoami(args)
	case "read":
		err = cmdRead(args)
	case "unread":
		err = cmdUnread(args)
	case "review":
		err = cmdReview(args)
	case "set":
		err = cmdSet(args)
	case "delete", "rm":
		err = cmdDelete(args)
	case "revise":
		err = cmdRevise(args)
	case "skill":
		fmt.Print(skillGuide)
	case "help", "-h", "--help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "recap: unknown command %q\n\n", cmd)
		usage(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "recap:", err)
		os.Exit(1)
	}
}

func usage(w *os.File) {
	fmt.Fprint(w, `recap — async review inbox for autonomous (tododo/deadman) work

The agent records each completed task here; you review the queue later, out of
band and out of git. Diffs stay in git (by sha); recap holds the review layer:
task, falsifiable check, result, verdict, and the you<->agent comment thread.

usage:
  recap add [flags]      record a completed task (run from inside the repo)
      --title T          short task title (required)
      --criterion C      the falsifiable success check, in words
      --check CMD        command that re-proves it (e.g. "go test -run X")
      --result R         observed result (e.g. PASS)
      --repo-path P      repo path        (default: git root of cwd)
      --repo NAME        short repo name  (default: basename of repo path)
      --sha SHA          commit to review (default: short HEAD)
      --status S         pending|approved|redo (default: pending)
      --parent ID        the task this one fixes forward (links lineage)
      --summary S        reviewer briefing shown atop the preview (not the commit msg)

  recap ls [flags]       list tasks (default: pending only)
      --status S         filter by status
      --repo NAME        filter by repo
      --all              show every status

  recap show <id> [--stat]
                         task metadata + thread + its diff (git show by sha)

  recap redo             the rework queue: tasks marked 'redo' with their
                         threads — run this at the START of each loop cycle

  recap comment <id> --who you|agent --body TEXT
  recap set <id> pending|approved|redo
  recap delete <id>...   remove task(s) and their reviews/comments (alias: rm)

  recap revise <id> [--sha SHA --summary TEXT]
                         attach a fix-forward diff to an EXISTING task (instead of
                         a new task): appends a revision, resolves the open
                         request_changes review, and returns the item to the inbox
                         for re-review. View revisions with 'o' in the TUI.

  recap review comment <task> --body TEXT [--file F --line N --anchor H --snippet S]
                         add a comment to the task's draft review
  recap review submit  <task> --verdict request_changes|approve|comment [--summary TEXT]
                         publish the draft; request_changes flips the task to
                         redo and drops a breadcrumb in the repo's TODO
  recap review show <review-id>
                         the agent's work order: verdict + summary + anchored
                         comments + the original criterion
  recap review resolve <review-id>
                         mark a review addressed (after a fix-forward commit)
  recap review discard <task>      drop the draft
  recap review ls [--state S] [--repo NAME] [--all]
                         inspect reviews (default: current repo only)

  recap skill            print the agent loop guide (for tododo/deadman-todo)
  recap help

db: $RECAP_DB or ~/.config/recap/recap.db
`)
}

// --- git helpers -----------------------------------------------------------

func git(dir string, args ...string) (string, error) {
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := c.Output()
	return strings.TrimSpace(string(out)), err
}

func gitTopLevel(dir string) (string, error) { return git(dir, "rev-parse", "--show-toplevel") }

// currentRepo is the basename of the cwd's git top-level — the default scope so a
// loop running in one project never sees another's tasks/comments. "" when not in a
// git repo (callers treat "" as "all repos"). --all opts out of the scoping.
func currentRepo() string {
	if cwd, err := os.Getwd(); err == nil {
		if top, err := gitTopLevel(cwd); err == nil {
			return filepath.Base(top)
		}
	}
	return ""
}

// currentRepoPath is the cwd's git top-level path (not just the basename) — needed
// to resolve the repo's TODO file for the todo tier of `recap next`. "" when not in
// a git repo.
func currentRepoPath() string {
	if cwd, err := os.Getwd(); err == nil {
		if top, err := gitTopLevel(cwd); err == nil {
			return top
		}
	}
	return ""
}
func gitShortHead(repo string) (string, error) {
	return git(repo, "rev-parse", "--short", "HEAD")
}

// resolveSHA turns any commit-ish (a ref like HEAD, a branch, or a short hash)
// into a concrete short hash, so a recorded review pins the EXACT commit. Without
// this, `--sha HEAD` stored the literal "HEAD" and `git show HEAD` always rendered
// the current tip — making every recorded diff drift to whatever HEAD is now.
func resolveSHA(repo, ref string) (string, error) {
	return git(repo, "rev-parse", "--short", ref)
}

// --- commands --------------------------------------------------------------

func cmdAdd(args []string) error {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	title := fs.String("title", "", "task title (required)")
	criterion := fs.String("criterion", "", "falsifiable success check")
	check := fs.String("check", "", "command that re-proves it")
	result := fs.String("result", "", "observed result (e.g. PASS)")
	repoPath := fs.String("repo-path", "", "repo path (default: git root of cwd)")
	repo := fs.String("repo", "", "short repo name (default: basename)")
	sha := fs.String("sha", "", "commit sha (default: short HEAD)")
	status := fs.String("status", StatusPending, "pending|approved|redo")
	parent := fs.Int64("parent", 0, "id of the task this fixes forward")
	summary := fs.String("summary", "", "reviewer briefing: what you did + why + what to watch (richer than the commit msg)")
	fs.Parse(args)

	if *title == "" {
		return fmt.Errorf("--title is required")
	}
	if *repoPath == "" {
		cwd, _ := os.Getwd()
		top, err := gitTopLevel(cwd)
		if err != nil {
			return fmt.Errorf("not in a git repo (pass --repo-path): %w", err)
		}
		*repoPath = top
	}
	if *repo == "" {
		*repo = filepath.Base(*repoPath)
	}
	if *sha == "" {
		*sha = "HEAD"
	}
	// resolve whatever ref was given (incl. the "HEAD" default) to a concrete hash,
	// so the recorded diff is pinned and doesn't drift as new commits land.
	if h, err := resolveSHA(*repoPath, *sha); err == nil {
		*sha = h
	}

	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	if *parent != 0 {
		if _, err := st.Get(*parent); err != nil {
			return fmt.Errorf("--parent: %w", err)
		}
	}
	id, err := st.Add(Task{
		Repo: *repo, RepoPath: *repoPath, SHA: *sha, Title: *title,
		Criterion: *criterion, CheckCmd: *check, Result: *result, Status: *status,
		ParentID: *parent, Summary: *summary,
	})
	if err != nil {
		return err
	}
	if *parent != 0 {
		fmt.Printf("recorded #%d  %s  %s  [%s]  (fixes #%d)\n", id, *repo, *title, *status, *parent)
	} else {
		fmt.Printf("recorded #%d  %s  %s  [%s]\n", id, *repo, *title, *status)
	}
	notify.Reload() // nudge any open TUI to refresh its inbox
	return nil
}

func statusGlyph(s string) string {
	switch s {
	case StatusApproved:
		return "✓"
	case StatusRedo:
		return "↻"
	default:
		return "●"
	}
}

func printTaskLine(t Task) {
	fmt.Printf("%-4d %s %-8s %-10s %s\n", t.ID, statusGlyph(t.Status), t.CreatedAt[11:], t.Repo, t.Title)
}

func cmdLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	status := fs.String("status", "", "filter by status")
	repo := fs.String("repo", "", "filter by repo")
	all := fs.Bool("all", false, "show every status")
	fs.Parse(args)

	filter := *status
	if filter == "" && !*all {
		filter = StatusPending
	}
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	tasks, err := st.List(filter, *repo)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		fmt.Println("(no tasks)")
		return nil
	}
	for _, t := range tasks {
		printTaskLine(t)
	}
	return nil
}

func parseID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid task id %q", s)
	}
	return id, nil
}

// splitID pulls a leading positional id off args so flags may follow it
// (Go's flag package stops at the first non-flag, so `show 1 --stat` would
// otherwise drop --stat). Falls back to flags-first form via the empty return.
func splitID(args []string) (id string, rest []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	statOnly := fs.Bool("stat", false, "show diffstat only, not the full diff")
	idStr, rest := splitID(args)
	fs.Parse(rest)
	if idStr == "" {
		idStr = fs.Arg(0)
	}
	if idStr == "" {
		return fmt.Errorf("usage: recap show <id> [--stat]")
	}
	id, err := parseID(idStr)
	if err != nil {
		return err
	}

	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	t, err := st.Get(id)
	if err != nil {
		return err
	}

	fmt.Printf("#%d  %s  [%s]\n", t.ID, t.Title, t.Status)
	fmt.Printf("repo:   %s  (%s)\n", t.Repo, t.RepoPath)
	fmt.Printf("sha:    %s\n", t.SHA)
	fmt.Printf("when:   %s\n", t.CreatedAt)
	if t.Criterion != "" {
		fmt.Printf("check:  %s\n", t.Criterion)
	}
	if t.CheckCmd != "" {
		fmt.Printf("cmd:    %s\n", t.CheckCmd)
	}
	if t.Result != "" {
		fmt.Printf("result: %s\n", t.Result)
	}
	if t.Summary != "" {
		fmt.Printf("\nsummary:\n  %s\n", t.Summary)
	}

	comments, _ := st.Comments(id)
	if len(comments) > 0 {
		fmt.Println("\nthread:")
		for _, c := range comments {
			fmt.Printf("  %s (%s): %s\n", c.Who, c.CreatedAt, c.Body)
		}
	}

	if t.SHA != "" && t.RepoPath != "" {
		fmt.Println("\ndiff:")
		showArgs := []string{"show"}
		if *statOnly {
			showArgs = append(showArgs, "--stat")
		}
		showArgs = append(showArgs, t.SHA)
		out, err := git(t.RepoPath, showArgs...)
		if err != nil {
			fmt.Printf("  (could not read diff for %s: %v)\n", t.SHA, err)
		} else {
			fmt.Println(out)
		}
	}
	return nil
}

func cmdRedo(args []string) error {
	fs := flag.NewFlagSet("redo", flag.ExitOnError)
	allRepos := fs.Bool("all", false, "across all repos (default: current repo only)")
	fs.Parse(args)
	repo := ""
	if !*allRepos {
		repo = currentRepo() // scope to this project so a loop never drains another's
	}
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	// the rework queue is derived, not flag-driven: a task needs rework only while
	// its newest *submitted* review is request_changes and unresolved. Reading the
	// stale `status` field instead let resolved tasks linger here forever (and made
	// the CLI disagree with the TUI, which already uses ReviewState).
	all, err := st.List("", repo)
	if err != nil {
		return err
	}
	var tasks []Task
	for i := len(all) - 1; i >= 0; i-- { // List is id DESC; drain oldest first
		if st.ReviewState(all[i].ID) == StateRework {
			tasks = append(tasks, all[i])
		}
	}
	if len(tasks) == 0 {
		fmt.Println("(no rework queued)")
		return nil
	}
	for _, t := range tasks {
		printTaskLine(t)
		comments, _ := st.Comments(t.ID)
		for _, c := range comments {
			fmt.Printf("       %s: %s\n", c.Who, c.Body)
		}
	}
	return nil
}

func cmdComment(args []string) error {
	fs := flag.NewFlagSet("comment", flag.ExitOnError)
	who := fs.String("who", "", "you|agent")
	body := fs.String("body", "", "comment text")
	idStr, rest := splitID(args)
	fs.Parse(rest)
	if idStr == "" {
		idStr = fs.Arg(0)
	}
	if idStr == "" {
		return fmt.Errorf("usage: recap comment <id> --who you|agent --body TEXT")
	}
	id, err := parseID(idStr)
	if err != nil {
		return err
	}
	if *who != "you" && *who != "agent" {
		return fmt.Errorf("--who must be 'you' or 'agent'")
	}
	if *who == "agent" {
		*who = identityWho() // use the agent's session name if it has named itself
	}
	if *body == "" {
		return fmt.Errorf("--body is required")
	}
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	if _, err := st.AddComment(id, *who, *body); err != nil {
		return err
	}
	notify.Reload() // push: any open TUI shows the comment without a refresh
	fmt.Printf("commented on #%d\n", id)
	return nil
}

// cmdUnread lists reviewer comments the agent hasn't read yet — the loop's feedback
// inbox. Thread replies (recap reply / TUI 'r') don't bump a review's state, so they
// never show in `review ls`; this surfaces them with their [cN] ids so you can act.
func cmdUnread(args []string) error {
	fs := flag.NewFlagSet("unread", flag.ExitOnError)
	allRepos := fs.Bool("all", false, "across all repos (default: current repo only)")
	fs.Parse(args)
	repo := ""
	if !*allRepos {
		repo = currentRepo() // scope to this project so a loop never sees another's feedback
	}
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	cs, err := st.UnreadByAgent(repo)
	if err != nil {
		return err
	}
	if len(cs) == 0 {
		fmt.Println("no unread reviewer comments")
		return nil
	}
	fmt.Printf("unread reviewer comments (%d):\n", len(cs))
	for _, c := range cs {
		loc := "general"
		if c.File != "" {
			loc = c.File
			if c.Line > 0 {
				loc += fmt.Sprintf(":%d", c.Line)
			}
		}
		kind := "comment"
		if c.ParentID != 0 {
			kind = fmt.Sprintf("reply→c%d", c.ParentID)
		}
		fmt.Printf("  [c%d] task #%d · %s · %s\n      %s\n", c.ID, c.TaskID, loc, kind, c.Body)
	}
	fmt.Println("\n(act on each, then: recap read <comment-id> …  to clear the receipt)")
	return nil
}

// cmdRead records the agent's read-receipt on one or more comments (clears them
// from `recap unread`). Pushes so the user's open TUI fills the agent-read dot live.
func cmdRead(args []string) error {
	var ids []int64
	for _, a := range args {
		id, err := parseID(strings.TrimPrefix(a, "c"))
		if err != nil {
			return fmt.Errorf("usage: recap read <comment-id>…")
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return fmt.Errorf("usage: recap read <comment-id>…")
	}
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.MarkReadAgent(ids...); err != nil {
		return err
	}
	notify.Reload() // push: the user's open TUI fills the agent-read dot without a refresh
	fmt.Printf("marked %d comment(s) read\n", len(ids))
	return nil
}

// cmdWhoami sets (or shows) the agent's session identity — the name + personal
// colour it gives itself for the loop, so its review comments read as a distinct
// voice. Recap-only; never in commits. With no args it prints the current identity
// and any configured name-theme hint.
func cmdWhoami(args []string) error {
	// hand-parse so --color works whether it comes before or after the name (Go's
	// flag package stops at the first positional, which would swallow it).
	var color string
	var nameParts []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--color" || a == "-color":
			if i+1 < len(args) {
				color = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--color="):
			color = strings.TrimPrefix(a, "--color=")
		case strings.HasPrefix(a, "-color="):
			color = strings.TrimPrefix(a, "-color=")
		default:
			nameParts = append(nameParts, a)
		}
	}
	name := strings.TrimSpace(strings.Join(nameParts, " "))
	if name == "" && color == "" {
		cur, c := loadIdentity()
		if cur == "" {
			fmt.Println("agent has not named itself yet")
		} else {
			fmt.Printf("agent: %s  (colour %02X%02X%02X)\n", cur, c.R, c.G, c.B)
		}
		if cfg, _ := LoadConfig(); cfg.NameTheme != "" {
			fmt.Printf("name theme: %s\n", cfg.NameTheme)
		}
		return nil
	}
	if err := saveIdentity(name, color); err != nil {
		return err
	}
	notify.Reload() // push: the name/colour shows in any open TUI without a refresh
	fmt.Printf("named: %s\n", name)
	return nil
}

// cmdCurrent shows the in-flight item without advancing — what `recap next` last
// handed out (and hasn't been completed). "(idle)" when there's nothing.
func cmdCurrent(args []string) error {
	ref, title := loadCurrent(currentRepo())
	if ref == "" {
		fmt.Println("(idle — recap next to take work)")
		return nil
	}
	fmt.Printf("▸ %s  %s\n", ref, title)
	return nil
}

// cmdEmote sets a reaction emote on a comment — the agent acknowledging reviewer
// feedback at a glance (e.g. recap emote 12 👍), without a full reply. Pass an
// empty emote to clear it.
func cmdEmote(args []string) error {
	fs := flag.NewFlagSet("emote", flag.ExitOnError)
	emoteFlag := fs.String("emote", "", "reaction (e.g. 👍, 👀, ✅); empty clears")
	idStr, rest := splitID(args)
	fs.Parse(rest)
	positional := fs.Args()
	if idStr == "" { // no flag-style id: take the first positional as the id
		if len(positional) == 0 {
			return fmt.Errorf("usage: recap emote <comment-id> <emote>  (ids show as [cN] in review show)")
		}
		idStr, positional = positional[0], positional[1:]
	}
	commentID, err := parseID(idStr)
	if err != nil {
		return fmt.Errorf("usage: recap emote <comment-id> <emote>  (ids show as [cN] in review show)")
	}
	emote := *emoteFlag
	if emote == "" { // positional form: recap emote <id> 👍
		emote = strings.TrimSpace(strings.Join(positional, " "))
	}
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.SetEmote(commentID, emote); err != nil {
		return err
	}
	notify.Reload() // push: the emote appears in any open TUI without a refresh
	fmt.Printf("emoted %s on comment #%d\n", emote, commentID)
	return nil
}

// cmdReply records a reply to a specific comment (threading). The agent uses this
// to answer reviewer feedback in place; the reply nests under the comment in
// `recap review show` and the comments pane. who defaults to "agent".
func cmdReply(args []string) error {
	fs := flag.NewFlagSet("reply", flag.ExitOnError)
	who := fs.String("who", "agent", "you|agent")
	body := fs.String("body", "", "reply text")
	idStr, rest := splitID(args)
	fs.Parse(rest)
	if idStr == "" {
		idStr = fs.Arg(0)
	}
	if idStr == "" {
		return fmt.Errorf("usage: recap reply <comment-id> --body TEXT [--who you|agent]")
	}
	commentID, err := parseID(idStr)
	if err != nil {
		return err
	}
	if *who != "you" && *who != "agent" {
		return fmt.Errorf("--who must be 'you' or 'agent'")
	}
	if *who == "agent" {
		*who = identityWho() // use the agent's session name if it has named itself
	}
	if *body == "" {
		return fmt.Errorf("--body is required")
	}
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	if _, err := st.AddReply(commentID, *who, *body); err != nil {
		return err
	}
	notify.Reload() // push: the reply threads into any open TUI without a refresh
	fmt.Printf("replied to comment #%d\n", commentID)
	return nil
}

// cmdDelete removes one or more tasks (and their reviews/comments). It's a
// user-invoked verb — the autonomous loop never deletes — so it acts directly,
// reporting each task it removed by title for an audit trail.
func cmdDelete(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: recap delete <id>... (alias: rm)")
	}
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	var deleted int
	for _, a := range args {
		id, err := parseID(a)
		if err != nil {
			return err
		}
		t, err := st.Get(id)
		if err != nil {
			return err
		}
		if err := st.Delete(id); err != nil {
			return err
		}
		fmt.Printf("deleted #%d  %s  %s\n", t.ID, t.Repo, t.Title)
		deleted++
	}
	if deleted > 0 {
		notify.Reload() // nudge any open TUI to drop the removed task(s)
	}
	return nil
}

// cmdRevise attaches a fix-forward diff to an existing task instead of recording a
// separate child task — so one item carries its whole change history rather than
// the inbox filling with near-duplicate entries. It appends the revision, resolves
// the open request_changes review (returning the item to the inbox via derived
// state), and nudges any open TUI.
func cmdRevise(args []string) error {
	fs := flag.NewFlagSet("revise", flag.ExitOnError)
	sha := fs.String("sha", "", "the fix-forward commit (default: short HEAD of the task's repo)")
	summary := fs.String("summary", "", "what changed in this revision (shown with its diff)")
	idStr, rest := splitID(args)
	fs.Parse(rest)
	if idStr == "" {
		idStr = fs.Arg(0)
	}
	if idStr == "" {
		return fmt.Errorf("usage: recap revise <id> [--sha SHA --summary TEXT]")
	}
	id, err := parseID(idStr)
	if err != nil {
		return err
	}
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	t, err := st.Get(id)
	if err != nil {
		return err
	}
	if *sha == "" {
		*sha = "HEAD"
	}
	// pin the ref to a concrete hash (see resolveSHA) so the revision diff is fixed.
	if h, err := resolveSHA(t.RepoPath, *sha); err == nil {
		*sha = h
	}
	if *sha == "" || *sha == "HEAD" {
		return fmt.Errorf("--sha required (could not resolve HEAD for %s)", t.RepoPath)
	}
	if _, err := st.AddRevision(id, *sha, *summary); err != nil {
		return err
	}
	resolved, err := st.resolveOpenRequestChanges(id)
	if err != nil {
		return err
	}
	if resolved != 0 {
		fmt.Printf("revised #%d  +%s  (review #%d addressed → back to inbox)\n", id, *sha, resolved)
	} else {
		fmt.Printf("revised #%d  +%s\n", id, *sha)
	}
	notify.Reload()
	return nil
}

// --- review --------------------------------------------------------------

func cmdReview(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: recap review comment|submit|show|resolve|discard|ls …")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "comment":
		return cmdReviewComment(rest)
	case "submit":
		return cmdReviewSubmit(rest)
	case "show":
		return cmdReviewShow(rest)
	case "resolve":
		return cmdReviewResolve(rest)
	case "discard":
		return cmdReviewDiscard(rest)
	case "ls", "list":
		return cmdReviewLs(rest)
	default:
		return fmt.Errorf("recap review: unknown subcommand %q", sub)
	}
}

func cmdReviewComment(args []string) error {
	fs := flag.NewFlagSet("review comment", flag.ExitOnError)
	file := fs.String("file", "", "file the comment anchors to")
	line := fs.Int("line", 0, "line within the diff")
	anchor := fs.String("anchor", "", "hunk header for context")
	snippet := fs.String("snippet", "", "the diff line(s) commented on")
	body := fs.String("body", "", "comment text (required)")
	idStr, rest := splitID(args)
	fs.Parse(rest)
	if idStr == "" {
		idStr = fs.Arg(0)
	}
	if idStr == "" {
		return fmt.Errorf("usage: recap review comment <task> --body TEXT [--file F --line N --anchor H --snippet S]")
	}
	id, err := parseID(idStr)
	if err != nil {
		return err
	}
	if *body == "" {
		return fmt.Errorf("--body is required")
	}
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	if _, err := st.AddReviewComment(id, "you", *body, *file, *line, *anchor, *snippet); err != nil {
		return err
	}
	where := "general"
	if *file != "" {
		where = fmt.Sprintf("%s:%d", *file, *line)
	}
	fmt.Printf("draft comment on #%d (%s)\n", id, where)
	return nil
}

func cmdReviewSubmit(args []string) error {
	fs := flag.NewFlagSet("review submit", flag.ExitOnError)
	verdict := fs.String("verdict", "", "request_changes|approve|comment (required)")
	summary := fs.String("summary", "", "overall note — what to change")
	idStr, rest := splitID(args)
	fs.Parse(rest)
	if idStr == "" {
		idStr = fs.Arg(0)
	}
	if idStr == "" {
		return fmt.Errorf("usage: recap review submit <task> --verdict request_changes|approve|comment [--summary TEXT]")
	}
	id, err := parseID(idStr)
	if err != nil {
		return err
	}
	if *verdict == "" {
		return fmt.Errorf("--verdict is required (request_changes|approve|comment)")
	}
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	rv, res, err := submitReview(st, id, *verdict, *summary)
	if err != nil {
		return err
	}
	fmt.Printf("submitted review #%d on task #%d  [%s]\n", rv.ID, id, rv.Verdict)
	if res.line != "" {
		if res.wrote {
			fmt.Printf("queued in %s:\n  %s\n", res.path, res.line)
		} else {
			if res.err != nil {
				fmt.Fprintf(os.Stderr, "recap: could not write TODO (%v)\n", res.err)
			}
			fmt.Printf("add to your TODO:\n  %s\n", res.line)
		}
	}
	notify.Reload()
	return nil
}

// todoResult reports what submitReview did with the TODO breadcrumb.
type todoResult struct {
	line  string // the breadcrumb (empty if none was due, e.g. approve/comment)
	path  string // resolved TODO path (empty if no template configured)
	wrote bool   // whether the line was appended
	err   error  // write error, if any
}

// submitReview publishes the task's draft review and, for request_changes, drops
// the TODO breadcrumb into the repo's human-owned TODO. Shared by the CLI and
// the TUI so both behave identically.
func submitReview(st *Store, taskID int64, verdict, summary string) (Review, todoResult, error) {
	rv, err := st.SubmitReview(taskID, verdict, summary)
	if err != nil {
		return Review{}, todoResult{}, err
	}
	var res todoResult
	if rv.Verdict == VerdictRequestChanges {
		t, _ := st.Get(taskID)
		res.line = todoBreadcrumb(rv, t)
		cfg, cerr := LoadConfig()
		path, perr := cfg.todoPathFor(t.RepoPath)
		if cerr == nil && perr == nil && path != "" {
			res.path = path
			if e := appendTODO(path, res.line); e != nil {
				res.err = e
			} else {
				res.wrote = true
			}
		}
	}
	return rv, res, nil
}

func cmdReviewShow(args []string) error {
	idStr, _ := splitID(args)
	if idStr == "" {
		return fmt.Errorf("usage: recap review show <review-id>")
	}
	id, err := parseID(idStr)
	if err != nil {
		return err
	}
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	rv, err := st.GetReview(id)
	if err != nil {
		return err
	}
	t, err := st.Get(rv.TaskID)
	if err != nil {
		return err
	}

	fmt.Printf("review #%d on task #%d  ·  %s  ·  %s  ·  sha %s\n",
		rv.ID, t.ID, strings.ToUpper(strings.ReplaceAll(rv.Verdict, "_", " ")), t.Repo, t.SHA)
	fmt.Printf("task:      %s\n", t.Title)
	if t.Criterion != "" {
		fmt.Printf("criterion: %s   [original]\n", t.Criterion)
	}
	if rv.Summary != "" {
		fmt.Printf("\nsummary (what to change):\n  %s\n", rv.Summary)
	}

	comments, _ := st.ReviewComments(rv.ID)
	if top, byParent := splitThread(comments); len(top) > 0 {
		fmt.Printf("\ncomments (%d):\n", len(top))
		for _, c := range top {
			if c.File != "" {
				loc := c.File
				if c.Anchor != "" {
					loc += "  " + c.Anchor
				}
				if c.Line > 0 {
					loc += fmt.Sprintf("  (line %d)", c.Line)
				}
				fmt.Printf("  ┌ [c%d] %s\n", c.ID, loc)
				if c.Snippet != "" {
					fmt.Printf("  │   %s\n", c.Snippet)
				}
				fmt.Printf("  └ %s%s\n", c.Body, emoteSuffix(c))
			} else {
				fmt.Printf("  • [c%d] %s%s\n", c.ID, c.Body, emoteSuffix(c))
			}
			printReplies(c.ID, byParent, 0)
			fmt.Println()
		}
	}

	// also surface any loose thread comments on the task (review_id NULL). These
	// were historically invisible here — never hide a comment again.
	if thread, _ := st.Comments(t.ID); len(thread) > 0 {
		var loose []Comment
		for _, c := range thread {
			if c.ReviewID == 0 {
				loose = append(loose, c)
			}
		}
		if top, byParent := splitThread(loose); len(top) > 0 {
			fmt.Printf("\nthread (%d):\n", len(top))
			for _, c := range top {
				fmt.Printf("  • [c%d] %s (%s): %s%s\n", c.ID, c.Who, c.CreatedAt, c.Body, emoteSuffix(c))
				printReplies(c.ID, byParent, 0)
			}
		}
	}
	return nil
}

// splitThread separates top-level comments from replies, returning the top-level
// ones (in order) and a parent_id → replies index for nesting. A reply whose
// parent isn't in the set (shouldn't happen) is treated as top-level so it's
// never hidden.
func splitThread(cs []Comment) (top []Comment, byParent map[int64][]Comment) {
	byParent = map[int64][]Comment{}
	present := map[int64]bool{}
	for _, c := range cs {
		present[c.ID] = true
	}
	for _, c := range cs {
		if c.ParentID != 0 && present[c.ParentID] {
			byParent[c.ParentID] = append(byParent[c.ParentID], c)
		} else {
			top = append(top, c)
		}
	}
	return top, byParent
}

// printReplies renders a comment's reply subtree, indented by depth (general
// threading: a reply can itself have replies).
func printReplies(parentID int64, byParent map[int64][]Comment, depth int) {
	for _, r := range byParent[parentID] {
		fmt.Printf("%s↳ [c%d] %s (%s): %s%s\n", strings.Repeat("  ", depth+2), r.ID, r.Who, r.CreatedAt, r.Body, emoteSuffix(r))
		printReplies(r.ID, byParent, depth+1)
	}
}

// emoteSuffix renders a comment's reaction as a trailing "  👍" (or "" if none).
func emoteSuffix(c Comment) string {
	if c.Emote == "" {
		return ""
	}
	return "  " + c.Emote
}

func cmdReviewResolve(args []string) error {
	idStr, _ := splitID(args)
	if idStr == "" {
		return fmt.Errorf("usage: recap review resolve <review-id>")
	}
	id, err := parseID(idStr)
	if err != nil {
		return err
	}
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.ResolveReview(id); err != nil {
		return err
	}
	fmt.Printf("review #%d resolved\n", id)
	notify.Reload()
	return nil
}

func cmdReviewDiscard(args []string) error {
	idStr, _ := splitID(args)
	if idStr == "" {
		return fmt.Errorf("usage: recap review discard <task>")
	}
	id, err := parseID(idStr)
	if err != nil {
		return err
	}
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.DiscardReview(id); err != nil {
		return err
	}
	fmt.Printf("discarded draft review on #%d\n", id)
	return nil
}

func cmdReviewLs(args []string) error {
	fs := flag.NewFlagSet("review ls", flag.ExitOnError)
	state := fs.String("state", "", "filter by state (draft|submitted|resolved)")
	repo := fs.String("repo", "", "filter by repo (default: current repo)")
	all := fs.Bool("all", false, "show reviews across all repos")
	fs.Parse(args)

	// scope to the current repo by default, so a loop draining reviews only sees
	// its own. --repo overrides; --all shows everything.
	if !*all && *repo == "" {
		if cwd, err := os.Getwd(); err == nil {
			if top, err := gitTopLevel(cwd); err == nil {
				*repo = filepath.Base(top)
			}
		}
	}
	if *all {
		*repo = ""
	}

	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	reviews, err := st.ListReviews(*state, *repo)
	if err != nil {
		return err
	}
	if len(reviews) == 0 {
		fmt.Println("(no reviews)")
		return nil
	}
	for _, rv := range reviews {
		fmt.Printf("#%-4d task #%-4d %-16s %-10s %s\n", rv.ID, rv.TaskID, rv.State, rv.Verdict, rv.Summary)
	}
	return nil
}

func cmdSet(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: recap set <id> pending|approved|redo")
	}
	id, err := parseID(args[0])
	if err != nil {
		return err
	}
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.SetStatus(id, args[1]); err != nil {
		return err
	}
	fmt.Printf("#%d -> %s\n", id, args[1])
	notify.Reload()
	return nil
}
