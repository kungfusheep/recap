package snooze

import (
	"bufio"
	"github.com/kungfusheep/recap/db"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A skipped todo is snoozed, not just passed: `recap next --skip` on a todo records its
// ref here so it leaves the active queue. Without this a permanently-blocked todo (one
// waiting on an external freeze, say) keeps the queue non-empty forever — so `recap next
// --wait` can never park and the hard loop busy-spins re-handing the same blocked item.
// Snoozing lets the queue drain to empty so --wait parks until genuinely new work lands.
//
// The snooze has a TTL so a cleared blocker re-surfaces for another look. A todo whose
// TEXT changes gets a new ref (its hash), so edits re-surface for free. Per-repo, beside
// the db — same namespacing as the cursor.
var TTL = 6 * time.Hour

// Now is the clock, overridable in tests.
var Now = func() int64 { return time.Now().Unix() }

func path(repo string) (string, error) {
	dbp, err := db.Path()
	if err != nil {
		return "", err
	}
	name := "skipped"
	if repo != "" {
		name = "skipped-" + strings.ReplaceAll(repo, string(os.PathSeparator), "_")
	}
	return filepath.Join(filepath.Dir(dbp), name), nil
}

// Load returns the repo's still-active snoozed todo refs (expired ones ignored).
func Load(repo string) map[string]bool {
	out := map[string]bool{}
	p, err := path(repo)
	if err != nil {
		return out
	}
	f, err := os.Open(p)
	if err != nil {
		return out
	}
	defer f.Close()
	cutoff := Now() - int64(TTL.Seconds())
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ref, ts, ok := strings.Cut(strings.TrimSpace(sc.Text()), "\t")
		if !ok {
			continue
		}
		if n, err := strconv.ParseInt(ts, 10, 64); err == nil && n >= cutoff {
			out[ref] = true
		}
	}
	return out
}

// Record records (or refreshes) a todo ref's snooze timestamp, dropping expired
// entries so the file doesn't grow without bound.
func Record(repo, ref string) error {
	p, err := path(repo)
	if err != nil {
		return err
	}
	now := Now()
	cutoff := now - int64(TTL.Seconds())
	keep := map[string]int64{}
	if f, err := os.Open(p); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			r, ts, ok := strings.Cut(strings.TrimSpace(sc.Text()), "\t")
			if !ok {
				continue
			}
			if n, err := strconv.ParseInt(ts, 10, 64); err == nil && n >= cutoff && r != ref {
				keep[r] = n
			}
		}
		f.Close()
	}
	keep[ref] = now

	var b strings.Builder
	for r, ts := range keep {
		b.WriteString(r)
		b.WriteByte('\t')
		b.WriteString(strconv.FormatInt(ts, 10))
		b.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(b.String()), 0o644)
}
