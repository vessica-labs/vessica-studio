# Vessica Studio Threat Model

Status: review of the repository as of 2026-08-11. This document describes the
current implementation and recommends future work. It does not claim that the
recommended controls have been implemented.

## Scope and assumptions

This review covers the `vstd` process, studio content repositories, the browser
player, public hosting, the optional Claude worker, OpenAI APIs, GitHub Device
Flow, Telnyx, Resend, S3-compatible storage, headless Chrome, FFmpeg, and Git.

The default intended deployment is a trusted operator authoring locally.
`public` mode adds untrusted audience traffic and presenter authentication.
`studio` and `present` modes treat the machine owner as the presenter. A studio
repository, its slides, companions, themes, configuration, and queued requests
must therefore be treated as executable or semi-executable input—not as passive
documents.

This is a code review and architecture threat model, not a live penetration test
or audit of a particular Railway deployment, OAuth application, bucket policy,
or provider account.

## Security objectives

- Prevent unauthorized reading or mutation of private decks and presenter data.
- Keep OpenAI, Telnyx, Resend, S3, Git, and session credentials confidential.
- Prevent untrusted content or audience input from gaining presenter authority.
- Preserve deck, library, audience, and Git integrity.
- Limit external actions, compute spend, uploads, and agent runs to authorized
  users and expected volumes.
- Keep presenter and audience experiences available during a live event.
- Maintain an attributable record of sensitive actions without logging secrets
  or unnecessary personal data.

## Assets

| Asset | Sensitivity | Where it exists |
|---|---|---|
| Decks, companions, themes, evidence | Private to public | Content repository and built HTML |
| Audience names, messages, phone numbers, call transcripts | Personal/confidential | `_vessica/<deck>` and process memory |
| Provider credentials | Secret | Environment, configured credential commands, process memory |
| Presenter and share sessions | Authentication material | HMAC-signed cookies and URLs |
| Git remote credentials and repository state | Secret/integrity-critical | Environment, command arguments, `.git`, remote |
| Generated assets and artifacts | Private to public | `library`, `_vessica`, S3-compatible storage |
| OpenAI usage and external actions | Financial/reputational | Provider accounts and API calls |

## Actors

- Trusted local author or presenter.
- Allowlisted hosted presenter.
- Audience member holding a valid share link.
- Anonymous internet user without a share link.
- Contributor or supplier of a studio repository, deck, theme, or agent prompt.
- Malicious webhook or WebSocket caller impersonating Telnyx.
- Compromised third-party provider, dependency, CLI, browser, or media tool.
- Compromised or prompt-injected headless agent.

## Trust boundaries and data flow

1. **Browser to `vstd`:** deck HTML and JavaScript share an origin with edit,
   presenter, audience, export, and external-action endpoints.
2. **Local versus public mode:** local modes implicitly trust the caller;
   public mode uses GitHub allowlisting and HMAC-signed sessions/share links.
3. **Studio repository to host process:** YAML configuration can select service
   endpoints and shell commands; slide HTML runs in the browser; queued work can
   trigger image/video processing or an agent.
4. **`vstd` to providers:** bearer keys and personal data cross into OpenAI,
   Telnyx, Resend, GitHub, and S3-compatible services.
5. **`vstd` to subprocesses:** shell, Claude CLI, Git, Chrome, FFmpeg, and
   FFprobe inherit host capabilities and often the process environment.
6. **Audience to presenter:** names, chat, SMS, and call events are persisted and
   broadcast into a live presentation context.
7. **Local disk to hosted object storage:** video bytes are uploaded under
   content-derived keys and later served locally or through presigned URLs.

## Existing controls

- Deck and slide identifiers use restrictive validation before filesystem
  access in `internal/studio` and most server handlers.
- Public deck access uses deck-scoped, expiring HMAC tokens; cookies are
  `HttpOnly`, `SameSite=Lax`, and secure in public mode.
- Legacy presenter sessions are HMAC-signed, expire after 12 hours, and are
  accepted only for GitHub logins in `VSTD_ALLOWED_GITHUB`. Optional
  collaboration mode instead stores hashed, revocable account and deck-scoped
  player sessions in PostgreSQL.
