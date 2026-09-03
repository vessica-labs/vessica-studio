---
name: market-refresh
description: Refresh, update, or restamp time-sensitive market data, statistics, and dated claims in a Vessica Studio/vstd deck before delivery.
---

# Refresh dated content

Read `../../docs/conventions.md`, or run `vstd skill conventions` when the skill
is loaded outside the plugin file tree.

Run `vstd cloud workspace status` to detect connection state. Refresh the same
paired local files, and keep offline or unconnected work explicitly unsynced.

1. **Find the perishables**: grep the deck for as-of stamps, dates, `[X]`/placeholder
   pills, and companions whose Evidence cites dated sources. Confirm scope with the user
   (which slides, how deep).
2. **Re-research** each claim with web search: prefer primary sources; capture URL + date;
   note credibility tier. Compare against the companion's Evidence — record what CHANGED,
   not just new numbers (changes are the insight: "Microsoft moved from X to Y since May").
3. **Update** via the slide-edit contract: fragment numbers/claims, Evidence rewritten with
   new sources and dates, Talk track adjusted if the story changed, as-of stamps restamped
   (visible pill on market-scan-style slides), Log line "refreshed YYYY-MM-DD: <summary>".
4. **Escalate story-level shifts**: if research contradicts a slide's Intent (not just its
   numbers), do NOT silently rewrite the argument — present the finding and proposed
   change to the user first.
5. **Report**: changed-claims table (old → new → source), slides touched, anything that
   now needs a bigger rethink.
