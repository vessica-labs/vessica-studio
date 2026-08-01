---
name: slide-edit
description: Edit existing slides in a Vessica Studio deck — content changes, redesigns, data updates, layout fixes — honoring the companion contract. Use when the user asks to change, update, redesign, fix, restyle, or rewrite a slide or page in a vstd/Vessica deck.
---

# Edit slides

Read `../../docs/conventions.md`. This skill exists to enforce one thing: **edits are
context-aware**.

1. **Companion first — non-negotiable.** Read `NNNN-slug.md` before touching the
   fragment. Check Intent (does the requested change serve it? If it conflicts, say so
   before editing), Evidence (never contradict it silently — if new facts arrive, update
   Evidence with sources), Visual direction (preserve unless the change targets it), and
   Log (don't re-introduce something already tried and rejected).
2. **Edit the fragment** at the formatting standard. Prefer the edit API when the engine
   runs (`PUT .../fragment`, `PUT .../title`); files directly otherwise. Keep the single
   `<section>` structure and theme classes.
3. **Update the companion**: Key ideas/Evidence if content changed; Talk track if the
   message changed (keep next-slide cue); dated Log line describing the edit and why.
4. **Critic-check when warranted**: layout changes and redesigns get a screenshot pass;
   pure text tweaks may skip it (state that you did).
5. If the user is renumbering/reordering: move BOTH files of each slide together, and fix
   the surrounding Talk-track cues.
