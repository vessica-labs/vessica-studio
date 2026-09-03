# Vessica Studio Testing

- Status: `Current repository verification contract`
- Owner: `Matthew Kropp`
- Last verified: `2026-09-03`
- Scope: `Go engine, HTTP server, CLI, plugin packaging, media adapters, and optional cloud client`

## Testing Objectives

Protect the paired-file contract, public CLI/API compatibility, server-mode
authorization, deterministic builds/exports, credential handling, plugin parity,
and local-first behavior when optional services are absent.

## Test Layers

| Layer | Purpose | Location | Required For |
| --- | --- | --- | --- |
| Package tests | Domain and adapter behavior with real temporary files/handlers | `internal/**`, `cmd/**` | Every behavior change |
| Plugin tests | Embedded skill/package parity | `plugin` | Plugin, convention, launcher changes |
| Integration tests | Cross-package CLI/server and external-adapter contracts | Package `_test.go` files and fixtures | Public routes, CLI, file format, cloud protocol |
| Race checks | Concurrency and shared-state safety | All Go packages | Broad, server, worker, or release-risk changes |

## Commands

| Check | Command | Expected Result |
| --- | --- | --- |
| Focused studio | `go test ./internal/studio` | Pass |
| Focused server | `go test ./internal/server` | Pass |
| Focused library | `go test ./internal/library` | Pass |
| Plugin parity | `go test ./plugin` | Pass |
| Cloud protocol | `go test ./internal/cloud` | Pass |
| Cloud authentication | `go test ./internal/cloudauth` | Pass |
| Cloud content and workspace | `go test ./internal/studio ./internal/cloudworkspace` | Pass |
| Cloud publication | `go test ./internal/cloudpublish` | Pass |
| Cloud CLI | `go test ./cmd/vstd` | Pass |
| Format | `test -z "$(gofmt -l $(find cmd internal plugin -name '*.go' -type f))"` | No output |
| Full tests | `go test ./...` | Pass |
| Race tests | `go test -race ./...` | Pass |
| Vet | `go vet ./...` | Pass |
| Build | `go build ./cmd/vstd` | Pass |
| Architecture | `python3 .harness/scripts/arch-lint.py` | Pass |
| Diff hygiene | `git diff --check` | Pass |

## Requirements by Change Type

- Bug: add a failing observable regression test, implement the fix, then run the
  nearest package gate.
- CLI/API/file-format/cloud-protocol feature: focused tests, compatibility docs,
  affected integration tests, and the full gate.
- Plugin workflow: update canonical `plugin` source first and run plugin parity.
- Security, server, concurrency, or release-risk change: include race tests.
- Documentation-only: `git diff --check`; run package tests if documentation is
  embedded or contract-tested.

Ticket plans separate fast `iteration_checks`, a one-time affected-package
`ticket_gate`, and downstream `pipeline_gates`. Coder agents do not rerun the
whole repository or browser suites unless their ticket owns that boundary; lint
and QA deduplicate the wider gates.

## Test Data and Dependencies

Use temporary directories, `httptest`, deterministic clocks/IDs, fake remote
servers, and test credential stores. Core tests require no live network,
credentials, Railway, OpenAI, S3, browser, or FFmpeg service. Optional tool
coverage is reported explicitly when unavailable.

The generic Agent Harness checkpoint does not include Go. Before the coder
stage, `.harness/scripts/ensure-go.sh` installs the pinned official Go archive
when necessary, verifies its published SHA-256 checksum, and exposes `go` and
`gofmt` through `/usr/local/bin`. Existing developer installations are reused.

## Determinism and Flake Policy

Tests must isolate ports, paths, environment, credentials, and mutable state.
Do not mask flakes with retries. Diagnose the cause before repeating the same
unchanged command/failure more than twice. Playwright work, if introduced, uses
`HARNESS_PLAYWRIGHT_WORKERS` for sandbox-safe concurrency.

## CI Gates

Formatting, `go test ./...`, `go test -race ./...`, `go vet ./...`, CLI build,
architecture lint, and diff hygiene block completion for public compatibility or
release-risk changes. Focused ticket checks may run earlier; the pipeline owns
the deduplicated final gates.

## Required Evidence

Record exact commands, pass/fail results, relevant fixtures, omitted optional
checks, compatibility impact, and residual risks. Cloud-client acceptance
evidence must include login/logout, clone/connect, offline edit, sync, conflict,
publish, credential revocation/redaction, and local-only regressions.

PR-30 regression coverage also exercises endpoint-scoped credentials, endpoint
mismatch rejection, arbitrary remote error-code redaction, read-capability
negotiation, remote-digest connect semantics, explicit conflict-head resolution,
paired-slide validation, symlinked destinations, unreadable existing trees,
credential-command rejection, and recovery after a simulated interrupted pull.
