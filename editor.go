package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

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

// runEditorAt suspends the TUI and opens file at line in $EDITOR (cwd = repo),
// replacing our view until the editor exits, then restores the TUI. The terminal
// is handed over via ExitRawMode and reclaimed via EnterRawMode; ForceRedraw
// repaints (re-entering raw mode clears the screen, so a diff-render against the
// stale front buffer would leave it blank).
func runEditorAt(repo, file string, line int) {
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

// editDiffLine opens the picked diff line in $EDITOR (the pickAction for the editor
// jump flow).
func editDiffLine(m diffLineMeta) {
	t, ok := selectedTask()
	if !ok {
		statusMsg = "no task selected"
		return
	}
	runEditorAt(t.RepoPath, m.File, m.Line)
}

// openEditorPick enters glyph's jump-label mode over the diff; picking a labelled
// line opens THAT line in $EDITOR (mirrors the comment line-picker, per review #148).
func openEditorPick() {
	if !anyCommentableRow() {
		statusMsg = "(no diff lines to open)"
		return
	}
	pickAction = editDiffLine
	uiApp.EnterJumpMode()
}
