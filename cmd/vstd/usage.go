package main

import "fmt"

func usage() {
	fmt.Print(`vstd — Vessica Studio engine

Usage:
  vstd init [dir]                     scaffold a studio content root
  vstd new <deck> [--title T]         create a deck
  vstd list                           list decks
  vstd fork <deck> <client>           fork a deck for a client (deck--client)
  vstd diff-upstream <fork>           slides changed upstream since fork
  vstd build <deck>|--all             assemble build/index.html
  vstd release-build [deck] --output DIR
                                      emit immutable hosted-release artifacts + manifest
  vstd image-delivery --input FILE --output FILE
                                      create a source-preserving display WebP + JSON receipt
  vstd delivery-resources --output DIR export trusted shared engine/theme resources
  vstd agent                          run a redesign sweep (--deck NAME --slide ID to scope)
  vstd editor-session [flags]       isolated gateway-authenticated editor transport
  vstd editor-transform             bounded snapshot/render/visual-edit JSON transform
  vstd serve [deck] [flags]           serve studio (watch, live reload, edit API)
  vstd asset gen --prompt P [flags]   generate a library image (gpt-image-2)
  vstd asset list|find [--tags a,b --family F]   browse/reuse the library
  vstd asset add-video <file> [--slug S --tags a,b --no-transcode]
                                      ingest a video (normalize, poster, manifest)
  vstd asset add-bundle <zip> --slug ID [--entrypoint index.html --poster img/demo.jpg]
  vstd asset inspect-bundle <zip> [--entrypoint index.html --json]
  vstd asset push|pull                sync video bytes with the S3 bucket
  vstd chart promote-text <deck> <slide> [--dry-run]
                                      promote inline SVG text to editable overlays
  vstd bundle <deck>                  self-contained folder export (videos included)
  vstd key check                      verify OpenAI key resolution
  vstd qr <deck> [--ttl 72] [--host U]  mint a signed audience share link + QR
  vstd skill [name]                   print an agent skill (deck-new, slide-edit, …);
                                      no name lists all — canonical workflow
                                      instructions for any agent (Claude, Codex)
  vstd railway up                     one-command Railway setup + deploy
  vstd railway status|<args>          linked project info / CLI passthrough
  vstd cloud login|logout|account     pair and inspect a Cloud account
  vstd cloud presentation <command>   list, open, or create a presentation for agent work
  vstd cloud workspace <command>      list, create, clone, connect, status, pull, or sync
  vstd cloud publish create|status    publish or inspect a synchronized revision
  vstd cloud diagnostics              show sanitized protocol information
  vstd worktree begin|finish          isolate agent edits and reconcile automatically
  vstd reconcile                     merge three checkpoint snapshots from JSON stdin
  vstd version

Serve flags:
  --root DIR      studio root (default ".")
  --port N        (default from studio.yaml / PORT env, 4400)
  --mode M        studio | present | public   (default studio)
  --agent         run the optional redesign worker alongside the server

Asset gen flags:
  --root DIR  --prompt P  --family F  --tags a,b  --size 1024x1024  --slug S  --dry-run

Environment:
  OPENAI_API_KEY        image generation + realtime token minting
  VSTD_SECRET           session + share-link signing in public mode
  VSTD_GITHUB_CLIENT_ID / VSTD_ALLOWED_GITHUB
                        GitHub Device Flow presenter authentication
  VSTD_COLLABORATION=1   PostgreSQL-backed single-team collaboration
  DATABASE_URL           required PostgreSQL URL for collaboration
  VSTD_APP_ORIGIN / VSTD_PLAYER_ORIGIN
                        separate HTTPS app and executable-player origins
  VSTD_OWNER_GITHUB_LOGIN
                        GitHub login that bootstraps/administers the team
  VSTD_CONTENT_SYNC=1   enable presenter-only hosted content editing
  VSTD_GIT_REPO / _BRANCH / _TOKEN
                        content repository and scoped write credential
  VSTD_AGENT=1          enable the optional headless redesign worker
  VSTD_CLOUD_ENDPOINT  Vessica Studio Cloud API endpoint
  VSTD_EDITOR_TOKEN     gateway-only credential for editor-session (never browser-visible)
  VSTD_CHROMIUM         Chrome/Chromium binary for visual export/migration
  PORT                  overrides port (Railway sets this)
  VSTD_S3_ENDPOINT / _BUCKET / _ACCESS_KEY / _SECRET_KEY / _REGION
                        S3-compatible bucket for video assets (Railway
                        Storage Bucket); or studio.yaml storage: block
`)
}
