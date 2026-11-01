# Decisions presentation controls: local validation

Pre-release validation recorded November 1, 2026 for v0.7.60.
The Cloud broker changes are a separate private-repository change; consuming them
requires the public-engine release and an explicit Cloud engine-lock update.

## Behavior exercised

- Explicit navigation and highlighting use native input-transcript Decisions
  triggers alongside the existing Responses delegation.
- Both arrival orders are covered: Decisions first and backend first. A correlated
  delayed agent call cannot execute a second relative page move.
- Current-slide state, explicit numeric destinations, presenter authority,
  corrections, stale results, low confidence, unknown choices and session teardown
  are checked. Failed fast execution leaves the backend able to finish the action.
- Mixed requests retain the existing web-search and detailed-thinking endpoints.
  The complete companion/context and meeting-brief paths remain in place.
- Older hosts, local Realtime and dictation retain their existing transport.
  A definitive provider rejection of Decisions access falls back without changing
  credentials or provider permissions.

## Automated checks

Engine: `go test ./...`, `go test -race ./...`, `go vet ./...`, CLI build,
formatting and diff hygiene passed during implementation. The final changed Studio
package was rechecked after adjustments. Checks used the available Go 1.26.5 with
`GOTOOLCHAIN=local` and a temporary build cache: the cached 1.26.10 toolchain selected
by `go.mod` was missing standard-library packages. Exact pinned-toolchain CI is
still required before release.

The Cloud non-PostgreSQL suite passed 795 tests (8 skipped) after installing its
checksum-verified existing engine pin. That run excluded the pre-existing
`supply-chain.test.ts` file, which expects engine 0.7.58 while the checked-in lock
specifies 0.7.59. A separate run confirmed that assertion fails without any changes
to the lock or supply-chain code. Focused voice tests, TypeScript type checking,
production build, changed-file lint/format checks, secret scan and Cloud architecture
lint passed. Database acceptance and hosted-browser deployment checks were not run.

The engine architecture lint has existing failures: the 937-line
`internal/server/pptx_capture.js` exceeds its file-size rule, and the scanner flags
an existing ignored `.env` file. Neither was changed by this feature.

## Live provider check

A short primary-WebSocket session used synthetic speech, native Decisions triggers
and a `gpt-6.1-sol` Responses backend with `gpt-live-1`. The provider accepted the
trigger configuration, emitted input transcripts and Decisions results, and the
engine dispatcher executed one `goto_slide` action. The later Responses tool call
was received and did not execute another move. The session closed successfully.
No presenter microphone, production Studio session or production deployment was used.

This was a functional smoke test, not a latency benchmark. Its one fast action
occurred about 2.85 seconds after the synthetic audio stream began, including
leading silence and speech playback. The initial 0.9 confidence cutoff prevented
fast execution despite correct classifications; observed native confidence led to
a preliminary 0.5 cutoff, alongside lexical, destination and state checks and a
200 ms correction window. A representative command corpus and real microphone
trials should determine production thresholds and median/tail latency claims.
