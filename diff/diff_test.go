package diff

import "testing"

// a rename carries BOTH ends of the move: OldPath from "rename from", Path from
// "rename to", status "renamed" — including a pure rename (similarity 100%, no
// hunks), which previously surfaced only the new path.
func TestParseRenameCapturesOldPath(t *testing.T) {
	patch := `diff --git a/old/name.go b/new/name.go
similarity index 100%
rename from old/name.go
rename to new/name.go
diff --git a/changed.go b/changed.go
index 111..222 100644
--- a/changed.go
+++ b/changed.go
@@ -1,1 +1,1 @@
-x
+y
`
	files := Parse(patch)
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d", len(files))
	}
	r := files[0]
	if r.Status != "renamed" || r.OldPath != "old/name.go" || r.Path != "new/name.go" {
		t.Fatalf("rename not captured: status=%q old=%q new=%q", r.Status, r.OldPath, r.Path)
	}
	if len(r.Hunks) != 0 {
		t.Fatalf("pure rename should have no hunks, got %d", len(r.Hunks))
	}
	// the modified file is untouched by the rename handling
	m := files[1]
	if m.Status != "modified" || m.OldPath != "" || m.Path != "changed.go" {
		t.Fatalf("modified file wrong: %+v", m)
	}
}

// a rename WITH edits keeps both paths and its hunks.
func TestParseRenameWithEdits(t *testing.T) {
	patch := `diff --git a/a.go b/b.go
similarity index 90%
rename from a.go
rename to b.go
index 111..222 100644
--- a/a.go
+++ b/b.go
@@ -1,1 +1,1 @@
-old line
+new line
`
	files := Parse(patch)
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Status != "renamed" || f.OldPath != "a.go" || f.Path != "b.go" {
		t.Fatalf("rename+edit not captured: %+v", f)
	}
	if len(f.Hunks) != 1 || len(f.Hunks[0].Lines) < 2 {
		t.Fatalf("hunks lost on rename+edit: %+v", f.Hunks)
	}
	if f.Hunks[0].Lines[0].Kind != LineDel || f.Hunks[0].Lines[1].Kind != LineAdd {
		t.Fatalf("hunk content wrong: %+v", f.Hunks[0].Lines)
	}
}
