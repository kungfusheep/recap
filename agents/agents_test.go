package agents

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kungfusheep/recap/cursor"
	"github.com/kungfusheep/recap/db"
)

// Snapshot groups identities by NAME across repos, ranks status working >
// parked > idle, ignores stale flares, and carries the most recent task.
func TestSnapshot(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("RECAP_DB", filepath.Join(tmp, "recap.db"))
	st, err := db.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	write := func(repo, name, hex string) {
		body := name + "\n" + hex + "\n"
		if err := os.WriteFile(filepath.Join(tmp, "identity-"+repo), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("wren-repo", "Wren", "#aabbcc")
	write("wren-lab", "Wren", "#aabbcc")
	write("finch-repo", "Finch", "#ccaabb")
	write("stale-repo", "Heron", "#bbccaa")

	st.Add(db.Task{Repo: "wren-repo", RepoPath: "/tmp/w", Title: "shipped the wrenizer", Status: db.StatusPending})
	cursor.Save("finch-repo", "todo:abc", "polishing the finch cache")
	cursor.Save("stale-repo", "todo:zzz", "ancient business")
	old := time.Now().Add(-3 * time.Hour)
	os.Chtimes(filepath.Join(tmp, "current-stale-repo"), old, old)

	snap, err := Snapshot(st)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snap) != 3 {
		t.Fatalf("want 3 grouped agents, got %d: %+v", len(snap), snap)
	}
	byName := map[string]Agent{}
	for _, a := range snap {
		byName[a.Name] = a
	}
	if w := byName["Wren"]; len(w.Repos) != 2 || w.LastWork != "shipped the wrenizer" {
		t.Fatalf("Wren grouping wrong: %+v", w)
	}
	if f := byName["Finch"]; f.Status != Working || f.Flare != "polishing the finch cache" {
		t.Fatalf("Finch should be working: %+v", f)
	}
	if h := byName["Heron"]; h.Status != Idle {
		t.Fatalf("Heron's 3h flare must read idle: %+v", h)
	}
}
