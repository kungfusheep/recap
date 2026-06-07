package main

import "testing"

// filterOmniItems lists the repo filters as selectable palette items — "all repos"
// plus one per project — and each item's action sets the filter. (todo #123)
func TestFilterOmniItems(t *testing.T) {
	st := testStore(t)
	prevStore, prevRepos, prevFltr := uiStore, repos, repoFltr
	uiStore = st
	t.Cleanup(func() {
		uiStore = prevStore
		repos = prevRepos
		repoFltr = prevFltr
		vmRows = nil
		sel = 0
	})

	repos = []string{"alpha", "beta"}
	items := filterOmniItems()
	if len(items) != 3 {
		t.Fatalf("want 3 filter items (all + alpha + beta), got %d", len(items))
	}
	if items[0].Label != "filter: all repos" {
		t.Fatalf("first filter item should be 'filter: all repos', got %q", items[0].Label)
	}
	labels := map[string]omniItem{}
	for _, it := range items {
		labels[it.Label] = it
	}
	for _, want := range []string{"filter: all repos", "filter: alpha", "filter: beta"} {
		if _, ok := labels[want]; !ok {
			t.Fatalf("missing filter item %q", want)
		}
	}

	// selecting a repo item applies that filter
	repoFltr = ""
	labels["filter: beta"].Action()
	if repoFltr != "beta" {
		t.Fatalf("selecting 'filter: beta' should set repoFltr=beta, got %q", repoFltr)
	}
	// selecting "all repos" clears it
	labels["filter: all repos"].Action()
	if repoFltr != "" {
		t.Fatalf("selecting 'filter: all repos' should clear repoFltr, got %q", repoFltr)
	}
}
