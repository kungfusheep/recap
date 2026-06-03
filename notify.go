package main

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Live refresh: an open reviewer TUI registers its PID in a pidfile beside the
// db; `recap add` sends SIGUSR1 to those PIDs so the TUI reloads its inbox
// without a restart. The pidfile follows $RECAP_DB, so isolated test dbs get
// isolated pidfiles (no cross-talk). Stale PIDs are pruned on send.

// pidFilePath returns the reload pidfile path (db path with a .pids suffix).
func pidFilePath() (string, error) {
	db, err := dbPath()
	if err != nil {
		return "", err
	}
	return db + ".pids", nil
}

func readPIDs(path string) []int {
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

func writePIDs(path string, pids []int) error {
	var b strings.Builder
	for _, p := range pids {
		b.WriteString(strconv.Itoa(p))
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// registerUIPID adds this process to the pidfile so `recap add` can signal it.
// Returns a cleanup func that removes it (call on TUI exit).
func registerUIPID() func() {
	path, err := pidFilePath()
	if err != nil {
		return func() {}
	}
	me := os.Getpid()
	pids := append(prunePIDs(readPIDs(path)), me)
	_ = writePIDs(path, pids)
	return func() {
		remaining := pids[:0]
		for _, p := range readPIDs(path) {
			if p != me {
				remaining = append(remaining, p)
			}
		}
		_ = writePIDs(path, remaining)
	}
}

// prunePIDs drops PIDs that are no longer alive (signal 0 probes liveness).
func prunePIDs(pids []int) []int {
	alive := pids[:0]
	for _, p := range pids {
		if proc, err := os.FindProcess(p); err == nil && proc.Signal(syscall.Signal(0)) == nil {
			alive = append(alive, p)
		}
	}
	return alive
}

// notifyReload sends SIGUSR1 to every registered TUI so it reloads its inbox.
// Best-effort: a missing pidfile or dead PID is a harmless no-op, so headless
// CLI use is unaffected. Prunes dead PIDs as a side effect.
func notifyReload() {
	path, err := pidFilePath()
	if err != nil {
		return
	}
	pids := readPIDs(path)
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
		_ = writePIDs(path, alive)
	}
}
