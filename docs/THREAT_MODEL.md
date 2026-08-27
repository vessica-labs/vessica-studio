# Vessica Studio Threat Model

Status: updated 2026-08-26 following a source review of `vessica-studio`, a
review of the `vessica-studio-mattkropp` implementation, and non-destructive
inspection of the associated Railway deployment. This document records the
intended architecture, current controls, residual risks, and remediation work.
It is not a substitute for an authorized external penetration test.

## Scope and product assumptions

This review covers the `vstd` engine, studio content repositories, the browser
player, collaboration and public hosting, the Codex redesign worker, Railway
Sandbox execution, OpenAI APIs, GitHub authentication and content sync,
Telnyx, Resend, S3-compatible storage, PostgreSQL, headless Chrome,
LibreOffice, FFmpeg, and Git.

Vessica Studio is intentionally a presentation publishing platform. Decks may
be publicly shareable, public links may be forwarded, and public audience
members are expected to load presentation HTML and referenced media. Public
availability of a deck explicitly classified and published as public is not a
confidentiality failure.

That product choice does not make every resource public. The security boundary
must continue to prevent a public deck or share link from granting access to:

- private or unlisted decks;
- media and source attachments belonging only to another deck;
- account, team, editing, presenter, or provider-action authority;
- audience personal data, call/SMS records, agent inputs, or credentials; and
- repository, database, object-storage, or deployment administration.

Public links should therefore be treated as bearer capabilities that can be
forwarded. They provide convenient distribution, not strong recipient identity.
Confidential presentations require an authenticated or explicitly restricted
sharing mode.

The default local deployment remains a trusted operator authoring locally.
`studio` and `present` modes treat the machine owner as the presenter. A studio
repository, its slides, companions, themes, configuration, and queued requests
must be treated as executable or semi-executable input rather than passive
documents.

## Hosted execution architecture

The required hosted architecture separates the public API service from Codex
execution:

1. The main Railway API service authenticates users, serves the catalog and
   player, accepts bounded redesign requests, and records job state.
2. Each hosted Codex job executes in a disposable Railway Sandbox, not inside
   the main API service.
3. The sandbox receives only the job's scoped content, minimal short-lived
   credentials, explicit network policy, and a bounded execution lifetime.
4. The sandbox returns a reviewable result or proposed content change. It does
   not receive the API service's database, Telnyx, Resend, S3, session-signing,
   or unrelated repository credentials.
5. Publication remains a separate, attributable action with branch and content
   scope enforcement.

The engine still contains an in-process `VSTD_AGENT` runner for local or legacy
operation. That path is not an acceptable isolation boundary for a hosted
deployment. Production conformance requires evidence that the API service does
not enable the in-process runner, does not contain a usable Codex execution
credential, and dispatches jobs only to Railway Sandbox. This is tracked as
TM-001 until verified by an automated deployment test and live configuration
evidence.

## Security objectives

- Prevent unauthorized reading or mutation of content not intentionally
  published as public.
- Preserve the intended visibility classification of every deck and asset.
- Keep OpenAI, Telnyx, Resend, S3, Git, database, and session credentials
  confidential and out of public-facing execution contexts.
- Prevent public or executable deck content from gaining account, presenter,
  editing, provider-action, or cross-deck authority.
- Contain prompt injection and untrusted repository content inside a disposable
  Railway Sandbox with minimal authority.
- Preserve deck, library, audience, database, and Git integrity.
- Limit external actions, compute spend, uploads, connections, and agent runs
  to authorized users and expected volumes.
- Keep presenter and audience experiences available during a live event.
- Maintain an attributable record of sensitive actions without logging secrets
  or unnecessary personal data.
- Produce sufficient security, recovery, and provenance evidence for an IT
  review and authorized penetration test.

## Assets

| Asset | Sensitivity | Where it exists |
|---|---|---|
| Public decks and referenced presentation media | Intentionally public after publication | Content repository, built HTML, player, object storage |
| Private/unlisted decks, companions, themes, sources, and evidence | Confidential | Content repository, sandbox job inputs, storage |
| Audience names, messages, phone numbers, and call transcripts | Personal/confidential | PostgreSQL, runtime state, provider systems |
| Provider and deployment credentials | Secret | Railway variables, credential broker, short-lived sandbox injection |
| Account, player, presenter, share, and handoff sessions | Authentication material | Hashed database records, signed tokens, cookies, tab session storage |
| Git remote credentials and repository state | Secret/integrity-critical | Credential broker, GitHub, sandbox or sync worker |
| Generated assets and artifacts | Public or private according to owning deck | Library, sandbox outputs, S3-compatible storage |
| OpenAI usage and external actions | Financial/reputational | Provider accounts and API calls |
| Audit and deployment evidence | Security-sensitive | PostgreSQL, logs, CI, Railway, GitHub |

