# ADR: Native local-first cloud client boundary

- Decision ID: ADR-VES-13-native-cloud-client
- Status: Accepted
- Date: 2026-09-02
- PRD: .harness/runs/run_1a7549c9c85c4f377b4f/artifacts/prd.md (VES-13)
- Applies to: cmd/vstd cloud commands; internal/cloud, internal/cloudauth, internal/cloudworkspace, internal/cloudpublish; internal/studio content enumeration and validation; plugin canonical workflows; native authentication, credentials, workspace association, revision synchronization, conflict handling, publication, protocol compatibility, and local-only operation
- Supersedes: None

## Context

The engine owns the stable studio/deck/paired-slide file model and the canonical authoring plugin, while the private Vessica Studio Cloud service owns identity, authorization, revision storage, and publication policy. The public CLI currently has no native cloud client; existing internal/server/content_sync.go Git behavior is a hosted server adapter and cannot become the user-facing protocol. The new flow crosses untrusted network, identifier, credential, and filesystem boundaries and must preserve useful local operation through authentication, protocol, network, and conflict failures. No accepted, non-superseded ADR intersects this change. Private-cloud issues VES-6, VES-7, VES-9, and VES-12 remain the source of concrete wire schemas, so the public implementation needs one replaceable versioned transport boundary rather than cloud policy or Git mechanics.

## Decision Drivers

- Preserve the existing local file contract and all unauthenticated, offline authoring, build, serve, export, and skill-discovery flows.
- Keep SaaS identity, tenant authorization, revision storage, and publication policy in the private service.
- Never expose or persist session secrets in repositories, Git configuration, process arguments, output, errors, diagnostics, fixtures, manifests, or builds.
- Prevent stale-base synchronization, failed pulls, malicious paths, and partial writes from damaging either local content or cloud head.
- Provide a versioned, bounded, deterministic protocol that fails before content mutation when incompatible.
- Keep public CLI and plugin terminology independent of Git and make compatibility behavior testable without live services.

## Decision

Add four internal domain/adapter packages behind a thin cmd/vstd cloud adapter. internal/cloud owns only the versioned HTTP protocol, capability negotiation, bounded decoding, typed remote operations, and structured error taxonomy. internal/cloudauth owns native login lifecycle and credential storage while consuming the cloud protocol. internal/cloudworkspace owns local association and synchronization orchestration while consuming internal/cloud and the studio-owned content projection. internal/cloudpublish owns revision selection and publication orchestration while consuming internal/cloud and read-only association state. The CLI composes these packages and renders stable, plain-text workspace/version/sync/conflict/publish states. It does not expose transport, Git, or credential mechanics.

Every cloud operation first negotiates a supported protocol/capability set, or uses a still-valid negotiation result scoped to the same endpoint and client invocation. Required capabilities are checked before authentication-dependent content work and always before filesystem or association mutation. Concrete request and response fields must be generated or manually mapped from the approved private-service public contract when that contract is available; private implementation types and policy must not be imported or copied into this repository.

### Components and Dependency Boundaries

cmd/vstd may depend on all four cloud packages and internal/studio. internal/cloudauth, internal/cloudworkspace, and internal/cloudpublish may depend on internal/cloud; internal/cloudworkspace may depend on internal/studio for the canonical content projection and validation. internal/cloud must depend only on standard transport primitives and must not depend on the CLI, studio filesystem, auth storage, publishing orchestration, internal/server, Git, or private-service code. internal/studio must not depend on cloud packages. Cloud packages must not depend on internal/server/content_sync.go, shell out to Git, or implement tenant, entitlement, billing, access, retention, or publication policy.

The studio package owns one deterministic content projection for cloud exchange: normalized relative paths and bytes for the supported versioned studio contract. Cloud workspace code must reuse it rather than duplicate file-model allowlists or validators. Generated builds, runtime state, credentials, the workspace association, Git data, request archives, and local video bytes are excluded. Remote snapshots are treated as untrusted and validated for allowed paths, duplicates, case collisions, symlinks, type, count, per-file size, and total size before entering a staging directory on the same filesystem.

