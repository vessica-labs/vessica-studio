# Vessica Studio conventions (shared reference)

Read this before any deck work. All skills in this plugin follow these rules.

## Content model

A studio root contains `studio.yaml`, `themes/<name>/{theme.css,tokens.json}`
(the player/HUD is engine-owned — never write a theme player.html),
`library/` (shared image library + `manifest.json`), `requests/` (async asset queue),
and `decks/<deck>/` with `deck.yaml`, `deck.css`, and `slides/`.

Each slide is TWO files with the same basename in `slides/`:
- `NNNN-slug.html` — exactly one `<section class="slide">` fragment using theme classes
  (s-title, s-lead, content, card, colcard, headband, chev, pill, numpill, icontile, aur, arc).
  Slide order = filename number.
- `NNNN-slug.md` — the companion: frontmatter (slide, status, visuals, layout) plus sections
  `## Intent`, `## Key ideas`, `## Evidence & sources`, `## Talk track`,
  `## Visual direction`, `## Log`.

Studio may also create an optional `NNNN-slug.link.yaml` provenance sidecar.
This is a Vessica-managed logical link, never an OS symlink. The HTML/Markdown
pair is the retained snapshot; the sidecar records the source deck/slide,
content and attachment hashes, and last refresh time. A linked target slide is
read-only: do not edit its fragment, companion, or attachments directly. Refresh
it from the source or detach it into a normal pair first. Reordering is allowed,
and copying a linked slide deliberately flattens its latest snapshot. Never
create link-to-link chains by hand.

Source artifacts used to create a slide live under `decks/<deck>/sources/` and
are listed in that slide's companion frontmatter:

```yaml
attachments:
  - name: source-deck.pptx
    path: sources/0030-source-deck-a1b2c3.pptx
    media_type: application/vnd.openxmlformats-officedocument.presentationml.presentation
    page: 3
```

Keep the original source attached when recreating an exhibit. Read the attachment
directly; `page` identifies the relevant PDF page or PPT slide. If PDF or
PowerPoint rendering is unavailable, preserve the attachment, update the
companion Log, and report that visual source comparison was not completed.

## The companion contract (absolute)

1. NEVER edit a fragment without reading its companion first — it holds the why,
   the evidence, and what was already tried.
2. NEVER finish an edit without updating the companion: adjust Key ideas/Evidence if
   content changed, keep Talk track consistent (it must end with the OPENING CUE of the
   next slide — it drives presentation-mode auto-advance), append a dated `## Log` line.
3. Check the studio root and deck for repository-specific agent instructions
   (for example `AGENTS.md` or `CLAUDE.md`); local rules override defaults here.

## Working modes — detect before acting

Start every authoring workflow with `vstd cloud workspace status`. Use that
command's result as the connection contract; never read or interpret cloud
association files directly.

- **Local-only workspace:** continue editing the paired HTML/Markdown files.
  Login and network access are optional and must never block authoring, build,
  serve, export, or skill discovery.
- **Connected workspace:** continue editing the same paired files locally. Use
  workspace, version, sync, conflict, and publish terminology: inspect with
  `vstd cloud workspace status`, incorporate a newer version with
  `vstd cloud workspace pull`, create an attributable version with
  `vstd cloud workspace sync`, and publish a selected revision with
  `vstd cloud publish create`.
- **Offline or failed status:** keep local work intact and report it as offline
  and unsynced. Never claim sync or publish succeeded. Local commands and direct
  file editing remain available.
- **Conflict:** do not replace local or cloud content. Re-run
  `vstd cloud workspace status`; pull only when the local projection is clean.
  Otherwise inspect the remote head in a separate clone, compare and reconcile
  the paired files explicitly, then use
  `vstd cloud workspace sync --resolve-head REVISION_ID` to acknowledge the
  exact recorded conflict head. Never
  ask for Git credentials or teach branch, push, pull, or rebase commands as the
  cloud workflow.
- **Interrupted pull:** stop other writers and use the local-only
  `vstd cloud workspace recover --root DIR` before editing. Preserve the recovery
  journal; do not bypass the pending-recovery error or delete recovery data.

- **Engine running?** `curl -s localhost:4400/api/decks` (port from studio.yaml). If yes,
  prefer the edit API; the browser live-reloads on every file change either way.
- **No engine:** edit files directly in the studio folder.
  Everything works file-first; the engine picks changes up when it runs.

Only provide a localhost deck URL after confirming the engine is serving it. If
the engine is not running, report the built artifact path or the `vstd serve`
command instead of implying that a live preview exists.

## Engine surface

CLI deck lifecycle: `vstd new`, `vstd list`, `vstd fork`, `vstd diff-upstream`,
`vstd build`, and `vstd serve`. Assets and tooling: `vstd asset gen`,
`vstd asset find`, `vstd asset add-video`, `vstd chart promote-text`,
`vstd key check`, and `vstd skill`.
HTTP (studio mode): `GET /api/decks`, `GET /api/deck/{d}/slide/{id}`,
`PUT .../fragment`, `PUT .../companion/{section}`, `PUT .../title`, `POST /api/deck/{d}/slides`.

## Formatting standard (executive default)

Canvas 1280×720. Large punchy type: body 22px, titles 42px and ONE line, lead-in 24px.
Layouts FILL the canvas — no large blank areas (use space-evenly, stretch, full-height
cards). One idea per slide; the title states the takeaway. Read the active theme's
`tokens.json` for palette/type scale; follow the theme's design rules (serif for
covers/dividers/statements only; restrained accent colors; rounded cards; no drop shadows).

## Visual element rule

