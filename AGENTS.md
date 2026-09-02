# Vessica Studio contributor guide

This repository contains the `vstd` engine. It is not a deck-content
repository. If you are authoring slides in a studio created by `vstd init`, use
the downstream guide at `codex/AGENTS.md` and load the canonical workflow with
`vstd skill <name>`.

## Choose the applicable workflow

- For engine changes in this repository, follow this root guide.
- For deck-content work requested in this repository, stop and confirm the
  target: this is the engine repository, not a content repository.
- In a downstream content repository, follow its `AGENTS.md` (distributed from
  `codex/AGENTS.md`) and load the relevant `vstd skill <name>` workflow.
- For embedded workflow changes, edit `plugin/skills/*/SKILL.md` or
  `plugin/docs/conventions.md` first. Update packaging tests and thin downstream
  launchers only when their public contract changes.
- For Agent Harness work, read the source Linear ticket, then the relevant
  `.harness/*.md` source-of-truth documents and accepted ADRs before coding.

## Architecture

Vessica Studio is local-first: a deck is a directory of HTML slide fragments
paired with Markdown companions. The engine reads and writes that file contract;
the browser is a view and structured editing surface over it.

- `cmd/vstd`: CLI parsing and orchestration. Keep business rules in internal
  packages when they can be expressed independently of flags and terminal I/O.
- `internal/studio`: studio configuration, decks, slides, builds, forks, and
  static export. This package owns the on-disk content model.
- `internal/library`: shared image/video manifest types and persistence.
- `internal/oai`: OpenAI HTTP calls and key resolution. It may depend on the
  library domain, but the library and video packages must not depend on OpenAI.
- `internal/video`: local video inspection, normalization, poster extraction,
  and catalog registration.
- `internal/s3`: S3-compatible media storage and request signing.
- `internal/server`: HTTP routes, serving modes, auth, editing, audience
  interaction, external actions, exports, and background workers.
- `plugin`: the canonical embedded deck-authoring skills and conventions.
- `codex`: thin prompt launchers plus the agent guide installed into downstream
  content repositories.

Dependency direction should run from the CLI/server adapters toward these
domain packages. Do not move shared domain types into an external-service
adapter for convenience.

Agent Harness source-of-truth documents:

- Architecture: `.harness/ARCHITECTURE.md`
- Product and UI design: `.harness/DESIGN.md`
- Security: `.harness/SECURITY.md`
- Testing: `.harness/TESTING.md`
- Deployment and release: `.harness/DEPLOY.md`
- Accepted decisions: `.harness/adrs/INDEX.md`

## Invariants

- Preserve the file contract: `studio.yaml`, `deck.yaml`, and paired
  `decks/<deck>/slides/<id>.html` plus `<id>.md` files.
- A slide fragment contains one root `<section class="slide">`. Companion
  Markdown remains the evidence, intent, talk-track, and edit-log record.
- The player/HUD is engine-owned at `internal/studio/templates/player.html`.
  Themes contribute presentation styling, not alternate player implementations.
- `studio` mode may edit content. `present` and `public` are read-only content
  modes. Treat any change to authorization or route exposure as security work.
- Local-only commands and plugin workflows must continue to work without a
  Vessica Studio Cloud account or network connection.
- Cloud client code belongs in this public repository, but multi-tenant identity,
  billing, hosted execution, publication policy, and SaaS business rules belong
  only in the private `vessica-studio-cloud` service.
- Never put credentials in built decks, manifests, logs, fixtures, Git config,
  or commits. Preserve environment-first secret resolution and redact values in
  diagnostics. Native cloud credentials must use the OS credential store when
  available.
- `decks/*/build/`, `dist/`, local video bytes, request archives, and local
  Vessica/audience state are generated or runtime data. Do not hand-edit or
  commit generated output unless a fixture explicitly requires it.
- Public CLI commands, HTTP routes, YAML/JSON fields, cloud protocol messages,
  and the deck format are compatibility surfaces. Add tests and documentation
  for intentional changes.

## Agent workflows

`plugin/skills/*/SKILL.md` and `plugin/docs/conventions.md` are the single source
of truth for deck-authoring workflows. Agent integrations consume them through
the plugin or through thin launchers in `codex/prompts/` that call `vstd skill`.
Change the canonical files first and keep the packaging parity test green. Do not
duplicate full workflow instructions in launchers or README files.

The root `AGENTS.md` governs engine contributions. `codex/AGENTS.md` is a
distribution artifact for content repositories; do not expand it into an engine
architecture guide.

## Before editing

- Run `git status --short` and preserve existing user changes.
- Identify whether the change touches a public compatibility surface.
- Exclude generated and runtime paths from searches and edits unless working on
  an explicit fixture.
- Read the nearest relevant package tests before changing behavior.
- Confirm which public artifacts need to stay aligned: CLI help, README,
  templates, embedded skills, conventions, and downstream launchers.

## Development workflow

Prefer focused tests while iterating:

```sh
go test ./internal/studio
go test ./internal/server
go test ./internal/library
go test ./plugin
```

Use test-driven development for behavior changes and regressions. Tests should
exercise observable behavior with real files and handlers where practical.
Avoid source-text assertions unless the text itself is the public artifact.

Match verification to the change:

- Documentation-only: run `git diff --check`. Run package tests when the
  documentation is embedded, generated, or validated by tests.
- Focused code change: run the nearest package tests and formatting checks.
- Public API, CLI, server, cloud protocol, or deck-format change: run focused
  and relevant integration tests, update public documentation, then run the
  full gate.
- Broad refactor or release-risk change: run the full gate, including race tests.

The full gate is:

```sh
test -z "$(gofmt -l $(find cmd internal plugin -name '*.go' -type f))"
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/vstd
git diff --check
```

Optional tools are feature-specific: Chrome/Chromium powers PDF export, FFmpeg
and FFprobe power the full video pipeline, and the Railway CLI powers hosted
deployment. Core builds and unit tests must not require their network services.
When an optional dependency is unavailable, run the remaining applicable checks
and report the omitted verification explicitly.

## Change discipline

- Keep edits scoped; the larger CLI and server files are not permission for an
  unrelated rewrite.
- Preserve user changes in a dirty worktree and avoid destructive Git commands.
- Update `README.md` when user-facing setup, commands, dependencies, or behavior
  changes.
- Security reviews may identify changes without authorizing them. Do not turn a
  report-only recommendation into code or deployment configuration.
- Harness coder tickets run their focused checks and one ticket gate; lint and
  QA own deduplicated repository-wide and acceptance gates.

## Definition of done

- Observable behavior and public compatibility changes have focused tests.
- Applicable package checks and the full gate pass at the stage assigned by the
  checked-in Harness pipeline.
- CLI help, README, embedded plugin workflows, and compatibility documentation
  remain aligned.
- Credentials, generated output, and runtime Harness state are absent from the
  commit.
- User-facing cloud behavior preserves local-first operation and the public/private
  repository boundary.

## Escalation conditions

Stop for human direction before destructive compatibility changes, credential or
provider-permission changes, weakening local-only operation or security boundaries,
publishing a release, deploying a hosted environment, or merging a Harness-created
pull request without explicit authority.