### Interfaces and Contracts

internal/cloud exposes a context-aware Client with an injected HTTP transport, endpoint, client version, response limits, and access-token source. Its typed operations cover capabilities; device/browser authorization initiation and polling; refresh, revoke, and account inspection; workspace listing and snapshot/head retrieval; explicit-base revision creation; publish request; and publication lookup. Requests carry an explicit protocol version and required capability names. Responses are accepted only with expected status, content type, schema/version, identifiers, and size bounds. Unknown additive response fields are tolerated within a negotiated version; unknown required capabilities, unsupported versions, malformed responses, redirects outside the configured trusted origin, and minimum-client failures produce typed errors and no mutation. Retries are limited to idempotent reads and explicitly idempotent requests using caller-supplied operation keys; revision creation and publication are never blindly replayed.

Authentication is exposed through narrow Store and protocol interfaces. Production stores save only renewable session material under a stable service/account key in a supported OS credential store; access tokens remain memory-only. Test stores are injectable and deterministic. If a secure OS store is unavailable, login fails with an actionable error; there is no plaintext file, environment, or Git fallback. Login opens or prints the provider-approved verification URL and non-secret user code as required by the native flow, never a device secret. Logout attempts revocation when possible and unconditionally deletes the local item; revocation failure is reported as a warning after confirmed local removal. Account output contains identity metadata only.

The local association is a versioned, non-secret .vstd/cloud-workspace.json containing endpoint identity, opaque workspace identifier, synchronized base revision, and optional pending conflict head. It is written with mode 0600 by atomic replace, is never part of the cloud content projection or builds, and is validated rather than trusted. Connect validates an existing studio before writing it. Clone materializes into a newly created empty target by staging and atomic rename. Status combines the association, deterministic local projection digest, and remote head when reachable, and represents local dirty/unsynced, offline, expired-session, conflict, and incompatible-protocol states separately.

Sync submits the normalized content snapshot against the association's explicit base with a unique idempotency key. Only a successful attributable revision response advances the base. A stale-base response records only non-secret conflict metadata and preserves both heads and all local files. Pull replaces content only when the local projection still matches the recorded base; otherwise it stages and reports a conflict without overwriting. Explicit conflict resolution is an acknowledgment on sync after the author has reconciled local paired files against the identified remote head; it names that head as the new explicit base and remains subject to the same compare-and-create server precondition. No automatic merge, discard, force push, or silent base advance is permitted.

Publish accepts an explicit revision or, when omitted, only the single synchronized association revision; dirty local state is not published implicitly. Publication state and URL are opaque service-returned values validated for shape and rendered without applying policy locally. Default output is stable text with actionable next commands and no color dependency; advanced diagnostics may include endpoint origin, protocol/capabilities, opaque revision/request IDs, and state categories, but never headers, tokens, authorization payloads, credential-store values, or raw secret-bearing bodies.

### Data and State

Cloud content remains the existing studio contract; no cloud-owned fields are added to studio.yaml, deck.yaml, or slide companions. Association metadata is local client state, contains no credentials, and changes only after validated clone/connect or confirmed revision transitions. Credential lifecycle is independent of workspace metadata, so logout does not alter content or association. Revision creation is immutable and attributable server-side, guarded by workspace plus explicit base and an idempotency key. Local digests are deterministic over normalized projected paths and bytes and are used for status/precondition evidence, not as authority for cloud identity.

All multi-file incoming changes use validate-then-stage-then-commit ordering. Before commit, the implementation rechecks the local base/digest observed at operation start; concurrent local edits abort the operation. Replacement preserves unrelated excluded local state and uses rollback/backup mechanics sufficient to restore the prior projected tree if commit cannot complete. Temporary staging and conflict data are cleaned on success and are bounded; failed-operation artifacts contain no credentials and may be retained only when needed for explicit recovery.

### Failure Handling and Observability

