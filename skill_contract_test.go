package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kungfusheep/recap/db"
)

// These tests pin the exact CLI surface the `recap` skill instructs the
// tododo/deadman loop to use. The skill is intentionally thin and tells the
// agent to discover commands via `recap help`, but the loop's three verbs
// (add --sha, review ls --state submitted, review show, review resolve) and the
// fix-forward lineage (add --parent) are load-bearing — if a flag is renamed or
// dropped, the loop breaks silently. This is the falsifiable guard: it shells
// out to the built binary exactly as the skill does.
//
// If you change recap's CLI, update the recap skill (~/.claude/skills/recap and
// ~/.codex/skills/recap) and this test together.

var recapBin string

func TestMain(m *testing.M) {
	// the detail pane's banner/comments/diff load runs on a goroutine in
	// production (detailKick → fetchDetail → staged swap); under test it runs
	// inline so a single refreshDetail() lands the full effect deterministically.
	detailKick = func(t db.Task, row taskVM, key string, reset bool) {
		applyDetail(fetchDetail(t, row, key, reset))
	}
	dir, err := os.MkdirTemp("", "recap-bin")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	recapBin = filepath.Join(dir, "recap")
	build := exec.Command("go", "build", "-o", recapBin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		panic("build recap for contract tests: " + err.Error() + "\n" + string(out))
	}
	os.Exit(m.Run())
}

