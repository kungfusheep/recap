package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/kungfusheep/recap/config"
	"github.com/kungfusheep/recap/cursor"
	"github.com/kungfusheep/recap/db"
	"github.com/kungfusheep/recap/notify"
	"github.com/kungfusheep/recap/todo"
)

// cmdPropose creates a cross-repo work proposal: a document under multi-party
// review, stored in the recap db (no repo artifacts until the human signs off).
// Tagged repos are notified through the message queue — same delivery, read
// receipts, and parking semantics as every other peer message.
func cmdPropose(args []string) error {
	fs := flag.NewFlagSet("propose", flag.ExitOnError)
	target := fs.String("target", "", "repo that would OWN the proposed work (required)")
	title := fs.String("title", "", "proposal title (required)")
	body := fs.String("body", "", "the proposal document (or --file)")
	file := fs.String("file", "", "read the proposal document from a file")
	tags := fs.String("tag", "", "comma-separated repos to notify for review")
	resolves := fs.String("resolves", "", "todo ref this proposal RESOLVES (todo:<hash> from recap next) — marks the todo done; nothing lands in the review inbox")
	fs.Parse(args)
	if *file != "" {
		b, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		*body = string(b)
	}
	st, err := db.Open()
	if err != nil {
		return err
	}
	defer st.Close()

	var tagged []string
	if *tags != "" {
		tagged = strings.Split(*tags, ",")
	}
	id, err := st.AddProposal(db.Proposal{
		Title: *title, Body: *body,
		ProposerRepo: currentRepo(), ProposerWho: identityWho(),
		TargetRepo: *target,
	}, tagged)
	if err != nil {
		return err
	}
	// one attention ping per party through the durable queue (digest model:
	// further activity won't re-ping while this sits unread)
	parties, _ := st.ProposalParties(id)
	for _, r := range parties {
		if r == currentRepo() {
			continue
		}
		st.SendProposalPing(id, currentRepo(), identityWho(), r,
			fmt.Sprintf("proposal #%d awaits your review: %q (target: %s) — recap proposal show %d", id, *title, *target, id))
	}
	notify.Reload()
	fmt.Printf("proposal #%d opened (target %s, parties: %s)\n", id, *target, strings.Join(parties, ", "))
	// a todo that ASKED for this proposal resolves through the act of opening
	// it (todo:a7d5f91d): the line ticks, the queue advances, and no task lands
	// in the review inbox — the proposal row IS the reviewable artifact.
	if *resolves != "" {
		item := findRef(buildQueue(st, currentRepo(), currentRepoPath()), *resolves)
		switch {
		case item.Ref == "":
			fmt.Fprintf(os.Stderr, "recap: proposal opened, but no queue item %q to resolve\n", *resolves)
		case item.Kind != "todo":
			fmt.Fprintf(os.Stderr, "recap: proposal opened, but %q is a %s, not a todo\n", *resolves, item.Kind)
		default:
			if cfg, err := config.LoadConfig(); err == nil {
				if path, err := todo.PathFor(cfg.TODOTemplate, currentRepoPath()); err == nil && path != "" {
					if err := markTodoLineDone(path, item.Title); err != nil {
						fmt.Fprintf(os.Stderr, "recap: couldn't mark the TODO line (%v)\n", err)
					}
				}
			}
			if cur, _ := cursor.Load(currentRepo()); cur == *resolves {
				cursor.Save(currentRepo(), "", "")
			}
			notify.Reload()
			fmt.Printf("resolved %s via proposal #%d\n", *resolves, id)
		}
	}
	return nil
}

