---
name: cloud-presentation
description: Find, open, edit, review, or create a Vessica Studio Cloud presentation from chat when the user names a Cloud presentation or asks to work in Cloud without an already-open local studio.
---

# Work with Cloud presentations from chat

Read `../../docs/conventions.md` first, or run `vstd skill conventions` when the
skill is loaded outside the plugin file tree. This workflow turns a Cloud
presentation into the same file contract used by every other Vessica skill.

## Connect safely

Run `vstd cloud account`. If the user is not signed in, run `vstd cloud login`,
immediately give them the exact device-approval URL and code, wait for approval,
and verify with `vstd cloud account`. Never ask the user to paste an access or
refresh token into chat.

A public or shared viewing link does not grant authoring access. Only edit a
presentation that the signed-in account's Cloud presentation list marks as
editable. If the user can view but cannot author it, explain that distinction
and offer to create their own presentation or an authorized copy; do not copy
without their confirmation.

## Open an existing presentation

From any directory, run:

```sh
vstd cloud presentation open "TITLE OR ID" --json
```

The command searches every page of the account's presentations, accepts an
exact ID, exact title, or one unambiguous partial title, and returns a persistent
managed `root`. It reuses that checkout on later requests and reconciles it with
the latest Cloud revision. If the title is ambiguous, show the matching titles
and IDs and ask the user to choose; never guess or create a duplicate.

For edits, begin an isolated agent workspace from the returned root:

```sh
vstd worktree begin --root ROOT
```

Use the returned worktree for all reads and writes. Load the most specific
workflow with `vstd skill slide-edit`, `slide-add`, `deck-review`,
`market-refresh`, or `deck-fork`, and follow it exactly. Finish changes with:

```sh
vstd worktree finish --root WORKTREE
```

This reconciles concurrent browser and agent changes and synchronizes the
result to Cloud. Report an offline or failed sync as pending; do not describe it
as saved to Cloud. A read-only review does not need an agent worktree.

## Create a presentation

Frame the audience, setting, length, theme, and narrative first, following
`vstd skill deck-new`. Confirm the outline before creating Cloud state. Then run:

```sh
vstd cloud presentation create --title "TITLE" --json
```

The command creates a persistent managed checkout, one starter deck, and the
first durable Cloud revision. Use the returned `root`; do not scaffold a second
studio or deck. Begin an agent worktree, author the starter deck with the
deck-new workflow, and finish the worktree to synchronize it.

If the user asks for a new client presentation, use the named client's audience
and context in the framing. If they want to tailor an existing presentation,
use the deck-fork workflow after opening the source instead of silently changing
the original.

Synchronization saves versions but does not publish or share them. Publish or
change access only when the user explicitly requests it.