## Actors

- Trusted local author or presenter.
- Hosted account owner, administrator, editor, or presenter.
- Audience member holding a public or restricted share link.
- Anonymous internet user without a valid deck capability.
- Contributor or supplier of a studio repository, deck, theme, attachment, or
  agent prompt.
- Malicious webhook or WebSocket caller impersonating Telnyx.
- Compromised third-party provider, dependency, CLI, browser, media tool, or
  build source.
- Compromised or prompt-injected Codex process inside a Railway Sandbox.
- Attacker with a stolen password, browser session, player bearer, Git token,
  or provider credential.

## Trust boundaries and data flow

1. **Browser to app origin:** account/catalog cookies, team state, deck
   metadata, and editing actions cross this boundary.
2. **Browser to player origin:** executable deck HTML receives only a
   deck-and-mode capability; app cookies must remain unavailable.
3. **Public deck to private estate:** intentionally public content must not
   authorize other decks, private library media, sources, or account actions.
4. **API service to Railway Sandbox:** an untrusted redesign request becomes a
   bounded job with scoped files, minimal credentials, controlled egress, and
   a terminal lifetime.
5. **Studio repository to local process:** YAML can select endpoints and local
   credential commands; slide HTML runs in a browser; media invokes native
   tools. An untrusted repository requires explicit trust.
6. **Services to providers:** bearer keys and personal data cross into OpenAI,
   Telnyx, Resend, GitHub, Railway, PostgreSQL, and S3-compatible services.
7. **Provider to callback endpoints:** Telnyx HTTP and media events arrive from
   the public internet and require independent provider authentication and
   replay protection.
8. **Audience to presenter:** names, chat, SMS, and call events are persisted
   and broadcast into a live presentation context.
9. **Source to production:** GitHub revisions, container images, Railway
   configuration, and content synchronization determine the running artifact.

## Existing controls

- Collaboration mode separates the account/catalog origin from executable deck
  content and rejects an invalid same-origin configuration.
- Account cookies are host-only. Player launches use short-lived, single-use
  handoffs and deck/mode-scoped sessions.
- Collaboration account mutations require both an exact app Origin and a
  per-session CSRF token. Player mutations require an Authorization bearer.
- Passwords use Argon2id. Account and player session tokens are stored as
  hashes, are revocable, and use bounded lifetimes.
- Deck and slide identifiers use restrictive validation before filesystem
  access in `internal/studio` and most server handlers.
- Public deck access uses deck-scoped, expiring capabilities. Public sharing is
  an intentional product feature, not an assumption that all studio content is
  public.
- Collaboration events record authentication, invitation, password reset,
  membership, visibility, external-share, and fork actions without secrets.
- Content mutation routes are constrained by serving mode, while Vessica action
  routes use a presenter gate.
- Request bodies, uploads, API responses, calls, chat turns, chat sessions,
  realtime token mints, agent runs, and PDF rendering have several explicit
  size, count, rate, or time limits.
- The PDF print route uses a random one-time job key and loopback access.
- Video object keys are content-addressed and S3 downloads use short-lived
  signed URLs.
- The production database is not directly exposed through a Railway public
  domain or TCP proxy.
- The application container runs as a non-root user.
- The hosted engine revision can be pinned to an exact Git commit.
- Existing Go tests and `go vet` pass; the docs-site dependency audit and
  repository secret scans were clean at the time of review.

These controls reduce risk but do not close the residual risks below.

## Threat analysis

