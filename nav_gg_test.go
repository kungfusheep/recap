package main

import "testing"

// gg / G jump the list selection to the first / last row, clamping on an empty list.
// (todo #124)
func TestSelectTopBottom(t *testing.T) {
	defer func() { vmRows = nil; sel = 0 }()
	vmRows = []taskVM{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}}

	sel = 2
	selectBottom()
	if sel != 3 {
		t.Fatalf("selectBottom: sel=%d, want 3 (last)", sel)
	}
	selectTop()
	if sel != 0 {
		t.Fatalf("selectTop: sel=%d, want 0 (first)", sel)
	}
	vmRows = nil
	selectBottom()
	if sel != 0 {
		t.Fatalf("selectBottom on empty list: sel=%d, want 0", sel)
	}
}
