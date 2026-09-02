# Vessica Studio Product and UI Design

- Status: `Current public CLI, local web UI, player, and plugin guidance`
- Owner: `Matthew Kropp`
- Last verified: `2026-09-02`
- Scope: `Public/local user experience, including optional cloud-client commands`

## Product Design Principles

- Local-first: a user can author, build, serve, and export without an account.
- Companion-first: HTML expresses the slide and Markdown preserves intent,
  evidence, talk track, and edit history.
- Agent-native: Codex and the canonical plugin are the primary authoring surface.
- Hide infrastructure: use workspace, version, sync, conflict, and publish terms
  instead of requiring Git or Railway knowledge.
- Honest state: offline, unsynced, conflicted, failed, and published states are
  explicit and actionable.

## Target Users and Core Journeys

| User | Need | Core Journey | Success Signal |
| --- | --- | --- | --- |
| Local author | Build presentations with Codex | Initialize, edit paired files, build, serve, export | Complete work without cloud login |
| Cloud-connected author | Collaborate without manual Git | Log in, connect/clone, sync, resolve conflict, publish | No token or Git command in the happy path |
| Presenter | Deliver and share a stable deck | Serve locally or open a published URL | Predictable player and navigation |
| Plugin maintainer | Improve reusable authoring behavior | Update canonical skill, verify package parity | One workflow source remains canonical |

## Information Architecture

The CLI groups local studio operations, media/library operations, server/export
operations, plugin skill discovery, and optional `cloud` account/workspace flows.
The local browser UI remains an engine-owned view over files. Hosted account,
billing, team administration, job orchestration, and publication management UI
belong to the private cloud product.

## Interaction Patterns and States

- Commands print concise success output and actionable errors with non-zero exits.
- Destructive or conflict-resolving actions require explicit selection.
- Cloud status distinguishes local head, cloud head, dirty/unsynced files,
  offline state, protocol incompatibility, and credential expiry.
- Authentication uses a browser/device flow; secrets are never echoed.
- Local files remain usable after any network or cloud failure.

## Visual System

The player/HUD and local web UI are engine-owned templates and styles. Themes
control presentation styling only. CLI output should remain readable in plain
terminals and must not rely on color alone.

## Responsive Design and Accessibility

Browser surfaces support desktop presentation/editing and narrow control views.
Keyboard operation, visible focus, semantic labels, reduced-motion preferences,
and sufficient contrast are required. CLI state must also be available as text.

## Design Validation

For CLI journeys, test help text, success/error output, offline behavior, and
credential redaction. For browser/player changes, inspect representative
viewports and keyboard flows. Plugin changes require packaging/parity tests and
must continue to source canonical workflows from the installed public `vstd`.