| Category | Scenario | Current or intended control | Residual risk |
|---|---|---|---|
| Spoofing / tampering | An attacker forges a Telnyx webhook or media connection and injects messages or changes call state. | Unknown senders and call IDs are filtered. | HTTP requests are not yet proven to use Telnyx signature, timestamp, and replay verification. The media connection needs a high-entropy per-call credential before upgrade. |
| Elevation / disclosure | A companion, attachment, or repository prompt-injects Codex. | Hosted Codex execution is required to run in a disposable Railway Sandbox outside the API service. | Sandbox isolation, job-scoped credentials, egress policy, publication boundaries, and absence of the in-process production runner require automated and live verification. |
| Elevation / tampering | A malicious public slide executes JavaScript and invokes privileged endpoints. | Collaboration mode uses separate app/player origins and a deck/mode capability. | Deck code can exercise the authority available to its player mode and can read a bearer held in tab storage. It must not gain account cookies, unrelated deck access, or unrestricted provider actions. No sandboxed content iframe or effective CSP is yet established. |
| Information disclosure | A public share holder fetches unrelated library content. | Public access is expected for the shared deck and its referenced media. | Library authorization is not yet proven to be scoped to assets referenced by that deck. Public deck A must not disclose private deck B's assets. |
| Spoofing / abuse | An attacker performs credential stuffing or abuses password recovery. | Passwords use Argon2id, errors are generic, and account mutations use exact Origin plus CSRF. | Durable per-account/IP throttling, failed-login audit, MFA/passkeys, and privileged-session management remain incomplete. Origin validation is not authentication for a non-browser client. |
| Spoofing / CSRF | Another origin induces a logged-in user to change account, team, or deck state. | Collaboration account mutations require exact Origin and CSRF; player mutations require a bearer. | Legacy hosted routes require equivalent coverage, and provider callbacks require provider authentication rather than browser CSRF controls. |
| Elevation / disclosure | A cloned studio sets credential commands or an alternate OpenAI endpoint to malicious values. | Commands and endpoints are explicit configuration. | Opening an untrusted repository can execute a shell command or send a key to an attacker-controlled host unless repository trust and endpoint policy are enforced. |
| Information disclosure | Local studio mode becomes reachable from the LAN. | Documentation describes local mode as trusted authoring. | The listener binds all interfaces by default and local modes treat callers as presenters. |
| Tampering | Crafted paths or symlinks escape expected content roots. | Identifiers have multiple regular-expression checks and some containment checks. | Validation is distributed. A centralized safe-join and symlink policy with adversarial tests is still needed. |
| Denial of service / cost | Public traffic consumes chat, model, upload, SSE, callback, export, or provider capacity. | Several body, count, concurrency, and process-local limits exist. | Some limits are global, reset on restart, trust forwarded headers, or do not work across replicas. Public sharing increases the importance of distributed abuse controls and provider budgets. |
| Repudiation | A sensitive provider or publishing action cannot be attributed. | Collaboration mode records identity, membership, visibility, sharing, and fork events. | Failed authentication, agent dispatch/result, provider actions, correlation IDs, retention, and a tamper-resistant audit sink remain incomplete. |
| Supply chain | A dependency, base image, downloaded installer, global Codex package, browser/media binary, or content revision is compromised. | Go modules are recorded in `go.sum` and the engine supports exact revision pinning. | Images, installers, and global packages are not all version-and-digest pinned; SBOM, image signing, container scanning, and protected deployment provenance are incomplete. |
| Availability / recovery | A deploy, corruption, or provider outage loses audience state or interrupts an event. | Git stores content and PostgreSQL stores collaboration state. | Backup/PITR restore evidence, runtime-state durability, multi-instance behavior, and an event recovery runbook have not been demonstrated. |

## Risk treatment for public sharing

The following are accepted product characteristics when a deck is explicitly
classified as public:

- anyone with the public URL may view the presentation;
- the URL may be indexed, copied, forwarded, recorded, or screenshotted;
- presentation HTML and referenced public media are delivered to an untrusted
  browser; and
- audience participation endpoints are exposed to internet traffic and must be
  designed for abuse resistance.

This acceptance does not extend to private decks, cross-deck media, source
attachments, presenter capabilities, account data, provider actions, or
audience personal data. The product should make the classification clear at
publication time and require a restricted sharing mode for confidential decks.

## Remediation tracker

All remediation items are owned by **Matt Kropp**. Status values are `Open`,
`Verify`, `Planned`, `Accepted`, or `Complete`. An item is complete only when
its exit evidence is recorded against the exact production revision.

