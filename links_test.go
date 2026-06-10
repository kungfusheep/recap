package main

import (
	"github.com/kungfusheep/recap/links"
	"reflect"
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
)

// pasting a screenshot inserts a [[path]] link into the prompt field at the
// cursor, space-separated from existing text.
func TestInsertCommentLink(t *testing.T) {
	prev := promptUI.Field
	t.Cleanup(func() { promptUI.Field = prev })

	promptUI.Field = InputState{}
	promptUI.insertLink("/tmp/recap-1.png")
	if promptUI.Field.Value != "[[/tmp/recap-1.png]]" {
		t.Fatalf("into empty: %q", promptUI.Field.Value)
	}
	promptUI.insertLink("/tmp/recap-2.png")
	if promptUI.Field.Value != "[[/tmp/recap-1.png]] [[/tmp/recap-2.png]]" {
		t.Fatalf("append spacing: %q", promptUI.Field.Value)
	}
	// the extractor round-trips what was inserted
	links := links.Extract(promptUI.Field.Value)
	if len(links) != 2 || links[0] != "/tmp/recap-1.png" {
		t.Fatalf("extract after insert: %v", links)
	}
	// when text already ends in a space, no extra space is added
	promptUI.Field = InputState{Value: "see this ", Cursor: len("see this ")}
	promptUI.insertLink("/tmp/x.png")
	if promptUI.Field.Value != "see this [[/tmp/x.png]]" || strings.Contains(promptUI.Field.Value, "  ") {
		t.Fatalf("trailing-space handling: %q", promptUI.Field.Value)
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
		if got := links.Extract(tc.body); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("links.Extract(%q) = %v, want %v", tc.body, got, tc.want)
		}
	}
}
