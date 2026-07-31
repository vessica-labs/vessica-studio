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

## Environment

```
OPENAI_API_KEY       image generation + realtime tokens
VSTD_PRESENTER_KEY   interim presenter auth for --mode public
VSTD_MODE, PORT      mode/port overrides (Railway sets PORT)
```

## Roadmap

- Presentation mode with gpt-realtime-2 (wake-word "Vessica", voice Q&A,
  auto-advance from talk-track cues, element highlighting)
- GitHub OAuth presenter auth; signed audience share links + QR; live-follow
- Dictation authoring (speak the talk track; slides form live)
- PDF / PPTX export; `vstd mcp`

## License

MIT © Vessica Labs
