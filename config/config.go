// Package config loads recap's small on-disk config and resolves a repo's TODO path.
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Config is recap's small config (~/.config/recap/config.toml or $RECAP_CONFIG).
// The TODO-path template locates a repo's plain-text TODO so recap next can fold
// its incomplete lines into the work queue (and the TUI's upcoming section can
// show them). recap no longer writes into the TODO — the db is the source.
type Config struct {
	// TODOTemplate is a path with a {relpath} placeholder, expanded per repo.
	// {relpath} is the repo path relative to $HOME. A leading ~ is expanded.
	// Empty means recap can't locate a repo's TODO (no todo tier for it).
	//
	// e.g. "~/Library/Mobile Documents/iCloud~md~obsidian/Documents/O Notes/reponotes/{relpath}/TODO.md"
	TODOTemplate string

	// NameTheme optionally guides the name the agent picks for itself each session
	// (e.g. "birds", "greek", "lumon") — purely a hint the agent reads via `recap
	// whoami`. Empty = the agent picks freely; naming works either way.
	NameTheme string
}

func configPath() (string, error) {
	if p := os.Getenv("RECAP_CONFIG"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "recap", "config.toml"), nil
}

// LoadConfig reads the config file. A missing file is not an error — it returns
// a zero Config (TODO writing simply disabled).
func LoadConfig() (Config, error) {
	var c Config
	p, err := configPath()
	if err != nil {
		return c, err
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	defer f.Close()

	// tiny key = "value" parser — recap's config is intentionally trivial, so a
	// dependency-free line reader beats pulling in a TOML library.
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch k {
		case "todo_template":
			c.TODOTemplate = v
		case "name_theme":
			c.NameTheme = v
		}
	}
	return c, sc.Err()
}

// expandHome replaces a leading ~ with the user's home dir.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// TODOPathFor resolves the TODO template for a repo path, substituting
// {relpath} (repo path relative to $HOME). Returns "" if no template is set.
func (c Config) TODOPathFor(repoPath string) (string, error) {
	if c.TODOTemplate == "" {
		return "", nil
	}
	rel := repoPath
	if home, err := os.UserHomeDir(); err == nil {
		if r, err := filepath.Rel(home, repoPath); err == nil && !strings.HasPrefix(r, "..") {
			rel = r
		}
	}
	return expandHome(strings.ReplaceAll(c.TODOTemplate, "{relpath}", rel)), nil
}

// AppendTODO appends a single line to the file, creating it (and parent dirs) if
// needed. Appending a plain line to a plain file is safe and reversible.
func AppendTODO(path, line string) error {
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
