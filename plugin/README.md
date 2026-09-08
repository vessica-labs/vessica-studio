# vessica-studio plugin

Cowork plugin for building presentations on the [Vessica Studio](https://github.com/vessica-labs/vessica-studio)
engine (`vstd`). Skills: cloud-presentation, deck-new, slide-add, slide-edit,
deck-fork, deck-review, market-refresh. Shared rules in `docs/conventions.md` — the companion contract
(research .md paired with every slide .html), the required-visual rule with a
reuse-first image library, and the layout→visual→critic generation loop.

Works file-first: the Cloud workflow opens or creates a persistent managed local
checkout, and the authoring skills use isolated worktrees that reconcile browser
and agent changes before synchronizing to Cloud. Local-only work remains fully
supported.

Install (Claude Code / Cowork):

```
/plugin marketplace add vessica-labs/vessica-studio
/plugin install vessica-studio@vessica-studio
```

The same skill files are the single source of truth for every agent runtime:
they are embedded in the `vstd` binary and printable with `vstd skill <name>`,
which is how the Codex prompt launchers in `../codex/` load them.
