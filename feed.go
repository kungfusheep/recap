package main

import (
	"sync"
	"time"

	. "github.com/kungfusheep/glyph"
)

// The notification stream — mail's bottom-right feed, brought over wholesale
// (mail ui.Feed): transient status lines stack in the corner, live for a TTL,
// fade out, and cap at a small limit. It replaces the single status bar that
// sat one row above the focus line (todo:a5f726bf).
//
// Threading follows recap's staged-apply discipline rather than mail's
// direct-mutation tick: pushes and fade ticks build a fresh view slice under
// the feed's own mutex and STAGE it; the render thread swaps it into the
// bound slice at frame top (refreshDetail, the staged seam). The fade is
// driven by the existing gated ticker — it only spins while the feed is live
// (or a flare is in flight), so an idle app stays event-driven.

type notification struct {
	Text    string
	Opacity float64
}

type feedEntry struct {
	text string
	at   time.Time
}

type feed struct {
	mu    sync.Mutex
	items []feedEntry
	now   func() time.Time
	ttl   time.Duration
	fade  time.Duration
	limit int

	staged []notification
	dirty  bool
}

func newFeed(now func() time.Time) *feed {
	if now == nil {
		now = time.Now
	}
	return &feed{
		now:   now,
		ttl:   2800 * time.Millisecond,
		fade:  700 * time.Millisecond,
		limit: 6,
	}
}

// push appends a line and stages the refreshed view. Safe from any goroutine.
func (f *feed) push(text string) {
	if text == "" {
		return
	}
	f.mu.Lock()
	f.items = append(f.items, feedEntry{text: text, at: f.now()})
	if len(f.items) > f.limit {
		copy(f.items, f.items[len(f.items)-f.limit:])
		f.items = f.items[:f.limit]
	}
	f.stageLocked()
	f.mu.Unlock()
}

// tick re-stages the fade frame; reports whether anything is still live (the
// ticker's gate). Safe from the ticker goroutine.
func (f *feed) tick() bool {
	f.mu.Lock()
	f.stageLocked()
	live := len(f.items) > 0
	f.mu.Unlock()
	return live
}

func (f *feed) stageLocked() {
	now := f.now()
	keep := f.items[:0]
	view := make([]notification, 0, len(f.items))
	for _, e := range f.items {
		age := now.Sub(e.at)
		if age >= f.ttl {
			continue
		}
		keep = append(keep, e)
		view = append(view, notification{Text: e.text, Opacity: f.opacity(age)})
	}
	f.items = keep
	f.staged, f.dirty = view, true
}

// take pops the staged view (nil, false when nothing new) — render thread.
func (f *feed) take() ([]notification, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.dirty {
		return nil, false
	}
	f.dirty = false
	return f.staged, true
}

func (f *feed) opacity(age time.Duration) float64 {
	fadeStart := f.ttl - f.fade
	if age <= fadeStart {
		return 1
	}
	if age >= f.ttl {
		return 0
	}
	return float64(f.ttl-age) / float64(f.fade)
}

// --- recap glue -------------------------------------------------------------

var (
	statusFeed = newFeed(nil)
	// the bound view state — render thread only (swapped at the staged seam).
	feedItems   []notification
	feedVisible bool
)

// toast is THE status channel: it pushes onto the corner feed and keeps the
// statusMsg var current (tests and any residual readers see the last line).
func toast(text string) {
	statusMsg = text
	statusFeed.push(text)
	if text != "" && uiApp != nil {
		uiApp.RequestRender()
	}
}

// drainFeed swaps a staged fade/push frame into the bound slice — render
// thread, called from the staged-apply seam.
func drainFeed() {
	if v, ok := statusFeed.take(); ok {
		feedItems = v
		feedVisible = len(v) > 0
	}
}

// feedRow renders one notification: dot + text, fading via per-item opacity.
func feedRow(n *notification) Component {
	return HBox.Width(49).Opacity(&n.Opacity)(
		Space(),
		Text("● ").FG(&cHunk),
		Text(&n.Text).FG(&cBright),
	)
}

// feedOverlay is the bottom-right notification stack (mail's placement).
func feedOverlay() Component {
	return If(&feedVisible).Then(
		Overlay.BottomRight().Offset(-2, -1)(
			VBox.Width(49).Gap(1)(
				ForEach(&feedItems, feedRow),
			),
		),
	)
}
