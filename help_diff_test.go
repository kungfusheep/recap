package main

import (
	"strings"
	"testing"

	. "github.com/kungfusheep/glyph"
)

// the ? cheatsheet shows the diff keybinds, including the new fold + file-nav keys.
// (add diff keybinds to ? menu) — verified by rendering the overlay.
func TestHelpShowsDiffKeybinds(t *testing.T) {
	buf := NewBuffer(90, 30)
	Build(helpOverlay()).Execute(buf, 90, 30)
	var all string
	for y := 0; y < 30; y++ {
		all += " " + buf.GetLine(y)
	}
	for _, want := range []string{"diff", "next / prev file", "fold file / all", "] / [", "z / Z"} {
		if !strings.Contains(all, want) {
			t.Fatalf("? cheatsheet missing %q\n%s", want, all)
		}
	}
}
