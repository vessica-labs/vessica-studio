---
name: slide-add
description: Add, insert, or create one or more slides or sections in an existing Vessica Studio/vstd deck using the companion, visual, and critic workflow.
---

# Add slides to an existing deck

Read `../../docs/conventions.md`, or run `vstd skill conventions` when the skill
is loaded outside the plugin file tree. Locate the studio root and target deck
(`vstd list` or `decks/` listing; confirm if ambiguous). Run
`vstd cloud workspace status` to detect connection state; add slides directly
to paired local files even when offline or unconnected.

1. **Context**: read `deck.yaml`, the slide filename list (the arc), and the companions of
   the 2–3 slides around the insertion point — match voice, level, and layout pacing.
   Before changing or renumbering a neighboring slide, check for its `.link.yaml`;
   refresh or detach a linked slide instead of editing its pair or attachments.
   Read neighbors' Talk tracks: the PREVIOUS slide's talk track must gain the new slide's
   opening cue (update it), and the new slide's talk track ends with the NEXT slide's cue.
2. **Pick the id**: midpoint numbering between neighbors (0045 between 0040 and 0050).
   If no gap remains, renumber later slides (rename both files of each) — or use
   `POST /api/deck/{d}/slides` when the engine is running.
3. **Generate** with the same four-step loop as deck-new: companion → fragment →
   required visual element → critic pass.
   For any chart/graph/plot, follow the conventions' hybrid chart contract:
   geometry in one `.chart-art` SVG and all text as editable `.chart-label`
   overlays inside a selectable `data-chart-group` container.
   If the slide is based on a PDF, PPT/PPTX, or image, preserve that original under
   `decks/<deck>/sources/` and list it in companion frontmatter `attachments:` with
   the relevant `page`. Render and compare the finished slide to that source,
   correct fidelity issues, and record the critic result in Log.
4. **Verify** the deck still builds; report the slide id, what the critic
   flagged/fixed, and any screenshot or source comparison skipped because optional
   tooling was unavailable.
