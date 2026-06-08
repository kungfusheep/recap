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
| Per-row comment anchoring | `ForEach` exposes **no** per-item geometry; `List` exposes only the **selected** row (`SelectedRef`) | ⚠️ not built-in |
| Per-row jump targets | `AddJumpTarget` needs explicit screen coords per row; no declarative per-row extraction | ⚠️ hard |

The obstacle is shared by 3 and 4: **glyph never hands you each row's rendered `(x,y)`.**
`NodeRef` captures a node's `Rect` after layout, but you can't attach one *per item* inside
a `ForEach`, and `List` tracks only the selected row. The hand-rolled renderer sidesteps
this entirely — it owns the row→buffer-Y mapping, so `diffMeta[rowIdx]` and the jump
coordinate `diffViewRef.Y + (y - scrollY)` fall out for free. A component tree would have
to *re-derive* that mapping it doesn't expose.

## Options

**A — add a small glyph primitive first, then convert (best long-term).**
Teach `ForEach`/`List` (or a new `RowList`) to expose each visible row's geometry — e.g.
per-item `NodeRef`s, or an `OnRows([]Rect)` callback after layout. That single capability
unlocks *any* interactive scrollable-rows view, not just this diff. Then the diff becomes a
declarative `RowList` of styled rows; band/wash are row `.Fill`s; anchoring + jump read the
exposed per-row rects. Cost: real framework work + tests; benefit: reusable, and the diff
gets simpler. **Recommended if we want the declarative version.**

**B — hybrid (not worth it).** Render rows as components for the visuals but keep a parallel
coordinate-tracking pass for anchoring/jump. This duplicates the row↔coordinate bookkeeping
the hand-rolled version already does once — more code, two sources of truth, no real win.

**C — keep the hand-rolled renderer (status quo).** It's genuinely well-matched: per-row
interactivity *wants* an explicit row→coordinate map, which is exactly what manual buffer
rendering gives you. The file-header band's `buf.Set` is consistent with this model (same as
the commented-line wash). Cost: nothing; it stays "low-level" but for a good reason.

## Recommendation

Don't do a naive 1:1 component rewrite — it would either duplicate coordinate tracking (B)
or drop comment-anchoring + jump-to-line (a regression). The clean path is **A**: add a
per-row-geometry capability to glyph's row components, *then* the diff (and future
interactive lists) can be declarative. If we're not ready to invest in that primitive,
**C** (keep it hand-rolled) is the right call, and the band staying `buf.Set` is correct.

Either way it's a separate, sized piece of work — this records the feasibility, not the
build.
