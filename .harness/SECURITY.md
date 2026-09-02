# Vessica Studio Security

- Status: `Current public engine security contract`
- Owner: `Matthew Kropp`
- Last verified: `2026-09-02`
- Scope: `Local files, HTTP modes, native cloud credentials, provider adapters, and built presentations`

## Security Scope

Protect local presentation sources, companion evidence, media, provider
credentials, cloud session credentials, and audience data. The private cloud
service owns server-side tenant isolation, billing, hosted sandboxing, and
publication policy; this client must not duplicate or weaken those controls.

## Data Classification

| Data Class | Examples | Storage | Handling Rules |
| --- | --- | --- | --- |
| Public | CLI help, plugin instructions, released source | Repository/package | Review compatibility and provenance |
| User content | HTML/Markdown, media manifests, exports | User-selected local repository | Preserve paths and permissions; do not upload implicitly |
| Sensitive content | Sources, notes, audience responses | Local repository/runtime state | Exclude from public builds unless deliberately selected |
| Secret | API keys, OAuth tokens, refresh credentials | Environment or OS credential store | Never log, commit, place in Git config, or serialize into studio files |

## Trust Boundaries

Trust boundaries exist at local HTTP routes, browser/player content, filesystem
paths and symlinks, external provider calls, Git remotes, and the versioned cloud
API. Presentation HTML is untrusted executable content and must not gain app or
native-client credentials.

## Authentication and Authorization

- `studio` routes may mutate; `present` and `public` content modes remain read-only.
- Native cloud login uses short-lived access plus revocable refresh/session
  handling suitable for an installed CLI.
- Cloud authorization decisions are enforced server-side; the client treats
  workspace and revision identifiers as untrusted selectors.
- Logout removes local credentials without modifying presentation files.

## Secrets and Configuration

Use environment-first key resolution for provider adapters and the OS credential
store for cloud credentials where supported. Tests use deterministic fake stores.
Diagnostics, errors, fixtures, builds, manifests, Git configuration, and shell
commands must not contain secret values.

## Secure Input and Output Handling

Validate and confine filesystem paths; reject traversal and unsafe symlinks.
Avoid shell interpolation for external commands. Validate remote URLs, protocol
versions, response sizes, and content types. Encode browser output for its
context and keep executable presentation content isolated from privileged state.

## Dependencies and Supply Chain

Use Go modules and pinned GitHub Actions from reviewed sources. Release artifacts
must map to source tags/commits and pass the full gate. Cloud protocol additions
must not introduce private source or credentials into public packages.

## Security Verification

- Run package tests for path confinement, auth modes, credential resolution, and
  redaction near affected code.
- Run `go test ./...`, `go test -race ./...`, `go vet ./...`, the architecture
  lint, and `git diff --check` for public security or protocol changes.
- Cloud-client work requires fake-cloud, credential-store, stale-base/conflict,
  offline, and leak-scan coverage.

## Escalation and Reporting

Stop and preserve evidence for exposed credentials, cross-workspace access,
path escape, writable public/present routes, presentation-to-app credential
access, destructive compatibility changes, or provider permission changes.
