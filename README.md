# Vessica Studio

Vessica Studio is a local-first, agent-driven presentation engine. A deck is a
directory of plain HTML slides paired with Markdown research and talk tracks.
Claude or Codex can work directly on those files while `vstd` builds the deck,
serves a live-reloading player, manages media, and runs presentation mode.

The result is a presentation workflow that is inspectable, diffable, forkable,
and deployable with ordinary files and Git—without locking the deck inside a
proprietary editor.

Part of [Vessica Labs](https://github.com/vessica-labs). Licensed under MIT.

## What it does

- **File-native authoring:** every slide is an HTML fragment with a companion
  Markdown file for intent, evidence, sources, visual direction, and talk track.
- **Agent workflows:** one canonical set of deck workflows is packaged for
  Claude and exposed to Codex through `vstd skill`.
- **Live studio:** rebuilds on file changes, reloads connected browsers, and
  provides a structured edit API with stale-write protection.
- **Presentation runtime:** an engine-owned player supplies navigation, HUD,
  editing controls, live follow, and OpenAI Realtime integration across themes.
- **Reusable media:** generate images, upload or normalize video, maintain a
  shared asset manifest, and sync large video bytes to S3-compatible storage.
- **Audience interaction:** signed deck links, QR entry, per-person web chat,
  live audience pulse, and optional Telnyx/Resend actions.
- **Portable output:** build static deck HTML, export PDF through headless
  Chrome, or create a self-contained folder bundle with referenced media.
- **Hosted presentation mode:** deploy a presenter/audience surface to Railway
  with GitHub sign-in, presenter-only Git-backed editing, and expiring
  deck-scoped share links.

## Requirements

- [Go 1.22 or newer](https://go.dev/doc/install)
- Optional: Chrome or Chromium for PDF export
- Optional: FFmpeg and FFprobe for normalized video and poster extraction
- Optional: an OpenAI API key for generated images, Realtime, audience chat,
  web search, and code-interpreter features
- Optional: Railway CLI and an S3-compatible bucket for hosted deployments with
  video

## Install

Install the latest released command with Go:

```sh
go install github.com/vessica-labs/vessica-studio/cmd/vstd@latest
```

Or build from source:

```sh
git clone https://github.com/vessica-labs/vessica-studio.git
cd vessica-studio
make build
./vstd version
```

Ensure `$(go env GOPATH)/bin` is on your `PATH` when using `go install`.

## Quick start

```sh
vstd init my-studio
cd my-studio
vstd new product-story --title "Our Product Story"
vstd serve product-story
```

Open [http://localhost:4400](http://localhost:4400). Edit the paired files under
`decks/product-story/slides/`; the server watches the content tree and reloads
the presentation.

To build without starting a server:

```sh
vstd build product-story
```

The generated deck is written to
`decks/product-story/build/index.html`. Build output is disposable and should
not be edited by hand.

## The file contract

```text
my-studio/
├── studio.yaml                    # theme, port, OpenAI, and storage settings
├── themes/
│   └── default/
│       ├── theme.css              # presentation styling
│       └── tokens.json            # design tokens
├── library/
│   ├── manifest.json              # image and video catalog
│   ├── img/                       # generated or uploaded images
│   ├── video/                     # large local video bytes; normally ignored
│   └── video-posters/             # generated poster frames
├── requests/                      # asynchronous image/video request queue
└── decks/
    └── product-story/
        ├── deck.yaml              # title, theme, visibility, fork provenance
        ├── deck.css               # deck-specific style overrides
        └── slides/
            ├── 0010-cover.html    # one root <section class="slide">
            └── 0010-cover.md      # evidence, sources, talk track, and edit log
```

The companion is part of the slide contract, not optional documentation. Agent
workflows read it before changing a slide and update it with every completed
edit. The talk track also acts as the presenter agent's cue sheet.

The player and HUD are embedded in the engine. Themes control presentation
styling through `theme.css`; they do not provide alternate player markup.

## Architecture

The engine keeps the file model separate from delivery and provider adapters:

| Package | Responsibility |
|---|---|
| `cmd/vstd` | CLI parsing and orchestration, including Railway operations |
| `internal/studio` | Studio configuration, decks, slides, builds, forks, and export |
| `internal/library` | Shared image/video catalog types and manifest persistence |
| `internal/oai` | OpenAI authentication, image generation, and Realtime token calls |
| `internal/video` | Video inspection, normalization, poster extraction, and catalog registration |
| `internal/s3` | S3-compatible storage, signing, upload, and download |
| `internal/server` | HTTP routes, serving modes, auth, live events, audience tools, and workers |
| `plugin` | Canonical embedded agent workflows and authoring conventions |

The CLI and server orchestrate domain packages. External-service adapters may
depend on the neutral studio and library models; those models do not depend on
OpenAI, S3, Railway, or agent runtimes.

## Commands

| Command | Purpose |
|---|---|
| `vstd init [dir]` | Scaffold a studio root and default theme |
| `vstd new <deck> [--title T]` | Create a deck with a starter slide |
| `vstd list` | List decks and slide counts |
| `vstd fork <deck> <client>` | Create a client fork with provenance |
| `vstd diff-upstream <fork>` | Show parent changes since a fork was created |
| `vstd build <deck>` / `--all` | Build one deck or every deck |
| `vstd agent` | Run one optional headless redesign-queue sweep |
| `vstd serve [deck]` | Serve, watch, live reload, and expose the edit surface |
| `vstd asset gen` | Generate and catalog an image |
| `vstd asset list\|find` | Browse the shared asset library |
| `vstd asset add-video` | Normalize, posterize, hash, and catalog a video |
| `vstd asset push\|pull` | Synchronize video bytes with object storage |
| `vstd bundle <deck>` | Export a portable folder with referenced media |
| `vstd qr <deck>` | Mint an expiring audience link and QR image |
| `vstd skill [name]` | Print canonical authoring workflows |
| `vstd railway up` | Configure and deploy a content repository to Railway |

Run `vstd help` for flags and `vstd skill` for the installed workflow list.

## Serving modes

| Mode | Intended use | Content writes | Access model |
|---|---|---:|---|
| `studio` | Local authoring | Yes | Local machine owner is presenter |
| `present` | Local presenting | No | Local machine owner is presenter |
| `public` | Hosted presenting | No | GitHub allowlisted presenter or signed audience share |

Select a mode with `vstd serve --mode studio|present|public` or `VSTD_MODE`.
Do not expose `studio` mode directly to an untrusted network; its edit and
upload routes intentionally trust the local operator.

## Use with Claude

The repository ships a Claude plugin containing six workflows: `deck-new`,
`deck-fork`, `deck-review`, `market-refresh`, `slide-add`, and `slide-edit`.

```text
/plugin marketplace add vessica-labs/vessica-studio
/plugin install vessica-studio@vessica-studio
```

In Claude Code or Cowork, ask naturally for the matching deck task or invoke a
workflow directly, such as `/vessica-studio:deck-new`.

## Use with Codex

Install the thin Codex prompt launchers:

```sh
git clone https://github.com/vessica-labs/vessica-studio.git
./vessica-studio/codex/install.sh
```

The launchers call `vstd skill <name>`, so Claude and Codex read the same
embedded workflow rather than maintaining duplicate instructions. Copy
[`codex/AGENTS.md`](codex/AGENTS.md) into a studio content repository for
deck-authoring guidance.

Contributors changing this engine should follow the root
[`AGENTS.md`](AGENTS.md), which maps package ownership and validation rules.

## Configuration

A new studio starts with:

```yaml
theme_default: default
port: 4400
openai:
  image_model: gpt-image-2
  realtime_model: gpt-realtime-2
  base_url: https://api.openai.com/v1
```

OpenAI key resolution is:

1. `OPENAI_API_KEY`
2. `VSTD_OPENAI_KEY`
3. `openai.api_key_cmd` from `studio.yaml`

For example, store a key in macOS Keychain and resolve it at runtime:

```sh
security add-generic-password -U -s vessica-openai -a "$USER" -w 'sk-...'
```

```yaml
openai:
  api_key_cmd: security find-generic-password -s vessica-openai -w
```

`vstd key check` confirms that a key resolves without printing it.

Important hosted settings include:

| Setting | Purpose |
|---|---|
| `VSTD_SECRET` | HMAC secret for presenter sessions and audience share links |
| `VSTD_GITHUB_CLIENT_ID` | GitHub OAuth application with Device Flow enabled |
| `VSTD_ALLOWED_GITHUB` | Comma-separated presenter login allowlist |
| `VSTD_CONTENT_SYNC` | Set to `1` to allow authenticated presenters to edit hosted content |
| `VSTD_GIT_REPO`, `VSTD_GIT_BRANCH`, `VSTD_GIT_TOKEN` | Content repository, branch, and repository-scoped write token for hosted sync |
| `VSTD_GIT_DEBOUNCE_SECONDS`, `VSTD_GIT_POLL_SECONDS` | Optional hosted push batching and remote polling intervals |
| `VSTD_AGENT`, `VSTD_AGENT_CMD` | Enable the optional headless redesign worker; defaults to `claude`, or use `codex` with `OPENAI_API_KEY` |
| `VSTD_S3_ENDPOINT`, `VSTD_S3_BUCKET`, `VSTD_S3_ACCESS_KEY`, `VSTD_S3_SECRET_KEY`, `VSTD_S3_REGION` | S3-compatible video storage |
| `PUBLIC_URL` | Public base URL used by audience and call links |
| `TELNYX_API_KEY`, `TELNYX_FROM_NUMBER`, `TELNYX_CONNECTION_ID` | Optional SMS and call actions |
| `RESEND_API_KEY`, `RESEND_FROM` | Optional email actions |

Storage endpoint, bucket, region, and credential-command fallbacks can also be
set in the `storage` block of `studio.yaml`; environment variables take
precedence.

## Public hosting

`public` mode keeps audiences read-only. Presenters authenticate through GitHub
Device Flow and must appear in `VSTD_ALLOWED_GITHUB`. When
`VSTD_CONTENT_SYNC=1`, an authenticated presenter can use the same sticky-note
and direct-edit controls as a local studio: saves update the running instance
immediately, then a background worker batches and pushes content-only commits
to `VSTD_GIT_REPO`. It also polls the configured branch for remote changes.

The Git token is supplied to Git through an ephemeral authorization header; it
is not written into the remote URL, command arguments, or `.git/config`. Scope
it to Contents read/write on the content repository only. Configure Railway
watch paths so content commits do not rebuild the service; keep deployment
files such as `Dockerfile` and `railway.json` as the rebuild triggers.

Without content sync, `public` mode remains read-only. Audiences always enter
through expiring, deck-scoped signed links. `vstd railway up` assists a content
repository that already contains its deployment files, sets the core Railway
variables, deploys it, and records the assigned public host.

Video bytes are intentionally kept out of Git. Use `vstd asset push` to place
them in configured S3-compatible storage before relying on hosted playback.

## Development

```sh
make build
go test ./...
go test -race ./...
go vet ./...
```

The complete contributor validation contract and package map are in
[`AGENTS.md`](AGENTS.md). Tests use local fixtures and do not require live
OpenAI, GitHub, Railway, Telnyx, Resend, or S3 credentials.

## Security

Vessica Studio crosses several trust boundaries when public hosting, audience
channels, external actions, or a headless agent are enabled. Local authoring is
the default; enable only the integrations you need, keep secrets out of content
repositories, and place public deployments behind TLS.

The current controls, assumptions, and prioritized hardening recommendations
are documented in [`docs/THREAT_MODEL.md`](docs/THREAT_MODEL.md). That document
distinguishes existing protections from proposed work.

## Contributing

Issues and focused pull requests are welcome. Preserve the file-based deck
contract and public compatibility surfaces, add tests for behavior changes, and
run the full validation gate before opening a pull request.

## License

[MIT](LICENSE) © Vessica Labs
