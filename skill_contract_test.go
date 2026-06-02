package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	for _, want := range []string{"add", "review", "--sha", "--parent", "resolve", "submit"} {
		if !strings.Contains(out, want) {
			t.Errorf("recap help missing %q the skill depends on:\n%s", want, out)
		}
	}
}

// loop record verb: commit then `recap add --sha HEAD`.
func TestSkillContract_AddWithSHA(t *testing.T) {
	db := contractDB(t)
	out := mustRun(t, db, "add",
		"--repo", "demo", "--repo-path", "/tmp/demo",
		"--title", "task one", "--criterion", "builds",
		"--check", "go build ./...", "--result", "PASS", "--sha", "abc1234")
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
	mustRun(t, db, "add", "--repo", "demo", "--repo-path", "/tmp/demo", "--title", "orig", "--sha", "aaa")
	out := mustRun(t, db, "add", "--repo", "demo", "--repo-path", "/tmp/demo",
		"--title", "fix", "--parent", "1", "--sha", "bbb")
	if !strings.Contains(out, "fixes #1") {
		t.Fatalf("add --parent did not link to #1:\n%s", out)
	}
}

// the full inbox->fix-forward contract the loop drives:
// review ls --state submitted, review show, review resolve.
func TestSkillContract_ReviewDrainAndResolve(t *testing.T) {
	db := contractDB(t)
	mustRun(t, db, "add", "--repo", "demo", "--repo-path", "/tmp/demo",
		"--title", "reviewable", "--criterion", "builds", "--sha", "abc1234")

	// a human submits a review (TUI/CLI) — the loop does NOT do this, but we
	// simulate it so we can assert the loop's read/resolve path.
	mustRun(t, db, "review", "comment", "1", "--body", "tighten this", "--file", "x.go", "--line", "10")
	mustRun(t, db, "review", "submit", "1", "--verdict", "request_changes", "--summary", "do the thing")

	// loop step: drain submitted reviews
	drained := mustRun(t, db, "review", "ls", "--state", "submitted")
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
	if after := mustRun(t, db, "review", "ls", "--state", "submitted"); strings.Contains(after, "request_changes") {
		t.Fatalf("resolved review still appears as submitted:\n%s", after)
	}
}
