---
name: deck-review
description: Run a full quality and consistency review of a Vessica Studio deck — visual QA screenshots, companion integrity, cross-slide consistency, library hygiene. Use when the user asks to review, QA, critique, polish, or check a deck, or before an important delivery.
---

# Review a whole deck

Read `../../docs/conventions.md`. This is the deck-level critic — run before deliveries.

1. **Visual sweep**: build, then screenshot EVERY slide (Playwright, 1280×720, `#/<n>`).
   Review each against the critic checklist. Fix trivial issues directly (via slide-edit
   contract); list judgment calls for the user.
2. **Consistency pass** across slides: type-scale drift, palette violations, duplicated
   near-identical icons/images that should be one library asset (dedup into the library and
   update references), inconsistent card/headband patterns, page-fill outliers.
3. **Companion integrity**: every slide has a companion; frontmatter `visuals:` matches
   assets actually referenced; Talk tracks form a continuous chain of next-slide cues;
   Evidence present on slides making factual claims; stale `status: draft` flags.
4. **Freshness**: slides with as-of dates or `[X]` placeholder markers → list what needs
   refresh (offer market-refresh where applicable).
5. **Report**: deliver a short review doc — fixed silently / fixed & logged / needs Matt's
   call / refresh queue — plus before/after screenshots for significant fixes.
