package main

import (
	"os"
	"path/filepath"
	"strings"
)

// The in-flight marker is now a pure protocol fact, not a thing the agent declares:
// `recap next` records the item it hands out as the "current" cursor, and the TUI
// flares it. This file is just the cursor's storage — the current work item's stable
// ref plus its display title — kept beside the db so any process can set it and the
// TUI re-reads it on the reload signal. Advancing (another `recap next`) overwrites
// it; completing the item drops it from the queue so the next `recap next` moves on.
func currentPath() (string, error) {
	db, err := dbPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(db), "current"), nil
}

// loadCurrent returns the current item's ref + display title ("","" if none).
func loadCurrent() (ref, title string) {
	p, err := currentPath()
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

// currentTitle is the flare text — the current item's title, "" when idle.
func currentTitle() string {
	_, title := loadCurrent()
	return title
}

// saveCurrent sets the cursor to ref/title, or clears it when ref is empty.
func saveCurrent(ref, title string) error {
	p, err := currentPath()
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
