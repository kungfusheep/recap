package main

import "github.com/kungfusheep/recap/db"

// inboxView is the inbox's state in one concrete struct (the 5a–5d pattern): the
// task list and its flattened row VMs, selection, repo filter, expand/lookup maps,
// and the reload/refresh bookkeeping driven by selection changes. One package
// instance (inboxUI) — fields are pointer-bound into the compiled views
// (&inboxUI.Rows, &inboxUI.CountText…), so the struct must be a stable package
// var. No interfaces, no injection.
type inboxView struct {
	Tasks []db.Task
	Rows  []taskVM // flattened: task headers + (when expanded) their revision children
	Sel   int      // index into Rows, NOT Tasks (a row may be a revision child)
	// KeepSelOnReload makes the next reloadTasks hold the cursor at its current index
	// instead of chasing the selected task by id. Set by user marks (approve/submit) so
	// the marked item leaves and the NEXT item slides up under the cursor — a clean path
	// down the list — without changing the async-insert behaviour (which still tracks the
	// task so a pushed item never yanks the reader's place).
	KeepSelOnReload bool
	RepoFilter      string
	Repos           []string

	// Expanded tracks which tasks are expanded into their revision children
	// (mail's thread-expand). Keyed by task id so it survives Rows rebuilds.
	Expanded map[int64]bool
	// TaskByID resolves the selected row's task without re-querying (rebuilt each
	// reloadTasks). Rows carry only a task ID; this maps back to the full db.Task.
	TaskByID map[int64]db.Task

	Count                 int // number of pending (inbox) tasks — shown in the header
	CountText, FilterText string

	// selection-change bookkeeping: the watch loop compares these against the live
	// values each frame and refreshes the detail pane when they drift.
	LastSel, LastLen int
	LastFilter       string
	DetailDirty      bool
	// LastDiffKey identifies the diff currently shown (task:rev:sha). refreshDetail only
	// resets the diff scroll when this changes — so an inbox reload that adds an item but
	// leaves the selected task unchanged keeps the reader's scroll position.
	LastDiffKey string
	// DoneOldLimit caps how many completed items OLDER THAN A DAY the inbox list renders;
	// the rest sit behind a "load more" row (avoids rendering a huge done history). Recent
	// (< 24h) completed items always show. "load more" raises this by a batch.
	DoneOldLimit int
}

// inboxUI is the single instance the views bind against.
var inboxUI = inboxView{
	Expanded:     map[int64]bool{},
	TaskByID:     map[int64]db.Task{},
	DoneOldLimit: 20,
}
