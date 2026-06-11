package main

import "testing"

// gg / G jump the list selection to the first / last row, clamping on an empty list.
// (todo #124)
func TestSelectTopBottom(t *testing.T) {
	defer func() { inboxUI.Rows = nil; inboxUI.Sel = 0 }()
	inboxUI.Rows = []taskVM{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}}

	inboxUI.Sel = 2
	selectBottom()
	if inboxUI.Sel != 3 {
		t.Fatalf("selectBottom: inboxUI.Sel=%d, want 3 (last)", inboxUI.Sel)
	}
	selectTop()
	if inboxUI.Sel != 0 {
		t.Fatalf("selectTop: inboxUI.Sel=%d, want 0 (first)", inboxUI.Sel)
	}
	inboxUI.Rows = nil
	selectBottom()
	if inboxUI.Sel != 0 {
		t.Fatalf("selectBottom on empty list: inboxUI.Sel=%d, want 0", inboxUI.Sel)
	}
}
