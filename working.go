package main

import (
	"os"
	"path/filepath"
	"strings"
)

// The in-flight marker: a one-line note of what the agent is ACTIVELY working on
// right now — set explicitly by the loop (`recap working "<what>"`), not guessed
// from the TODO order. The upcoming section surfaces it as the flared item, so the
// "in progress" cue reflects the real focus (which may be review feedback for some
// other item, not the next TODO line). Stored beside the db as a plain file so any
// process can set it and the TUI can re-read it on the reload signal.
func workingPath() (string, error) {
	db, err := dbPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(db), "working"), nil
}

// loadWorking returns the current in-flight note ("" if none). Tiny file; safe to
// read on the reload path alongside the inbox reload.
func loadWorking() string {
	p, err := workingPath()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// saveWorking sets (or, with "", clears) the in-flight note.
func saveWorking(text string) error {
	p, err := workingPath()
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strings.TrimSpace(text)+"\n"), 0o644)
}
