# Vessica Studio content repo — agent guide (Codex)

This repository is a Vessica Studio content root: decks are directories of
HTML slide fragments (`decks/<deck>/slides/NNNN-slug.html`, one
`<section class="slide">` per file) paired with companion markdown carrying
the research and talk track. The `vstd` engine builds, serves, and
live-reloads them; the player UI (HUD, edit mode, PDF export) is engine-owned
— themes contribute `theme.css` only.

Copy this file into a content repo (or merge into its AGENTS.md).

## Ground rules

- Install the engine if missing: `go install github.com/vessica-labs/vessica-studio/cmd/vstd@latest`
- Before any deck work, read the authoring conventions: run `vstd skill conventions`.
  Charts use its hybrid editable structure; existing inline SVG text can be
  promoted with `vstd chart promote-text <deck> <slide> --dry-run` followed by
  the write command after preview.
- For a specific workflow, run `vstd skill <name>` and follow it exactly:
  `deck-new`, `deck-fork`, `deck-review`, `market-refresh`, `slide-add`, `slide-edit`
- Preview with `vstd serve` (default http://localhost:4400) — it watches and
  rebuilds; never edit files under `decks/*/build/` (generated).
- Slide canvas is fixed 1280×720. Slide status lives on the root `<section>`:
  `data-hidden="1"` (skipped in presentation) and `data-parked="1"` ("unused",
  excluded from navigation and PDF export).
