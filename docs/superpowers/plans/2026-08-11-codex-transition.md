# Codex Transition Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Align contributor guidance and public documentation with the repository, correct the asset-library dependency boundary, and produce a report-only threat model.

**Architecture:** Introduce `internal/library` as the neutral owner of manifest persistence and asset types, with OpenAI and video features depending on it. Keep runtime contracts stable, document the engine in root `AGENTS.md` and `README.md`, and capture security recommendations separately without changing security behavior.

**Tech Stack:** Go 1.22, standard library HTTP/filesystem packages, embedded Markdown/HTML assets, Gorilla WebSocket, YAML v3.

## Global Constraints

- Do not change public CLI command names or flags.
- Do not change HTTP paths or request/response contracts.
- Do not change `studio.yaml`, `deck.yaml`, slide, or manifest formats.
- Do not change serving-mode authorization behavior.
- Do not add a new runtime dependency or framework.
- Do not initialize a Vessica workspace.
- Do not implement threat-model recommendations.
- Do not broadly split `cmd/vstd` or `internal/server` in this pass.
- Preserve unrelated user files and repository history.

---

### Task 1: Extract the Shared Asset Library

**Files:**
- Create: `internal/library/manifest_test.go`
- Create: `internal/library/manifest.go`
- Modify: `internal/oai/oai.go`
- Modify: `internal/video/video.go`
- Modify: `internal/server/image.go`
- Modify: `cmd/vstd/main.go`

**Interfaces:**
- Produces: `library.Manifest`, `library.StyleFamily`, `library.Asset`, `library.VideoAsset`, `library.Load(string) (*Manifest, error)`, and `(*Manifest).Save(string) error`.
- Consumes: the existing `library/manifest.json` schema without any JSON changes.

- [x] **Step 1: Write failing manifest behavior tests**

Add table-focused tests proving missing-file defaults, valid mixed-asset loads,
contextual malformed-JSON errors, and pretty-printed save/load round trips.

- [x] **Step 2: Run the focused tests and verify RED**

Run: `go test ./internal/library -run 'TestLoad|TestManifestSave' -v`

Expected: compilation failure because `internal/library` does not yet expose
the tested types and functions.

- [x] **Step 3: Implement the minimal library package**

Move the existing manifest structs and persistence behavior into
`internal/library/manifest.go`. Preserve all field names and tags. Make `Save`
return JSON marshal errors rather than discard them, ensure its target directory
exists only when callers already supply it, and keep mode `0644`.

- [x] **Step 4: Run focused tests and verify GREEN**

Run: `go test ./internal/library -v`

Expected: all library tests pass.

- [x] **Step 5: Redirect consumers to the neutral package**

Update OpenAI image generation, video ingest/find, image upload, and CLI
manifest reads to use `internal/library`. Preserve return payload shapes and
manifest bytes semantically. Avoid compatibility aliases unless compilation
shows a consumer outside the identified files needs one.

- [x] **Step 6: Format and verify the refactor**

Run: `gofmt -w internal/library/*.go internal/oai/oai.go internal/video/video.go internal/server/image.go cmd/vstd/main.go`

Run: `go test ./internal/library ./internal/oai ./internal/video ./internal/server ./cmd/vstd`

Expected: all packages pass and no import cycle is introduced.

### Task 2: Add the Repository Contributor Harness

**Files:**
- Create: `AGENTS.md`
- Preserve: `codex/AGENTS.md`

**Interfaces:**
- Consumes: the checked-in package layout, Makefile targets, embedded workflow contract, and public compatibility constraints.
- Produces: root contributor instructions that apply to the whole repository.

- [x] **Step 1: Write root contributor guidance**

Document product purpose, package ownership, architectural dependency direction,
engine/content-repository distinction, file and security invariants, generated
files, focused test commands, and the complete validation gate. Explicitly point
deck authors to `codex/AGENTS.md` and `vstd skill`.

- [x] **Step 2: Check guidance against repository facts**

Run: `go list ./...`

Run: `git diff --check -- AGENTS.md codex/AGENTS.md`

Expected: every documented package exists, no downstream template changed, and
the new Markdown has no whitespace errors.

### Task 3: Test Agent Workflow Packaging

**Files:**
- Create: `plugin/plugin_test.go`