Typed failures distinguish offline/timeout, unauthenticated/expired, forbidden/not found, stale base/conflict, incompatible protocol/minimum client, invalid remote content, rate limit, and internal service response. CLI errors are concise, non-zero, and identify a safe retry, login, status, pull, or reconciliation action. Empty lists and no-change syncs are successful explicit states. Network ambiguity after an idempotent mutation is resolved by operation-key lookup or status rather than resubmission with a new key. Local commands never initialize cloud packages or contact the network.

Structured internal diagnostics record operation name, duration, sanitized endpoint origin, HTTP class, capability/version, workspace/revision/request identifiers, and result category. They redact authorization and credential fields at construction and never log request/response bodies from authentication endpoints. Fake servers, deterministic clocks/IDs, real temporary trees, and sentinel-secret scans prove negotiation, replay behavior, preservation, and redaction without live dependencies.

### Security and Privacy

Workspace, revision, publication, URL, filenames, headers, and server errors are untrusted. Endpoint configuration must require HTTPS except explicit loopback test/development endpoints, reject credential-bearing URLs, restrict redirects to the trusted origin, and prevent tokens from being sent cross-origin. Filesystem projection and application reject absolute paths, traversal, unsafe separators, special files, symlinks, case collisions, and writes outside the resolved studio root. Presentation HTML never receives credentials or privileged client handles.

The client sends only an explicit user-invoked snapshot of the studio-owned projection and never scans or uploads arbitrary repository files. Authentication and authorization remain service decisions; client checks improve UX but do not grant access. Secrets are held for the shortest practical lifetime, redacted through shared cloudauth-safe error construction, and passed in HTTP headers from memory rather than shell arguments. New credential-store dependencies require reviewed go.mod and go.sum changes.

### Deployment and Compatibility

Land the protocol boundary before auth, workspace, and publishing consumers; then wire the CLI and plugin guidance. The auth ticket therefore depends on the protocol ticket. Cloud features are additive and dormant unless vstd cloud is invoked. The versioned association and protocol are public compatibility surfaces: readers reject unsupported association major versions without mutation, tolerate known additive fields, and never silently downgrade required capabilities. No migration of existing studios is required; connect creates association state and logout leaves it intact.

A client release must follow the existing tag-driven process after focused, package, repository-wide, race, build, architecture, parity, leak, and local-only verification. Rollback is installing the prior binary; association data remains non-secret and must stay readable or fail safely. The client cannot be released as interoperable until the private service's approved public auth, capability, revision, and publication schemas are aligned in internal/cloud fixtures. This ADR authorizes neither a private-cloud deployment nor a public release.

## Consequences

### Positive

- Local authoring remains independent of cloud availability and credentials.
- A single typed boundary contains protocol churn and prevents private SaaS policy from leaking into the engine.
- Explicit-base, staged filesystem operations make conflicts and ambiguous failures non-destructive.
- OS-backed renewable credentials and construction-time redaction sharply reduce secret exposure paths.
- Studio-owned projection rules prevent a second, divergent definition of synchronizable content.

### Tradeoffs and Risks

- Users without a supported OS credential store cannot persist a login and must use local-only workflows until secure storage is available.
- Conflict resolution is intentionally explicit and may require manual paired-file reconciliation.
- Atomic multi-file replacement and concurrent-edit detection add implementation complexity and platform-specific edge cases.
- Final interoperability remains coupled to the approved private-service wire contract and minimum-version policy.

## Alternatives Considered

### Expose hosted Git synchronization as the CLI workflow

- Rejected because: it exposes infrastructure and credentials, couples native behavior to internal/server, and cannot express the required versioned capability and publication contracts.

### Store cloud credentials or association in studio.yaml or Git configuration

- Rejected because: credentials would cross repository and build boundaries, while cloud linkage would contaminate the stable content model and synchronized snapshots.

### Use a plaintext credential fallback for headless systems

- Rejected because: deterministic convenience does not satisfy the secret-storage invariant; tests can inject a fake store and production can fail safely.

### Automatically merge or force stale revisions

- Rejected because: silent resolution can overwrite local or cloud work and moves revision policy into the client.

### Let cloudworkspace define its own file allowlist

- Rejected because: duplicated file-model rules would drift from internal/studio and undermine the public engine as the sole content-contract owner.
