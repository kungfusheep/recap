package main

import "testing"

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

// buildUpcomingRows flares the first (in-progress / next) item with a ▸ marker and
// the rest with a plain bullet, so the next-to-be-worked task stands out.
func TestBuildUpcomingRowsFlare(t *testing.T) {
	rows := buildUpcomingRows([]string{"first", "second", "third"})
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0].Marker != "▸ " {
		t.Fatalf("first marker = %q, want ▸ (in-progress flare)", rows[0].Marker)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].Marker != "· " {
			t.Fatalf("row %d marker = %q, want · ", i, rows[i].Marker)
		}
	}
	if buildUpcomingRows(nil) == nil {
		// make(...,0) returns non-nil empty slice; just assert it's empty
		t.Fatal("expected non-nil empty slice")
	}
	if len(buildUpcomingRows(nil)) != 0 {
		t.Fatal("empty input should give empty rows")
	}
}
