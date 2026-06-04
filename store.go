package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// Review verdicts. request_changes and approve flip the task status; comment is
// a non-blocking note (FYI) that leaves the task status untouched.
const (
	VerdictRequestChanges = "request_changes"
	VerdictApprove        = "approve"
	VerdictComment        = "comment"
)

func validVerdict(v string) bool {
	return v == VerdictRequestChanges || v == VerdictApprove || v == VerdictComment
}

// Review lifecycle: a draft accumulates comments locally; submit publishes the
// batch atomically; resolve marks it addressed after a fix-forward commit.
const (
	ReviewDraft     = "draft"
	ReviewSubmitted = "submitted"
	ReviewResolved  = "resolved"
)

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
	ParentID  int64  // the task this one fixes forward (0 = none)
	Summary   string // agent-written reviewer briefing (richer than the commit msg)
}

// Review is a batch of reviewer feedback against a task: a verdict, an overall
// summary (the "new prompt"), and N comments — submitted atomically.
type Review struct {
	ID          int64
	TaskID      int64
	Verdict     string // request_changes | approve | comment
	Summary     string
	State       string // draft | submitted | resolved
	CreatedAt   string
	SubmittedAt string
}

// Comment is one message against a task. A loose thread message has ReviewID 0;
// a review comment belongs to a Review and may be anchored to a diff line
// (File/Line/Anchor) with the code captured at review time (Snippet).
type Comment struct {
	ID        int64
	TaskID    int64
	ReviewID  int64 // 0 = loose thread message
	ParentID  int64 // 0 = top-level; else the comment this one replies to
	Who       string
	Body      string
	File      string
	Line      int
	Anchor    string // hunk header, e.g. "@@ -10,7 +10,9 @@"
	Snippet   string // the diff line(s) commented on
	CreatedAt string
	Emote     string // optional reaction (e.g. 👍) — the agent's ack of this comment
	ReadAgent string // timestamp the agent marked this read ("" = unread) — receipt
	ReadUser  string // timestamp the user's TUI marked this read ("" = unread)
}

