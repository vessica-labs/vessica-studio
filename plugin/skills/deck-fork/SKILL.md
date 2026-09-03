---
name: deck-fork
description: Fork, duplicate, clone, or tailor a Vessica Studio/vstd deck for a specific client, company, audience, industry, or event.
---

# Fork a deck for a client

Read `../../docs/conventions.md`, or run `vstd skill conventions` when the skill
is loaded outside the plugin file tree.

Run `vstd cloud workspace status` to detect connection state. Forking remains a
local paired-file workflow when the workspace is offline or unconnected.

1. **Fork**: `vstd fork <deck> <client>` (creates `decks/<deck>--<client>` with parent
   hashes for later diffing). No CLI: copy the deck directory (minus `build/`), then add
   `forked_from`, `fork_date`, and per-slide sha256 `parent_hashes` to `deck.yaml`, and
   suffix the title.
2. **Customization interview** (ask the user unless already specified): client
   industry & context, which examples/value modules to localize, tone changes, slides to
   drop, confidentiality notes.
3. **Customize** via the slide-edit contract per slide (companions travel with the fork —
   log fork-specific changes so upstream merges stay tractable). Typical passes: swap
   industry examples and worked numbers, retitle the cover, prune modules, adjust the
   offer slide.
   Before customizing a slide, check for its `.link.yaml`; refresh or detach it
   instead of editing the linked pair or attachments directly.
4. **Later upstream sync**: `vstd diff-upstream <fork>` lists parent slides changed since
   fork. Port changes slide-by-slide as an agent-assisted rebase: read BOTH versions +
   BOTH companions, apply the upstream improvement while preserving client customizations,
   log the port. Never blind-copy over a customized slide.
