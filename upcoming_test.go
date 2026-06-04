package main

import (
	"strings"
	"testing"
)

// upcomingFromItems surfaces the next incomplete tasks in file order, skips done
// + non-task lines, caps at upcomingMax, and truncates long text with an ellipsis.
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

	long := []todoItem{{IsTask: true, Text: "this task text is definitely longer than the column allows for sure"}}
	g := upcomingFromItems(long)
	if r := []rune(g[0]); len(r) != upcomingTextLen || r[len(r)-1] != '…' {
		t.Fatalf("truncation wrong: len=%d last=%q (%q)", len(r), string(r[len(r)-1]), g[0])
	}
}

// buildUpcomingRows flares the EXPLICIT in-flight marker (▸, bright) at the top and
// lists the next TODO tasks with a plain bullet — the flare follows what the agent
// marked, not the list order.
func TestBuildUpcomingRowsFlare(t *testing.T) {
	rows := buildUpcomingRows("addressing review #176", []string{"first", "second", "third"})
	if len(rows) != 4 { // marker + 3 tasks
		t.Fatalf("got %d rows, want 4", len(rows))
	}
	if rows[0].Line != "▸ addressing review #176" {
		t.Fatalf("flare row = %q, want the in-flight marker", rows[0].Line)
	}
	for i := 1; i < len(rows); i++ {
		if !strings.HasPrefix(rows[i].Line, "· ") {
			t.Fatalf("row %d line = %q, want '· ' prefix", i, rows[i].Line)
		}
	}

	// no marker → no flare, just bulleted tasks (the flare isn't faked from item 0)
	plain := buildUpcomingRows("", []string{"first", "second"})
	if len(plain) != 2 {
		t.Fatalf("no-marker rows = %d, want 2", len(plain))
	}
	for i, r := range plain {
		if !strings.HasPrefix(r.Line, "· ") {
			t.Fatalf("no-marker row %d = %q, want plain bullet (no flare)", i, r.Line)
		}
	}
	if len(buildUpcomingRows("", nil)) != 0 {
		t.Fatal("empty input should give empty rows")
	}
}
