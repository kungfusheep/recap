package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// diffTarget resolves the file + line to open from the diff at the current scroll
// position: the first code row (File set, real line number) at or after the top of
// the viewport, falling back to the first code row in the whole diff.
func diffTarget() (file string, line int, repo string) {
	t, ok := selectedTask()
	if !ok {
		return "", 0, ""
	}
	repo = t.RepoPath
	start := 0
	if diffLayer != nil {
		start = diffLayer.ScrollY()
	}
	pick := func(from int) (string, int, bool) {
		for i := from; i < len(diffMeta); i++ {
			if diffMeta[i].File != "" && diffMeta[i].Line > 0 {
				return diffMeta[i].File, diffMeta[i].Line, true
			}
		}
		return "", 0, false
	}
	if f, l, ok := pick(start); ok {
		return f, l, repo
	}
	if f, l, ok := pick(0); ok {
		return f, l, repo
	}
	return "", 0, repo
}

// editorArgs builds argv to open file at line. The +N form positions the cursor in
// vim/nvim/nano/emacs (the user runs neovim); line 0 just opens the file. EDITOR
// may carry flags ("nvim --clean"), so it's split on spaces.
func editorArgs(editor, file string, line int) []string {
	args := strings.Fields(editor)
	if len(args) == 0 {
		args = []string{"vim"}
	}
	if line > 0 {
		args = append(args, fmt.Sprintf("+%d", line))
	}
	return append(args, file)
}

// openInEditor suspends the TUI and opens the diff's file at its line in $EDITOR,
// replacing our view until the editor exits, then restores the TUI. The terminal
// is handed over via ExitRawMode and reclaimed via EnterRawMode; ForceRedraw
// repaints (re-entering raw mode clears the screen).
func openInEditor() {
	file, line, repo := diffTarget()
	if file == "" {
		statusMsg = "no file under the diff to open"
		return
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}
	argv := editorArgs(editor, file, line)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = repo
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	sc := uiApp.Screen()
	_ = sc.ExitRawMode()
	runErr := cmd.Run()
	_ = sc.EnterRawMode()
	uiApp.ForceRedraw()
	if runErr != nil {
		statusMsg = "editor: " + runErr.Error()
	} else {
		statusMsg = fmt.Sprintf("edited %s:%d", file, line)
	}
}
