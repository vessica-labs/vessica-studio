# Vessica Studio

**A local-first, agent-driven presentation studio built on ordinary files.**

[![Go 1.22+](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go)](https://go.dev/doc/install)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

[Documentation](https://studio-docs.vessica.ai/) ·
[Install the CLI and Codex plugin](https://studio-docs.vessica.ai/install/) ·
[Web editor guide](https://studio-docs.vessica.ai/web-editor/) ·
[Architecture](https://studio-docs.vessica.ai/architecture/) ·
[Vessica voice agent](https://studio-docs.vessica.ai/voice-agent/)

Vessica Studio turns a directory of HTML slide fragments and Markdown
companions into a live, editable presentation. Use the `vstd` CLI to create,
serve, review, fork, export, and deploy decks. Use Codex or another coding
agent to work directly on the same transparent file format.

The result is a presentation workflow that is inspectable, diffable, forkable,
and deployable with Git—without locking the deck inside a proprietary editor.

## Contents

- [Why Vessica Studio](#why-vessica-studio)
- [Documentation](https://studio-docs.vessica.ai/)
- [Installation](#installation)
- [Five-minute quickstart](#five-minute-quickstart)
- [Use Vessica Studio with Codex](#use-vessica-studio-with-codex)
- [How a studio is organized](#how-a-studio-is-organized)
- [CLI manual](#cli-manual)
- [Studio and player features](#studio-and-player-features)
- [Configuration](#configuration)
- [Hosted deployment](#hosted-deployment)
- [Development and contributing](#development-and-contributing)

## Why Vessica Studio

- **File-native authoring.** Every slide is an HTML fragment paired with a
  Markdown companion containing intent, evidence, sources, visual direction,
  talk track, and edit history.
- **Agent-ready workflows.** Six canonical authoring workflows are embedded in
  the `vstd` binary and shared across Codex and other agent runtimes.
- **Live editing.** The studio watches files, rebuilds decks, reloads connected
  browsers, and supports direct canvas, companion, sticky-note, and voice edits.
- **Editable output.** Download PDF or PowerPoint from the player. PowerPoint
  export preserves supported text, shapes, chart labels, and images as editable
  objects instead of flattening every slide into one image.
- **Reusable media.** Generate images, ingest video, maintain a shared asset
  manifest, paste images onto slides, and sync large video files to
  S3-compatible storage.
- **Source-aware creation.** Attach PDFs, PowerPoint files, documents,
  spreadsheets, or images to a slide companion so an editing agent can inspect
  the original source. Source-backed redesigns can run a separate visual critic
  pass against the reference.
- **Forkable decks.** Create audience- or client-specific variants while
  retaining upstream provenance and detecting later parent changes.
- **Organized and reusable work.** Keep a personal one-level folder view of
  owned and shared presentations, then send one or many slides to another deck
  as editable copies or read-only linked snapshots.
- **Presentation and audience modes.** Present locally, publish a read-only
  audience surface, mint expiring deck links, and optionally enable chat, voice,
  SMS, email, and live audience pulse.
- **Local-first, cloud-capable.** Author without a hosted service, connect or
  clone a Cloud workspace, synchronize revisions, and publish a selected
  revision without managing Git credentials or branches.
- **Single-team collaboration.** Invite teammates by email, keep per-user
  private catalogs, share selected decks read-only, and fork shared work before
  editing it.

## Installation

For the visual setup path, including Codex desktop and CLI plugin installation,
see the [installation guide](https://studio-docs.vessica.ai/install/).

### Requirements

- [Go 1.22 or newer](https://go.dev/doc/install)
- Git, if you want version control or hosted content sync

Optional tools unlock additional features:

| Tool | Used for |
|---|---|
| Chrome or Chromium | PDF export, editable PowerPoint export, chart-text migration, and visual review |
| FFmpeg and FFprobe | Video normalization, metadata inspection, and poster extraction |
| LibreOffice and Poppler | Rendering attached PowerPoint/PDF sources for visual comparison |
| OpenAI API key | Image generation, Vessica voice, transcription, audience chat, and agent tools |
| Railway CLI | Hosted deployment and service management |
| S3-compatible bucket | Storage for video bytes that should not live in Git |

### Install with Go

```sh
go install github.com/vessica-labs/vessica-studio/cmd/vstd@latest
vstd version
```

If `vstd` is not found after installation, add Go's binary directory to your
`PATH`:

```sh
export PATH="$(go env GOPATH)/bin:$PATH"
```

### Build from source

```sh
git clone https://github.com/vessica-labs/vessica-studio.git
cd vessica-studio
make build
./vstd version
```

Use `make install PREFIX="$HOME/.local"` for a user-local installation, or
`make go-install` to install through Go.

### Optional OpenAI setup

The core file, build, serve, and presentation workflows do not need an API key.
To enable AI-backed features:

```sh
export OPENAI_API_KEY="sk-..."
vstd key check
```

For a key that does not remain in shell history or a repository, use a secret
manager and set `openai.api_key_cmd` in `studio.yaml`. A macOS Keychain example
is included under [Configuration](#openai).

## Five-minute quickstart

Create a content repository and its first deck:

```sh
vstd init my-studio
cd my-studio
vstd new product-story --title "Our Product Story"
vstd serve product-story
```

## Optional Vessica Studio Cloud workflow

These additive commands require a compatible hosted native-client API. The
private service integration is tracked in VES-74; fake-cloud tests do not imply
the public hosted service is available yet.

Local authoring, builds, serving, export, and skill discovery do not require a
Cloud account or network connection. To use a Cloud workspace, first sign in
with the native device flow:

```sh
vstd cloud login
vstd cloud account
vstd cloud workspace list
```

`login` prints a verification URL and user code. Renewable session material is
stored in the operating system credential store; access tokens are kept only in
memory. If secure credential storage is unavailable, login fails instead of
writing a plaintext fallback.

Clone a workspace into a directory that does not yet exist, or connect a valid
existing studio:

```sh
vstd cloud workspace clone WORKSPACE_ID --target my-studio
vstd cloud workspace connect WORKSPACE_ID --root my-studio
```

The connection is recorded as non-secret local state in
`.vstd/cloud-workspace.json`. It does not change the `studio.yaml`, `deck.yaml`,
or paired slide-file contract and is excluded from synchronized content and
build output.

From a connected studio, inspect and synchronize revisions with:

```sh
vstd cloud workspace status
vstd cloud workspace pull
vstd cloud workspace sync --message "Update product story"
```

`status` reports the recorded base revision, Cloud head, and whether the local
files are synchronized, unsynced, conflicted, or offline. `pull` refuses to
replace unsynced local content. `sync` compares against the recorded base; if
the Cloud head advanced, it reports a conflict and preserves both the local
files and remote head for manual reconciliation.

Publish the current synchronized revision, or select a revision explicitly:

```sh
vstd cloud publish create
vstd cloud publish create --revision REVISION_ID
vstd cloud publish status PUBLICATION_ID
```

After a sync conflict, inspect the remote head (for example, clone it to a separate
directory) and reconcile the paired files locally. Acknowledge the exact recorded
conflict head with `vstd cloud workspace sync --resolve-head REVISION_ID`; the
server still rejects the operation if that head has changed again. Reconnecting
an already connected studio is refused so it cannot silently reset the base.

Pull keeps a durable, private local recovery journal before replacing files. If
interrupted, studio operations fail closed. Stop other writers, then run
`vstd cloud workspace recover --root DIR` to restore the pre-pull projection.
Do not delete the recovery journal manually. Unrelated Git and local state are
preserved. Directory durability depends on the host filesystem (Windows lacks
directory fsync); recovery is not a substitute for backups.

Cloud sessions are isolated by normalized endpoint. Keep `VSTD_CLOUD_ENDPOINT`
consistent with the workspace association; a mismatch fails before content work.
Snapshots reject credential commands and custom credential-bearing provider
endpoints in `studio.yaml`; configure those providers locally via environment
variables instead. These restrictions affect cloud exchange only, not local use.

Publishing without `--revision` is refused when local changes or a conflict are
pending. The command reports the publication ID, selected revision, service
status, and URL. Run `vstd cloud diagnostics` for sanitized endpoint and
protocol compatibility details, and `vstd cloud logout` to revoke the session
when reachable and remove the local credential.

Open [http://localhost:4400/d/product-story/](http://localhost:4400/d/product-story/).
The starter deck contains one cover slide. Edit its paired files under
`decks/product-story/slides/`, or ask Codex to build out the deck. The server
rebuilds on file changes and refreshes connected browsers.

In a second terminal:

```sh
cd my-studio
vstd list
vstd build product-story
```

The static build is written to `decks/product-story/build/index.html`. Build
output is disposable; edit the source files under `slides/`, never `build/`.

From the player HUD you can:

- navigate or present the deck;
- enter slide-edit mode and edit supported elements;
- open and edit the current slide's Markdown companion;
- attach or inspect source files;
- paste an image from the clipboard onto the slide;
- leave a sticky-note instruction for the redesign agent;
- download PDF or editable PowerPoint.

## Use Vessica Studio with Codex

Vessica Studio keeps its authoring instructions in six canonical skills:

| Skill | Use it for |
|---|---|
| `deck-new` | Frame, outline, create, and visually review a new deck |
| `slide-add` | Insert one or more context-aware slides into an existing narrative |
| `slide-edit` | Edit content or layout while preserving the companion contract |
| `deck-fork` | Tailor a deck for a client, audience, industry, or event |
| `deck-review` | Run whole-deck visual, narrative, and companion QA |
| `market-refresh` | Re-research dated claims and update time-sensitive slides |

The skill files are embedded in `vstd`; the CLI can list or print them at any
time:

```sh
vstd skill
vstd skill conventions
vstd skill slide-edit
```

### Install the Codex plugin

Add the repository's marketplace, then install Vessica Studio:

```sh
codex plugin marketplace add vessica-labs/vessica-studio --ref main
codex plugin add vessica-studio@vessica-studio
```

Start a new Codex task after installation so the six plugin skills are
discovered. In Codex CLI, `/plugins` opens the plugin browser; in Codex desktop,
use the Plugins surface. Verify the install with `codex plugin list`.

The repository also retains legacy thin prompt launchers for environments that
do not support plugins. Clone the repository and run `./codex/install.sh`; it
copies launchers into `${CODEX_HOME:-$HOME/.codex}/prompts`. The launchers call
`vstd skill` at runtime and require a new Codex session after installation.

Use a launcher explicitly:

```text
/vstd-deck-new Build a 12-slide board presentation about our product strategy
/vstd-slide-add Add a customer proof slide after 0040
/vstd-slide-edit Redesign slide 0060 around the attached source PDF
/vstd-deck-review Review the entire deck before tomorrow's presentation
/vstd-deck-fork Fork product-story for Acme's executive team
/vstd-market-refresh Refresh every market statistic through August 2026
```

You can also ask Codex naturally. A useful request names the studio root or
deck, the audience, the desired outcome, and any constraints:

```text
Use the Vessica Studio slide-edit workflow to simplify slide 0070 in the
product-story deck. Preserve its evidence and talk-track transition, keep the
chart editable, and run a visual critic pass.
```

Codex should read `vstd skill conventions` before deck work and the relevant
workflow before acting. Copy [`codex/AGENTS.md`](codex/AGENTS.md) into a studio
content repository to make those rules persistent for that repository. The
guide tells Codex to preserve slide/companion pairs, use the canonical skills,
and avoid generated build output.

Plugin skills may be invoked explicitly from Codex's skill picker or with the
`$` skill syntax, for example `$vessica-studio:slide-edit`. Codex can also select
a skill implicitly from its description. See the
[Codex plugin documentation](https://learn.chatgpt.com/docs/plugins) and
[Codex skills documentation](https://learn.chatgpt.com/docs/build-skills) for
current installation, discovery, and invocation behavior.

### What the skills enforce

The workflows are more than prompt templates. They preserve the authoring
contract:

1. Read the companion before changing a slide.
2. Keep factual claims aligned with evidence and sources.
3. Update the talk track and its transition to the next slide.
4. Record the completed change in the companion log.
5. Give every generated slide a meaningful visual.
6. Run a visual critic pass after layout or design changes.
7. Compare source-backed slides directly with the attached original.

The source of truth lives in [`plugin/skills`](plugin/skills) and
[`plugin/docs/conventions.md`](plugin/docs/conventions.md). The Codex launchers
do not duplicate those instructions.

### Claude Code and Cowork

The same workflows are packaged as a Claude plugin:

```text
/plugin marketplace add vessica-labs/vessica-studio
/plugin install vessica-studio@vessica-studio
```

Start a new session, ask naturally for a deck task, or invoke a workflow such
as `/vessica-studio:deck-new`. Both integrations ultimately read the same files
embedded in `vstd`.

## How a studio is organized

```text
my-studio/
├── studio.yaml                    # engine, model, port, and storage settings
├── themes/
│   └── default/
│       ├── theme.css              # presentation styling
│       └── tokens.json            # design tokens
├── library/
│   ├── manifest.json              # shared image and video catalog
│   ├── img/                       # generated or uploaded images
│   ├── video/                     # local video bytes; keep out of Git
│   └── video-posters/             # generated poster frames
├── requests/                      # asynchronous media and redesign requests
├── site/                          # optional public homepage and assets
└── decks/
    └── product-story/
        ├── deck.yaml              # title, theme, visibility, fork provenance
        ├── deck.css               # deck-specific style overrides
        ├── sources/               # original PDFs, PPTX files, images, and documents
        ├── build/
        │   └── assets/powerpoint/    # disposable per-slide PowerPoint cache
        └── slides/
            ├── 0010-cover.html    # one root <section class="slide">
            ├── 0010-cover.md      # companion evidence, intent, talk track, and log
            └── 0010-cover.link.yaml # optional Vessica-managed slide link provenance
```

### The slide pair

Each slide has two source files with the same basename:

- `NNNN-slug.html` contains exactly one root
  `<section class="slide">`. Slide order follows the numeric filename prefix;
  sparse numbering such as `0010`, `0020`, and `0030` leaves room to insert.
- `NNNN-slug.md` contains frontmatter plus `Intent`, `Key ideas`, `Evidence &
  sources`, `Talk track`, `Visual direction`, and `Log` sections.

The companion is part of the content model, not optional documentation. It is
the agent's context, evidence record, and presenter cue sheet.

A linked slide still has the normal HTML/Markdown pair as a durable snapshot.
Its optional `.link.yaml` sidecar points to the source deck and slide and records
source hashes and the last refresh. Linked target slides are read-only until
detached. When the target owner retains source access, Studio refreshes changed
HTML, companion content, and source attachments. If the source disappears or
access is revoked, the last snapshot continues to render and is marked stale to
presenters; audience views and exports show only the slide. Copying a linked
slide flattens its latest snapshot into an ordinary editable slide.

Source files used to create a slide live in `decks/<deck>/sources/` and are
declared in companion frontmatter:

```yaml
attachments:
  - name: source-deck.pptx
    path: sources/0030-source-deck-a1b2c3.pptx
    media_type: application/vnd.openxmlformats-officedocument.presentationml.presentation
    page: 3
```

### Engine-owned player, theme-owned presentation styling

The player and HUD are embedded in the engine. Themes provide `theme.css` and
design tokens; they do not replace the player markup. This keeps navigation,
editing, export, Vessica, video controls, and audience behavior consistent
across themes.

Slides use a fixed 1280×720 canvas. Mark an element with
`data-current-month-year` to render the viewer's current month and year, such as
`August 2026`, at runtime and in browser-backed exports.

## CLI manual

Run `vstd help` for the installed command summary. Examples below assume the
current directory contains `studio.yaml`. Most studio commands also accept
`--root DIR`; it is a per-command flag, not a global flag.

### Create and inspect studios

#### `vstd init [dir]`

Scaffold a studio root without overwriting existing files:

```sh
vstd init quarterly-presentations
```

Creates `studio.yaml`, the default theme, an empty asset manifest, `decks/`,
`library/`, and `requests/`.

#### `vstd new <deck> [--title T] [--root DIR]`

Create a deck with metadata, deck CSS, and a paired starter cover slide:

```sh
vstd new operating-model --title "The AI Operating Model"
```

Deck names use lowercase letters, digits, and hyphens.

#### `vstd list [--root DIR]`

List every deck, slide count, title, and fork parent:

```sh
vstd list
```

### Build, serve, and present

#### `vstd build <deck> [--root=DIR]`
#### `vstd build --all [--root=DIR]`

Assemble one deck or every deck into generated `build/index.html` files:

```sh
vstd build operating-model
vstd build --all
```

#### `vstd release-build [deck] --output DIR [--root DIR]`

Build one deck for immutable hosted delivery through the public engine contract.
The command requires a revision-identifiable, unmodified `vstd` binary and an
empty output directory. It writes a self-contained `index.html`, only the
referenced release assets, and `release-manifest.json` with the exact engine
version/revision plus deterministic artifact and manifest SHA-256 checksums.
When `deck` is omitted, the studio must contain exactly one deck.

```sh
vstd release-build product-story --output /tmp/product-story-release
```

The release output contains no companion Markdown or authoring source. Missing,
unsafe, symlink-escaped, oversized, or mutable assets fail the build rather than
producing a partial release.

Consumers installing directly from an immutable Git revision must embed the
same full revision so the binary can verify and report it. For example, with
`REVISION` set to a 40-character commit SHA:

```sh
go install -ldflags "-X main.releaseBuildRevision=$REVISION" \
  "github.com/vessica-labs/vessica-studio/cmd/vstd@$REVISION"
```

Running `vstd build` without a deck also builds all decks.
When building from outside the studio directory, use the `--root=DIR` form so
the deck name remains unambiguous to the current command parser.

#### `vstd serve [deck] [flags]`

Build, watch, live reload, and expose the player and HTTP editing surface:

```sh
vstd serve operating-model
vstd serve operating-model --port 4500
vstd serve --mode present
vstd serve --mode public
vstd serve operating-model --agent
```

Flags:

| Flag | Meaning |
|---|---|
| `--root DIR` | Studio root; default `.` |
| `--port N` | Override `studio.yaml`, `PORT`, and the default port |
| `--mode studio\|present\|public` | Select the authorization and editing mode |
| `--agent` | Run the optional redesign worker alongside the server |

Serving modes:

| Mode | Intended use | Content writes | Access model |
|---|---|---:|---|
| `studio` | Local authoring | Yes | Local operator is the presenter |
| `present` | Local presenting | No | Local operator is the presenter |
| `public` | Hosted presenting | Presenter-only when content sync is enabled | GitHub-allowlisted presenter or signed audience link |

Do not expose `studio` mode to an untrusted network. Its write routes trust the
local operator.

#### PDF and PowerPoint export

Start `vstd serve`, open a deck, choose **Entire deck** or **Current slide** in
the Download menu, and then choose PDF, visual-exact PowerPoint, or editable
PowerPoint. Presenters receive all three formats; audience sessions receive PDF
only. Browser-backed export requires Chrome or Chromium on the machine running
`vstd`; visual-exact PowerPoint rasterization also requires Poppler.

The HTTP routes accept an optional non-parked slide ID, for example
`/api/deck/operating-model/export.pdf?slide=0030-economics` and
`/api/deck/operating-model/export.pptx?mode=editable&slide=0030-economics`.
Without `slide`, they preserve the existing whole-deck behavior.

PDF is a fixed-layout document and is regenerated per request. PowerPoint
stores disposable per-slide artifacts and a manifest under
`decks/<deck>/build/assets/powerpoint/`. Repeating an export reuses unchanged
slides; changing a fragment, referenced local asset, theme CSS, or deck CSS
invalidates only the affected entries (theme/deck CSS naturally affect the
whole deck). The cache is excluded from Git, content sync, file-watcher reloads,
bundles, and source-asset manifests. Delete that directory at any time to force
a clean rebuild. Editable PowerPoint converts supported rendered elements into
native PresentationML objects; complex browser effects may not translate
exactly, so review the downloaded deck before delivery.

#### `vstd bundle <deck> [--root DIR]`

Create a portable folder containing `index.html` and the referenced local media:

```sh
vstd bundle operating-model
```

Output is written to `decks/<deck>/build/bundle/`. Keep the folder together when
presenting offline. This is the preferred static export for video-bearing decks.

### Fork and maintain deck variants

#### `vstd fork <deck> <client> [--root DIR]`

Create `decks/<deck>--<client>` with fork provenance and per-slide parent
hashes:

```sh
vstd fork operating-model acme
```

#### `vstd diff-upstream <fork> [--root DIR]`

Report slides added, removed, or changed in the parent since the fork:

```sh
vstd diff-upstream operating-model--acme
```

This is a detection tool, not an automatic merge. Use the `deck-fork` or
`slide-edit` workflow to port upstream improvements while preserving client
customizations.

### Images and the shared asset library

#### `vstd asset gen`

Generate and register an image:

```sh
vstd asset gen \
  --prompt "Editorial illustration of human and AI collaboration" \
  --family editorial \
  --tags workforce,collaboration \
  --size 1536x1024 \
  --slug human-ai-team
```

Flags:

| Flag | Meaning |
|---|---|
| `--prompt P` | Required image prompt |
| `--family F` | Style family from `library/manifest.json` |
| `--tags a,b` | Comma-separated retrieval tags |
| `--size WxH` | Requested image size; default `1024x1024` |
| `--slug S` | Stable asset ID slug |
| `--dry-run` | Show the resolved prompt and ID without calling the API |
| `--root DIR` | Studio root |

Generated files are cataloged in `library/manifest.json` and referenced from
slides as `/library/<file>`. Reuse an existing asset before generating a near
duplicate.

#### `vstd asset list` and `vstd asset find`

Browse the manifest or search by family and tags:

```sh
vstd asset list
vstd asset find --tags workforce,collaboration
vstd asset find --family editorial
```

Tag matching is “any of” the supplied tags. `find` prints a generation hint when
no asset matches.

### Video assets and object storage

#### `vstd asset add-video <file>`

Inspect, optionally normalize, posterize, hash, and register a video:

```sh
vstd asset add-video ./demo.mov --slug product-demo --tags demo,product
```

Flags:

| Flag | Meaning |
|---|---|
| `--slug S` | Asset ID; defaults from the filename |
| `--tags a,b` | Comma-separated retrieval tags |
| `--no-transcode` | Keep the original bytes while still hashing and posterizing |
| `--root DIR` | Studio root |

When storage is configured, ingestion also uploads missing bytes. Reference a
cataloged video by ID, never by a hard-coded source path:

```html
<video class="vid" data-vstd-video="product-demo" data-autoplay data-loop></video>
```

The player owns source resolution and playback. Video slides include a
persistent sound on/off control for editing and presenting.

#### `vstd asset push` and `vstd asset pull`

Synchronize manifest-referenced video files with an S3-compatible bucket:

```sh
vstd asset push
vstd asset pull
```

`push` uploads missing objects. `pull` restores video bytes missing from a fresh
checkout. Keep `library/video/` out of Git; commit the manifest and posters.

### Editable chart migration

Charts should use a hybrid structure by default: inline SVG for plotted geometry
and positioned HTML for human-readable text. The chart can be selected as a
group while labels remain editable and export as PowerPoint text boxes.

#### `vstd chart promote-text <deck> <slide>`

Preview and migrate text from an existing inline SVG chart:

```sh
vstd chart promote-text operating-model 0060-growth --dry-run
vstd chart promote-text operating-model 0060-growth
```

Additional flags are `--root DIR` and `--chromium PATH`. The command uses
rendered browser geometry, appends a companion log entry, and refuses partial
writes when it encounters unsupported curved or zero-size text. It does not
migrate raster charts.

### Agent and skill commands

#### `vstd skill [name]`

List embedded skills or print one workflow/reference document:

```sh
vstd skill
vstd skill conventions
vstd skill deck-review
```

The output is plain Markdown designed to be read by any coding agent.

#### `vstd agent [--root DIR]`

Run one synchronous sweep of queued redesign requests:

```sh
VSTD_AGENT=1 vstd agent
```

Use `vstd serve --agent` or `VSTD_AGENT=1 vstd serve` for a continuous background
worker. The default coding agent is Claude; set `VSTD_AGENT_CMD` or the related
agent environment variables to use another command and tune limits. Hosted
Codex workers must set `VSTD_AGENT_SANDBOX=railway`: the API service dispatches
each pass to a disposable, network-isolated Railway Sandbox and refuses to run
Codex in-process in public mode. The dispatcher requires a project-scoped
`RAILWAY_TOKEN`, `RAILWAY_ENVIRONMENT_ID`, and the Railway JavaScript SDK at
`VSTD_RAILWAY_SDK` (default `/opt/vstd-sandbox/node_modules/railway/dist/index.js`).
The sandbox receives only the requested deck, its theme and referenced library
assets, matching asset requests, the `vstd` binary, and an OpenAI credential
resolved by Railway variable reference.

### Keys and audience links

#### `vstd key check [--root DIR]`

Confirm that an OpenAI key resolves without printing the key or its source:

```sh
vstd key check
```

#### `vstd qr <deck>`

Mint an expiring, signed, deck-scoped audience URL and save a QR image:

```sh
vstd qr operating-model --ttl 24
vstd qr operating-model --ttl 72 --host https://decks.example.com
```

The signing secret comes from `VSTD_SECRET` or `share_secret_cmd`. The QR image
is saved to `decks/<deck>/build/share-qr.png`.

### Railway commands

#### `vstd railway up`

Configure and deploy a content repository that already contains its deployment
files:

```sh
vstd railway up --dry-run
vstd railway up --client-id GITHUB_OAUTH_CLIENT_ID --allowed octocat
```

Flags:

| Flag | Meaning |
|---|---|
| `--root DIR` | Studio root |
| `--client-id ID` | GitHub OAuth app client ID with Device Flow enabled |
| `--allowed a,b` | Comma-separated presenter GitHub logins |
| `--github-token TOKEN` | Read token for cloning a private engine repository |
| `--with-openai-key` | Copy the locally resolved OpenAI key without prompting |
| `--dry-run` | Print the deployment plan without calling Railway |

The command ensures the Railway CLI is available, signs in if needed, links or
creates a project, sets core variables, deploys, obtains a domain, and stores the
public host in `studio.yaml`. It requires a `Dockerfile` and the related
deployment files in the content repository.

#### `vstd railway status` and passthrough

```sh
vstd railway status
vstd railway logs
vstd railway variables
```

`status` reports the linked service and saved public host. Any other arguments
are passed to the Railway CLI from the studio root.

### Help and version

```sh
vstd help
vstd version
```

`help` prints the installed command and environment summary. `version` is useful
when reporting a bug or confirming which engine build a hosted studio runs.

## Studio and player features

### Direct editing and the top ribbon

In studio mode, select a supported text, shape, image, or chart element on the
slide. Common formatting and object controls appear in the top ribbon so they do
not obscure the canvas or run off-screen. Keyboard shortcuts are suppressed
while typing in sticky notes, companion fields, dialogs, or editable elements.

Pictures include both `<img>` elements and elements whose CSS uses a background
image. Select a picture and choose **Crop** to drag the image within its fixed
frame; the arrow keys provide fine positioning. CSS background pictures also
expose **Zoom −** and **Zoom +** controls to loosen or tighten the crop. These
position and size changes are saved with the slide fragment like other direct
edits.

Drag across blank slide canvas to draw a marquee around multiple objects. The
selected objects receive one combined outline; drag any selected object to move
the group, use the arrow keys to nudge it, or press Delete to remove every
selected object in one undoable edit. Object-specific formatting, resizing, and
picture cropping remain available when exactly one object is selected.

### Companion drawer and source attachments

The companion button opens the current slide's Markdown in a side drawer. The
drawer renders the document for reading and supports direct editing. Vessica can
open the companion and apply dictated narrative edits.

Attach a PDF, PPT/PPTX, document, spreadsheet, or image to the companion. The
file becomes part of the slide's source record and is available to the editing
agent. A source-aware request can be as direct as:

```text
Look at exhibit A on page 12 of the attached PDF and add the missing customer
segment detail to this slide.
```

### Images from the clipboard

Enter slide-edit mode, copy an image, and paste it onto the slide. Vessica Studio
adds the image to the library, content-hash deduplicates it, and creates an
editable canvas element.

### Hybrid editable charts

Use one `data-chart-group` container containing:

- an SVG `.chart-art` layer for axes, gridlines, lines, bars, areas, dots, and
  decorative geometry; and
- absolutely positioned `.chart-label[data-edit]` HTML elements for ticks,
  legends, values, annotations, and other text.

Keep an accessible description on the chart geometry with `aria-label`,
`<title>`, or `<desc>`. Vessica can highlight a chart when the spoken topic
matches its accessible description or visible labels. Slide titles are excluded
from Vessica highlighting.

### Video playback

The player starts and stops cataloged videos as slides enter and leave. Video
slides expose a small persistent sound switch so editing does not repeatedly
trigger audio. Use data attributes such as `data-autoplay`, `data-loop`,
`data-unmuted`, and `data-keep-time` to control behavior.

## Configuration

A new `studio.yaml` starts with:

```yaml
theme_default: default
port: 4400
openai:
  image_model: gpt-image-2
  realtime_model: gpt-realtime-2
  base_url: https://api.openai.com/v1
```

Other supported top-level settings include `app_host`, `public_host`,
`share_secret_cmd`, and `storage`. In hosted collaboration mode, `app_host` is
the authenticated catalog/team origin and `public_host` is the separate player
and audience origin.

### OpenAI

Key resolution order is:

1. `OPENAI_API_KEY`
2. `VSTD_OPENAI_KEY`
3. `openai.api_key_cmd` in `studio.yaml`

Example using macOS Keychain:

```sh
security add-generic-password -U -s vessica-openai -a "$USER" -w 'sk-...'
```

```yaml
openai:
  image_model: gpt-image-2
  realtime_model: gpt-realtime-2
  base_url: https://api.openai.com/v1
  api_key_cmd: security find-generic-password -s vessica-openai -w
```

Run `vstd key check` after configuring the resolver.

### S3-compatible storage

Storage can be configured in YAML:

```yaml
storage:
  endpoint: https://s3.example.com
  bucket: presentation-media
  region: us-east-1
  access_key_cmd: security find-generic-password -s vstd-s3-access -w
  secret_key_cmd: security find-generic-password -s vstd-s3-secret -w
```

Or with environment variables:

| Variable | Purpose |
|---|---|
| `VSTD_S3_ENDPOINT` | S3-compatible API endpoint |
| `VSTD_S3_BUCKET` | Bucket name |
| `VSTD_S3_REGION` | Region |
| `VSTD_S3_ACCESS_KEY` | Access key |
| `VSTD_S3_SECRET_KEY` | Secret key |

Environment variables take precedence over YAML.

### Runtime and hosted environment variables

| Variable | Purpose |
|---|---|
| `PORT` | Override the configured HTTP port; Railway sets this |
| `VSTD_MODE` | `studio`, `present`, or `public` |
| `VSTD_CHROME` / `VSTD_CHROMIUM` | Browser executable for exports and migration |
| `VSTD_SECRET` | Sign presenter sessions and audience links |
| `VSTD_GITHUB_CLIENT_ID` | GitHub OAuth application with Device Flow enabled |
| `VSTD_ALLOWED_GITHUB` | Comma-separated presenter login allowlist |
| `VSTD_COLLABORATION` | Set to `1` to enable PostgreSQL-backed single-team collaboration |
| `DATABASE_URL` | PostgreSQL connection URL; required in collaboration mode |
| `VSTD_APP_ORIGIN` | Exact HTTPS origin for marketing, authentication, catalogs, and team administration |
| `VSTD_PLAYER_ORIGIN` | Separate exact HTTPS origin for deck execution, audience links, and player APIs |
| `VSTD_DOCS_URL` | Documentation-site URL opened from the presentation catalog; defaults to `https://studio-docs.vessica.ai` |
| `VSTD_OWNER_GITHUB_LOGIN` | GitHub login that may bootstrap and administer the team |
| `PUBLIC_URL` | Public base URL for audience and action links |
| `VSTD_CONTENT_SYNC` | Set to `1` to enable authenticated hosted editing |
| `VSTD_GIT_REPO` | Git content repository URL |
| `VSTD_GIT_BRANCH` | Content branch to push and poll |
| `VSTD_GIT_TOKEN` | Repository-scoped content write token |
| `VSTD_GIT_DEBOUNCE_SECONDS` | Delay before batching hosted edits into a push |
| `VSTD_GIT_POLL_SECONDS` | Remote polling interval |
| `VSTD_AGENT` | Set to `1` to enable the redesign worker |
| `VSTD_AGENT_CMD` | Agent command or supported agent name |
| `VSTD_AGENT_SANDBOX` | Set to `railway` for hosted Codex execution; required for Codex in public mode |
| `VSTD_AGENT_SANDBOX_SECRET_SERVICE` | Railway service name that owns the Codex credential; defaults to `RAILWAY_SERVICE_NAME` |
| `VSTD_AGENT_SANDBOX_SECRET_VARIABLE` | Railway variable referenced as the sandbox Codex credential; defaults to `OPENAI_API_KEY` |
| `VSTD_RAILWAY_SDK` | Absolute path to the Railway JavaScript SDK entrypoint |
| `RAILWAY_TOKEN` | Project-scoped Railway token used only by the sandbox dispatcher |
| `VSTD_AGENT_TIMEOUT` | Redesign timeout, such as `30m` |
| `VSTD_AGENT_CRITIC_TIMEOUT` | Source-critic timeout, such as `20m` |
| `VSTD_AGENT_CONCURRENCY` | Maximum concurrent redesign jobs |
| `VSTD_AGENT_MAX_PER_HOUR` | Hourly redesign-job limit |
| `VSTD_GIT_PUSH` | Set to `1` to commit and push completed redesign passes |
| `VSTD_GIT_REMOTE` | Bootstrap remote used by the legacy redesign-worker Git path |
| `VSTD_TOOLS_MODEL` | Model used for Vessica tools |
| `VSTD_TRANSCRIBE_MODEL` | Model used for transcription |
| `TELNYX_API_KEY`, `TELNYX_FROM_NUMBER`, `TELNYX_CONNECTION_ID` | Optional SMS and calling actions |
| `RESEND_API_KEY`, `RESEND_FROM`, `VSTD_CONTACT_TO` | Optional email and public contact actions |

Never place credentials in slide files, deck metadata, built output, manifests,
fixtures, or Git history.

## Hosted deployment

`public` mode keeps audiences read-only. Presenters authenticate through GitHub
Device Flow and must appear in `VSTD_ALLOWED_GITHUB`. Successful sign-in opens
the non-cacheable `/presentations` index so a public marketing homepage at `/`
cannot mask the authenticated presenter session.

When `VSTD_CONTENT_SYNC=1`, an authenticated presenter can use sticky-note,
direct-edit, companion, attachment, and pasted-image controls in the hosted
player. A save updates the running instance immediately. A background worker
then batches content-only commits to the configured Git repository and polls the
branch for remote changes.

The Git token is sent through an ephemeral authorization header; it is not
written into the remote URL, command arguments, or `.git/config`. Scope it to
Contents read/write on the content repository only.

To keep content saves from redeploying the application, configure Railway watch
paths around deployment and engine files rather than the deck content paths.
Video bytes remain outside Git and are synchronized through object storage.

Without content sync, `public` mode remains read-only. Audiences enter through
expiring deck-scoped links created by `vstd qr`.

### Team collaboration mode

Collaboration mode is additive and disabled by default. It requires public mode,
PostgreSQL, and separate app/player origins:

```yaml
app_host: https://studio.example.com
public_host: https://present.example.com
```

```sh
VSTD_MODE=public
VSTD_COLLABORATION=1
DATABASE_URL=postgresql://...
VSTD_APP_ORIGIN=https://studio.example.com
VSTD_PLAYER_ORIGIN=https://present.example.com
VSTD_OWNER_GITHUB_LOGIN=octocat
```

Startup runs additive schema migrations under a PostgreSQL advisory lock and
reconciles filesystem decks into the catalog. The first matching GitHub login
becomes the owner and claims previously unowned decks as private. The owner can
invite members from `/team`; invitation and password-reset email uses
`RESEND_API_KEY` and `RESEND_FROM`.

The owner signs in through a guided GitHub device flow. Vessica opens GitHub in
a new tab, keeps the temporary code visible with its remaining lifetime, and
copies it when the code is clicked. The in-progress flow is retained in tab
`sessionStorage`, so a reload or temporary network interruption does not force
the owner to race for or lose the code.

Each user owns their decks. Team visibility grants other active members View,
Present, export, and Fork access, but never Edit or sharing authority. Forks are
private copies with upstream hashes and provenance. Removing a member revokes
their sessions immediately and transfers their decks to the owner without
changing visibility.

The presentation directory adds one-level personal folders plus All, Mine,
Shared, and Recent views. Folder placement belongs only to the signed-in user:
moving a shared deck does not change its owner, visibility, team permissions, or
any teammate's organization. A permanent personal Trash folder keeps removed
presentations intact and out of the normal catalog views; moving them to another
folder restores them. Trash cannot be renamed, deleted, or emptied. Deleting a
regular folder returns its presentations to the root. Local serving persists the
same organization as an atomic mode-`0600` JSON file in the OS user-config
directory, keyed by the canonical studio root, so it never enters deck content
or Git.

Slide transfer requires Fork access to the source and Edit access to an owned
target. Copy creates a normal target-themed slide pair and content-deduplicated
copies of companion attachments. Link creates a target-themed, read-only live
snapshot; link-to-link chains are rejected. Player-grid selections cross to the
app picker through a five-minute single-use intent bound to the authenticated
user, carried only in the URL fragment. Retained linked snapshots intentionally
remain in the target after source access is revoked; detach them if that
continuity behavior is not appropriate for a particular deck.

The account cookie is host-only on the app origin and is never player
authorization. App launches exchange a 60-second, single-use handoff for a
12-hour deck/mode bearer kept in tab `sessionStorage`; every player request
revalidates membership, deck ownership, visibility, and mode. Native media uses
a separate player-host-only HttpOnly cookie accepted only by media handlers.
Keep the deployment at one application replica while its writable Git checkout
remains a single-writer process.

### Owner monitoring

Collaboration mode also enables an owner-only `/observability` workspace,
linked from the presentation catalog immediately above Documentation. It shows
7-, 30-, and 90-day audience summaries, named QR-chat participants, anonymous
privacy-preserving visitor labels, presentation and slide views, team activity,
sanitized HTTP 5xx routes, and model token usage from both direct OpenAI API
requests and Codex agent runs dispatched to Railway Sandboxes.

Audience and player requests never wait for analytics writes. Events enter a
bounded in-process queue and are flushed to PostgreSQL in small background
batches; if that queue reaches capacity, events are dropped and the count is
shown to the owner. Viewer records use a random first-party HttpOnly identifier.
The engine does not store viewer IP addresses, user agents, share tokens,
prompts, model responses, or error response bodies in observability data.
OpenAI API totals come from response usage and authenticated Realtime events.
Codex totals come from the privacy-safe model, session, and total-token summary
printed by each CLI run. Sandbox execution metadata records the disposable
sandbox identifier for attribution; the CLI does not report an input/output
split, so that breakdown is intentionally left blank in the dashboard.
The owner route and its JSON API re-check the permanent team-owner capability
on every request; ordinary team members and the player origin cannot access
them.

To roll back application behavior without discarding collaboration data,
disable `VSTD_COLLABORATION`, restore the former canonical `public_host`, and
redeploy. Migrations are additive and are not reversed.

See [`docs/THREAT_MODEL.md`](docs/THREAT_MODEL.md) for trust boundaries,
existing controls, and hardening recommendations.

## Architecture

| Package | Responsibility |
|---|---|
| `cmd/vstd` | CLI parsing and orchestration, including Railway operations |
| `internal/studio` | Configuration, deck/slide persistence, builds, forks, and export |
| `internal/library` | Shared image/video catalog types and manifest persistence |
| `internal/oai` | OpenAI authentication and API calls |
| `internal/video` | Video inspection, normalization, poster extraction, and registration |
| `internal/s3` | S3-compatible storage and request signing |
| `internal/server` | HTTP routes, modes, auth, editing, exports, audience tools, sync, and workers |
| `plugin` | Canonical agent skills and authoring conventions |

The CLI and server adapters depend on domain packages. The neutral studio and
library models do not depend on OpenAI, S3, Railway, or agent runtimes.

## Development and contributing

Issues and focused pull requests are welcome. Preserve the file contract and
public compatibility surfaces, keep credentials out of fixtures and logs, and
add tests for behavior changes.

Build and run the standard checks:

```sh
make build
go test ./...
go test -race ./...
go vet ./...
```

The full contributor guide, package ownership map, and validation contract are
in [`AGENTS.md`](AGENTS.md). Tests use local fixtures and do not require live
OpenAI, GitHub, Railway, Telnyx, Resend, or S3 credentials.

## License

[Apache License 2.0](LICENSE) © Vessica Labs
