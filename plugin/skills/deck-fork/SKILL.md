---
name: deck-fork
description: Fork a Vessica Studio deck for a specific client or audience and customize it. Use when the user wants to fork, duplicate, clone, or tailor a deck for a client, company, industry, or event — e.g. "fork missing-middle for Acme", "make a client version of this deck".
---

# Fork a deck for a client

Read `../../docs/conventions.md`.

1. **Fork**: `vstd fork <deck> <client>` (creates `decks/<deck>--<client>` with parent
   hashes for later diffing). No CLI: copy the deck directory (minus `build/`), then add
   `forked_from`, `fork_date`, and per-slide sha256 `parent_hashes` to `deck.yaml`, and
   suffix the title.
2. **Customization interview** (AskUserQuestion, unless already specified): client
   industry & context, which examples/value modules to localize, tone changes, slides to
   drop, confidentiality notes.
3. **Customize** via the slide-edit contract per slide (companions travel with the fork —
   log fork-specific changes so upstream merges stay tractable). Typical passes: swap
   industry examples and worked numbers, retitle the cover, prune modules, adjust the
   offer slide.
4. **Later upstream sync**: `vstd diff-upstream <fork>` lists parent slides changed since
   fork. Port changes slide-by-slide as an agent-assisted rebase: read BOTH versions +
   BOTH companions, apply the upstream improvement while preserving client customizations,
   log the port. Never blind-copy over a customized slide.
