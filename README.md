# Vessica Studio

**A local-first, agent-driven presentation engine.** Decks are directories of
HTML slide fragments, each paired with a companion markdown file carrying the
research, evidence, and talk track behind the slide. AI agents (Claude) write
the files; `vstd` builds, serves, watches, generates imagery, and hosts
presentation mode.

Part of the [Vessica Labs](https://github.com/vessica-labs) family.

## Why files?

The file system is the contract. Agents edit plain files (locally or through
a git repo); the engine watches and rebuilds; the browser live-reloads. No
plugin API to version, no database — a deck is a folder you can diff, fork,
and `git push` to production.

## Install

```
go install github.com/vessica-labs/vessica-studio/cmd/vstd@latest
```

## Quick start

```
vstd init my-studio && cd my-studio
vstd new my-deck --title "My Presentation"
vstd serve                      # http://localhost:4400
```

## Content model

```
studio-root/
├── studio.yaml                 # config (theme default, port, OpenAI models)
├── themes/<name>/              # theme.css + player.html + tokens.json
├── library/                    # shared generated-image library + manifest.json
├── requests/                   # async asset-generation queue (yaml files)
└── decks/<deck>/
    ├── deck.yaml               # title, theme, visibility, fork provenance
    ├── deck.css                # deck-specific overrides
    └── slides/
        ├── 0010-cover.html     # ONE <section> fragment per slide
        └── 0010-cover.md       # companion: Intent / Key ideas / Evidence &
                                #   sources / Talk track / Visual direction / Log
```

**The companion contract:** an agent never edits a slide's HTML without
reading its companion first, and never finishes an edit without updating it.
The Talk track section doubles as the presenter agent's cue sheet.

## Commands

```
vstd init [dir]                  scaffold a studio root (embedded default theme)
vstd new <deck> [--title T]      create a deck
vstd list                        list decks
vstd fork <deck> <client>        fork for a client → decks/<deck>--<client>
vstd diff-upstream <fork>        slides changed in the parent since fork time
vstd build <deck>|--all          assemble decks/<deck>/build/index.html
vstd serve [deck] [--mode M]     serve + watch + live reload + edit API
vstd asset gen --prompt P ...    generate a library image (gpt-image-2)
```

## Serving modes

| mode | use | capabilities |
|---|---|---|
| `studio` (default) | local authoring | everything: edit API, asset queue, watch |
| `present` | local presenting | read-only content + realtime token minting |
| `public` | hosted (Railway) | read-only; tokens require presenter auth |

## HTTP API (structured edit surface)

```
GET  /api/decks
GET  /api/deck/{deck}/slide/{id}                      → {fragment, companion}
PUT  /api/deck/{deck}/slide/{id}/fragment             (studio)
PUT  /api/deck/{deck}/slide/{id}/companion/{section}  (studio)
PUT  /api/deck/{deck}/slide/{id}/title                (studio)
POST /api/deck/{deck}/slides                          (studio) {id,title,html?}
GET  /api/events                                      SSE: reload
POST /api/realtime/token                              ephemeral gpt-realtime secret
```

The player's edit mode saves through this API when served; opened as a plain
file it falls back to downloading the edited deck.

## Images

`vstd asset gen` (or a yaml file dropped in `requests/`) calls the OpenAI
images API, applies the style-family prompt prefix from
`library/manifest.json` (what keeps icon 30 looking like icon 3), stores the
PNG in `library/img/`, and records it in the manifest. The API key is read
from `OPENAI_API_KEY` and never leaves the engine.

## API key configuration

Resolution order: `OPENAI_API_KEY` env → `VSTD_OPENAI_KEY` env → the
`api_key_cmd` shell command from studio.yaml. Recommended on macOS — store
the key in Keychain once:

```
security add-generic-password -U -s vessica-openai -a $USER -w 'sk-...'
```

then in `studio.yaml`:

```yaml
openai:
  api_key_cmd: security find-generic-password -s vessica-openai -w
```

`vstd key check` verifies resolution without printing the key. The key never
appears in built decks, server responses, or logs.

## Environment

```
OPENAI_API_KEY          image generation + realtime tokens (or api_key_cmd)
VSTD_SECRET             HMAC secret: sessions + share links (public mode)
VSTD_GITHUB_CLIENT_ID   GitHub OAuth app (device flow) for presenter sign-in
VSTD_ALLOWED_GITHUB     comma-separated presenter GitHub logins
VSTD_MODE, PORT         mode/port overrides (Railway sets PORT)
```

## Public mode (hosted)

`--mode public` serves decks read-only for a hosted instance (Railway):
presenter sign-in via GitHub Device Flow (`VSTD_GITHUB_CLIENT_ID` +
`VSTD_ALLOWED_GITHUB` allowlist — public client ID only, no secret), signed
deck-scoped audience share links (`vstd qr <deck> --ttl 72 --host URL`, HMAC
`VSTD_SECRET`), gated library assets, presenter-only rate-limited realtime
tokens, and live-follow (audience browsers track the presenter's slide over
SSE, with break-off/rejoin). See a content repo's DEPLOY.md for Railway setup.

## Roadmap

- Presentation mode with gpt-realtime-2 (wake-word "Vessica", voice Q&A,
  auto-advance from talk-track cues, element highlighting)
- Dictation authoring (speak the talk track; slides form live)
- PDF / PPTX export; `vstd mcp`

## License

MIT © Vessica Labs
