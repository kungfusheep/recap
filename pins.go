package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Pinned tasks float to a "PINNED" section atop the inbox (toggle with 'p'). The pin set
// is task IDs persisted beside the db (cross-repo, like the cursor) so pins survive
// restarts. `pinned` is the in-memory set, loaded once via ensurePins.
var pinned map[int64]bool

func ensurePins() {
	if pinned == nil {
		pinned = loadPins()
	}
}

func pinsPath() (string, error) {
	db, err := dbPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(db), "pins"), nil
}

func loadPins() map[int64]bool {
	out := map[int64]bool{}
	p, err := pinsPath()
	if err != nil {
		return out
	}
	f, err := os.Open(p)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if id, err := strconv.ParseInt(strings.TrimSpace(sc.Text()), 10, 64); err == nil {
			out[id] = true
		}
	}
	return out
}

func savePins(pins map[int64]bool) error {
	p, err := pinsPath()
	if err != nil {
		return err
	}
	var b strings.Builder
	for id, on := range pins {
		if on {
			b.WriteString(strconv.FormatInt(id, 10))
			b.WriteByte('\n')
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(b.String()), 0o644)
}

// setPin pins or unpins a task id and persists the change. The low-level mutation,
// shared by togglePin and its undo.
func setPin(id int64, on bool) {
	ensurePins()
	if on {
		pinned[id] = true
	} else {
		delete(pinned, id)
	}
	savePins(pinned)
}

// togglePin pins/unpins the selected task and persists the change, then rebuilds so it
// floats to (or drops from) the PINNED section. The cursor tracks the task by id, so it
// follows the item as it moves. The toggle is pushed onto the undo stack so `u` reverses
// it (restoring the previous pin state).
func togglePin() {
	t, ok := selectedTask()
	if !ok {
		return
	}
	ensurePins()
	was := pinned[t.ID]
	id := t.ID
	setPin(id, !was)
	pushUndo(func() {
		setPin(id, was)
		if was {
			statusMsg = fmt.Sprintf("re-pinned #%d", id)
		} else {
			statusMsg = fmt.Sprintf("unpinned #%d", id)
		}
		reloadTasks()
	})
	if was {
		statusMsg = fmt.Sprintf("unpinned #%d  ·  u to undo", id)
	} else {
		statusMsg = fmt.Sprintf("pinned #%d  ·  u to undo", id)
	}
	reloadTasks()
}
