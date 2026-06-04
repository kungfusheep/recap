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
