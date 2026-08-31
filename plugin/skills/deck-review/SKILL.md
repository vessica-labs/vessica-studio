---
name: deck-review
description: Review, QA, critique, polish, or check a Vessica Studio/vstd deck for visual quality, companion integrity, consistency, and library hygiene.
---

# Review a whole deck

Read `../../docs/conventions.md`, or run `vstd skill conventions` when the skill
is loaded outside the plugin file tree. This is the deck-level critic — run
before deliveries.

1. **Visual sweep**: build, then screenshot EVERY slide (Playwright, 1280×720, `#/<n>`).
   Review each against the critic checklist. Unless the user requested a report-only
   review, fix trivial issues via the slide-edit contract and list judgment calls.
   Before any fix, check for the slide's `.link.yaml`; refresh or detach a linked
   slide instead of editing its pair or attachments directly. If screenshot tooling
   is unavailable, perform static/build checks and report the visual QA gap.
2. **Consistency pass** across slides: type-scale drift, palette violations, duplicated
   near-identical icons/images that should be one library asset (dedup into the library and
   update references), inconsistent card/headband patterns, page-fill outliers.
3. **Companion integrity**: every slide has a companion; frontmatter `visuals:` matches
   assets actually referenced; Talk tracks form a continuous chain of next-slide cues;
   Evidence present on slides making factual claims; stale `status: draft` flags.
4. **Freshness**: slides with as-of dates or `[X]` placeholder markers → list what needs
   refresh (offer market-refresh where applicable).
5. **Report**: deliver a short review doc — findings only / fixed & logged / needs
   user or owner decision / refresh queue — plus before/after screenshots for
   significant fixes when screenshots are available.
