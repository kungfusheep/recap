package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// linkRe matches a [[target]] reference embedded in a comment — the lightweight
// way to attach a screenshot or any file to feedback (e.g. [[/tmp/shot.png]]).
// The text box accepts these verbatim; openLinks resolves them on demand.
var linkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// extractLinks returns the [[target]] references in a comment body, in order,
// de-whitespaced. Empty targets are skipped.
func extractLinks(body string) []string {
	var out []string
	for _, m := range linkRe.FindAllStringSubmatch(body, -1) {
		if t := strings.TrimSpace(m[1]); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// pasteClipboardImage writes the clipboard's image to a temp PNG and returns its
// path, so a pasted screenshot can be referenced from a comment with [[path]].
// macOS has no pngpaste, so it asks osascript for the clipboard as PNG; if the
// clipboard holds no image that errors and the empty temp file is cleaned up.
func pasteClipboardImage() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("clipboard image paste is macOS-only")
	}
	dir, err := attachmentsDir()
	if err != nil {
		return "", err
	}
	// persist beside the db, NOT in $TMPDIR — macOS purges temp files, which would
	// leave the comment's [[link]] dangling. UnixNano keeps the name unique.
	path := filepath.Join(dir, fmt.Sprintf("screenshot-%d.png", time.Now().UnixNano()))
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	f.Close()
	// «class PNGf» is the clipboard's PNG representation; writing it raw yields a
	// valid .png file. open-for-access needs the file to exist, hence CreateTemp.
	cmd := exec.Command("osascript",
		"-e", "set png to (the clipboard as «class PNGf»)",
		"-e", fmt.Sprintf("set fp to (open for access POSIX file %q with write permission)", path),
		"-e", "set eof fp to 0",
		"-e", "write png to fp",
		"-e", "close access fp",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(path)
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("no image in clipboard (%s)", msg)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		os.Remove(path)
		return "", fmt.Errorf("clipboard image was empty")
	}
	return path, nil
}

// attachmentsDir is where pasted screenshots persist — beside the review db
// ($RECAP_DB's dir or ~/.config/recap), created on demand.
func attachmentsDir() (string, error) {
	db, err := dbPath()
	if err != nil {
		return "", err
	}
	d := filepath.Join(filepath.Dir(db), "attachments")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	return d, nil
}

// openLinks opens each [[target]] reference with the OS opener (open on macOS,
// xdg-open elsewhere). Returns how many it launched. Best-effort: a failed open
// is skipped, not fatal.
func openLinks(body string) int {
	opener := "open"
	if runtime.GOOS == "linux" {
		opener = "xdg-open"
	}
	n := 0
	for _, target := range extractLinks(body) {
		if err := exec.Command(opener, target).Start(); err == nil {
			n++
		}
	}
	return n
}
