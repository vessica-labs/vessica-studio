# Decisions presentation controls: validation

Validation notes for the v0.7.62 transcript-chunk and destination-grounding repair, November 1, 2026.
Cloud consumes this public-engine release through a separate verified engine pin.

## Hosted regression

An authenticated hosted WebRTC session accepted Decisions triggers alongside the
existing Responses delegation. Synthetic speech navigated once, highlighted the
requested element and left Vessica asleep. The thinking and web-search endpoints
both returned successful results. Session closure returned HTTP 204.

That v0.7.60 check also exposed a fast-path gap: the backend completed the controls,
while the dispatcher discarded useful native predictions across transcript chunks.
The provider emitted ` Next` with the correct next-page choice at confidence 0.56,
then ` slide` with a different choice at 0.28. A complete exact highlight target
received confidence 0.37 and fell back to the backend.

The dispatcher now keeps at most 32 bounded predictions within one utterance and
rechecks them against its accumulated words. Navigation below 0.5 confidence requires a complete literal next/previous/back or
numeric-page command and a matching bounded destination. Ambiguous and narrative
requests still require the original confidence floor. Absolute destination checks
remain enforced. Highlight confidence from 0.25 to 0.5 is
usable only when the complete spoken label exactly matches the selected bounded
element. Earlier highlight predictions also require this exact grounding.
Ambiguous, partial and very low-confidence choices retain the backend fallback.

A second hosted check of v0.7.61 confirmed native highlighting, thinking and
research, but correctly classified next-page choices at confidence 0.44 still fell
back to the backend. A local candidate tested over the hosted connection produced
both native control receipts while remaining asleep and suppressing duplicates.
The v0.7.62 regression covers this trace and rejects ambiguous and narrative
navigation at the same confidence. Deployed acceptance remains required.

## Automated coverage

The Node-backed Studio tests replay the hosted chunk sequence and verify that a
partial command waits, a useful earlier result survives a later irrelevant result,
and the corresponding delayed backend move is suppressed. They also cover exact
highlight grounding, partial and ambiguous targets, correction, expiry and results
from older utterances.

Existing tests cover both action arrival orders, current page/element revisions,
explicit numeric destinations, presenter authority, unknown choices, session
teardown, failed fast execution and preserved research/thinking routes. Local
Realtime, dictation and hosts without Decisions retain their existing transport.

The release gate includes formatting, `go test ./...`, `go test -race ./...`,
`go vet ./...`, the CLI build and diff hygiene using the pinned Go toolchain.
Cloud separately verifies its released binary checksums, API and database tests,
clean-clone behavior and both runtime container images.

## Hosted acceptance after rollout

Verify the exact engine and Cloud revisions and every production service before
starting the authenticated player. Use synthetic audio and an existing presenter
session, with no content edits. Require observed native control receipts for both
navigation and highlighting, confirm one page move after delayed backend calls,
and check that Vessica remains asleep. Exercise the existing thinking and research
endpoints, then confirm voice closure and reservation settlement.

This is functional acceptance. A representative command corpus and real microphone
trials are still needed for confidence calibration and median/tail latency claims.
The thresholds above are preliminary and do not establish a latency benchmark.