// run invokes the built binary against an isolated db and returns combined
// output + error, mirroring how the skill shells out.
func run(t *testing.T, db string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(recapBin, args...)
	cmd.Env = append(os.Environ(), "RECAP_DB="+db)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustRun(t *testing.T, db string, args ...string) string {
	t.Helper()
	out, err := run(t, db, args...)
	if err != nil {
		t.Fatalf("recap %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func contractDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "recap.db")
}

// the skill's bootstrap step relies on `recap help` succeeding and listing the
// verbs the loop uses.
func TestSkillContract_HelpListsLoopVerbs(t *testing.T) {
	db := contractDB(t)
	out := mustRun(t, db, "help")
	for _, want := range []string{"add", "review", "--sha", "--parent", "--summary", "resolve", "submit"} {
		if !strings.Contains(out, want) {
			t.Errorf("recap help missing %q the skill depends on:\n%s", want, out)
		}
	}
}

// the reviewer briefing the loop always passes (--summary) round-trips and shows
// in `recap show`.
func TestSkillContract_AddWithSummary(t *testing.T) {
	db := contractDB(t)
	mustRun(t, db, "add", "--repo", "demo", "--repo-path", "/tmp/demo",
		"--title", "t", "--sha", "abc", "--force", "--summary", "streaming parser; watch EOF")
	out := mustRun(t, db, "show", "1")
	if !strings.Contains(out, "streaming parser; watch EOF") {
		t.Fatalf("recap show omits the summary briefing:\n%s", out)
	}
}

// the on-disk wrapper skill loads the embedded guide via `recap skill`; it must
// print and name the loop's verbs (this is the source of truth the agent reads).
func TestSkillContract_SkillGuide(t *testing.T) {
	db := contractDB(t)
	out := mustRun(t, db, "skill")
	if !strings.Contains(out, "recap add") || !strings.Contains(out, "recap review show") {
		t.Errorf("recap skill missing the loop verbs:\n%s", out)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("recap skill printed nothing — embed broken")
	}
	// help should advertise the skill command so it's discoverable
	if h := mustRun(t, db, "help"); !strings.Contains(h, "skill") {
		t.Errorf("recap help does not mention the skill command:\n%s", h)
	}
}

// loop record verb: commit then `recap add --sha HEAD`.
func TestSkillContract_AddWithSHA(t *testing.T) {
	db := contractDB(t)
	out := mustRun(t, db, "add",
		"--repo", "demo", "--repo-path", "/tmp/demo",
		"--title", "task one", "--criterion", "builds",
		"--check", "go build ./...", "--result", "PASS", "--sha", "abc1234", "--force")
	if !strings.Contains(out, "recorded #1") {
		t.Fatalf("add --sha did not record task 1:\n%s", out)
	}
	if ls := mustRun(t, db, "ls", "--all"); !strings.Contains(ls, "task one") {
		t.Fatalf("recorded task not listed:\n%s", ls)
	}
}

// fix-forward lineage verb: a fix task links back to the task it fixes.
func TestSkillContract_AddWithParent(t *testing.T) {
	db := contractDB(t)
	mustRun(t, db, "add", "--repo", "demo", "--repo-path", "/tmp/demo", "--title", "orig", "--sha", "aaa", "--force")
	out := mustRun(t, db, "add", "--repo", "demo", "--repo-path", "/tmp/demo",
		"--title", "fix", "--parent", "1", "--sha", "bbb", "--force")
	if !strings.Contains(out, "fixes #1") {
		t.Fatalf("add --parent did not link to #1:\n%s", out)
	}
}

// the full inbox->fix-forward contract the loop drives:
// review ls --state submitted, review show, review resolve.
func TestSkillContract_ReviewDrainAndResolve(t *testing.T) {
	db := contractDB(t)
	mustRun(t, db, "add", "--repo", "demo", "--repo-path", "/tmp/demo",
		"--title", "reviewable", "--criterion", "builds", "--sha", "abc1234", "--force")

	// a human submits a review (TUI/CLI) — the loop does NOT do this, but we
	// simulate it so we can assert the loop's read/resolve path.
	mustRun(t, db, "review", "comment", "1", "--body", "tighten this", "--file", "x.go", "--line", "10")
	mustRun(t, db, "review", "submit", "1", "--verdict", "request_changes", "--summary", "do the thing")

	// loop step: drain submitted reviews
	drained := mustRun(t, db, "review", "ls", "--all", "--state", "submitted")
	if !strings.Contains(drained, "request_changes") {
		t.Fatalf("review ls --state submitted did not surface the review:\n%s", drained)
	}

	// loop step: read the work order
	order := mustRun(t, db, "review", "show", "1")
	for _, want := range []string{"do the thing", "x.go", "tighten this"} {
		if !strings.Contains(order, want) {
			t.Errorf("review show missing %q from the work order:\n%s", want, order)
		}
	}

	// loop step: resolve after fix-forward
	resolved := mustRun(t, db, "review", "resolve", "1")
	if !strings.Contains(resolved, "resolved") {
		t.Fatalf("review resolve did not confirm:\n%s", resolved)
	}
	if after := mustRun(t, db, "review", "ls", "--all", "--state", "submitted"); strings.Contains(after, "request_changes") {
		t.Fatalf("resolved review still appears as submitted:\n%s", after)
	}
}

// review ls scopes to a repo via --repo, so the loop only drains its own work.
func TestSkillContract_ReviewLsRepoScope(t *testing.T) {
	db := contractDB(t)
	mustRun(t, db, "add", "--repo", "alpha", "--repo-path", "/tmp/alpha", "--title", "a", "--sha", "a1", "--force")
	mustRun(t, db, "add", "--repo", "beta", "--repo-path", "/tmp/beta", "--title", "b", "--sha", "b1", "--force")
	mustRun(t, db, "review", "comment", "1", "--body", "x")
	mustRun(t, db, "review", "submit", "1", "--verdict", "request_changes")
	mustRun(t, db, "review", "comment", "2", "--body", "y")
	mustRun(t, db, "review", "submit", "2", "--verdict", "request_changes")

	alpha := mustRun(t, db, "review", "ls", "--repo", "alpha", "--state", "submitted")
	if !strings.Contains(alpha, "task #1") || strings.Contains(alpha, "task #2") {
		t.Fatalf("--repo alpha should show only task #1:\n%s", alpha)
	}
	all := mustRun(t, db, "review", "ls", "--all", "--state", "submitted")
	if !strings.Contains(all, "task #1") || !strings.Contains(all, "task #2") {
		t.Fatalf("--all should show both:\n%s", all)
	}
}

// the create-work verb: `recap todo "text"` appends an unchecked task to the
// repo's TODO file (resolved via todo_template). Cross-repo targeting is a
// MECHANICAL barrier: refused unless the TARGET's owner opted in with
// `recap todo --open` (run inside that repo) — the comms-model rule, enforced.
func TestSkillContract_TodoCreatesWork(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "recap.db")
	cfgPath := filepath.Join(dir, "config.toml")
	todoFile := filepath.Join(dir, "notes", "proj", "TODO.md")
	// a literal template (no {relpath}) resolves the same file for any repo —
	// enough for the contract; path templating is covered in todo's own tests.
	if err := os.WriteFile(cfgPath, []byte("todo_template = \""+todoFile+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "RECAP_DB="+db, "RECAP_CONFIG="+cfgPath)
	target := filepath.Join(dir, "proj")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, ga := range [][]string{{"init", "-q"}} {
		c := exec.Command("git", ga...)
		c.Dir = target
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", ga, err, out)
		}
	}

	run := func(dir string, args ...string) (string, error) {
		cmd := exec.Command(recapBin, args...)
		cmd.Env = env
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// cross-repo WITHOUT the target's opt-in: refused, pointing at the comms model
	out, err := run(".", "todo", "--repo-path", target, "sneaky cross-repo task")
	if err == nil {
		t.Fatalf("cross-repo todo without opt-in must be refused, got:\n%s", out)
	}
	if !strings.Contains(out, "recap send") {
		t.Fatalf("refusal should point at the comms model:\n%s", out)
	}
	if b, _ := os.ReadFile(todoFile); strings.Contains(string(b), "sneaky") {
		t.Fatalf("refused todo still landed:\n%s", b)
	}

	// the OWNER opts in from inside the target repo; cross-repo then lands
	if out, err := run(target, "todo", "--open"); err != nil {
		t.Fatalf("owner --open failed: %v\n%s", err, out)
	}
	if out, err := run(".", "todo", "--repo-path", target, "wire the new verb end to end"); err != nil {
		t.Fatalf("opted-in cross-repo todo failed: %v\n%s", err, out)
	}
	b, err := os.ReadFile(todoFile)
	if err != nil {
		t.Fatalf("TODO file not created: %v", err)
	}
	if !strings.Contains(string(b), "- [ ] wire the new verb end to end") {
		t.Fatalf("task not appended as an unchecked item:\n%s", b)
	}

	// own-repo adds never need opt-in (run from inside the target)
	if out, err := run(target, "todo", "own queue task"); err != nil {
		t.Fatalf("own-repo todo failed: %v\n%s", err, out)
	}
	b, _ = os.ReadFile(todoFile)
	if !strings.Contains(string(b), "- [ ] own queue task") {
		t.Fatalf("own-repo add lost:\n%s", b)
	}

	// --close revokes
	if out, err := run(target, "todo", "--close"); err != nil {
		t.Fatalf("owner --close failed: %v\n%s", err, out)
	}
	if out, err := run(".", "todo", "--repo-path", target, "should be refused again"); err == nil {
		t.Fatalf("cross-repo todo after --close must be refused:\n%s", out)
	}

	// help advertises the verb
	if h := mustRun(t, db, "help"); !strings.Contains(h, "recap todo") {
		t.Errorf("recap help does not mention the todo verb:\n%s", h)
	}
}

// slice 1 of the proposal workflow: propose opens a document under review,
// notifies parties through the message queue, and show/ls read it back.
func TestSkillContract_ProposalSlice1(t *testing.T) {
	dbPath := contractDB(t)
	out := mustRun(t, dbPath, "propose", "--target", "tui", "--title", "preserve bg",
		"--body", "## Problem\nplain writes drop the cell bg", "--tag", "mail")
	if !strings.Contains(out, "proposal #1 opened") {
		t.Fatalf("propose did not open #1:\n%s", out)
	}
	// the tagged + target repos got durable queue messages
	for _, repo := range []string{"tui", "mail"} {
		msgs := mustRun(t, dbPath, "messages", "--all")
		if !strings.Contains(msgs, "→ "+repo) || !strings.Contains(msgs, "proposal #1 awaits your review") {
			t.Fatalf("no notification message for %s:\n%s", repo, msgs)
		}
	}
	show := mustRun(t, dbPath, "proposal", "show", "1")
	for _, want := range []string{"OPEN", "preserve bg", "target:   tui", "plain writes drop the cell bg"} {
		if !strings.Contains(show, want) {
			t.Fatalf("proposal show missing %q:\n%s", want, show)
		}
	}
	if ls := mustRun(t, dbPath, "proposal", "ls"); !strings.Contains(ls, "preserve bg") {
		t.Fatalf("proposal ls missing the open item:\n%s", ls)
	}
	if h := mustRun(t, dbPath, "help"); !strings.Contains(h, "recap propose") {
		t.Errorf("help does not advertise propose:\n%s", h)
	}
}

// slice 2: comments thread on the proposal, fan to every other party through
// the queue, and @repo mentions join the conversation with an invite.
func TestSkillContract_ProposalComments(t *testing.T) {
	dbPath := contractDB(t)
	mustRun(t, dbPath, "propose", "--target", "tui", "--title", "preserve bg",
		"--body", "doc body", "--tag", "mail")
	out := mustRun(t, dbPath, "proposal", "comment", "1", "--body", "what about quantize? @calendar should weigh in")
	if !strings.Contains(out, "commented on proposal #1") {
		t.Fatalf("comment failed:\n%s", out)
	}
	// thread shows in proposal show
	show := mustRun(t, dbPath, "proposal", "show", "1")
	if !strings.Contains(show, "thread (1)") || !strings.Contains(show, "what about quantize?") {
		t.Fatalf("thread missing from show:\n%s", show)
	}
	// @calendar joined as a party and was invited
	if !strings.Contains(show, "calendar") {
		t.Fatalf("@mention did not join the parties:\n%s", show)
	}
	msgs := mustRun(t, dbPath, "messages", "--all")
	if !strings.Contains(msgs, "you were @mentioned on proposal #1") {
		t.Fatalf("no @mention invite in the queue:\n%s", msgs)
	}
	// the comment fanned to the other parties (tui + mail at least)
	if !strings.Contains(msgs, "[proposal #1]") {
		t.Fatalf("comment not fanned to parties:\n%s", msgs)
	}
}
