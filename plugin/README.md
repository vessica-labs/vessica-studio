# vessica-studio plugin

Cowork plugin for building presentations on the [Vessica Studio](https://github.com/vessica-labs/vessica-studio)
engine (`vstd`). Skills: deck-new, slide-add, slide-edit, deck-fork, deck-review,
market-refresh. Shared rules in `docs/conventions.md` — the companion contract
(research .md paired with every slide .html), the required-visual rule with a
reuse-first image library, and the layout→visual→critic generation loop.

Works file-first: skills detect whether a `vstd` engine is running (edit API +
live reload) and fall back to direct file edits in the content repo otherwise.
