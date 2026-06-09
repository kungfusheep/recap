package main

import (
	"fmt"
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/todo"
)

// upcomingFromItems surfaces the next incomplete tasks in file order, skips done
// + non-task lines, caps at upcomingMax, and keeps the full text (the row clips to
// the column width at render time — no hard-coded truncation).
func TestUpcomingFromItems(t *testing.T) {
	items := []todo.Item{
		{Raw: "# heading"},                          // non-task: skipped
		{IsTask: true, Done: true, Text: "shipped"}, // done: skipped
		{IsTask: true, Text: "first"},
		{IsTask: true, Text: "second"},
		{IsTask: true, Done: true, Text: "also done"},
		{IsTask: true, Text: "third"},
		{IsTask: true, Text: "fourth"},
		{IsTask: true, Text: "fifth — should be cut by the cap"}, // beyond upcomingMax (4)
	}
	got := upcomingFromItems(items)
	want := []string{"first", "second", "third", "fourth"}
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
	g := upcomingFromItems([]todo.Item{{IsTask: true, Text: full}})
	if g[0] != full {
		t.Fatalf("text should be kept in full (no truncation), got %q", g[0])
	}
}

// buildUpcomingRows stores the raw task text + marks the in-flight row; the bullet/
// spinner prefix is added in buildUpcomingBlob.
func TestBuildUpcomingRows(t *testing.T) {
	inflight := fmt.Sprintf("todo:%08x", fnvHash("second"))
	rows := buildUpcomingRows([]string{"first", "second", "third"}, inflight)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0].Line != "first" || rows[1].Line != "second" {
		t.Fatalf("Line should be the raw text, got %q/%q", rows[0].Line, rows[1].Line)
	}
	if !rows[1].InFlight || rows[0].InFlight || rows[2].InFlight {
		t.Fatalf("only the matching ('second') row should be in-flight: %+v", rows)
	}
	if len(buildUpcomingRows(nil, "")) != 0 {
		t.Fatal("empty input should give empty rows")
	}
}

// buildUpcomingBlob renders the rows to one multi-line string: a "·" bullet per row,
// the in-flight row showing the current spinner frame so it animates in place.
func TestBuildUpcomingBlob(t *testing.T) {
	rows := []upcomingRow{{Line: "alpha"}, {Line: "beta", InFlight: true}}
	blob := buildUpcomingBlob(rows, 0)
	lines := strings.Split(blob, "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), blob)
	}
	if lines[0] != "· alpha" {
		t.Fatalf("non-flight row = %q, want '· alpha'", lines[0])
	}
	if !strings.HasSuffix(lines[1], " beta") || strings.HasPrefix(lines[1], "·") {
		t.Fatalf("in-flight row should have a spinner prefix (not '·'), got %q", lines[1])
	}

	// empty → a placeholder line (the section's fixed Height reserves the rest), never ""
	if got := buildUpcomingBlob(nil, 0); got != "· nothing upcoming" {
		t.Fatalf("empty upcoming blob = %q, want placeholder", got)
	}
}

// the upcoming section reserves a FIXED height (upcomingMax rows), so the inbox list
// below sits at the same screen row regardless of how many upcoming tasks the selected
// project has — no more "slapping about" when moving between projects (#1f3c631d).
func TestUpcomingSectionFixedHeight(t *testing.T) {
	prevStore, prevApp, prevOmni := uiStore, uiApp, omni
	st := testStore(t)
	uiStore = st
	uiApp = NewApp()
	omni = newOmniBox(uiApp, omniCommands())
	t.Cleanup(func() {
		uiStore, uiApp, omni = prevStore, prevApp, prevOmni
		vmRows = nil
		hasUpcoming = false
		upcomingBlob = ""
	})
	st.Add(Task{Repo: "r", RepoPath: "/tmp/r", Title: "INBOXMARKER", Status: StatusPending})
	reloadTasks()
	hasUpcoming = true
	upcomingWidth = 30

	inboxY := func() int {
		tmpl := Build(buildMain())
		buf := NewBuffer(120, 40)
		tmpl.Execute(buf, 120, 40)
		for y := 0; y < 40; y++ {
			if strings.Contains(buf.GetLine(y), "INBOXMARKER") {
				return y
			}
		}
		return -1
	}

	upcomingBlob = "· one\n· two"
	y2 := inboxY()
	upcomingBlob = "· one\n· two\n· three\n· four\n· five"
	y5 := inboxY()

	if y2 < 0 || y5 < 0 {
		t.Fatalf("inbox marker not rendered: y(2 upcoming)=%d, y(5 upcoming)=%d", y2, y5)
	}
	if y2 != y5 {
		t.Fatalf("inbox shifted with upcoming count: y(2)=%d vs y(5)=%d — section is not fixed height", y2, y5)
	}
}

// the inbox flare follows the cursor ref (re-marked when the async ref lands), and
// never sticks where a prior reload left it — the "spinner stuck on #73" bug. Revision
// child rows (RevIdx >= 0) never flare.
func TestMarkInFlight(t *testing.T) {
	defer func() { vmRows = nil; currentRef = "" }()
	vmRows = []taskVM{{ID: 73, RevIdx: -1}, {ID: 65, RevIdx: -1}, {ID: 65, RevIdx: 0}}

	currentRef = "amends:65"
	markInFlight()
	if vmRows[0].InFlight || !vmRows[1].InFlight {
		t.Fatalf("cursor amends:65 → only #65 header flares, got %v/%v", vmRows[0].InFlight, vmRows[1].InFlight)
	}
	if vmRows[2].InFlight {
		t.Fatal("revision child row must not flare")
	}
	// cursor moves → flare follows (not stuck on #73's neighbour)
	currentRef = "amends:73"
	markInFlight()
	if !vmRows[0].InFlight || vmRows[1].InFlight {
		t.Fatalf("flare should follow cursor to #73, got %v/%v", vmRows[0].InFlight, vmRows[1].InFlight)
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
