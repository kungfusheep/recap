package main

import (
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
)

func flatText(rows [][]Span) string {
	var b strings.Builder
	for _, r := range rows {
		for _, sp := range r {
			b.WriteString(sp.Text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// the briefing mini-markup: bullets get a glyph + hanging indent, Label: leads
// and **bold** render bold, `code` gets the identifier colour, and styles
// survive wrapping. Plain text still renders as a wrapped paragraph.
func TestSummaryBodyMarkup(t *testing.T) {
	text := "Why: the cache was rebuilt **every frame** which broke `RequestRender` timing.\n" +
		"- Verify: run `go test -run TestX` and watch the spinner\n" +
		"- second bullet long enough that it must wrap onto a continuation line to prove hanging indent works here\n" +
		"plain closing paragraph"
	rows := summaryBody(text, 60)
	flat := flatText(rows)

	// markup characters never reach the screen
	if strings.Contains(flat, "**") || strings.Contains(flat, "`") {
		t.Fatalf("markup leaked into rendered text:\n%s", flat)
	}
	// label lead is its own bold-bright span
	if rows[0][1].Text != "Why:" || rows[0][1].Style.Attr&AttrBold == 0 {
		t.Fatalf("Why: lead not bold, got %+v", rows[0][1])
	}
	// **bold** segment is bold
	found := false
	for _, sp := range rows[0] {
		if strings.TrimSpace(sp.Text) == "every" && sp.Style.Attr&AttrBold != 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("bold segment not styled: %+v", rows[0])
	}
	// bullets start with the glyph; continuation lines hang-indent without it
	if !strings.Contains(flat, "  · Verify:") {
		t.Fatalf("bullet row missing glyph/label:\n%s", flat)
	}
	bulletLines := 0
	for _, r := range rows {
		if len(r) > 0 && r[0].Text == "  · " {
			bulletLines++
		}
	}
	if bulletLines != 2 {
		t.Fatalf("want 2 bullet rows, got %d:\n%s", bulletLines, flat)
	}
	if !strings.Contains(flat, "\n    ") {
		t.Fatalf("wrapped bullet continuation should hang-indent:\n%s", flat)
	}
	// `code` spans carry the identifier colour, split across words
	code := false
	for _, r := range rows {
		for _, sp := range r {
			if strings.Contains(sp.Text, "TestX") && sp.Style.FG == cHunk {
				code = true
			}
		}
	}
	if !code {
		t.Fatalf("code span not coloured:\n%s", flat)
	}

	// unstructured text: single paragraph, wrapped, no markup machinery
	plain := summaryBody("just a plain old summary with no structure at all", 24)
	if len(plain) < 2 {
		t.Fatalf("plain text should wrap at width: %d rows", len(plain))
	}
	for _, r := range plain {
		if len(r) > 0 && r[0].Text != "  " {
			t.Fatalf("plain rows keep the two-space lead, got %q", r[0].Text)
		}
	}
}
