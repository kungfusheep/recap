package main

import "testing"

// the command palette must expose every action (not just quit) so the keys are
// discoverable, and each entry must be wired to a handler with searchable text.
func TestOmniCommandsCoverActions(t *testing.T) {
	cmds := omniCommands()

	want := []string{
		"approve", "comment", "submit (amends)", "unsubmit → inbox",
		"re-run verification (--check)", "filter repo", "next pane", "previous pane", "help", "quit",
	}
	have := map[string]omniItem{}
	for _, c := range cmds {
		have[c.Label] = c
	}
	for _, label := range want {
		it, ok := have[label]
		if !ok {
			t.Fatalf("palette missing %q action", label)
		}
		if it.Action == nil {
			t.Fatalf("%q has no Action wired", label)
		}
		if it.Section == "" {
			t.Fatalf("%q has no Section", label)
		}
		if omniSearchText(&it) == "" {
			t.Fatalf("%q has empty search text", label)
		}
	}
	// guard against regressing back to the single quit-only palette
	if len(cmds) < len(want) {
		t.Fatalf("palette shrank to %d commands, want >= %d", len(cmds), len(want))
	}
}
