# Vessica Studio Architecture

- Status: `Production-capable public engine and plugin`
- Owner: `Matthew Kropp`
- Last verified: `2026-09-03`
- Scope: `Public vstd CLI, engine, embedded Codex plugin, local server, and cloud client`

## System Context

Vessica Studio is a local-first Go application for creating, building, serving,
presenting, and exporting HTML/Markdown presentation repositories. It also ships
the canonical Codex plugin used to author those repositories. The private
Vessica Studio Cloud service may be reached through versioned client contracts,
but the public engine remains usable without that service.

## Component Map

| Component | Responsibility | Owning Path | Public Interface |
| --- | --- | --- | --- |
| CLI adapter | Parse commands and coordinate domain services | `cmd/vstd` | `vstd` command and help text |
| Studio domain | File contract, builds, forks, releases, and export | `internal/studio` | Go package and generated artifacts |
| Local server | Studio/present/public modes and HTTP adapters | `internal/server` | Local/hosted HTTP routes |
| Media services | Library, video, OpenAI, and S3 adapters | `internal/library`, `internal/video`, `internal/oai`, `internal/s3` | Domain APIs and external provider calls |
| Player | Engine-owned presentation runtime | `internal/studio/templates/player.html` | Built presentation HTML |
| Plugin | Canonical authoring skills and conventions | `plugin` | Packaged Codex plugin |
| Downstream launchers | Thin workflow launchers for initialized studios | `codex` | `vstd skill` delegation |
| Cloud protocol | Versioned HTTP operations, capability negotiation, bounded responses, and typed failures | `internal/cloud` | Go client consumed by cloud domains |
| Cloud authentication | Device login, in-memory access tokens, refresh lifecycle, and OS credential storage | `internal/cloudauth` | Session manager and credential-store abstraction |
| Cloud workspace | Association state, canonical content projection, clone/connect/status/pull/sync orchestration | `internal/cloudworkspace`, `internal/studio/cloud_content.go` | `.vstd/cloud-workspace.json` and workspace operations |
| Cloud publication | Synchronized revision selection and publication orchestration | `internal/cloudpublish` | Publication create/status operations |

## Dependency Rules

- CLI and HTTP adapters depend on internal domain packages; domain packages do
  not depend on command-line or browser presentation concerns.
- `internal/oai` may depend on library types; library and video packages do not
  depend on OpenAI.
- The player is owned only by the engine. Themes provide styles, not alternate
  player implementations.
- `internal/cloudauth`, `internal/cloudworkspace`, and `internal/cloudpublish`
  depend on `internal/cloud`; workspace synchronization also depends on the
  studio-owned content projection. `internal/cloud` does not depend on those
  consumers, the CLI, the studio filesystem, Git, or `internal/server`.
- Cloud client code must not embed SaaS billing, tenant policy, hosted
  execution, or publication implementation.
- Plugin workflow bodies live under `plugin/`; `codex/prompts` stays thin.

## Critical Flows

1. Local authoring reads and writes `studio.yaml`, `deck.yaml`, and paired slide
   HTML/Markdown through `internal/studio`.
2. Build/export validates the file contract and emits generated output without
   changing source companions.
3. Server modes expose mutable studio routes only in `studio`; `present` and
   `public` remain read-only.
4. Cloud sync authenticates through a native-client flow, compares an explicit
   base revision, and exchanges the existing file contract over a versioned API.
5. Cloud publish selects an attributable revision and requests the private
   control plane to publish it; the public client never implements server policy.

## External Interfaces

| Interface | Direction | Contract | Failure Behavior |
| --- | --- | --- | --- |
| Filesystem | Local | Stable studio/deck/paired-slide contract | Validate before mutation; preserve sources on failure |
| OpenAI API | Outbound | Environment-resolved requests in `internal/oai` | Return actionable errors without exposing keys |
| S3-compatible storage | Outbound | Signed media operations in `internal/s3` | Fail closed and keep local content usable |
| Vessica Studio Cloud API | Outbound | Versioned native-client auth, sync, and publish protocol | Fail safely; retain local files and report unsynced state |
| Git | Local/remote | Repository history for content and source | Advanced mechanism; never require manual Git in the cloud happy path |

## Architectural Invariants

- The public repository is the sole owner of the file model, engine, player,
  export logic, CLI, and plugin.
- Local-only operation never requires cloud login or network access.
- Stale-base sync cannot overwrite a newer cloud revision.
- Native credentials are redacted and never stored in studio files or Git.
- Compatibility surfaces receive tests and documentation in the same change.
- Architecture lint preserves standard file-size and secret-file rules; exact
  pre-existing oversized files are grandfathered, not a precedent for new ones.

## Known Constraints

- `cmd/vstd/main.go`, three server files, and the player template predate the
  Harness size gate and are explicitly grandfathered. Changes should reduce or
  avoid increasing those files where practical.
- Chrome/Chromium, FFmpeg/FFprobe, Railway, OpenAI, and S3 are optional or
  feature-specific dependencies; the core test/build path stays offline-capable.
- The implemented Cloud protocol is exercised against deterministic fake
  services. Interoperability with the private service still depends on aligning
  its approved public schemas and minimum-version policy before release.

## Architecture Decision Records

The applicability and supersession index is [`.harness/adrs/INDEX.md`](./adrs/INDEX.md).
Read only accepted, non-superseded records that intersect the affected paths or
interfaces. Runtime-injected ADRs remain ignored; durable accepted records live
under `.harness/adrs/accepted/`.