- Collaboration mode separates the account/catalog origin from executable deck
  content. Player launches use short-lived single-use handoffs, keep the
  deck/mode bearer in tab session storage, and revalidate membership, ownership,
  visibility, and mode on every request.
- Collaboration account mutations require both an exact app Origin and a
  per-session CSRF token. The app cookie is host-only and is never accepted by
  player APIs.
- Collaboration events record authentication, invitation, password reset,
  membership, visibility, external-share, and fork actions without secrets.
- Public content mutation routes are blocked by the serving mode, while Vessica
  action routes use a presenter gate.
- Request bodies, uploads, API responses, calls, chat turns, chat sessions,
  realtime token mints, agent runs, and PDF rendering have several explicit
  size, count, rate, or time limits.
- Private library assets require some valid access in public mode.
- The PDF print route uses a random one-time job key and loopback access.
- Provider secrets stay server-side in the intended flow, and `vstd key check`
  does not print the resolved OpenAI key.
- Video object keys are content-addressed; S3 downloads use short-lived signed
  URLs.
- The agent records output and makes a best-effort attempt to revert newly
  introduced out-of-scope Git changes after a pass. This cleanup is not a
  reliable containment boundary.

These controls reduce risk but do not close the gaps below.

## Threat analysis

| Category | Scenario | Current control | Residual risk |
|---|---|---|---|
| Spoofing / tampering | An attacker forges a Telnyx webhook or connects to the media WebSocket, injects messages, or changes call state. | Endpoints are inert without a configured Telnyx key; unknown SMS senders and call IDs are ignored. | Requests are not authenticated with Telnyx signatures. The WebSocket upgrader accepts every origin. Known numbers/call IDs can be targeted. |
| Elevation / tampering | A malicious slide or theme runs JavaScript when a presenter views it and calls privileged endpoints. | Collaboration mode serves executable decks from a separate player origin with only one deck/mode capability; app cookies are unavailable. | Deck code intentionally receives its current player capability and can exercise every authority of that mode. Legacy non-collaboration public mode remains same-origin. No CSP or iframe sandbox is applied. |
| Elevation / disclosure | A cloned studio sets `openai.api_key_cmd`, `share_secret_cmd`, or storage credential commands to shell payloads. | Commands are explicit configuration fields and run locally. | Opening or serving an untrusted repository can execute arbitrary shell commands. A malicious OpenAI `base_url` can receive the operator's API key on the next API call. |
| Elevation / disclosure | Companion text or repository content prompt-injects the headless Claude worker. | Prompt states a file scope; Git later reverts new out-of-scope changes; hourly and concurrency caps exist. | The worker is launched with permission bypass, Bash, inherited environment, and network access. Post-hoc Git cleanup cannot undo secret exfiltration, external actions, deletion outside Git, or pushed commits. |
| Spoofing / CSRF | Another origin induces a logged-in user to change catalog or team state. | Collaboration account mutations require exact Origin and a per-session CSRF token; cookies use `SameSite=Lax`. Player mutations require an Authorization bearer. | Legacy non-collaboration presenter routes do not have the same CSRF-token scheme, and provider callbacks need their own authentication rather than browser CSRF controls. |
| Information disclosure | `studio` mode is started on a laptop and becomes reachable from the LAN. | Documentation describes it as local authoring. | `http.ListenAndServe(":port", ...)` binds all interfaces, and local modes treat every caller as presenter. Edit, upload, tool, and token routes are therefore exposed to reachable peers. |
| Information disclosure | A share holder fetches unrelated library content. | `/library` requires presenter, loopback, or any valid share cookie. | Access is not scoped to the deck that references an asset. One shared deck can grant access to other private library assets if paths are guessed or learned. |
| Information disclosure / tampering | Git credentials embedded in `VSTD_GIT_REMOTE` leak through `.git/config`, process inspection, or error logs. | The variable is optional and Git is used only when the worker is configured for it. | The remote is passed on the command line, stored by Git, and included in formatted Git errors. A token-bearing URL can be exposed. |
| Tampering | Crafted file paths escape expected roots during uploads, artifacts, requests, or bundle/export operations. | Deck, slide, asset, and artifact identifiers have several regular-expression checks; video request paths use `filepath.Rel`. | Validation is distributed and some filesystem helpers accept raw path components. Symlink traversal and platform-specific containment deserve dedicated tests and a centralized safe-join boundary. |
| Denial of service / cost | Audience or anonymous callers create excessive model calls, uploads, SSE clients, OAuth flows, or provider actions. | Chat sessions/turns and realtime token mints are capped; uploads and bodies have size limits. | Several limits are process-global, in-memory, or absent. Restarts reset counters; SSE subscribers, device flows, image requests, and external actions can exhaust memory, spend, sockets, or provider quotas. |
| Repudiation | A sensitive action occurs during a live session and cannot be attributed. | Collaboration mode has a PostgreSQL audit schema for identity, membership, visibility, external sharing, and forks; some calls, messages, and agent runs are also logged. | Audit retention, request/provider correlation IDs, a tamper-resistant sink, and complete coverage of tool-side effects remain undefined. |
| Supply chain | A dependency, downloaded Railway installer, Chrome/FFmpeg binary, Claude CLI, or repository asset is compromised. | Go dependencies are versioned in `go.sum`; the engine is buildable from source. | The Railway helper downloads and runs a remote shell installer; optional binaries and GitHub actions/releases are not covered by an explicit provenance or vulnerability-scanning policy. |

