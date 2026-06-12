package db

import (
	"fmt"
	"strings"
)

// Proposal is a cross-repo work proposal: a DOCUMENT under multi-party review,
// living entirely in the recap db (no repo artifacts until sign-off). Its own
// table — not a task kind — so the feature can grow fields without riding
// every task query (the reviewer's call on docs/proposal-workflow.md Q1).
type Proposal struct {
	ID           int64
	Title        string
	Body         string // the proposal document (briefing markup renders it)
	ProposerRepo string
	ProposerWho  string
	TargetRepo   string // the repo that would own the work
	Status       string // open | approved | declined
	CreatedAt    string
	DecidedAt    string
}

const (
	ProposalOpen     = "open"
	ProposalApproved = "approved"
	ProposalDeclined = "declined"
)

const proposalSchema = `
CREATE TABLE IF NOT EXISTS proposals (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	title TEXT NOT NULL,
	body TEXT NOT NULL,
	proposer_repo TEXT NOT NULL,
	proposer_who TEXT,
	target_repo TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'open',
	created_at TEXT NOT NULL,
	decided_at TEXT
);
CREATE TABLE IF NOT EXISTS proposal_parties (
	proposal_id INTEGER NOT NULL,
	repo TEXT NOT NULL,
	added_at TEXT NOT NULL,
	PRIMARY KEY (proposal_id, repo)
);`

// AddProposal creates an open proposal and registers the initial parties:
// the proposer, the target, and any tagged repos (deduplicated).
func (s *Store) AddProposal(p Proposal, tags []string) (int64, error) {
	if p.Title == "" || p.Body == "" {
		return 0, fmt.Errorf("proposal title and body are required")
	}
	if p.TargetRepo == "" {
		return 0, fmt.Errorf("--target repo is required (who would own the work)")
	}
	res, err := s.db.Exec(`INSERT INTO proposals
		(title, body, proposer_repo, proposer_who, target_repo, status, created_at)
		VALUES (?,?,?,?,?,?,?)`,
		p.Title, p.Body, p.ProposerRepo, p.ProposerWho, p.TargetRepo, ProposalOpen, NowStamp())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	seen := map[string]bool{}
	for _, r := range append([]string{p.ProposerRepo, p.TargetRepo}, tags...) {
		r = strings.TrimSpace(r)
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		if err := s.AddProposalParty(id, r); err != nil {
			return id, err
		}
	}
	return id, nil
}

// AddProposalParty registers a repo as an interested party (idempotent).
func (s *Store) AddProposalParty(id int64, repo string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO proposal_parties (proposal_id, repo, added_at) VALUES (?,?,?)`,
		id, repo, NowStamp())
	return err
}

// ProposalParties lists the repos party to a proposal, in join order.
func (s *Store) ProposalParties(id int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT repo FROM proposal_parties WHERE proposal_id = ? ORDER BY added_at, repo`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const proposalCols = `id, title, body, proposer_repo, COALESCE(proposer_who,''), target_repo, status, created_at, COALESCE(decided_at,'')`

func scanProposal(row interface{ Scan(...any) error }) (Proposal, error) {
	var p Proposal
	err := row.Scan(&p.ID, &p.Title, &p.Body, &p.ProposerRepo, &p.ProposerWho,
		&p.TargetRepo, &p.Status, &p.CreatedAt, &p.DecidedAt)
	return p, err
}

// ProposalByID fetches one proposal.
func (s *Store) ProposalByID(id int64) (Proposal, error) {
	return scanProposal(s.db.QueryRow(`SELECT `+proposalCols+` FROM proposals WHERE id = ?`, id))
}

// Proposals lists proposals, newest first; status "" = all.
func (s *Store) Proposals(status string) ([]Proposal, error) {
	rows, err := s.db.Query(`SELECT `+proposalCols+` FROM proposals
		WHERE (? = '' OR status = ?) ORDER BY id DESC`, status, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DecideProposal records the human's verdict (approved/declined). Sign-off
// side effects (ADR, managing todo) belong to the caller.
func (s *Store) DecideProposal(id int64, status string) error {
	if status != ProposalApproved && status != ProposalDeclined {
		return fmt.Errorf("status must be %s or %s", ProposalApproved, ProposalDeclined)
	}
	res, err := s.db.Exec(`UPDATE proposals SET status = ?, decided_at = ? WHERE id = ? AND status = ?`,
		status, NowStamp(), id, ProposalOpen)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no OPEN proposal #%d", id)
	}
	return nil
}

// ProposalComment is one entry in a proposal's deliberation thread. The thread
// lives here (recap is the record); delivery to parties rides the message queue.
type ProposalComment struct {
	ID         int64
	ProposalID int64
	WhoRepo    string
	WhoName    string
	Body       string
	CreatedAt  string
}

const proposalCommentSchema = `
CREATE TABLE IF NOT EXISTS proposal_comments (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	proposal_id INTEGER NOT NULL,
	who_repo TEXT NOT NULL,
	who_name TEXT,
	body TEXT NOT NULL,
	created_at TEXT NOT NULL
);`

// AddProposalComment appends to the thread; the commenting repo becomes a
// party if it wasn't already (commenting = interest).
func (s *Store) AddProposalComment(proposalID int64, whoRepo, whoName, body string) (int64, error) {
	if body == "" {
		return 0, fmt.Errorf("comment body is required")
	}
	if _, err := s.ProposalByID(proposalID); err != nil {
		return 0, fmt.Errorf("no proposal #%d", proposalID)
	}
	res, err := s.db.Exec(`INSERT INTO proposal_comments (proposal_id, who_repo, who_name, body, created_at)
		VALUES (?,?,?,?,?)`, proposalID, whoRepo, whoName, body, NowStamp())
	if err != nil {
		return 0, err
	}
	if err := s.AddProposalParty(proposalID, whoRepo); err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ProposalComments returns the thread, oldest first.
func (s *Store) ProposalComments(proposalID int64) ([]ProposalComment, error) {
	rows, err := s.db.Query(`SELECT id, proposal_id, who_repo, COALESCE(who_name,''), body, created_at
		FROM proposal_comments WHERE proposal_id = ? ORDER BY id`, proposalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProposalComment
	for rows.Next() {
		var c ProposalComment
		if err := rows.Scan(&c.ID, &c.ProposalID, &c.WhoRepo, &c.WhoName, &c.Body, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// LatestTaskPerRepo returns each repo's most recent task — the "last thing
// they did" line on the agent dashboard.
func (s *Store) LatestTaskPerRepo() (map[string]Task, error) {
	rows, err := s.db.Query(`SELECT ` + taskCols + ` FROM tasks t
		JOIN (SELECT repo r, MAX(id) mid FROM tasks GROUP BY repo) m ON t.id = m.mid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out[t.Repo] = t
	}
	return out, rows.Err()
}
