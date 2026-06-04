package main

import (
	"strings"
	"sync"
)

// The "upcoming" section is a compact, read-only peek at the next few incomplete
// TODO tasks for the selected task's repo, shown above the inbox — a mini version
// of the TODO display, not interactive. The TODO file is read OFF the render
// thread (no main-thread I/O): updateUpcoming stages an async load whenever the
// selected repo changes and the result is swapped in on the render thread, the
// same hand-off the SIGUSR1 inbox reload uses.
var (
	upcomingItems   []string // ≤upcomingMax next incomplete task texts (truncated)
	hasUpcoming     bool     // gates the section; mirrors len(upcomingItems) > 0
	upcomingRepo    string   // repo path currently shown (render-thread owned)
	upcomingLoading string   // repo path being loaded (render-thread owned, dedupe)

	upcomingMu     sync.Mutex
	upcomingStaged *upcomingResult // handed off from the loader goroutine
)

type upcomingResult struct {
	repo  string
	items []string
}

const (
	upcomingMax     = 5  // how many upcoming tasks to surface
	upcomingTextLen = 36 // per-row truncation for the narrow inbox column
)

// updateUpcoming runs on the render thread (from refreshDetail). It swaps in any
// finished async load, then kicks off a new load when the selected task's repo
// differs from what's shown. Cheap and idempotent — safe to call every frame.
func updateUpcoming() {
	upcomingMu.Lock()
	staged := upcomingStaged
	upcomingStaged = nil
	upcomingMu.Unlock()
	if staged != nil {
		upcomingRepo, upcomingItems = staged.repo, staged.items
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
		items := loadUpcoming(repo) // file read + parse, off the render thread
		upcomingMu.Lock()
		upcomingStaged = &upcomingResult{repo: repo, items: items}
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

// upcomingFromItems picks the first upcomingMax incomplete tasks, in file order,
// truncated for display. Pure — the testable core of loadUpcoming.
func upcomingFromItems(items []todoItem) []string {
	var out []string
	for _, it := range items {
		if !it.IsTask || it.Done {
			continue
		}
		out = append(out, truncateRunes(strings.TrimSpace(it.Text), upcomingTextLen))
		if len(out) == upcomingMax {
			break
		}
	}
	return out
}

// truncateRunes shortens s to at most max runes, marking the cut with an ellipsis.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}