**Interfaces:**
- Consumes: `plugin.Names`, `plugin.Skill`, `plugin.Conventions`, and Codex launcher files in `codex/prompts`.
- Produces: a repository-level contract that every canonical skill can be loaded and every skill has a same-named Codex launcher.

- [x] **Step 1: Write the failing launcher parity test**

Use real embedded skills and real repository files. Resolve the repository root
from the test file, enumerate `plugin.Names()`, assert each skill is non-empty,
and require `codex/prompts/vstd-<name>.md` to exist. Verify conventions are
non-empty through `plugin.Conventions()`.

- [x] **Step 2: Run and verify RED**

Temporarily include a sentinel expected workflow name in the test so the test
fails with a missing embedded skill, proving the assertion detects drift.

Run: `go test ./plugin -run TestPackagedWorkflowsMatchCodexLaunchers -v`

Expected: FAIL naming the sentinel workflow.

- [x] **Step 3: Remove the sentinel and retain the real contract**

Keep only the observable parity checks derived from the embedded plugin and
repository launchers.

- [x] **Step 4: Run and verify GREEN**

Run: `go test ./plugin -v`

Expected: all plugin contract tests pass.

### Task 4: Rewrite the Public README

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: `cmd/vstd` usage, `studio.Config`, route registration, plugin packaging, Makefile, and the package architecture.
- Produces: an accurate open-source landing page with no new runtime contract.

- [x] **Step 1: Rewrite README around the actual product**

Cover value proposition, features, prerequisites, installation, quick start,
architecture, content model, command overview, modes, Claude/Codex use,
configuration, hosting, development, security posture, contributing, and
license. Identify optional dependencies such as Chrome and FFmpeg only where
the relevant features need them.

- [x] **Step 2: Validate commands and claims against source**

Run: `go run ./cmd/vstd help`

Run: `go run ./cmd/vstd skill`

Run: `git diff --check -- README.md`

Expected: documented commands and workflow names match actual output, and the
README has no whitespace errors.

### Task 5: Write the Report-Only Threat Model

**Files:**
- Create: `docs/THREAT_MODEL.md`

**Interfaces:**
- Consumes: current routes, auth gates, local shell execution, OAuth flow,
  webhook handling, external providers, storage, filesystem paths, and agent
  worker behavior.
- Produces: prioritized recommendations only; no production code or configuration changes.

- [x] **Step 1: Map assets, actors, entry points, and trust boundaries**

Describe local and public deployment assumptions, presenter/audience roles,
credential and PII assets, and boundaries to GitHub, OpenAI, Telnyx, Resend,
S3-compatible storage, Git, Chrome, Claude CLI, and the local filesystem.

- [x] **Step 2: Record threats and current controls**

Use a STRIDE-oriented table with concrete code-path evidence. Separate existing
mitigations from gaps and avoid describing recommendations as implemented.

- [x] **Step 3: Prioritize recommendations**

Assign priority, likelihood, impact, proposed mitigation, and verification to
each recommendation. Include webhook authenticity, CSRF/origin controls,
content isolation/CSP, public-mode secret fail-closed behavior, subprocess
boundaries, path containment, OAuth flow lifecycle/rate limits, security
headers, upload/egress controls, audit logging, and dependency scanning where
supported by code evidence.

- [x] **Step 4: Check report-only boundary**

Run: `git diff --name-only`

Expected: no security implementation or deployment configuration file was
changed for a threat-model recommendation.

### Task 6: Full Verification and Review

**Files:**
- Review all modified files.

**Interfaces:**
- Consumes: all prior tasks.
- Produces: fresh verification evidence and a clean scoped diff.

- [x] **Step 1: Format check**

Run: `test -z "$(gofmt -l $(find cmd internal plugin -name '*.go' -type f))"`

Expected: exit 0 with no output.

- [x] **Step 2: Run tests and race detector**

Run: `go test ./...`

Run: `go test -race ./...`

Expected: all packages pass.

- [x] **Step 3: Run static analysis and build**

Run: `go vet ./...`

Run: `go build ./cmd/vstd`

Expected: both exit 0.

- [x] **Step 4: Review scope and documentation**

Run: `git diff --check`

Run: `git status --short`

Review the diff against every global constraint and success criterion. Remove
the generated root `vstd` binary if the build updates it; it is ignored and
must not be included in the change set.
