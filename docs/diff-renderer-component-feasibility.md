# Feasibility: making the diff renderer a glyph component

**Question (review #565):** the diff view is currently a hand-rolled buffer renderer
(`renderDiffLayer`: build `[][]Span` + a parallel `diffMeta`, write cells into a buffer,
`layer.SetBuffer`). We established components *can* render into a layer
(`Layer.SetContent(tmpl, w, h)` runs `tmpl.Execute` into the layer buffer). So: is it
feasible to rebuild the diff renderer as a declarative component tree?

**Short answer:** the *visual* half converts cleanly; the *interactive* half is the
blocker. Glyph has no way to read per-row screen coordinates from a `ForEach`/`List`, and
the diff's two interactive features depend on exactly that.

## What the diff renderer actually needs

1. **Styled rows** — file header, hunk header, +/‑/context lines with colours.
2. **Per-row backgrounds** — the file-header band and the commented-line wash, full width.
3. **Per-line comment anchoring** — picking a visible line anchors a comment to that
   file/line/hunk; needs each row mapped to its `diffMeta` entry *and* its screen row.
4. **Per-line jump targets** — in jump mode, every commentable visible row gets a label at
   its screen coordinates (`AddJumpTarget(x, y, …)`).
5. **Native scroll** of a large pre-rendered buffer.

## What components give us (glyph facts)

| Need | Component support | Verdict |
|---|---|---|
| Styled rows | `Text`/`HBox` with FG/BG | ✅ trivial |
| Per-row full-width BG | `HBox.Fill(color)` fills `boxW` edge-to-edge (`template.go` `FillRect`) | ✅ works |
| Native scroll | `SetContent(tmpl, w, totalH)` renders full height; layer windows it via `ScrollY` | ✅ but resets `scrollY=0` (save/restore, like `SetBuffer` already does) |
| Per-row comment anchoring | wrap each row in `Jump(row, onSelect)`; the `onSelect` closure carries that row's `diffMeta` | ✅ via Jump |
| Per-row / per-element jump targets | `Jump(child, onSelect)` registers a target at the child's rendered position; Rich spans also jump (`richSpanJumpFunc`) | ✅ via Jump / Rich spans |

**Correction (review #171 — I was too pessimistic).** Interactivity is *not* the obstacle I
first claimed. Two glyph mechanisms cover it declaratively:

- **`Jump(child, onSelect)`** (components.go:1817) wraps any component and registers a jump
  target at the child's *rendered* position, with `onSelect` a closure. Wrap each
  commentable row in `Jump(row, func(){ pickLine(diffMeta[i]) })` and both features fall
  out: a jump-mode label per row, and the closure carries that row's metadata — so **no
  manual coordinate extraction is needed at all**, which was the whole crux of my earlier
  "obstacle".
- **Rich spans** jump too (`richSpanJumpFunc` / `wrapSpansDraw`, template.go:8513), giving
  per-*element* targets — e.g. a file-header span can be its own target. That's exactly what
  opens the door to **per-file fold / open-close** (toggle a file's hunks).

The one genuine implementation detail: jump targets register at the child's *render*
coordinates. The diff is a `Layer` rendered off-screen (`SetBuffer`/`SetContent`) then
blitted with scroll, so a `Jump` inside that buffer sees buffer-relative coords, not screen.
The component version needs either the layer to translate jump coords by `screenY − scrollY`,
or to drive scroll where `Jump` sees screen coords. A detail to handle, not a blocker.
(Perf: window to visible rows for very large diffs — `SetContent` builds the full height.)

## Options

**A — convert directly with `Jump`-wrapped rows + Rich spans (no new primitive).**
Per the correction above, glyph already gives per-element targets, so no framework change is
needed first. Rebuild the diff as a column of `Jump`-wrapped rows (Rich spans for line
content; file-header rows as their own jump/fold targets); each row's `onSelect` closes over
its `diffMeta` for anchoring; band/wash are row `.Fill`s. Resolve the layer jump-coordinate
translation and window large diffs. Cost: a real rewrite + those two details; benefit:
declarative diff *and* it unlocks per-file fold/open-close. **Recommended if we want the
declarative version.**

**B — hybrid (not worth it).** Render rows as components for the visuals but keep a parallel
coordinate-tracking pass for anchoring/jump. This duplicates the row↔coordinate bookkeeping
the hand-rolled version already does once — more code, two sources of truth, no real win.

**C — keep the hand-rolled renderer (status quo).** It's genuinely well-matched: per-row
interactivity *wants* an explicit row→coordinate map, which is exactly what manual buffer
rendering gives you. The file-header band's `buf.Set` is consistent with this model (same as
the commented-line wash). Cost: nothing; it stays "low-level" but for a good reason.

## Revised recommendation

**Feasible, and worth it** — the per-file fold/open-close upside is real, and `Jump` + Rich
spans mean no new framework primitive is required (option A's "build a primitive first" is
no longer needed). Shape: rebuild `renderDiffLayer` as a column of `Jump`-wrapped rows (Rich
spans for line content; file-header rows as their own jump/fold targets), each row's
`onSelect` closing over its `diffMeta`; band/wash become row `.Fill`s. The two things to
nail are (1) the layer jump-coordinate translation and (2) windowing for large diffs —
implementation details, not blockers.

It's still a sized, self-contained task (a real rewrite + those two details), so it wants to
be its own todo rather than a drop-in — but the interactivity I'd flagged as the obstacle is
handled. This records the corrected feasibility, not the build.
