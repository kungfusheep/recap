// Package listener tracks which repos have a live parked loop (`recap next --wait`)
// RIGHT NOW — the "active listeners" a broadcast can reach. One pidfile per repo
// beside the db; liveness is checked at read time, so stale files from killed
// processes are pruned rather than trusted.
package listener

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/kungfusheep/recap/db"
)

func dir() (string, error) {
	p, err := db.Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), "listeners"), nil
}

// Register records this process as repo's live listener and returns a cleanup.
// Last writer wins (one waiter per repo is the normal shape); cleanup removes the
// file only if it still belongs to this pid.
func Register(repo string) func() {
	d, err := dir()
	if err != nil || repo == "" {
		return func() {}
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		return func() {}
	}
	path := filepath.Join(d, repo+".pid")
	pid := os.Getpid()
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return func() {}
	}
	return func() {
		if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) == strconv.Itoa(pid) {
			os.Remove(path)
		}
	}
}

// Active returns the repos with a live listener process, pruning stale pidfiles.
func Active() []string {
	d, err := dir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".pid") {
			continue
		}
		path := filepath.Join(d, name)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || pid <= 0 || syscall.Kill(pid, 0) != nil {
			os.Remove(path) // dead or garbage — prune so broadcasts don't target ghosts
			continue
		}
		out = append(out, strings.TrimSuffix(name, ".pid"))
	}
	return out
}