// cmdProposal is the proposal subcommand family: show / ls.
func cmdProposal(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: recap proposal show <id> | recap proposal ls [--all]")
	}
	st, err := db.Open()
	if err != nil {
		return err
	}
	defer st.Close()
	switch args[0] {
	case "show":
		if len(args) < 2 {
			return fmt.Errorf("usage: recap proposal show <id>")
		}
		id, err := parseID(args[1])
		if err != nil {
			return err
		}
		p, err := st.ProposalByID(id)
		if err != nil {
			return fmt.Errorf("no proposal #%d", id)
		}
		parties, _ := st.ProposalParties(id)
		fmt.Printf("proposal #%d  ·  %s  ·  %s\n", p.ID, strings.ToUpper(p.Status), p.CreatedAt)
		fmt.Printf("title:    %s\n", p.Title)
		fmt.Printf("proposer: %s@%s\n", dash(p.ProposerWho), p.ProposerRepo)
		fmt.Printf("target:   %s\n", p.TargetRepo)
		fmt.Printf("parties:  %s\n", strings.Join(parties, ", "))
		if p.DecidedAt != "" {
			fmt.Printf("decided:  %s\n", p.DecidedAt)
		}
		fmt.Printf("\n%s\n", p.Body)
		if cs, _ := st.ProposalComments(id); len(cs) > 0 {
			seen := st.PartyWatermark(id, currentRepo())
			fmt.Printf("\nthread (%d):\n", len(cs))
			marked := false
			depth := map[int64]int{}
			for _, c := range cs {
				if !marked && c.ID > seen && seen > 0 {
					fmt.Println("  ── new since your last look ──")
					marked = true
				}
				d := 0
				if c.ParentID != 0 {
					d = depth[c.ParentID] + 1
				}
				depth[c.ID] = d
				indent := strings.Repeat("  ", d)
				bullet := "•"
				if d > 0 {
					bullet = "↳"
				}
				anchor := ""
				if c.Line > 0 {
					anchor = fmt.Sprintf(" [line %d: %s]", c.Line, c.Snippet)
				}
				fmt.Printf("  %s%s pc%d [%s] %s@%s:%s %s\n", indent, bullet, c.ID, c.CreatedAt, dash(c.WhoName), c.WhoRepo, anchor, c.Body)
			}
			st.AdvancePartyWatermark(id, currentRepo(), cs[len(cs)-1].ID)
		}
		return nil
	case "ls":
		status := db.ProposalOpen
		if len(args) > 1 && args[1] == "--all" {
			status = ""
		}
		ps, err := st.Proposals(status)
		if err != nil {
			return err
		}
		if len(ps) == 0 {
			fmt.Println("(no proposals)")
			return nil
		}
		for _, p := range ps {
			fmt.Printf("#%-3d %-9s %s → %s  %s\n", p.ID, p.Status, p.ProposerRepo, p.TargetRepo, p.Title)
		}
		return nil
	case "comment":
		fs := flag.NewFlagSet("proposal comment", flag.ExitOnError)
		body := fs.String("body", "", "comment text (@repo adds that repo as a party)")
		line := fs.Int("line", 0, "anchor to a document line (1-based; snippet captured automatically)")
		replyTo := fs.Int64("reply-to", 0, "thread this under an existing comment id")
		if len(args) < 2 {
			return fmt.Errorf("usage: recap proposal comment <id> --body TEXT [--line N]")
		}
		id, err := parseID(args[1])
		if err != nil {
			return err
		}
		fs.Parse(args[2:])
		if *body == "" {
			return fmt.Errorf("--body is required")
		}
		// a line anchor captures the document line's text as the snippet, so
		// the anchor stays readable even if the document is later revised.
		snippet := ""
		if *line > 0 {
			doc, err := st.ProposalByID(id)
			if err != nil {
				return fmt.Errorf("no proposal #%d", id)
			}
			lines := strings.Split(doc.Body, "\n")
			if *line > len(lines) {
				return fmt.Errorf("--line %d is past the document's end (%d lines)", *line, len(lines))
			}
			snippet = strings.TrimSpace(lines[*line-1])
		}
		if _, err := st.AddProposalThreadComment(id, currentRepo(), identityWho(), *body, *line, snippet, *replyTo); err != nil {
			return err
		}
		// @mentions join the conversation: each @repo becomes a party; the ping
		// below is their invite.
		p, _ := st.ProposalByID(id)
		for _, m := range atMentions(*body) {
			st.AddProposalParty(id, m)
		}
		// digest model (c429): ONE attention ping per party per proposal — if a
		// party already has an unread ping, nothing more is sent; they read the
		// whole thread since their last look when they next open it.
		parties, _ := st.ProposalParties(id)
		for _, r := range parties {
			if r == currentRepo() {
				continue
			}
			st.SendProposalPing(id, currentRepo(), identityWho(), r,
				fmt.Sprintf("proposal #%d (%q) has new activity — recap proposal show %d", id, p.Title, id))
		}
		notify.Reload()
		fmt.Printf("commented on proposal #%d\n", id)
		return nil
	default:
		return fmt.Errorf("unknown proposal subcommand %q (show|ls|comment)", args[0])
	}
}

// atMentions extracts @repo tokens from a comment body.
func atMentions(body string) []string {
	var out []string
	for _, f := range strings.Fields(body) {
		if strings.HasPrefix(f, "@") && len(f) > 1 {
			out = append(out, strings.Trim(f[1:], ".,;:!?"))
		}
	}
	return out
}
