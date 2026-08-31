---
name: slide-edit
description: Change, update, redesign, fix, restyle, or rewrite an existing slide in a Vessica Studio/vstd deck while preserving its companion contract.
---

# Edit slides

Read `../../docs/conventions.md`, or run `vstd skill conventions` when the skill
is loaded outside the plugin file tree. This skill exists to enforce one thing:
**edits are context-aware**.

1. **Companion and link first — non-negotiable.** Check for
   `NNNN-slug.link.yaml`; refresh or detach a linked slide instead of editing its
   fragment, companion, or attachments. Then read `NNNN-slug.md` before touching the
   fragment. Check Intent (does the requested change serve it? If it conflicts, say so
   before editing), Evidence (never contradict it silently — if new facts arrive, update
   Evidence with sources), Visual direction (preserve unless the change targets it), and
   Log (don't re-introduce something already tried and rejected).
2. **Edit the fragment** at the formatting standard. Prefer the edit API when the engine
   runs (`PUT .../fragment`, `PUT .../title`); files directly otherwise. Keep the single
   `<section>` structure and theme classes.
   When adding or rebuilding a chart, use the conventions' hybrid chart contract.
   When an existing inline SVG chart contains `<text>`, use
   `vstd chart promote-text <deck> <slide> --dry-run` and then the write command
   when appropriate; inspect and fine-tune the resulting editable overlays.
3. **Update the companion**: Key ideas/Evidence if content changed; Talk track if the
   message changed (keep next-slide cue); dated Log line describing the edit and why.
4. **Critic-check when warranted**: layout changes and redesigns get a screenshot pass;
   pure text tweaks may skip it (state that you did).
   If companion frontmatter has `attachments:`, render the cited source page/slide
   too, compare it directly with the 1280×720 result, and correct fidelity issues
   before finishing.
   If optional rendering tools are unavailable, complete the static/build checks,
   update the Log, and report the visual QA that could not be performed.
5. If the user is renumbering/reordering: move BOTH files of each slide together, and fix
   the surrounding Talk-track cues.
