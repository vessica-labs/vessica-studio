---
name: deck-new
description: Create a new presentation deck in a Vessica Studio content repo from a topic, outline, or conversation. Use when the user wants to create, build, start, or generate a new deck, presentation, or slide deck with Vessica Studio / vstd — e.g. "new deck about X", "build me a presentation on Y", "start a vessica deck".
---

# Create a new deck

Read `../../docs/conventions.md` first. Locate the studio root (folder with `studio.yaml` — connected folder, or ask; a git clone works too).

## 1. Frame it

If not already clear from conversation, ask (AskUserQuestion): audience & setting, length
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
   reuse from library → generate/queue → inline SVG. Record asset ids in the
   companion frontmatter `visuals:`.
4. **Critic pass**: run the critic loop (screenshot + checklist + fix ≤2 rounds).
   Batch: generate all slides, then screenshot/review in one Playwright pass.
   Source-backed slides require side-by-side visual comparison with the attached
   source preview and correction for values, labels, geometry, and omissions.

## 5. Finish

Build (`vstd build <deck>` or note the engine auto-builds), tell the user the deck URL
(`localhost:4400/d/<deck>/`), summarize slide list + any logged unresolved critic items.
