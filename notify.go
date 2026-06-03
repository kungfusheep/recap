package main

import "github.com/kungfusheep/recap/notify"

// pidFilePath returns the reload pidfile path (db path with a .pids suffix).
// notify itself is path-agnostic; main owns the db location.
func pidFilePath() (string, error) {
	db, err := dbPath()
	if err != nil {
		return "", err
	}
	return db + ".pids", nil
}

// registerUIPID registers this TUI in the pidfile; returns a cleanup func.
func registerUIPID() func() {
	path, err := pidFilePath()
	if err != nil {
		return func() {}
	}
	return notify.Register(path)
}

// notifyReload nudges any open TUI to reload its inbox.
func notifyReload() {
	if path, err := pidFilePath(); err == nil {
		notify.Reload(path)
	}
}
