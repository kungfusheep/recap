package main

import (
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
)

// the comment/todo prompt wraps a long body across multiple rows (todo #47), instead of
// scrolling it on one line. Verified by rendering the real prompt overlay. The two
// vertical markers are the title and a long body; we assert the body text appears on
// several rows and keeps its tail.
func TestPromptWrapsLongBody(t *testing.T) {
	prev, prevOpen := commentField, promptOpen
	t.Cleanup(func() { commentField = prev; promptOpen = prevOpen })
	promptOpen = true

	body := "this is a deliberately long comment body that must wrap across several lines inside the prompt overlay instead of scrolling horizontally on one single line"
	commentField = InputState{Value: body, Cursor: len(body)}
	promptTitle, promptLoc, promptSnip = "comment", "", ""

	tmpl := Build(inputPromptOverlay())
	buf := NewBuffer(90, 24)
	tmpl.Execute(buf, 90, 24)

	bodyRows, all := 0, ""
	for y := 0; y < 24; y++ {
		line := strings.TrimSpace(buf.GetLine(y))
		all += " " + line
		// a row is a body row if it carries words from the body (not the title/chrome)
		if strings.Contains(line, "wrap") || strings.Contains(line, "scrolling") || strings.Contains(line, "single line") || strings.Contains(line, "deliberately") {
			bodyRows++
		}
	}
	if bodyRows < 2 {
		t.Fatalf("prompt body did not wrap: %d body rows\n%s", bodyRows, all)
	}
	if !strings.Contains(all, "single line") {
		t.Fatalf("wrapped body lost its tail: %s", all)
	}
}