| ID | Priority | Status | Owner | Remediation | Exit evidence |
|---|---|---|---|---|---|
| TM-001 | P0 | Verify | Matt Kropp | Enforce Railway Sandbox as the only hosted Codex execution path. Keep the in-process `VSTD_AGENT` path disabled in the API service. Give each sandbox a scoped filesystem, minimal short-lived credentials, bounded lifetime, controlled egress, and no database, Telnyx, Resend, S3, signing, or unrelated Git authority. | Deployment test and live evidence prove the API has no active in-process runner or Codex credential; prompt-injection tests cannot read API secrets, reach blocked networks, write outside the job root, or publish directly; the sandbox and job are destroyed at termination. |
| TM-002 | P0 | Open | Matt Kropp | Authenticate Telnyx HTTP callbacks using the raw-body signature and timestamp, reject stale or replayed events, and authenticate media connections with a random expiring token bound to the call ID before WebSocket upgrade. | Valid provider fixtures pass; missing, invalid, expired, replayed, and wrong-call requests fail before any state change. |
| TM-003 | P0 | Open | Matt Kropp | Treat studio configuration as executable. Require explicit repository trust before running `*_cmd` fields, prefer argv-based helpers, require HTTPS, and require explicit approval before sending credentials to a non-approved OpenAI endpoint. | A hostile studio fixture cannot execute a command or receive a credential before trust is granted. |
| TM-004 | P1 | Open | Matt Kropp | Add durable per-IP/account login and recovery throttling, progressive delay, failed-auth audit, session management, and MFA or passkeys for owner/admin/presenter roles. | Automated abuse tests receive stable 429/lockout behavior across replicas; privileged access requires the configured second factor; sessions can be listed and revoked. |
| TM-005 | P1 | Open | Matt Kropp | Isolate executable slide content with a sandboxed content iframe or narrower capability bridge. Keep app cookies and player authority out of the content frame, add a deliberate CSP, and require confirmation or step-up authorization for external provider actions. | A malicious public slide cannot read a player bearer, call account APIs, access another deck, or send email/SMS/calls without an authorized parent-mediated action. |
| TM-006 | P1 | Open | Matt Kropp | Scope media, library files, source attachments, and exports to the authorized deck and visibility classification. | A public capability for deck A loads A's referenced public assets but receives 403 for private or unrelated assets belonging to deck B. |
| TM-007 | P1 | Open | Matt Kropp | Upgrade vulnerable Go modules and the Go toolchain, pin builder/runtime images and global packages by version and digest, and rebuild the production artifact. | `govulncheck` against the production source/toolchain has no accepted reachable Critical/High finding; container scanning has no unremediated Critical/High finding; exact image digest and SBOM are recorded. |
| TM-008 | P1 | Open | Matt Kropp | Centralize root-aware filesystem joins and symlink policy for requests, attachments, artifacts, uploads, agent results, and exports. | Table-driven tests reject traversal, encoded separators, absolute paths, symlink escapes, and platform-specific bypasses while allowing valid nested content. |
| TM-009 | P1 | Open | Matt Kropp | Bind trusted local modes to `127.0.0.1` by default and require an explicit bind plus authentication/TLS decision for non-loopback access. | Integration tests prove the default listener is loopback and a non-loopback studio listener cannot start without explicit configuration. |
| TM-010 | P2 | Open | Matt Kropp | Add explicit HTTP read-header, request, write, and idle timeouts; bounded SSE/WebSocket connections; distributed per-IP/session/account/deck limits; trusted-proxy IP extraction; upload limits; and provider budgets. | Load tests show bounded memory and connections, predictable 429 responses, preserved presenter availability, and limits that work across replicas. |
| TM-011 | P2 | Open | Matt Kropp | Apply an origin- and route-specific security-header/cache matrix: HSTS after subdomain validation, `X-Content-Type-Options`, referrer policy, frame restrictions, permissions policy, CSP, and appropriate cache control. | Automated HTTP tests assert the expected header matrix for app, player, public deck, APIs, assets, and downloads. |
| TM-012 | P2 | Open | Matt Kropp | Protect Git and deployment integrity with short-lived GitHub App credentials, protected branches, required review and CI, PR-based sandbox output, redacted Git errors, and immutable production revisions. | No token appears in logs or `.git/config`; the sandbox cannot push to a protected production branch; a deployment maps to a reviewed commit, signed image digest, and content revision. |
| TM-013 | P2 | Open | Matt Kropp | Add supply-chain gates: secret scanning, dependency review, SBOM generation, signed provenance, container scanning, pinned/checksummed installer flows, and documented patch cadence. | CI blocks secrets and unaccepted reachable vulnerabilities; released artifacts verify against published provenance and SBOM. |
| TM-014 | P2 | Open | Matt Kropp | Extend structured audit coverage to failed authentication, sandbox creation/destruction, agent inputs/results, publication, exports, and Telnyx/Resend/OpenAI actions with correlation IDs, retention, redaction, and a tamper-resistant sink. | An operational review can reconstruct each sensitive action and actor without exposing credentials or unnecessary message/phone content. |
| TM-015 | P2 | Open | Matt Kropp | Document data classification, audience/call/chat consent, subprocessors, retention/deletion, incident response, secret rotation, and breach handling. | IT review package contains approved policies, data-flow and subprocessor inventory, retention settings, and a completed incident-response exercise. |
| TM-016 | P2 | Open | Matt Kropp | Configure and test encrypted PostgreSQL backup/PITR and define recovery for runtime presentation state and provider outages. | A dated restore drill meets recorded RPO/RTO targets and an event recovery runbook is exercised. |
| TM-017 | P2 | Verify | Matt Kropp | Reconcile local source, GitHub content revision, engine revision, container image, and Railway deployment so the complete production artifact is reproducible. | A manifest maps the running service to immutable engine/content commits, image digest, configuration version, SBOM, and test results. |
| TM-018 | P3 | Accepted | Matt Kropp | Maintain public sharing as an intentional product capability with explicit deck classification and clear publisher warning that public URLs are forwardable and not recipient authentication. | Product tests prove public and restricted modes are distinct; publication UI records classification; no public capability crosses into private content or privileged actions. |

