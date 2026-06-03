// Package notify is recap's live-refresh signalling: an open reviewer TUI
// registers its PID in a pidfile beside the db; the mutating CLI verbs send
// SIGUSR1 to those PIDs so the TUI reloads its inbox without a restart. The
// pidfile is the db path plus ".pids", so isolated dbs get isolated pidfiles.
// Stale PIDs are pruned on register and on send.
package notify

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// pidFilePath resolves the pidfile next to recap's db ($RECAP_DB or
// ~/.config/recap/recap.db, the same location the store uses), suffixed ".pids".
// notify owns this so callers need no wrappers.
func pidFilePath() string {
	db := os.Getenv("RECAP_DB")
	if db == "" {
		if home, err := os.UserHomeDir(); err == nil {
			db = filepath.Join(home, ".config", "recap", "recap.db")
		} else {
			db = "recap.db"
		}
	}
	return db + ".pids"
}

// ReadPIDs returns the PIDs recorded in the pidfile (empty if none/unreadable).
func ReadPIDs(path string) []int {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pids []int
	for _, f := range strings.Fields(string(data)) {
		if p, err := strconv.Atoi(f); err == nil {
			pids = append(pids, p)
		}
	}
	return pids
}

// WritePIDs overwrites the pidfile with the given PIDs.
func WritePIDs(path string, pids []int) error {
	var b strings.Builder
	for _, p := range pids {
		b.WriteString(strconv.Itoa(p))
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// PrunePIDs drops PIDs that are no longer alive (signal 0 probes liveness).
func PrunePIDs(pids []int) []int {
	alive := pids[:0]
	for _, p := range pids {
		if proc, err := os.FindProcess(p); err == nil && proc.Signal(syscall.Signal(0)) == nil {
			alive = append(alive, p)
		}
	}
	return alive
}

// Register adds this process to recap's pidfile so Reload can signal it.
// Returns a cleanup func that removes it (call on TUI exit).
func Register() func() {
	path := pidFilePath()
	me := os.Getpid()
	pids := append(PrunePIDs(ReadPIDs(path)), me)
	_ = WritePIDs(path, pids)
	return func() {
		remaining := pids[:0]
		for _, p := range ReadPIDs(path) {
			if p != me {
				remaining = append(remaining, p)
			}
		}
		_ = WritePIDs(path, remaining)
	}
}

// Reload sends SIGUSR1 to every registered TUI so it reloads its inbox.
// Best-effort: a missing pidfile or dead PID is a harmless no-op, so headless
// CLI use is unaffected. Prunes dead PIDs as a side effect.
func Reload() {
	path := pidFilePath()
	pids := ReadPIDs(path)
	if len(pids) == 0 {
		return
	}
	var alive []int
	for _, p := range pids {
		proc, err := os.FindProcess(p)
		if err != nil {
			continue
		}
		if proc.Signal(syscall.SIGUSR1) == nil {
			alive = append(alive, p)
		}
	}
	if len(alive) != len(pids) {
		_ = WritePIDs(path, alive)
	}
}
