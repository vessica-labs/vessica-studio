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

## The companion contract (absolute)

1. NEVER edit a fragment without reading its companion first — it holds the why,
   the evidence, and what was already tried.
2. NEVER finish an edit without updating the companion: adjust Key ideas/Evidence if
   content changed, keep Talk track consistent (it must end with the OPENING CUE of the
   next slide — it drives presentation-mode auto-advance), append a dated `## Log` line.
3. Check the studio root and deck for a CLAUDE.md — repo-specific rules there override
   defaults here.

## Working modes — detect before acting

- **Engine running?** `curl -s localhost:4400/api/decks` (port from studio.yaml). If yes,
  prefer the edit API; the browser live-reloads on every file change either way.
- **No engine** (cloud session): edit files directly via the connected folder or git clone.
  Everything works file-first; the engine picks changes up when it runs.

## Engine surface

CLI: `vstd new|list|fork|diff-upstream|build|serve|asset gen|asset find|key check`.
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

Users can also drag-drop image (png/jpg/gif/webp) and video files onto a slide
in the player's edit mode — the engine registers them in the manifest
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
For high-stakes decks run the critic as a separate subagent for an independent eye.
