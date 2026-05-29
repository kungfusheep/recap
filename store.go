package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Status values for a review task.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRedo     = "redo"
)

func validStatus(s string) bool {
	return s == StatusPending || s == StatusApproved || s == StatusRedo
}

// Task is one completed unit of autonomous work awaiting (or past) review.
// The diff itself is NOT stored — SHA + RepoPath point into git, fetched on
// demand. This db is purely the private review layer.
type Task struct {
	ID        int64
	Repo      string // short display name (basename of repo path)
	RepoPath  string // absolute path, for git operations
	SHA       string
	Title     string
	Criterion string // the falsifiable success check, in words
	CheckCmd  string // command that re-proves it
	Result    string // e.g. PASS / FAIL / the observed result
	Status    string // pending | approved | redo
	CreatedAt string
}

// Comment is one message in a task's async review thread (you <-> agent).
type Comment struct {
	ID        int64
	TaskID    int64
	Who       string // "you" | "agent"
	Body      string
	CreatedAt string
}

type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  repo       TEXT NOT NULL,
  repo_path  TEXT NOT NULL,
  sha        TEXT,
  title      TEXT NOT NULL,
  criterion  TEXT,
  check_cmd  TEXT,
  result     TEXT,
  status     TEXT NOT NULL DEFAULT 'pending',
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS comments (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id    INTEGER NOT NULL,
  who        TEXT NOT NULL,
  body       TEXT NOT NULL,
  created_at TEXT NOT NULL
);
`

// dbPath resolves the global review db location: $RECAP_DB or ~/.config/recap/recap.db.
func dbPath() (string, error) {
	if p := os.Getenv("RECAP_DB"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "recap", "recap.db"), nil
}

// Open opens the global review db (creating it and its dir if needed).
func Open() (*Store, error) {
	p, err := dbPath()
	if err != nil {
		return nil, err
	}
	return OpenAt(p)
}

// OpenAt opens (and migrates) the review db at the given path.
func OpenAt(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func nowStamp() string { return time.Now().Format("2006-01-02 15:04:05") }

// Add records a completed task and returns its id.
func (s *Store) Add(t Task) (int64, error) {
	if t.Title == "" {
		return 0, fmt.Errorf("task title is required")
	}
	if t.Status == "" {
		t.Status = StatusPending
	}
	if !validStatus(t.Status) {
		return 0, fmt.Errorf("invalid status %q", t.Status)
	}
	if t.CreatedAt == "" {
		t.CreatedAt = nowStamp()
	}
	res, err := s.db.Exec(
		`INSERT INTO tasks (repo, repo_path, sha, title, criterion, check_cmd, result, status, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		t.Repo, t.RepoPath, t.SHA, t.Title, t.Criterion, t.CheckCmd, t.Result, t.Status, t.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.Repo, &t.RepoPath, &t.SHA, &t.Title, &t.Criterion,
		&t.CheckCmd, &t.Result, &t.Status, &t.CreatedAt)
	return t, err
}

const taskCols = `id, repo, repo_path, sha, title, criterion, check_cmd, result, status, created_at`

// Get returns one task by id.
func (s *Store) Get(id int64) (Task, error) {
	row := s.db.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE id = ?`, id)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return Task{}, fmt.Errorf("no task #%d", id)
	}
	return t, err
}

// List returns tasks filtered by optional status and repo (empty = no filter),
// newest first.
func (s *Store) List(status, repo string) ([]Task, error) {
	q := `SELECT ` + taskCols + ` FROM tasks WHERE 1=1`
	var args []any
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	if repo != "" {
		q += ` AND repo = ?`
		args = append(args, repo)
	}
	q += ` ORDER BY id DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetStatus updates a task's review status.
func (s *Store) SetStatus(id int64, status string) error {
	if !validStatus(status) {
		return fmt.Errorf("invalid status %q (want pending|approved|redo)", status)
	}
	res, err := s.db.Exec(`UPDATE tasks SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no task #%d", id)
	}
	return nil
}

// AddComment appends a message to a task's review thread.
func (s *Store) AddComment(taskID int64, who, body string) (int64, error) {
	if _, err := s.Get(taskID); err != nil {
		return 0, err
	}
	res, err := s.db.Exec(
		`INSERT INTO comments (task_id, who, body, created_at) VALUES (?,?,?,?)`,
		taskID, who, body, nowStamp())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Comments returns a task's thread, oldest first.
func (s *Store) Comments(taskID int64) ([]Comment, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, who, body, created_at FROM comments WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.TaskID, &c.Who, &c.Body, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
