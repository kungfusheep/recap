package main

import (
	"reflect"
	"strings"
	"testing"
)

// pasting a screenshot inserts a [[path]] link into the prompt, space-separated
// from existing text, and never pollutes the saved commentText with the caret.
func TestInsertCommentLink(t *testing.T) {
	prev, prevLines := commentText, commentLines
	t.Cleanup(func() { commentText = prev; commentLines = prevLines })

	commentText = ""
	insertCommentLink("/tmp/recap-1.png")
	if commentText != "[[/tmp/recap-1.png]]" {
		t.Fatalf("into empty: %q", commentText)
	}
	insertCommentLink("/tmp/recap-2.png")
	if commentText != "[[/tmp/recap-1.png]] [[/tmp/recap-2.png]]" {
		t.Fatalf("append spacing: %q", commentText)
	}
	// the extractor round-trips what was inserted
	links := extractLinks(commentText)
	if len(links) != 2 || links[0] != "/tmp/recap-1.png" {
		t.Fatalf("extract after insert: %v", links)
	}
	// when text already ends in a space, no extra space is added
	commentText = "see this "
	insertCommentLink("/tmp/x.png")
	if commentText != "see this [[/tmp/x.png]]" || strings.Contains(commentText, "  ") {
		t.Fatalf("trailing-space handling: %q", commentText)
	}
}

// comments can embed [[file]] references (e.g. screenshots); extractLinks pulls
// them out in order, trims whitespace, and skips empties.
func TestExtractLinks(t *testing.T) {
	cases := []struct {
		body string
		want []string
	}{
		{"see [[/tmp/shot.png]] for the glitch", []string{"/tmp/shot.png"}},
		{"two: [[ a.png ]] and [[b.png]]", []string{"a.png", "b.png"}},
		{"no links here", nil},
		{"empty [[]] ignored, keep [[ok.png]]", []string{"ok.png"}},
		{"[[~/Desktop/Screenshot 2026.png]]", []string{"~/Desktop/Screenshot 2026.png"}},
	}
	for _, tc := range cases {
		if got := extractLinks(tc.body); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("extractLinks(%q) = %v, want %v", tc.body, got, tc.want)
		}
	}
}
