package db

import (
	"database/sql"
	"fmt"
)

// Message is one agent→agent async note, addressed to a REPO (not a process): it
// queues durably in the shared db, so "nobody listening in that project" just means
// it waits — the next loop that runs `recap next` there picks it up, and a parked
// `recap next --wait` wakes on it. The human sees all traffic (TUI header count +
// `recap messages`); messages coordinate work, they never carry verdicts — approvals
// stay human.
type Message struct {
	ID        int64
	FromRepo  string // sender's repo (its loop scope)
	FromWho   string // sender's per-repo identity name (e.g. "Kestrel")
	ToRepo    string // target repo — the queue this lands in
	ParentID  int64  // threads a reply under an earlier message (0 = top-level)
	TaskID    int64  // optional anchor to a task (0 = repo-level note)
	Body      string
	CreatedAt string
	ReadAgent string // when the TARGET repo's agent read it (clears it from the queue)
	ReadUser  string // when the human saw it
}

const messageSchema = `
CREATE TABLE IF NOT EXISTS messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	from_repo TEXT NOT NULL,
	from_who TEXT NOT NULL,
	to_repo TEXT NOT NULL,
	parent_id INTEGER,
	task_id INTEGER,
	body TEXT NOT NULL,
	created_at TEXT NOT NULL,
	read_agent TEXT,
	read_user TEXT
);`

const messageCols = `id, from_repo, from_who, to_repo, COALESCE(parent_id,0), COALESCE(task_id,0), body, created_at, COALESCE(read_agent,''), COALESCE(read_user,'')`

func scanMessage(row interface{ Scan(...any) error }) (Message, error) {
	var m Message
	err := row.Scan(&m.ID, &m.FromRepo, &m.FromWho, &m.ToRepo, &m.ParentID, &m.TaskID,
		&m.Body, &m.CreatedAt, &m.ReadAgent, &m.ReadUser)
	return m, err
}

// SendMessage queues a message for toRepo. parentID threads it under an earlier
// message (0 for top-level); taskID optionally anchors it to a task.
func (s *Store) SendMessage(fromRepo, fromWho, toRepo string, parentID, taskID int64, body string) (int64, error) {
	if toRepo == "" && fromRepo == "" {
		// an empty target is the HUMAN (agent→human DM replies); the human
		// sending to nobody is the only nonsensical combination.
		return 0, fmt.Errorf("target repo is required")
	}
	if body == "" {
		return 0, fmt.Errorf("message body is required")
	}
	if fromWho == "" {
		fromWho = "agent"
	}
	if parentID != 0 {
		var exists int64
		if err := s.db.QueryRow(`SELECT id FROM messages WHERE id = ?`, parentID).Scan(&exists); err == sql.ErrNoRows {
			return 0, fmt.Errorf("no message m%d to reply to", parentID)
		} else if err != nil {
			return 0, err
		}
	}
	res, err := s.db.Exec(
		`INSERT INTO messages (from_repo, from_who, to_repo, parent_id, task_id, body, created_at)
		 VALUES (?,?,?,?,?,?,?)`,
		fromRepo, fromWho, toRepo, nullID(parentID), nullID(taskID), body, NowStamp())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UnreadMessages returns the messages addressed to repo that its agent hasn't read
// — the peer tier of `recap next`. Oldest first (FIFO).
func (s *Store) UnreadMessages(repo string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT `+messageCols+` FROM messages
		WHERE to_repo = ? AND COALESCE(read_agent,'') = '' ORDER BY id`, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Messages lists a repo's traffic in both directions (sent + received), oldest
// first — the human-readable ledger behind `recap messages`. repo=="" lists all.
func (s *Store) Messages(repo string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT `+messageCols+` FROM messages
		WHERE (? = '' OR to_repo = ? OR from_repo = ?) ORDER BY id`, repo, repo, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UnreadMessageCount is the cross-repo count of agent-unread messages — the TUI's
// header badge, so the human always sees pending peer traffic at a glance.
func (s *Store) UnreadMessageCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE COALESCE(read_agent,'') = ''`).Scan(&n)
	return n, err
}

// MarkMessageReadAgent records the target agent's read-receipt — this is what
// completes a message item in `recap next`.
func (s *Store) MarkMessageReadAgent(ids ...int64) error {
	return s.markMessageRead("read_agent", ids...)
}

// MarkMessageReadUser records the human's read-receipt (set by the TUI on view).
func (s *Store) MarkMessageReadUser(ids ...int64) error {
	return s.markMessageRead("read_user", ids...)
}

func (s *Store) markMessageRead(col string, ids ...int64) error {
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		res, err := s.db.Exec(`UPDATE messages SET `+col+` = ? WHERE id = ?`, NowStamp(), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("no message m%d", id)
		}
	}
	return nil
}

// LatestAgentComment returns the newest comment authored by an AGENT (not the
// human) — the TUI's arrival notifications watermark against it.
func (s *Store) LatestAgentComment() (id int64, who string, taskID int64) {
	s.db.QueryRow(`SELECT id, who, task_id FROM comments WHERE who != 'you' ORDER BY id DESC LIMIT 1`).
		Scan(&id, &who, &taskID)
	return
}

// LatestPeerMessage returns the newest agent-sent message (the human's own
// sends have no from_repo) — again for arrival notifications.
func (s *Store) LatestPeerMessage() (id int64, fromWho, fromRepo, body string) {
	s.db.QueryRow(`SELECT id, COALESCE(from_who,''), from_repo, body FROM messages WHERE from_repo != '' ORDER BY id DESC LIMIT 1`).
		Scan(&id, &fromWho, &fromRepo, &body)
	return
}

// MessageByID fetches one message.
func (s *Store) MessageByID(id int64) (Message, error) {
	rows, err := s.db.Query(`SELECT `+messageCols+` FROM messages WHERE id = ?`, id)
	if err != nil {
		return Message{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Message{}, fmt.Errorf("no message m%d", id)
	}
	return scanMessage(rows)
}

// MessagesWith returns the two-way DM channel between the HUMAN and one
// repo's loop, oldest first: the human's messages to the repo, and the
// loop's messages addressed straight back (to_repo ""). Agent↔agent
// traffic stays out of the dialogue.
func (s *Store) MessagesWith(repo string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT `+messageCols+` FROM messages
		WHERE (from_repo = '' AND to_repo = ?) OR (from_repo = ? AND to_repo = '') ORDER BY id`, repo, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
