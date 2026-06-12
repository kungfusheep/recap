package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/kungfusheep/recap/db"
	"github.com/kungfusheep/recap/notify"
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
	// notify the target + tagged repos through the durable queue
	parties, _ := st.ProposalParties(id)
	for _, r := range parties {
		if r == currentRepo() {
			continue
		}
		st.SendMessage(currentRepo(), identityWho(), r, 0, 0,
			fmt.Sprintf("proposal #%d awaits your review: %q (target: %s) — recap proposal show %d", id, *title, *target, id))
	}
	notify.Reload()
	fmt.Printf("proposal #%d opened (target %s, parties: %s)\n", id, *target, strings.Join(parties, ", "))
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
	default:
		return fmt.Errorf("unknown proposal subcommand %q (show|ls)", args[0])
	}
}