Every generated slide gets AT LEAST one visual element. Priority order:
1. **Reuse** a library asset: read `library/manifest.json`, match tags + style family
   (`vstd asset find --tags x,y` if CLI available). Repeating icons across slides is a
   feature — it creates consistency.
2. **Generate** via the engine: `vstd asset gen --prompt P --family F --tags a,b` when the
   CLI + key are available; otherwise write a yaml file into `requests/`
   (`prompt`, `family`, `tags`, `size`, `slug`) — the running engine generates it.
   ALWAYS use a styleFamilies key from the manifest so imagery stays consistent.
   Reference assets as `/library/img/<file>`; use a styled placeholder div until the
   file exists.
3. **Inline SVG** (thin-line icons, gradient/aurora art) when a raster image adds nothing.

## Charts and data graphics (hybrid by default)

When a user asks for a chart, graph, plot, or data-driven exhibit, build it as
a **hybrid editable chart** unless they explicitly request another format:

- Put plotted geometry only in one inline SVG with class `chart-art`: axes,
  gridlines, lines, bars, areas, dots, connectors, and decorative marks.
- Put every human-readable string outside the SVG as an absolutely positioned
  HTML element with classes/attributes `chart-label` and `data-edit`: tick
  labels, axis titles, series labels, legends, values, annotations, and callouts.
- Wrap both layers in a positioned container carrying `data-chart-group` and
  `data-edit`. The chart background/group can be selected and moved as one
  object; individual labels remain independently selectable, editable, and
  movable in Studio and export as PowerPoint text boxes.
- Preserve accessibility on the geometry SVG with `role="img"`, an
  `aria-label` or `<title>/<desc>`, and `aria-hidden="true"` only when the HTML
  labels plus surrounding prose fully communicate the chart.
- Do not bake text into a raster image. Raster-chart migration is out of scope;
  recreate it as a hybrid chart when editability is required.

Typical structure:

```html
<div class="chart-group" data-chart-group data-edit style="position:relative">
  <svg class="chart-art" viewBox="0 0 800 420" role="img"
       aria-label="Revenue rises from 2024 to 2027">
    <!-- geometry only: lines, bars, dots, gridlines -->
  </svg>
  <div class="chart-label" data-edit style="position:absolute;left:8%;top:86%">2024</div>
  <div class="chart-label" data-edit style="position:absolute;left:72%;top:18%">$42M</div>
</div>
```

To migrate an existing inline SVG chart, preview first, then promote its text:

```sh
vstd chart promote-text <deck> <slide> --dry-run
vstd chart promote-text <deck> <slide>
```

The command uses rendered browser geometry, removes supported SVG `<text>` and
direct `<tspan>` nodes, creates editable overlays, and appends the companion Log.
It refuses partial writes when it encounters unsupported curved `textPath` or
zero-size labels. After migration, build and screenshot the slide; adjust label
placement if font metrics differ from the original SVG.

Users can also drag-drop image (png/jpg/gif/webp) and video files onto a slide,
or copy/paste an image from the clipboard while slide edit mode is active. The
engine registers them in the manifest
(content-hash deduped) exactly like generated/ingested assets, so check the
manifest for `model: upload` entries before assuming an asset must be generated.

## Video assets

Library videos live in `manifest.json` under `videos` (id, duration,
dimensions, poster). Reference by id ONLY — never by file path:

    <video class="vid" data-vstd-video="<asset-id>" data-autoplay data-loop></video>

Playback flags (attributes, no values): `data-autoplay` (play on slide
enter; omit for click-to-play with native controls), `data-loop`,
`data-unmuted` (sound — the player still starts muted until a user
gesture/present-mode entry), `data-keep-time` (resume instead of restarting
on re-entry). The player resolves `/assets/video/<id>` and owns the
play/pause lifecycle; never hardcode `src` or `poster`.

Adding a video: `vstd asset add-video <file> [--slug S --tags a,b]` when the
CLI is available (normalizes to web H.264 +faststart, extracts a poster,
updates the manifest, syncs the bucket). From a cloud session: copy the file
to `library/inbox/<name>.mp4` via the bridge, then drop a yaml into
`requests/` with `type: video`, `path: library/inbox/<name>.mp4`, `slug`,
`tags` (+ optional `deck:`/`slide:`) — the running engine ingests it.
Video bytes are gitignored (`library/video/`); posters are committed. List
the asset id in the companion's `visuals:` frontmatter like any other asset.
Audiences on share links get poster + tap-to-stream, not autoplay.

## The critic loop (for generated/heavily-edited slides)

After building a slide: screenshot it (Playwright headless against the built deck or
served URL, viewport 1280×720, hash `#/<n>`), then review against the checklist:
- Title one line, states the takeaway; lead-in supports it
- Type scale ≥ standard; no text below 15px rendered
- Canvas filled; nothing overflows into the footer zone; no element collisions
- Palette/serif-sans usage matches theme rules; footer elements present
- The slide delivers the companion's Intent; the visual earns its place
- Content accurate to Evidence & sources
Fix and re-screenshot (max 2 iterations); log unresolved items in the companion `## Log`.
For high-stakes decks, use an independent review pass, with a subagent when the
environment supports and authorizes one.
When the companion has source attachments, the critic must compare a 1280×720
render of the result against a rendered preview of the cited source page/slide,
then correct fidelity problems before declaring the edit complete. Check values,
labels, geometry, hierarchy, omissions, and source attribution—not only style.
If Playwright, Chromium, or a running engine is unavailable, perform static
fragment and companion checks, run the applicable build/tests, log unresolved
items, and report that screenshot-based visual QA was not completed.
