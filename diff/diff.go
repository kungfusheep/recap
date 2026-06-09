package diff

import "strings"

// A small unified-diff model so we render a friendly view instead of
// prefix-matching raw `git show` output (which leaks plumbing + miscolours).

type LineKind byte

const (
	LineContext LineKind = ' '
	LineAdd     LineKind = '+'
	LineDel     LineKind = '-'
)

type Line struct {
	Kind LineKind
	Text string
}

type Hunk struct {
	Header string // the @@ … @@ context line
	Lines  []Line
}

type File struct {
	Path   string
	Status string // "new file" | "deleted" | "renamed" | "modified"
	Hunks  []Hunk
}

// Parse turns a `git show`/`git diff` patch into a model. It starts
// at the first "diff --git", so a leading commit/author/message preamble is
// ignored naturally.
func Parse(patch string) []File {
	var files []File
	var cur *File
	var hunk *Hunk

	for _, ln := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			files = append(files, File{Status: "modified", Path: pathFromDiffGit(ln)})
			cur = &files[len(files)-1]
			hunk = nil
		case cur == nil:
			continue // preamble before the first file
		case strings.HasPrefix(ln, "new file"):
			cur.Status = "new file"
		case strings.HasPrefix(ln, "deleted file"):
			cur.Status = "deleted"
		case strings.HasPrefix(ln, "rename to "):
			cur.Status = "renamed"
			cur.Path = strings.TrimPrefix(ln, "rename to ")
		case strings.HasPrefix(ln, "+++ b/"):
			cur.Path = strings.TrimPrefix(ln, "+++ b/")
		case strings.HasPrefix(ln, "index "), strings.HasPrefix(ln, "--- "),
			strings.HasPrefix(ln, "+++ "), strings.HasPrefix(ln, "old mode "),
			strings.HasPrefix(ln, "new mode "), strings.HasPrefix(ln, "similarity "),
			strings.HasPrefix(ln, "rename from "), strings.HasPrefix(ln, "GIT binary"),
			strings.HasPrefix(ln, "Binary files"):
			// plumbing header noise — skip
		case strings.HasPrefix(ln, "@@"):
			cur.Hunks = append(cur.Hunks, Hunk{Header: ln})
			hunk = &cur.Hunks[len(cur.Hunks)-1]
		case strings.HasPrefix(ln, `\ No newline`):
			// ignore
		case hunk != nil && len(ln) > 0:
			switch ln[0] {
			case '+':
				hunk.Lines = append(hunk.Lines, Line{Kind: LineAdd, Text: ln[1:]})
			case '-':
				hunk.Lines = append(hunk.Lines, Line{Kind: LineDel, Text: ln[1:]})
			case ' ':
				hunk.Lines = append(hunk.Lines, Line{Kind: LineContext, Text: ln[1:]})
			}
		case hunk != nil && ln == "":
			// blank context line within a hunk
			hunk.Lines = append(hunk.Lines, Line{Kind: LineContext, Text: ""})
		}
	}
	return files
}

// pathFromDiffGit extracts the b/ path from `diff --git a/X b/Y`.
func pathFromDiffGit(ln string) string {
	rest := strings.TrimPrefix(ln, "diff --git ")
	if i := strings.Index(rest, " b/"); i >= 0 {
		return rest[i+3:]
	}
	f := strings.Fields(rest)
	if len(f) > 0 {
		return strings.TrimPrefix(f[len(f)-1], "b/")
	}
	return rest
}

// TotalLines counts rendered lines across files (for truncation guards).
func TotalLines(files []File) int {
	n := 0
	for _, f := range files {
		n += 1 + len(f.Hunks)
		for _, h := range f.Hunks {
			n += len(h.Lines)
		}
	}
	return n
}
