// Command recap is the async review inbox for autonomous (tododo/deadman) work.
// The agent records each completed task; you review the queue later — out of
// band, out of git. Diffs live in git (pointed to by sha); this tool holds the
// private review layer (task, falsifiable check, result, verdict, thread).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
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
	case "set":
		err = cmdSet(args)
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
func gitShortHead(repo string) (string, error) {
	return git(repo, "rev-parse", "--short", "HEAD")
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
		if h, err := gitShortHead(*repoPath); err == nil {
			*sha = h
		}
	}

	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	id, err := st.Add(Task{
		Repo: *repo, RepoPath: *repoPath, SHA: *sha, Title: *title,
		Criterion: *criterion, CheckCmd: *check, Result: *result, Status: *status,
	})
	if err != nil {
		return err
	}
	fmt.Printf("recorded #%d  %s  %s  [%s]\n", id, *repo, *title, *status)
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

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	statOnly := fs.Bool("stat", false, "show diffstat only, not the full diff")
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: recap show <id> [--stat]")
	}
	id, err := parseID(fs.Arg(0))
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
	st, err := Open()
	if err != nil {
		return err
	}
	defer st.Close()
	tasks, err := st.List(StatusRedo, "")
	if err != nil {
		return err
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
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: recap comment <id> --who you|agent --body TEXT")
	}
	id, err := parseID(fs.Arg(0))
	if err != nil {
		return err
	}
	if *who != "you" && *who != "agent" {
		return fmt.Errorf("--who must be 'you' or 'agent'")
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
	fmt.Printf("commented on #%d\n", id)
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
	return nil
}
