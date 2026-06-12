package main

import (
	"fmt"
	"sync"

	. "github.com/kungfusheep/glyph"
	"github.com/kungfusheep/recap/db"
	"github.com/kungfusheep/recap/diff"
)

// The detail pane's content (review banner, comment rows, the diff itself) needs
// db queries and git subprocesses — acquisition, which never runs in the render
// path. refreshDetail only DETECTS the change and kicks a load; fetchDetail runs
// on a goroutine and stages a result; the next frame swaps it in (applyDetail).
// The same staged hand-off the upcoming section uses, keyed so a result that
// lands after the selection moved on is dropped instead of applied.

type detailResult struct {
	key        string // diff key (task:rev:sha) at kick time — stale results are dropped
	reset      bool   // reset the diff scroll on apply (the shown diff changed)
	taskID     int64
	propID     int64          // a proposal detail: comments are proposal-thread rows (pane mutations gate off)
	bannerMeta []diffLineMeta // anchors for banner rows (proposal documents are line-commentable)
	banner     [][]Span
	files      []diff.File
	filesText  string
	notFound   bool   // the sha didn't resolve in this checkout
	sha        string // for the not-found banner
	repoPath   string
	comments   []db.TaskComment
}

var (
	detailMu     sync.Mutex
	detailStaged *detailResult
)

// detailKick dispatches the acquisition. A package var so tests can swap in a
// synchronous version and keep refreshDetail deterministic.
var detailKick = func(t db.Task, row taskVM, key string, reset bool) {
	app := uiApp // snapshot: the goroutine must not read the mutable global
	go func() {
		stageDetail(fetchDetail(t, row, key, reset))
		if app != nil {
			app.RequestRender()
		}
	}()
}

func stageDetail(r *detailResult) {
	detailMu.Lock()
	detailStaged = r
	detailMu.Unlock()
}

// takeStagedDetail pops the staged result (nil when none) — render thread.
func takeStagedDetail() *detailResult {
	detailMu.Lock()
	r := detailStaged
	detailStaged = nil
	detailMu.Unlock()
	return r
}

// fetchDetail does the I/O off the render thread: the review-context banner
// (db + identity files), the task's comments (db), and the diff (git). Theme
// colours are read while building spans; they only change on the render thread
// during a theme switch, which invalidates the detail and re-kicks anyway.
func fetchDetail(t db.Task, row taskVM, key string, reset bool) *detailResult {
	r := &detailResult{key: key, reset: reset, taskID: t.ID, sha: row.DiffSHA, repoPath: t.RepoPath}
	if uiStore == nil {
		return r
	}
	r.banner = buildBanner(t)
	r.comments, _ = uiStore.TaskReviewComments(t.ID)
	if r.sha == "" || t.RepoPath == "" {
		r.filesText = "no diff — task has no sha"
		return r
	}
	r.filesText = changedFiles(t.RepoPath, r.sha)
	full, err := git(t.RepoPath, "show", "--format=", r.sha)
	if err != nil {
		// a sha this checkout can't resolve must SAY so — silently rendering
		// "no changes" hid a real data problem (agents recording shas from a
		// different clone/sandbox, so the commit never existed here).
		r.notFound = true
		r.filesText = "commit not found"
		return r
	}
	r.files = diff.Parse(full)
	return r
}

// applyDetail swaps a fetched result into the bound view state — render thread
// only, no I/O.
func applyDetail(r *detailResult) {
	diffUI.Banner = r.banner
	diffUI.BannerMeta = r.bannerMeta
	draftUI.PropID = r.propID
	if r.notFound {
		diffUI.Banner = append(diffUI.Banner,
			[]Span{span(fmt.Sprintf("⚠ commit %s not found in %s", r.sha, r.repoPath), cDel, true)},
			[]Span{span("  the work may have been recorded from a different checkout (clone/sandbox),", cSubtle, false)},
			[]Span{span("  or this repo's history was rewritten after recording", cSubtle, false)},
			[]Span{})
	}
	diffUI.Files = r.files
	diffUI.FilesText = r.filesText
	applyDraftComments(r.taskID, r.comments)
	if r.propID != 0 && len(r.comments) > 0 {
		draftUI.Note = fmt.Sprintf("thread · %d", len(r.comments))
	}
	setDiff(r.reset)
}
