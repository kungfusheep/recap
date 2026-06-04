package main

import (
	"strings"
	"sync"

	. "github.com/kungfusheep/glyph"
)

// The "upcoming" section is a compact, read-only peek at the next few incomplete
// TODO tasks for the selected task's repo, shown above the inbox — a mini version
// of the TODO display, not interactive. The TODO file is read OFF the render
// thread (no main-thread I/O): updateUpcoming stages an async load whenever the
// selected repo changes and the result is swapped in on the render thread, the
// same hand-off the SIGUSR1 inbox reload uses.
var (
	upcomingItems   []upcomingRow // ≤upcomingMax next incomplete tasks (display rows)
	hasUpcoming     bool          // gates the section; mirrors len(upcomingItems) > 0
	upcomingRepo    string        // repo path currently shown (render-thread owned)
	upcomingLoading string        // repo path being loaded (render-thread owned, dedupe)

	upcomingMu     sync.Mutex
	upcomingStaged *upcomingResult // handed off from the loader goroutine
)

// upcomingRow is one rendered upcoming line. The explicitly-marked in-flight item
// (set via `recap working`) carries the "in-progress" flare — a ▸ marker + brighter
// colour — and leads the list; the next TODO tasks follow with a plain bullet. Line
// (marker+text) and FG are precomputed so each row is a single full-width Text — the
// build-once ForEach renders them uniformly and they reflow on resize.
type upcomingRow struct {
	Line string
	FG   Color
}

type upcomingResult struct {
	repo    string
	working string // the in-flight marker at load time
	items   []string
}

const upcomingMax = 5 // how many upcoming tasks to surface

// updateUpcoming runs on the render thread (from refreshDetail). It swaps in any
// finished async load, then kicks off a new load when the selected task's repo
// differs from what's shown. Cheap and idempotent — safe to call every frame.
func updateUpcoming() {
	upcomingMu.Lock()
	staged := upcomingStaged
	upcomingStaged = nil
	upcomingMu.Unlock()
	if staged != nil {
		upcomingRepo = staged.repo
		upcomingItems = buildUpcomingRows(staged.working, staged.items)
		hasUpcoming = len(upcomingItems) > 0
	}

	t, ok := selectedTask()
	if !ok {
		return
	}
	if t.RepoPath == upcomingRepo || t.RepoPath == upcomingLoading {
		return // already shown or in flight
	}
	upcomingLoading = t.RepoPath
	repo := t.RepoPath
	go func() {
		working := loadWorking()    // the explicit in-flight marker
		items := loadUpcoming(repo) // TODO tasks — file read + parse, off the render thread
		upcomingMu.Lock()
		upcomingStaged = &upcomingResult{repo: repo, working: working, items: items}
		upcomingMu.Unlock()
		if uiApp != nil {
			uiApp.RequestRender()
		}
	}()
}

// loadUpcoming resolves repoPath's TODO file and returns its next incomplete
// tasks. Runs on a goroutine only. Any failure (no template, unreadable) yields
// an empty list, which simply hides the section.
func loadUpcoming(repoPath string) []string {
	cfg, err := LoadConfig()
	if err != nil {
		return nil
	}
	path, err := cfg.todoPathFor(repoPath)
	if err != nil || path == "" {
		return nil
	}
	items, err := readTodo(path)
	if err != nil {
		return nil
	}
	return upcomingFromItems(items)
}

// buildUpcomingRows builds the display rows: the explicit in-flight marker (if any)
// leads with the ▸ flare, then the next TODO tasks with a plain bullet. Runs on the
// render thread so it can read the current theme colours.
func buildUpcomingRows(working string, texts []string) []upcomingRow {
	rows := make([]upcomingRow, 0, len(texts)+1)
	if working != "" {
		rows = append(rows, upcomingRow{Line: "▸ " + working, FG: cBright})
	}
	for _, txt := range texts {
		rows = append(rows, upcomingRow{Line: "· " + txt, FG: cSubtle})
	}
	return rows
}

// upcomingFromItems picks the first upcomingMax incomplete tasks, in file order.
// Full text — the row Text clips to the inbox column width at render time, so the
// list uses whatever width the display gives it (no hard-coded truncation). Pure —
// the testable core of loadUpcoming.
func upcomingFromItems(items []todoItem) []string {
	var out []string
	for _, it := range items {
		if !it.IsTask || it.Done {
			continue
		}
		out = append(out, strings.TrimSpace(it.Text))
		if len(out) == upcomingMax {
			break
		}
	}
	return out
}
