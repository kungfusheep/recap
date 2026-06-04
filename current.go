package main

import (
	"os"
	"path/filepath"
	"strings"
)

// The in-flight marker is a pure protocol fact, not a thing the agent declares:
// `recap next` records the item it hands out as the "current" cursor, and the TUI
// flares it. This file is the cursor's storage — the current work item's stable ref
// plus its display title — kept beside the db so any process can set it and the TUI
// re-reads it on the reload signal. Advancing (another `recap next`) overwrites it;
// completing the item drops it from the queue so the next `recap next` moves on.
//
// The cursor is PER-REPO (one file per repo name) so two loops in different repos
// never share or clobber each other's in-flight item — the same namespacing the
// queue (amends/replies/todos) already uses. "" repo falls back to a shared file.
func currentPath(repo string) (string, error) {
	db, err := dbPath()
	if err != nil {
		return "", err
	}
	name := "current"
	if repo != "" {
		name = "current-" + strings.ReplaceAll(repo, string(os.PathSeparator), "_")
	}
	return filepath.Join(filepath.Dir(db), name), nil
}

// loadCurrent returns the repo's current item ref + display title ("","" if none).
func loadCurrent(repo string) (ref, title string) {
	p, err := currentPath(repo)
	if err != nil {
		return "", ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", ""
	}
	parts := strings.SplitN(strings.TrimRight(string(b), "\n"), "\t", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return strings.TrimSpace(parts[0]), ""
}

// currentTitle is the flare text — the repo's current item title, "" when idle.
func currentTitle(repo string) string {
	_, title := loadCurrent(repo)
	return title
}

// saveCurrent sets the repo's cursor to ref/title, or clears it when ref is empty.
func saveCurrent(repo, ref, title string) error {
	p, err := currentPath(repo)
	if err != nil {
		return err
	}
	if strings.TrimSpace(ref) == "" {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(ref+"\t"+title+"\n"), 0o644)
}