## IT and penetration-test readiness gates

Use OWASP ASVS Level 2 as the minimum application-control baseline and maintain
a control-to-evidence mapping. Do not represent the platform as ready for an
external penetration test until all P0 items are complete and each P1 item is
complete or has a documented, time-bounded risk acceptance.

The review package should contain:

- the current architecture and data-flow diagram, including the API-to-Railway
  Sandbox boundary;
- a live configuration assertion proving the in-process API worker is disabled;
- sandbox lifecycle, filesystem, secret, credential, egress, and teardown test
  evidence;
- authentication, authorization, CSRF, deck/asset isolation, callback replay,
  and abuse-control test results;
- the exact source revisions, image digest, SBOM, dependency and container scan
  results, and deployment provenance;
- Railway and GitHub access lists, MFA evidence, protected-branch settings, and
  secret rotation history;
- data classification, retention, deletion, consent, subprocessor, and incident
  response documentation;
- backup/PITR configuration and a successful restore-drill record; and
- the external penetration-test report, remediation evidence, and retest letter.

## Suggested security test sequence

1. Add unit and handler tests for safe joins, session expiry, deck/asset scope,
   webhook verification, request origins, public configuration, and rate limits.
2. Add a deployment assertion that hosted API instances cannot start with the
   local/in-process Codex runner enabled.
3. Run a hostile-studio fixture against fake provider endpoints and canary
   credentials.
4. Run a prompt-injection suite inside a real Railway Sandbox and verify secret,
   filesystem, egress, publication, and terminal teardown boundaries.
5. Exercise a staging deployment with public-audience load tests, provider test
   accounts, callbacks, WebSockets, uploads, and recovery scenarios.
6. Complete an OWASP ASVS Level 2 verification pass and close or formally
   accept every applicable control.
7. Perform an authorized external penetration test against the exact immutable
   staging or production candidate, then retest every material finding.

## Review triggers

Revisit this threat model when any of these change:

- serving-mode authorization, deck visibility, public-link behavior, or
  listener binding;
- app/player/content origin layout or executable deck capabilities;
- Railway Sandbox lifecycle, credentials, filesystem, egress, teardown, or
  publication behavior;
- the availability or configuration of the legacy in-process agent runner;
- OAuth, webhook, SMS/call/email, audience chat, or provider-action behavior;
- configurable endpoints or credential resolution;
- object-storage visibility and deck-to-asset authorization;
- upload, export, browser, or media-processing pipelines; or
- build inputs, GitHub protections, image provenance, backups, or Railway
  deployment topology.
