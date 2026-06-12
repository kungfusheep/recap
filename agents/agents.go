// Package agents aggregates the fleet's visible state — who is named, what
// they're doing, and what they last shipped — from the files and rows the
// loop already maintains (identity files, cursor flares, listener pidfiles,
// the task table). One Agent per NAME: identities legitimately span repos.
package agents

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kungfusheep/recap/cursor"
	"github.com/kungfusheep/recap/db"
	"github.com/kungfusheep/recap/listener"
)

// Status kinds, in display-priority order.
const (
	Idle = iota
	Parked
	Working
)

// FlareMaxAge: a cursor flare untouched this long is stale — the loop died or
// predates the park-clears-cursor fix — and must not read as working.
const FlareMaxAge = 2 * time.Hour

// Agent is one named agent's aggregated state across its repos.
type Agent struct {
	Name     string
	ColorHex string // raw hex from the identity file ("" when unset)
	Repos    []string
	Status   int           // Idle | Parked | Working (best across repos)
	Flare    string        // the in-flight title when Working
	FlareAge time.Duration // how fresh the flare is
	LastWork string        // most recent recorded task title across repos
	LastAt   string        // its timestamp
}

// Snapshot gathers every named agent. st may be nil (no last-work column).
func Snapshot(st *db.Store) ([]Agent, error) {
	dbp, err := db.Path()
	if err != nil {
		return nil, err
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(dbp), "identity-*"))
	live := map[string]bool{}
	for _, r := range listener.Active() {
		live[r] = true
	}
	var latest map[string]db.Task
	if st != nil {
		latest, _ = st.LatestTaskPerRepo()
	}

	byName := map[string]*Agent{}
	for _, m := range matches {
		repo := strings.TrimPrefix(filepath.Base(m), "identity-")
		name, hex := readIdentity(m)
		if name == "" {
			continue
		}
		a := byName[name]
		if a == nil {
			a = &Agent{Name: name, ColorHex: hex}
			byName[name] = a
		}
		a.Repos = append(a.Repos, repo)
		status, flare, age := Idle, "", time.Duration(0)
		switch {
		case cursor.Title(repo) != "" && cursor.Age(repo) < FlareMaxAge:
			status, flare, age = Working, cursor.Title(repo), cursor.Age(repo)
		case live[repo]:
			status = Parked
		}
		if status > a.Status {
			a.Status, a.Flare, a.FlareAge = status, flare, age
		}
		if t, ok := latest[repo]; ok && t.CreatedAt > a.LastAt {
			a.LastAt, a.LastWork = t.CreatedAt, t.Title
		}
	}

	out := make([]Agent, 0, len(byName))
	for _, a := range byName {
		sort.Strings(a.Repos)
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// readIdentity parses an identity file: first line the name, optional second
// line a #RRGGBB colour.
func readIdentity(path string) (name, hex string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	lines := strings.SplitN(strings.TrimSpace(string(b)), "\n", 2)
	name = strings.TrimSpace(lines[0])
	if len(lines) > 1 {
		hex = strings.TrimSpace(lines[1])
	}
	return name, hex
}