// Revision is one diff attached to a task. A task's first diff is its base
// (Task.SHA + Task.Summary); each fix-forward appends a Revision instead of
// spawning a separate task, so one item carries the whole change history.
type Revision struct {
	ID        int64
	TaskID    int64
	SHA       string
	Summary   string
	CreatedAt string
	Base      bool // true for the synthetic revision built from the task's own SHA
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
CREATE TABLE IF NOT EXISTS reviews (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id      INTEGER NOT NULL,
  verdict      TEXT,
  summary      TEXT,
  state        TEXT NOT NULL DEFAULT 'draft',
  created_at   TEXT NOT NULL,
  submitted_at TEXT
);
CREATE TABLE IF NOT EXISTS revisions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id    INTEGER NOT NULL,
  sha        TEXT NOT NULL,
  summary    TEXT,
  created_at TEXT NOT NULL
);
`

// addColumns lists the additive migrations layered on top of the base schema so
// older dbs upgrade in place. SQLite has no "ADD COLUMN IF NOT EXISTS", so we
// gate each on PRAGMA table_info.
var addColumns = []struct{ table, col, decl string }{
	{"tasks", "parent_id", "INTEGER"},
	{"tasks", "summary", "TEXT"}, // agent-written reviewer briefing (not the commit msg)
	{"comments", "review_id", "INTEGER"},
	{"comments", "file", "TEXT"},
	{"comments", "line", "INTEGER"},
	{"comments", "anchor", "TEXT"},
	{"comments", "snippet", "TEXT"},
	{"comments", "parent_id", "INTEGER"},
	{"comments", "emote", "TEXT"},
	{"comments", "read_agent", "TEXT"},
	{"comments", "read_user", "TEXT"},
}

func (s *Store) hasColumn(table, col string) (bool, error) {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) migrate() error {
	for _, a := range addColumns {
		has, err := s.hasColumn(a.table, a.col)
		if err != nil {
			return err
		}
		if !has {
			if _, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", a.table, a.col, a.decl)); err != nil {
				return err
			}
			// when the read-receipt columns are first added to an EXISTING db, treat
			// all prior comments as already-seen (read = their created_at) so `recap
			// unread` surfaces only genuinely new comments, not the whole backlog.
			if a.table == "comments" && (a.col == "read_agent" || a.col == "read_user") {
				if _, err := s.db.Exec("UPDATE comments SET " + a.col + " = created_at WHERE " + a.col + " IS NULL"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

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
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
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
		`INSERT INTO tasks (repo, repo_path, sha, title, criterion, check_cmd, result, status, created_at, parent_id, summary)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		t.Repo, t.RepoPath, t.SHA, t.Title, t.Criterion, t.CheckCmd, t.Result, t.Status, t.CreatedAt, nullID(t.ParentID), nullStr(t.Summary))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.Repo, &t.RepoPath, &t.SHA, &t.Title, &t.Criterion,
		&t.CheckCmd, &t.Result, &t.Status, &t.CreatedAt, &t.ParentID, &t.Summary)
	return t, err
}

const taskCols = `id, repo, repo_path, sha, title, criterion, check_cmd, result, status, created_at, COALESCE(parent_id,0), COALESCE(summary,'')`

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

// AddComment appends a loose message to a task's thread (not part of a review).
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

// SetEmote sets (or clears, with "") a reaction on a comment — the agent's
// lightweight acknowledgement of reviewer feedback without a full reply. One emote
// per comment; setting it again overwrites.
func (s *Store) SetEmote(commentID int64, emote string) error {
	res, err := s.db.Exec(`UPDATE comments SET emote = ? WHERE id = ?`, nullStr(emote), commentID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("no comment #%d", commentID)
	}
	return nil
}

// markRead stamps one of the read columns (read_agent / read_user) on the given
// comments with the current time. Idempotent; unknown ids are ignored.
func (s *Store) markRead(col string, ids ...int64) error {
	if len(ids) == 0 {
		return nil
	}
	q := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, nowStamp())
	for i, id := range ids {
		q[i] = "?"
		args = append(args, id)
	}
	_, err := s.db.Exec(`UPDATE comments SET `+col+` = ? WHERE id IN (`+strings.Join(q, ",")+`)`, args...)
	return err
}

// MarkReadAgent records the agent's read-receipt on the given comments.
func (s *Store) MarkReadAgent(ids ...int64) error { return s.markRead("read_agent", ids...) }

// MarkReadUser records the user's read-receipt (set by the TUI on view).
func (s *Store) MarkReadUser(ids ...int64) error { return s.markRead("read_user", ids...) }

// UnreadByAgent returns reviewer comments the agent hasn't marked read yet — the
// loop's actionable feedback inbox (thread replies don't bump a review's state, so
// they're invisible to `review ls`; this surfaces them). Oldest first.
func (s *Store) UnreadByAgent() ([]Comment, error) {
	rows, err := s.db.Query(`SELECT ` + commentCols + ` FROM comments
		WHERE who = 'you' AND COALESCE(read_agent,'') = '' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddReply records a reply to an existing comment — the threading primitive. The
// reply inherits the parent's task and review context (so a reply to a line
// comment stays anchored to that review, and a reply to a loose thread message
// stays loose) and points at the parent via parent_id. who defaults to "agent"
// (the loop replying to reviewer feedback). Works the same for general and line
// comments since it keys only on the parent's id, not its anchoring.
func (s *Store) AddReply(parentID int64, who, body string) (int64, error) {
	if body == "" {
		return 0, fmt.Errorf("reply body is required")
	}
	if who == "" {
		who = "agent"
	}
	var taskID, reviewID int64
	if err := s.db.QueryRow(
		`SELECT task_id, COALESCE(review_id,0) FROM comments WHERE id = ?`,
		parentID).Scan(&taskID, &reviewID); err != nil {
		return 0, fmt.Errorf("parent comment %d: %w", parentID, err)
	}
	res, err := s.db.Exec(
		`INSERT INTO comments (task_id, review_id, parent_id, who, body, created_at)
		 VALUES (?,?,?,?,?,?)`,
		taskID, nullID(reviewID), parentID, who, body, nowStamp())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const commentCols = `id, task_id, COALESCE(review_id,0), COALESCE(parent_id,0), who, body, COALESCE(file,''), COALESCE(line,0), COALESCE(anchor,''), COALESCE(snippet,''), created_at, COALESCE(emote,''), COALESCE(read_agent,''), COALESCE(read_user,'')`

// commentColsC is commentCols qualified to the comments alias "c", for queries
// that join reviews (so column names aren't ambiguous).
const commentColsC = `c.id, c.task_id, COALESCE(c.review_id,0), COALESCE(c.parent_id,0), c.who, c.body, COALESCE(c.file,''), COALESCE(c.line,0), COALESCE(c.anchor,''), COALESCE(c.snippet,''), c.created_at, COALESCE(c.emote,''), COALESCE(c.read_agent,''), COALESCE(c.read_user,'')`

func scanComment(row interface{ Scan(...any) error }) (Comment, error) {
	var c Comment
	err := row.Scan(&c.ID, &c.TaskID, &c.ReviewID, &c.ParentID, &c.Who, &c.Body, &c.File, &c.Line, &c.Anchor, &c.Snippet, &c.CreatedAt, &c.Emote, &c.ReadAgent, &c.ReadUser)
	return c, err
}

// Comments returns a task's full thread (loose + review comments), oldest first.
func (s *Store) Comments(taskID int64) ([]Comment, error) {
	rows, err := s.db.Query(`SELECT `+commentCols+` FROM comments WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// TaskComment is a review comment plus whether it sits on the task's open draft
// (editable) versus a submitted/resolved review (read-only record).
type TaskComment struct {
	Comment
	Draft bool
}

// TaskReviewComments returns every review comment on a task (across all its
// reviews), oldest first, each flagged Draft if it's on the open draft review.
// This is what the comments pane shows so feedback stays visible after submit.
func (s *Store) TaskReviewComments(taskID int64) ([]TaskComment, error) {
	rows, err := s.db.Query(
		`SELECT `+commentColsC+`, r.state
		   FROM comments c JOIN reviews r ON r.id = c.review_id
		  WHERE c.task_id = ? AND c.review_id IS NOT NULL
		  ORDER BY c.id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskComment
	for rows.Next() {
		var c Comment
		var state string
		if err := rows.Scan(&c.ID, &c.TaskID, &c.ReviewID, &c.ParentID, &c.Who, &c.Body,
			&c.File, &c.Line, &c.Anchor, &c.Snippet, &c.CreatedAt, &c.Emote, &c.ReadAgent, &c.ReadUser, &state); err != nil {
			return nil, err
		}
		out = append(out, TaskComment{Comment: c, Draft: state == ReviewDraft})
	}
	return out, rows.Err()
}

// --- reviews ---------------------------------------------------------------

// draftReview returns the task's open draft review id, creating one if none.
func (s *Store) draftReview(taskID int64) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		`SELECT id FROM reviews WHERE task_id = ? AND state = ? ORDER BY id DESC LIMIT 1`,
		taskID, ReviewDraft).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	if _, err := s.Get(taskID); err != nil {
		return 0, err
	}
	res, err := s.db.Exec(
		`INSERT INTO reviews (task_id, state, created_at) VALUES (?,?,?)`,
		taskID, ReviewDraft, nowStamp())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DraftInfo reports the task's open draft review id and its comment count
// without creating one (so merely viewing a task never spawns a draft).
func (s *Store) DraftInfo(taskID int64) (reviewID int64, comments int, ok bool) {
	err := s.db.QueryRow(
		`SELECT id FROM reviews WHERE task_id = ? AND state = ? ORDER BY id DESC LIMIT 1`,
		taskID, ReviewDraft).Scan(&reviewID)
	if err != nil {
		return 0, 0, false
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM comments WHERE review_id = ?`, reviewID).Scan(&comments)
	return reviewID, comments, true
}

// Derived review state for a task — computed from its reviews, never a stale
// flag. A task NEEDS REWORK only while its newest *submitted* review is
// request_changes and unresolved. Approve shows APPROVED. Everything else
// (incl. tasks that only have unsubmitted drafts) is PENDING. Drafts are
// invisible to this — they surface via DraftInfo as a row pill instead.
const (
	StatePending = "pending"
	StateRework  = "rework"
	StateDone    = "approved"
)

// ReviewState returns the derived state for a task: the verdict of its newest
// submitted review decides it (request_changes→rework unless resolved,
// approve→approved). With no governing review it falls back to the stored
// status for the approved case (tasks approved directly, before reviews existed),
// otherwise pending.
func (s *Store) ReviewState(taskID int64) string {
	var verdict, state string
	err := s.db.QueryRow(
		`SELECT COALESCE(verdict,''), state FROM reviews
		   WHERE task_id = ? AND state IN (?, ?)
		   ORDER BY id DESC LIMIT 1`,
		taskID, ReviewSubmitted, ReviewResolved).Scan(&verdict, &state)
	if err != nil {
		// no submitted/resolved review — honour a stored direct approval only.
		if t, e := s.Get(taskID); e == nil && t.Status == StatusApproved {
			return StateDone
		}
		return StatePending
	}
	if state == ReviewResolved {
		return StatePending // addressed — back in the normal queue
	}
	switch verdict {
	case VerdictApprove:
		return StateDone
	case VerdictRequestChanges:
		return StateRework
	default:
		return StatePending // a non-blocking "comment" verdict
	}
}

// AddReviewComment adds a comment to the task's open draft review, optionally
// anchored to a diff line. Returns the comment id.
func (s *Store) AddReviewComment(taskID int64, who, body, file string, line int, anchor, snippet string) (int64, error) {
	if body == "" {
		return 0, fmt.Errorf("comment body is required")
	}
	if who == "" {
		who = "you"
	}
	rid, err := s.draftReview(taskID)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(
		`INSERT INTO comments (task_id, review_id, who, body, file, line, anchor, snippet, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		taskID, rid, who, body, nullStr(file), nullInt(line), nullStr(anchor), nullStr(snippet), nowStamp())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// draftCommentGuard returns the comment if it belongs to an open draft review
// (the only state in which comments are editable). Submitted/resolved review
// comments are an immutable record.
func (s *Store) draftCommentGuard(commentID int64) (Comment, error) {
	var c Comment
	var state string
	err := s.db.QueryRow(
		`SELECT c.id, COALESCE(c.review_id,0), COALESCE(r.state,'')
		   FROM comments c LEFT JOIN reviews r ON r.id = c.review_id
		  WHERE c.id = ?`, commentID).Scan(&c.ID, &c.ReviewID, &state)
	if err == sql.ErrNoRows {
		return Comment{}, fmt.Errorf("no comment #%d", commentID)
	}
	if err != nil {
		return Comment{}, err
	}
	if c.ReviewID == 0 || state != ReviewDraft {
		return Comment{}, fmt.Errorf("comment #%d is not on an open draft review", commentID)
	}
	return c, nil
}

// UpdateComment edits a draft review comment's body. Only draft comments are
// mutable; submitted reviews are immutable.
func (s *Store) UpdateComment(commentID int64, body string) error {
	if body == "" {
		return fmt.Errorf("comment body is required")
	}
	if _, err := s.draftCommentGuard(commentID); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE comments SET body = ? WHERE id = ?`, body, commentID)
	return err
}

// DeleteComment removes a single draft review comment (without discarding the
// whole draft). Only draft comments are deletable.
func (s *Store) DeleteComment(commentID int64) error {
	if _, err := s.draftCommentGuard(commentID); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM comments WHERE id = ?`, commentID)
	return err
}

// SubmitReview finalizes the task's draft review: records the verdict + summary,
// stamps it submitted, and flips the task status (request_changes→redo,
// approve→approved; comment leaves status untouched). Returns the review.
func (s *Store) SubmitReview(taskID int64, verdict, summary string) (Review, error) {
	if !validVerdict(verdict) {
		return Review{}, fmt.Errorf("invalid verdict %q (want request_changes|approve|comment)", verdict)
	}
	rid, err := s.draftReview(taskID)
	if err != nil {
		return Review{}, err
	}
	if _, err := s.db.Exec(
		`UPDATE reviews SET verdict = ?, summary = ?, state = ?, submitted_at = ? WHERE id = ?`,
		verdict, summary, ReviewSubmitted, nowStamp(), rid); err != nil {
		return Review{}, err
	}
	switch verdict {
	case VerdictApprove:
		if err := s.SetStatus(taskID, StatusApproved); err != nil {
			return Review{}, err
		}
	case VerdictRequestChanges:
		if err := s.SetStatus(taskID, StatusRedo); err != nil {
			return Review{}, err
		}
	}
	return s.GetReview(rid)
}

// UnsubmitReview reverses the task's most recent submitted review back to a
// draft, so it leaves AMENDS and returns to the INBOX with its comments editable
// again. A resolved review is left alone (it's already been addressed).
func (s *Store) UnsubmitReview(taskID int64) error {
	var rid int64
	err := s.db.QueryRow(
		`SELECT id FROM reviews WHERE task_id = ? AND state = ? ORDER BY id DESC LIMIT 1`,
		taskID, ReviewSubmitted).Scan(&rid)
	if err == sql.ErrNoRows {
		return fmt.Errorf("no submitted review to unsubmit on #%d", taskID)
	}
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`UPDATE reviews SET state = ?, verdict = '', submitted_at = NULL WHERE id = ?`,
		ReviewDraft, rid); err != nil {
		return err
	}
	// derived state recomputes from reviews; also clear the legacy status flag so
	// CLI views (which still read tasks.status) agree.
	return s.SetStatus(taskID, StatusPending)
}

// Delete removes a task and everything scoped to it — all its comments and
// reviews — in one transaction, and detaches any fix-forward children (their
// parent_id is cleared so they don't dangle at a deleted task). Errors if the
// task doesn't exist.
func (s *Store) Delete(taskID int64) error {
	if _, err := s.Get(taskID); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM comments WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM reviews WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tasks SET parent_id = NULL WHERE parent_id = ?`, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tasks WHERE id = ?`, taskID); err != nil {
		return err
	}
	return tx.Commit()
}

// AddRevision appends a new diff to a task (a fix-forward commit), so the task
// accumulates revisions instead of spawning a child task. Returns the revision id.
func (s *Store) AddRevision(taskID int64, sha, summary string) (int64, error) {
	if sha == "" {
		return 0, fmt.Errorf("revision sha is required")
	}
	if _, err := s.Get(taskID); err != nil {
		return 0, err
	}
	res, err := s.db.Exec(
		`INSERT INTO revisions (task_id, sha, summary, created_at) VALUES (?,?,?,?)`,
		taskID, sha, nullStr(summary), nowStamp())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Revisions returns a task's full diff history, oldest first: the synthetic base
// revision (from the task's own SHA + Summary) followed by every appended
// revision. The last element is always the latest diff.
func (s *Store) Revisions(taskID int64) ([]Revision, error) {
	t, err := s.Get(taskID)
	if err != nil {
		return nil, err
	}
	out := []Revision{{
		TaskID: taskID, SHA: t.SHA, Summary: t.Summary, CreatedAt: t.CreatedAt, Base: true,
	}}
	rows, err := s.db.Query(
		`SELECT id, task_id, sha, COALESCE(summary,''), created_at
		   FROM revisions WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Revision
		if err := rows.Scan(&r.ID, &r.TaskID, &r.SHA, &r.Summary, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// resolveOpenRequestChanges resolves a task's newest submitted request_changes
// review — the one a fix-forward addresses — returning its id (0 if none is open).
// This is what returns a revised task to the inbox: with the blocking review
// resolved, ReviewState derives back to pending.
func (s *Store) resolveOpenRequestChanges(taskID int64) (int64, error) {
	var rid int64
	err := s.db.QueryRow(
		`SELECT id FROM reviews WHERE task_id = ? AND state = ? AND verdict = ? ORDER BY id DESC LIMIT 1`,
		taskID, ReviewSubmitted, VerdictRequestChanges).Scan(&rid)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if err := s.ResolveReview(rid); err != nil {
		return 0, err
	}
	// keep the legacy status flag in sync (like UnsubmitReview) so flag-based CLI
	// views — `recap ls` — agree with derived state: the task is addressed and back
	// in the normal queue.
	if err := s.SetStatus(taskID, StatusPending); err != nil {
		return 0, err
	}
	return rid, nil
}

// DiscardReview deletes the task's open draft review and its comments.
func (s *Store) DiscardReview(taskID int64) error {
	rid, err := s.draftReview(taskID)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM comments WHERE review_id = ?`, rid); err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM reviews WHERE id = ?`, rid)
	return err
}

// ResolveReview marks a submitted review addressed (after a fix-forward commit).
func (s *Store) ResolveReview(id int64) error {
	res, err := s.db.Exec(`UPDATE reviews SET state = ? WHERE id = ?`, ReviewResolved, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no review #%d", id)
	}
	return nil
}

const reviewCols = `id, task_id, COALESCE(verdict,''), COALESCE(summary,''), state, created_at, COALESCE(submitted_at,'')`

// reviewColsR is reviewCols qualified to the reviews alias "r", for queries that
// join tasks (so the column names aren't ambiguous).
const reviewColsR = `r.id, r.task_id, COALESCE(r.verdict,''), COALESCE(r.summary,''), r.state, r.created_at, COALESCE(r.submitted_at,'')`

func scanReview(row interface{ Scan(...any) error }) (Review, error) {
	var r Review
	err := row.Scan(&r.ID, &r.TaskID, &r.Verdict, &r.Summary, &r.State, &r.CreatedAt, &r.SubmittedAt)
	return r, err
}

// GetReview returns one review by id.
func (s *Store) GetReview(id int64) (Review, error) {
	row := s.db.QueryRow(`SELECT `+reviewCols+` FROM reviews WHERE id = ?`, id)
	r, err := scanReview(row)
	if err == sql.ErrNoRows {
		return Review{}, fmt.Errorf("no review #%d", id)
	}
	return r, err
}

// ReviewComments returns a review's comments, oldest first.
func (s *Store) ReviewComments(reviewID int64) ([]Comment, error) {
	rows, err := s.db.Query(`SELECT `+commentCols+` FROM comments WHERE review_id = ? ORDER BY id`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Reviews returns a task's reviews, oldest first.
func (s *Store) Reviews(taskID int64) ([]Review, error) {
	rows, err := s.db.Query(`SELECT `+reviewCols+` FROM reviews WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Review
	for rows.Next() {
		r, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestReviewIDs returns, per task, the id of its most recent review. Reviews are
// append-only so a higher id means a later review — i.e. the order in which tasks
// were last acted on (used to sort the done section "last completed first"). One
// query for the whole table; tasks with no review are simply absent from the map.
func (s *Store) LatestReviewIDs() (map[int64]int64, error) {
	rows, err := s.db.Query(`SELECT task_id, MAX(id) FROM reviews GROUP BY task_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]int64)
	for rows.Next() {
		var taskID, maxID int64
		if err := rows.Scan(&taskID, &maxID); err != nil {
			return nil, err
		}
		out[taskID] = maxID
	}
	return out, rows.Err()
}

// ListReviews returns reviews filtered by optional state and repo (empty = no
// filter), newest first. repo matches the parent task's repo, so the loop can
// scope review draining to the repo it's working in.
func (s *Store) ListReviews(state, repo string) ([]Review, error) {
	q := `SELECT ` + reviewColsR + ` FROM reviews r`
	var args []any
	var conds []string
	if repo != "" {
		q += ` JOIN tasks t ON t.id = r.task_id`
		conds = append(conds, `t.repo = ?`)
		args = append(args, repo)
	}
	if state != "" {
		conds = append(conds, `r.state = ?`)
		args = append(args, state)
	}
	for i, c := range conds {
		if i == 0 {
			q += ` WHERE ` + c
		} else {
			q += ` AND ` + c
		}
	}
	q += ` ORDER BY r.id DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Review
	for rows.Next() {
		r, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- null helpers ----------------------------------------------------------

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