## Prioritized recommendations

This table tracks remaining hardening work. Collaboration-mode controls that
now exist are called out explicitly so they are not mistaken for coverage in
legacy serving modes.

| Priority | Recommendation | Likelihood / impact | Proposed change | Verification |
|---|---|---|---|---|
| P0 | Treat studio configuration as executable and require explicit trust. | Medium / critical | Do not run `*_cmd` fields automatically for an untrusted root. Add a trust prompt or repository trust record, prefer argv-based credential helpers, restrict `openai.base_url` to HTTPS and require an explicit opt-in before sending credentials to non-OpenAI hosts. | Open a hostile fixture and prove no command runs and no key leaves the process before trust is granted. |
| P0 | Sandbox the headless agent before recommending hosted use. | High when enabled / critical | Run it in a disposable container or OS sandbox with a minimal environment, scoped filesystem mount, egress policy, separate short-lived credentials, protected Git branch, and approval gates for external actions. Remove permission bypass as the security boundary; retain Git cleanup only as defense in depth. | Prompt-injection tests attempt environment reads, network egress, out-of-root writes, destructive commands, and direct pushes; each must be blocked and audited. |
| P0 | Authenticate Telnyx HTTP and media callbacks. | Medium / high | Verify Telnyx webhook signatures against the raw body and timestamp, reject replay, and authenticate the media WebSocket with a high-entropy per-call token bound to the active call ID. Tighten origin/host handling where applicable. | Provider-signed fixtures pass; missing, invalid, expired, replayed, and wrong-call signatures fail before state changes. |
| P1 | Bind local authoring to loopback by default. | Medium / high | Add an explicit `--bind` setting, default local modes to `127.0.0.1`, and require an intentional opt-in plus authentication for non-loopback studio access. | Integration tests confirm the default listener is loopback and public mode remains deployable. |
| P1 | Extend executable-content isolation beyond collaboration mode. | Medium / high | Collaboration mode now uses separate origins and deck/mode capabilities. Add a sandboxed iframe or narrower bridge, CSP, and equivalent isolation for legacy hosted mode. | A malicious slide cannot exceed the selected deck/mode capability or access legacy presenter authority. |
| P1 | Complete request-integrity coverage. | Medium / high | Collaboration account routes now use exact Origin plus CSRF tokens. Add equivalent protection to legacy presenter routes and independently authenticate provider callbacks. | Cross-origin browser mutations fail in both modes; provider callbacks pass only with valid provider authentication. |
| P1 | Fail closed on missing public-mode security configuration. | Medium / high | Refuse to start public mode without a durable `VSTD_SECRET`, configured presenter identity provider, and non-empty allowlist. Keep ephemeral secrets only behind an explicit development flag. | Startup tests cover every missing/empty combination and show no public listener starts. |
| P1 | Scope assets to authorized decks. | Medium / medium-high | Maintain a deck-to-asset reference index or issue deck-scoped asset URLs/tokens. Do not treat any share cookie as authority for the entire library. | A share for deck A can load A's referenced assets but receives 403 for assets private to deck B. |
| P1 | Centralize safe filesystem joins and symlink policy. | Low-medium / high | Add root-aware helpers using cleaned relative paths and evaluated-parent checks; reject absolute paths, `..`, and symlink escapes. Use them for requests, artifacts, uploads, agent logs, and bundles. | Table tests cover traversal, encoded separators, symlink escapes, Windows-style input, and valid nested paths. |
| P2 | Replace process-global in-memory abuse controls. | High at public events / medium | Add per-IP/session/account rate limits, bounded SSE/device-flow/session maps, expiry cleanup, provider budgets, and reverse-proxy body/connection limits. Use a shared store for multi-instance hosting. | Load tests demonstrate bounded memory, stable latency, predictable 429 responses, and limits that survive instance scaling. |
| P2 | Protect Git credentials and pushes. | Medium when enabled / high | Use a credential helper or short-lived app token instead of token-bearing remote URLs; redact command arguments and errors; require protected target branches and reviewable pull requests. | Logs and `.git/config` contain no token; a worker cannot force-push or bypass branch protection. |
| P2 | Add consistent security headers and cache policy. | High / medium | Apply HSTS in TLS deployments, `X-Content-Type-Options`, a deliberate referrer policy, frame restrictions, permissions policy, and endpoint-specific cache headers. Design CSP together with content isolation rather than adding an ineffective permissive policy. | Automated HTTP tests assert the header matrix for engine pages, decks, APIs, assets, and downloads. |
| P2 | Constrain uploads and outbound destinations. | Medium / medium-high | Verify media by decoded type, enforce lower configurable size/dimension/duration limits, scan where appropriate, restrict redirect behavior and endpoint schemes, and apply allowlists/private-network blocking to configurable outbound hosts. | Polyglot, decompression-bomb, redirect, loopback, link-local, oversized, and wrong-MIME fixtures are rejected. |
| P2 | Mature structured, privacy-aware audit logging. | Medium / medium | Extend the collaboration audit schema with request/result/provider correlation, retention, and a tamper-resistant sink while continuing to redact tokens and minimize message/phone content. | Tests exercise redaction and correlation; operational review can reconstruct a sensitive action without exposing credentials. |
| P3 | Establish dependency and release provenance checks. | Medium / medium | Add `govulncheck`, secret scanning, dependency review, pinned/checksummed installer flows, SBOM generation, signed releases, and documented patch cadence. Avoid executing an unpinned remote installer from the application when possible. | CI blocks known reachable vulnerabilities/secrets and release artifacts verify against published provenance. |

## Suggested security test sequence

1. Add unit tests around safe joins, auth/session expiry, share scope, webhook
   verification, request origins, and public configuration validation.
2. Add handler-level adversarial tests for every state-changing public route.
3. Run a hostile-studio fixture against a sandboxed process with fake provider
   endpoints and canary credentials.
4. Run an agent prompt-injection suite in the proposed container/egress sandbox.
5. Exercise a staging deployment with rate/load tests and provider test accounts.
6. Only then perform an authorized external penetration test against the exact
   hosted deployment, including OAuth, share links, WebSockets, uploads, and
   callback replay.

## Review triggers

Revisit this threat model when any of these change:

- serving-mode authorization or listener binding;
- deck/player execution or origin layout;
- agent permissions, Git automation, or hosted worker topology;
- OAuth, webhook, SMS/call/email, or audience chat behavior;
- configurable endpoints or credential resolution;
- object storage visibility and share-link semantics;
- upload, export, browser, or media-processing pipelines.
