package main

import (
	"testing"

	"github.com/kungfusheep/recap/db"
)

// filterOmniItems lists the repo filters as selectable palette items — "all inboxUI.Repos"
// plus one per project — and each item's action sets the filter. (todo #123)
func TestFilterOmniItems(t *testing.T) {
	st := testStore(t)
	prevStore, prevRepos, prevFltr := uiStore, inboxUI.Repos, inboxUI.RepoFilter
	uiStore = st
	t.Cleanup(func() {
		uiStore = prevStore
		inboxUI.Repos = prevRepos
		inboxUI.RepoFilter = prevFltr
		inboxUI.Rows = nil
		inboxUI.Sel = 0
	})

	inboxUI.Repos = []string{"alpha", "beta"}
	items := filterOmniItems()
	if len(items) != 3 {
		t.Fatalf("want 3 filter items (all + alpha + beta), got %d", len(items))
	}
	if items[0].Label != "filter: all inboxUI.Repos" {
		t.Fatalf("first filter item should be 'filter: all inboxUI.Repos', got %q", items[0].Label)
	}
	labels := map[string]omniItem{}
	for _, it := range items {
		labels[it.Label] = it
	}
	for _, want := range []string{"filter: all inboxUI.Repos", "filter: alpha", "filter: beta"} {
		if _, ok := labels[want]; !ok {
			t.Fatalf("missing filter item %q", want)
		}
	}

	// selecting a repo item applies that filter
	inboxUI.RepoFilter = ""
	labels["filter: beta"].Action()
	if inboxUI.RepoFilter != "beta" {
		t.Fatalf("selecting 'filter: beta' should set inboxUI.RepoFilter=beta, got %q", inboxUI.RepoFilter)
	}
	// selecting "all inboxUI.Repos" clears it
	labels["filter: all inboxUI.Repos"].Action()
	if inboxUI.RepoFilter != "" {
		t.Fatalf("selecting 'filter: all inboxUI.Repos' should clear inboxUI.RepoFilter, got %q", inboxUI.RepoFilter)
	}
}

// todoOmniItems lists one "todo: <repo>" per distinct repo present (with a repo path), so
// any project's TODO list is reachable from the palette. (todo: project)
func TestTodoOmniItems(t *testing.T) {
	prev := inboxUI.Tasks
	t.Cleanup(func() { inboxUI.Tasks = prev })
	inboxUI.Tasks = []db.Task{
		{Repo: "alpha", RepoPath: "/a"},
		{Repo: "alpha", RepoPath: "/a"}, // dup repo → one item
		{Repo: "beta", RepoPath: "/b"},
		{Repo: "noPath"}, // no RepoPath → skipped
	}
	items := todoOmniItems()
	got := map[string]bool{}
	for _, it := range items {
		got[it.Label] = true
		if it.Action == nil {
			t.Fatalf("%q has no action", it.Label)
		}
	}
	if len(items) != 2 {
		t.Fatalf("want 2 todo items (alpha, beta), got %d: %v", len(items), got)
	}
	if !got["todo: alpha"] || !got["todo: beta"] {
		t.Fatalf("missing todo items: %v", got)
	}
	if got["todo: noPath"] {
		t.Fatal("repo without a path should be skipped")
	}
}
