---
name: slide-add
description: Add one or more slides to an existing Vessica Studio deck, with the full companion + visual + critic loop. Use when the user asks to add a slide, insert a page, create a new section in a deck, or expand a vstd/Vessica presentation.
---

# Add slides to an existing deck

Read `../../docs/conventions.md`. Locate the studio root and target deck (`vstd list` or
`decks/` listing; confirm if ambiguous).

1. **Context**: read `deck.yaml`, the slide filename list (the arc), and the companions of
   the 2–3 slides around the insertion point — match voice, level, and layout pacing.
   Read neighbors' Talk tracks: the PREVIOUS slide's talk track must gain the new slide's
   opening cue (update it), and the new slide's talk track ends with the NEXT slide's cue.
2. **Pick the id**: midpoint numbering between neighbors (0045 between 0040 and 0050).
   If no gap remains, renumber later slides (rename both files of each) — or use
   `POST /api/deck/{d}/slides` when the engine is running.
3. **Generate** with the same four-step loop as deck-new: companion → fragment →
   required visual element → critic pass.
   If the slide is based on a PDF, PPT/PPTX, or image, preserve that original under
   `decks/<deck>/sources/` and list it in companion frontmatter `attachments:` with
   the relevant `page`. Render and compare the finished slide to that source,
   correct fidelity issues, and record the critic result in Log.
4. **Verify** the deck still builds; report the slide id and what the critic flagged/fixed.
