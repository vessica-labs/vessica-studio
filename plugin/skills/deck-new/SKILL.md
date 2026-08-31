---
name: deck-new
description: Create, build, start, or generate a new presentation deck in a Vessica Studio/vstd content repository from a topic, outline, or conversation.
---

# Create a new deck

Read `../../docs/conventions.md` first, or run `vstd skill conventions` when the
skill is loaded outside the plugin file tree. Locate the studio root (folder with
`studio.yaml` — connected folder, or ask; a git clone works too).

## 1. Frame it

If not already clear from conversation, ask the user: audience & setting, length
(drives slide count), theme (list `themes/`), and whether research is needed now.
If the content came from a long chat (research already done), skip straight to outlining.

## 2. Outline before slides

Draft the narrative arc as a slide list (id, title, one-line intent each) and confirm with
the user before generating. Number ids sparsely (0010, 0020…) to leave insertion room.

## 3. Scaffold

`vstd new <deck> --title "T"` if CLI available; otherwise create `decks/<deck>/deck.yaml`
(title, theme, visibility: private, created), `deck.css`, and `slides/`.

## 4. Generate each slide — the full loop

For every slide, in order:
1. **Companion first**: write `NNNN-slug.md` — Intent, Key ideas (the full argument),
   Evidence & sources (with URLs/dates when researched), Talk track (ending with the next
   slide's opening cue), Visual direction, Log line.
   For any source-backed slide, copy the original into `decks/<deck>/sources/`
   and add companion frontmatter `attachments:` with the relevant PDF page or
   PowerPoint slide number.
2. **Fragment**: write `NNNN-slug.html` implementing the companion at the formatting
   standard, varying layout patterns for pacing (light/dark, columns, statements, dividers).
3. **Visual element** (required): follow the Visual element rule in conventions —
   reuse from library → generate/queue → inline SVG. Charts are the exception:
   use the conventions' hybrid chart structure with geometry in `.chart-art`
   SVG and all text in editable `.chart-label` overlays. Record asset ids in the
   companion frontmatter `visuals:`.
4. **Critic pass**: run the critic loop (screenshot + checklist + fix ≤2 rounds).
   Batch: generate all slides, then screenshot/review in one Playwright pass.
   Source-backed slides require side-by-side visual comparison with the attached
   source preview and correction for values, labels, geometry, and omissions.

## 5. Finish

Build (`vstd build <deck>` or note the engine auto-builds). Only provide the deck
URL after confirming the engine is running; otherwise report the built artifact
or the `vstd serve` command. Summarize the slide list, unresolved critic items,
and any visual QA that optional tooling prevented.
