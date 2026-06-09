// Package todo is recap's TODO application data: resolving a repo's TODO file
// path from a configured template, and appending to it. The template itself is
// application *config* (it lives in the config package); turning that template +
// a repo path into a concrete file, and writing TODO data, is this package's job.
package todo

import (
	"os"
	"path/filepath"
	"strings"
)

// expandHome replaces a leading ~ with the user's home dir.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// PathFor resolves a TODO template for a repo path, substituting {relpath} (the
// repo path relative to $HOME). Returns "" if the template is empty (no TODO
// located for that repo). The template is the caller's config value
// (config.Config.TODOTemplate) — kept as a param so this data package doesn't
// depend on the config package.
func PathFor(template, repoPath string) (string, error) {
	if template == "" {
		return "", nil
	}
	rel := repoPath
	if home, err := os.UserHomeDir(); err == nil {
		if r, err := filepath.Rel(home, repoPath); err == nil && !strings.HasPrefix(r, "..") {
			rel = r
		}
	}
	return expandHome(strings.ReplaceAll(template, "{relpath}", rel)), nil
}

// Append appends a single line to the file, creating it (and parent dirs) if
// needed. Appending a plain line to a plain file is safe and reversible.
func Append(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	_, err = f.WriteString(line)
	return err
}
