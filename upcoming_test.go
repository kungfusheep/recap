package main

import (
	"strings"
	"testing"
)

// upcomingFromItems surfaces the next incomplete tasks in file order, skips done
// + non-task lines, caps at upcomingMax, and keeps the full text (the row clips to
// the column width at render time — no hard-coded truncation).
func TestUpcomingFromItems(t *testing.T) {
	items := []todoItem{
		{Raw: "# heading"},                          // non-task: skipped
		{IsTask: true, Done: true, Text: "shipped"}, // done: skipped
		{IsTask: true, Text: "first"},
		{IsTask: true, Text: "second"},
		{IsTask: true, Done: true, Text: "also done"},
		{IsTask: true, Text: "third"},
		{IsTask: true, Text: "fourth"},
		{IsTask: true, Text: "fifth"},
		{IsTask: true, Text: "sixth — should be cut by the cap"}, // beyond upcomingMax
	}
	got := upcomingFromItems(items)
	want := []string{"first", "second", "third", "fourth", "fifth"}
	if len(got) != len(want) {
		t.Fatalf("got %d items, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d = %q, want %q", i, got[i], want[i])
		}
	}

	// long text is kept in full (not hard-truncated) — the Text clips to the column
	// width at render time, so a wider display shows more.
	full := "this task text is definitely longer than the column allows for sure"
	g := upcomingFromItems([]todoItem{{IsTask: true, Text: full}})
	if g[0] != full {
		t.Fatalf("text should be kept in full (no truncation), got %q", g[0])
	}
}

// buildUpcomingRows renders the TODO tasks as plain bulleted rows (the in-flight
// marker is rendered separately with a spinner, not as a row here).
func TestBuildUpcomingRows(t *testing.T) {
	rows := buildUpcomingRows([]string{"first", "second", "third"})
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	for i, r := range rows {
		if !strings.HasPrefix(r.Line, "· ") {
			t.Fatalf("row %d line = %q, want '· ' prefix", i, r.Line)
		}
	}
	if len(buildUpcomingRows(nil)) != 0 {
		t.Fatal("empty input should give empty rows")
	}
}

// the flare now reads the cursor title directly (recap next sets it) — no store
// lookup. The spinner shows whatever recap next recorded, and clears when idle.
func TestCurrentTitleFlare(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RECAP_DB", dir+"/recap.db")

	if currentTitle("wed") != "" {
		t.Fatalf("idle → empty flare, got %q", currentTitle("wed"))
	}
	saveCurrent("wed", "todo:abcd1234", "fix the width issue on the right")
	if got := currentTitle("wed"); got != "fix the width issue on the right" {
		t.Fatalf("flare = %q", got)
	}
	saveCurrent("wed", "", "")
	if currentTitle("wed") != "" {
		t.Fatalf("cleared → empty flare, got %q", currentTitle("wed"))
	}
}
