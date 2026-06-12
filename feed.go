package main

import (
	"sync"
	"time"

	. "github.com/kungfusheep/glyph"
)

// The notification stream — mail's bottom-right feed. Toasts stack in the
// corner, live for a TTL, fade out, cap at a small limit.
//
// The fade is GLYPH'S now (m209): each row is If(&n.Alive) with an In/Out
// opacity tween — the TTL flips Alive, glyph retains the row and runs the
// exit fade (the animating gate schedules the frames), and OnComplete removes
// the item from the slice. The old per-item Opacity field, the 120ms fade
// ticker, and the staged fade-frame projection are all deleted — recap's last
// per-frame app work went with them. Mutations still reach the bound slice
// only on the render thread: pushes and expiries stage under a mutex and the
// staged seam (drainFeed) applies them; OnComplete already runs render-side.

type notification struct {
	Text  string
	Alive bool // false → glyph runs the exit fade; OnComplete removes the item
}

const (
	feedTTL   = 2800 * time.Millisecond
	feedFade  = 700 * time.Millisecond
	feedLimit = 6
	feedWidth = 64 // the row's column budget
	// feedClip keeps the TEXT inside the row: dot + space eat 2 columns, and
	// the clip's own ellipsis must land before the row edge — text longer
	// than the row hard-clipped mid-word with no ellipsis (m280).
	feedClip = feedWidth - 3
)

type feed struct {
	mu      sync.Mutex
	adds    []string
	expires int
	// after is the TTL scheduler, overridable in tests (the real one arms a
	// timer that stages an expiry and requests a frame).
	after func()
}

func newFeed(after func()) *feed {
	f := &feed{}
	f.after = after
	if f.after == nil {
		f.after = func() {
			time.AfterFunc(feedTTL, func() {
				f.mu.Lock()
				f.expires++
				f.mu.Unlock()
				if app := uiApp; app != nil {
					app.RequestRender()
				}
			})
		}
	}
	return f
}

// push stages a toast (safe from any goroutine) and arms its TTL.
func (f *feed) push(text string) {
	if text == "" {
		return
	}
	f.mu.Lock()
	f.adds = append(f.adds, text)
	f.mu.Unlock()
	f.after()
}

// take pops the staged mutations — render thread.
func (f *feed) take() (adds []string, expires int) {
	f.mu.Lock()
	adds, expires = f.adds, f.expires
	f.adds, f.expires = nil, 0
	f.mu.Unlock()
	return
}

// --- recap glue -------------------------------------------------------------

var (
	statusFeed = newFeed(nil)
	// the bound view state — render thread only (mutated at the staged seam
	// and in glyph's OnComplete, which runs render-side).
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

// drainFeed applies staged pushes and TTL expiries to the bound slice —
// render thread, called from the staged-apply seam.
func drainFeed() {
	adds, expires := statusFeed.take()
	if len(adds) == 0 && expires == 0 {
		return
	}
	for _, a := range adds {
		feedItems = append(feedItems, notification{Text: clipTo(a, feedClip), Alive: true})
	}
	// cap: the stack stays small — overflow drops the oldest outright
	if n := len(feedItems) - feedLimit; n > 0 {
		feedItems = append(feedItems[:0], feedItems[n:]...)
	}
	// each expiry flips the OLDEST living toast — FIFO matches timer order
	// (uniform TTLs), and a limit-trimmed item simply forwards its expiry.
	for ; expires > 0; expires-- {
		for i := range feedItems {
			if feedItems[i].Alive {
				feedItems[i].Alive = false
				break
			}
		}
	}
	feedVisible = len(feedItems) > 0
}

// feedExitComplete is glyph's OnComplete for a finished exit fade — render
// thread. Exits complete in flip order, so the first dead item is the one.
func feedExitComplete() {
	for i := range feedItems {
		if !feedItems[i].Alive {
			feedItems = append(feedItems[:i], feedItems[i+1:]...)
			break
		}
	}
	feedVisible = len(feedItems) > 0
}

// feedRow renders one notification: dot + text; the exit fade is glyph's
// (If retains the row while the Out tween runs, then OnComplete removes it).
func feedRow(n *notification) Component {
	return If(&n.Alive).Then(
		HBox.Width(feedWidth).Opacity(In(1.0).Out(
			Animate.Duration(feedFade).Ease(EaseLinear).
				OnComplete(feedExitComplete)(0.0),
		))(
			Space(),
			Text("● ").FG(&cHunk),
			Text(&n.Text).FG(&cBright),
		),
	)
}

// feedOverlay is the bottom-right notification stack (mail's placement).
func feedOverlay() Component {
	return If(&feedVisible).Then(
		Overlay.BottomRight().Offset(-2, -1)(
			VBox.Width(feedWidth).Gap(1)(
				ForEach(&feedItems, feedRow),
			),
		),
	)
}
