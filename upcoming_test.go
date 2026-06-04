package main

import (
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
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

// the upcoming section reflows on resize: its divider (HRule) spans the container,
// so the SAME template re-executed at a wider size yields a wider rule — it isn't
// stuck at its initial sizing. Build once, execute twice (a real resize), and
// also activate the If at the narrow size first (the section loads async, so it
// first appears at whatever width is current, then the terminal is resized).
func TestUpcomingExpandsWithWidth(t *testing.T) {
	prevHas, prevItems := hasUpcoming, upcomingItems
	t.Cleanup(func() { hasUpcoming, upcomingItems = prevHas, prevItems })
	hasUpcoming = false
	upcomingItems = buildUpcomingRows("", []string{"alpha", "beta"})

	node := VBox.Fill(cPaneBG)(
		If(&hasUpcoming).Then(VBox.PaddingTRBL(0, 2, 1, 3).Gap(1)(
			Text("upcoming").FG(cSubtle).Bold(),
			VBox(ForEach(&upcomingItems, func(r *upcomingRow) Component { return Text(&r.Line).FG(&r.FG) })),
			HRule().FG(cMuted),
		)),
	)
	tmpl := Build(node) // built ONCE — re-executed at each width like a live resize
	rule := func(width int) int {
		buf := NewBuffer(width, 12)
		tmpl.Execute(buf, int16(width), 12)
		best := 0
		for y := 0; y < 12; y++ {
			if n := strings.Count(buf.GetLine(y), "─"); n > best {
				best = n
			}
		}
		return best
	}
	_ = rule(40)       // If inactive
	hasUpcoming = true // section loads (async) at the narrow size
	narrow := rule(40) // first appears at width 40
	wide := rule(90)   // resize wider — same template
	if narrow == 0 {
		t.Fatalf("no HRule rendered at width 40")
	}
	if wide <= narrow {
		t.Fatalf("upcoming divider should widen when the same template is re-executed wider: width40=%d width90=%d", narrow, wide)
	}
}
