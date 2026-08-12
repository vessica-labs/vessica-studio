# Codex Transition and Repository Quality Design

## Objective

Make Vessica Studio straightforward for Codex and open-source contributors to
understand and change, while preserving its public CLI, HTTP API, deck format,
and runtime behavior. The work also records a threat model without implementing
security changes during this pass.

## Current Architecture

Vessica Studio is a Go 1.22 application organized around these boundaries:

- `cmd/vstd`: CLI parsing and orchestration, including Railway deployment.
- `internal/studio`: the local-first deck model, scaffolding, building,
  forking, ordering, and static export.
- `internal/server`: HTTP serving, editing, presentation auth, live events,
  audience interaction, agent execution, PDF export, and external action tools.
- `internal/oai`: OpenAI HTTP integration and, currently, image-library data
  types and manifest persistence.
- `internal/video`: video normalization and manifest registration.
- `internal/s3`: S3-compatible object storage.
- `plugin`: the canonical embedded deck-authoring workflows used by Claude and
  exposed to Codex through `vstd skill`.
- `codex`: prompt launchers plus a downstream content-repository agent guide.

The principal architectural smell in scope is that `internal/video` imports
`internal/oai` to use asset-manifest types and persistence. The library
manifest is a domain concern shared by image and video features; it does not
belong to the OpenAI adapter.

## Repository Agent Harness

Create a root `AGENTS.md` for contributors working on this engine repository.
It will:

- describe the product and package ownership boundaries;
- distinguish engine development from deck-content authoring;
- identify `plugin/skills/*/SKILL.md` and `plugin/docs/conventions.md` as the
  canonical authoring workflow sources;
- preserve `codex/AGENTS.md` as the template copied into content repositories;
- state invariants for the file-based deck contract, engine-owned player,
  serving modes, secrets, generated output, and compatibility;
- define focused test commands and the full repository validation gate;
- direct contributors to update tests and documentation with behavior changes.

No `.vessica/` workspace or generated Vessica harness will be installed.

## README Redesign

Rewrite the root README as the public GitHub entry point. It will be accurate
to the checked-in implementation and include:

1. A concise product description and local-first rationale.
2. Installation prerequisites and a verified quick start.
3. A capability overview covering authoring, presentation, assets, audience
   interaction, export, agent workflows, and public hosting.
4. A compact architecture map and the deck-directory contract.
5. Command and serving-mode references at an appropriate overview level.
6. Claude and Codex setup, clearly separating engine contribution guidance
   from downstream content-repository guidance.
7. Configuration and secret resolution.
8. Development and validation commands.
9. Security and deployment caveats that describe current behavior without
   claiming unimplemented protections.
10. Contribution and license sections.

The README will avoid speculative roadmap claims and will not advertise a
command, endpoint, or security property that is not present in the repository.

## Asset Library Boundary

Create `internal/library` as the owner of the shared asset catalog:

- `Manifest`
- `StyleFamily`
- `Asset`
- `VideoAsset`
- `Load`
- `(*Manifest).Save`

`internal/oai` will continue to own OpenAI authentication and image generation,
but will consume the shared library package when reading or updating image
assets. `internal/video`, `internal/server`, and `cmd/vstd` will consume the
shared types directly where needed.

This is an internal refactor. JSON field names, manifest versioning, filenames,
and observable behavior must remain unchanged. Compatibility type aliases may
remain in `internal/oai` only if they materially reduce churn without obscuring
the new ownership boundary.

## Testing and Error Handling

Use test-driven development for the extracted library behavior:

- loading a missing manifest returns an initialized version-1 manifest;
- loading valid JSON preserves image, video, and style-family data;
- malformed JSON returns a contextual error;
- saving produces readable JSON that round-trips through `Load`.

Add workflow contract tests that verify:

- each embedded skill is discoverable and readable;
- the Codex prompt launchers correspond to the canonical embedded skills;
- shared conventions remain available through the plugin package.

Existing error behavior will be preserved unless a failing test demonstrates a
safe, compatibility-neutral correction. The full validation gate is:

```text
gofmt check
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/vstd
```

## Threat Model Deliverable

Produce a report-only threat model after implementation. It will document:

- assets: content, credentials, OAuth sessions, audience data, generated
  artifacts, Git state, and hosted media;
- actors: presenter, audience member, repository contributor, hosted attacker,
  malicious webhook caller, compromised content, and compromised agent;
- trust boundaries: browser/server, public/local modes, GitHub OAuth, OpenAI,
  Telnyx, Resend, S3-compatible storage, local shell commands, headless Chrome,
  and the filesystem/Git workspace;
- abuse cases using a STRIDE-style review;
- existing mitigations and evidence from the code;
- prioritized recommended improvements with severity, likelihood, impact, and
  suggested validation.

The threat-model recommendations will not be implemented in this work. If an
otherwise desirable refactor would alter security posture, it is excluded and
recorded as a recommendation instead.

## Compatibility and Scope Constraints

- Do not change public CLI command names or flags.
- Do not change HTTP paths or request/response contracts.
- Do not change `studio.yaml`, `deck.yaml`, slide, or manifest formats.
- Do not change serving-mode authorization behavior.
- Do not add a new runtime dependency or framework.
- Do not initialize a Vessica workspace.
- Do not implement threat-model recommendations.
- Do not broadly split `cmd/vstd` or `internal/server` in this pass.
- Preserve unrelated user files and repository history.

## Success Criteria

- A contributor can understand the architecture and validation contract from
  root `AGENTS.md` without reading the entire codebase.
- The README provides an accurate, polished open-source onboarding path.
- The asset manifest belongs to a neutral domain package and image/video code
  depend on it in the correct direction.
- New boundary and workflow tests pass alongside all existing validation.
- The final report clearly separates implemented maintainability improvements
  from unimplemented security recommendations.
