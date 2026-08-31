# AGENTS Improvement Spec

## Scope

This spec reviews the repository agent instructions in `AGENTS.md`, the
downstream content-repo guide in `codex/AGENTS.md`, and the embedded workflow
skills in `plugin/skills/*/SKILL.md` plus their shared reference
`plugin/docs/conventions.md`.

No `.ona/skills/` or `.cursor/rules/` directories were present in this
repository at review time.

## What Is Good

- `AGENTS.md` clearly separates engine work from downstream deck-content work.
  That distinction protects the engine repository from accidental slide-authoring
  changes and tells agents where the canonical authoring workflows live.
- The architecture map is useful and concrete. It names package ownership,
  expected dependency direction, and compatibility surfaces instead of giving
  vague project background.
- The invariants are strong. They explicitly protect the deck file contract,
  single-root slide fragments, engine-owned player/HUD, read-only modes,
  generated output, and secret handling.
- The development workflow gives focused package tests and a full completion
  gate. That gives agents a practical path for both iteration and final checks.
- The skill files are compact and action-oriented. Each skill states when it
  should be used and delegates shared standards to `plugin/docs/conventions.md`,
  which reduces drift.
- The conventions document contains valuable domain rules that are easy to test:
  companion-first editing, the hybrid chart structure, asset/library behavior,
  video references, source attachments, linked-slide handling, and critic-loop
  expectations.
- `codex/AGENTS.md` is intentionally thin as a distribution artifact, which
  matches the root guide's warning not to duplicate the full engine architecture
  there.

## What Is Missing

- There is no quick "choose your workflow" section in `AGENTS.md`. New agents
  have to infer whether they should follow engine instructions, downstream
  content instructions, or embedded skill instructions.
- The root guide does not state the expected baseline inspection commands for a
  dirty worktree, such as checking `git status --short` before editing and
  avoiding generated paths during searches.
- The full completion gate is expensive, especially `go test -race ./...`.
  `AGENTS.md` does not say what to do when a change is documentation-only,
  when optional tools are unavailable, or when the full gate is too slow for a
  small scoped task.
- The root guide tells agents to update `README.md` for user-facing changes but
  does not define which other public artifacts may need updates, such as CLI
  help text, templates, embedded skills, or downstream launcher prompts.
- Skill files mention `AskUserQuestion`, which is tool-specific wording. The
  instructions should use tool-agnostic language so Claude, Codex, and future
  agents can execute the same canonical content without translation.
- The skills require reading `../../docs/conventions.md`, but there is no
  validation that every skill keeps this path accurate and no fallback guidance
  if the skill is loaded through `vstd skill` rather than directly from the file
  tree.
- The critic loop requires screenshots and sometimes independent review, but
  the skills do not define a minimum acceptable fallback when Playwright,
  Chromium, or a running engine is unavailable.
- Source-backed slide instructions require preserving and comparing source
  artifacts, but the skills do not define accepted render tools or failure
  reporting when PDF/PPT rendering dependencies are absent.
- Linked-slide sidecar rules exist in conventions, but the individual edit,
  add, fork, and review skills do not remind agents to detect `.link.yaml`
  before mutating a slide.
- There is no explicit parity checklist for keeping root `AGENTS.md`,
  `codex/AGENTS.md`, skill descriptions, `plugin/docs/conventions.md`, and tests
  aligned when workflow behavior changes.

## What Is Wrong

- The root guide says "Claude reads them through the plugin" while the repo also
  ships Codex launchers. This is historically useful context, but as written it
  makes the canonical workflow sound partly agent-specific.
- The skill descriptions use "Vessica Studio / vstd" wording inconsistently.
  Some skills name Vessica, others mainly name deck actions. This can reduce
  reliable skill selection in systems that depend on descriptions.
- `deck-new` says to "Build (`vstd build <deck>` or note the engine
  auto-builds), tell the user the deck URL (`localhost:4400/d/<deck>/`)". A
  build alone does not guarantee a server is running at that URL.
- `deck-review` says to "Fix trivial issues directly" but does not bound that
  authority against user-requested report-only reviews. The root guide has a
  security-review warning, but the deck-review skill should carry the same
  consent boundary for non-security review work.
- The instructions use "Matt's call" in the deck-review report categories. That
  is too person-specific for a reusable embedded workflow and should be replaced
  with "user decision" or "owner decision".
- The conventions say "For high-stakes decks run the critic as a separate
  subagent", which assumes subagent availability. It should be phrased as an
  optional independent review step with a fallback.
- The conventions list `vstd asset add-video` in the video section, but the
  engine surface summary omits `asset add-video`. Agents using the summary could
  incorrectly assume the command is unsupported.

## Improvement Spec

### 1. Add a Workflow Router to `AGENTS.md`

Add a short section near the top:

- For engine changes, follow root `AGENTS.md`.
- For deck-content work in this repo, stop and confirm because this is not a
  content repository.
- For downstream content repositories, use `codex/AGENTS.md` and load the
  relevant `vstd skill <name>` workflow.
- For embedded workflow changes, edit `plugin/skills/*/SKILL.md` or
  `plugin/docs/conventions.md` first, then update packaging tests and downstream
  launcher text only as needed.

Acceptance criteria:

- A new agent can decide which instruction source applies without reading the
  whole document.
- The section does not duplicate full skill instructions.

### 2. Add a Baseline Safety Checklist

Add a compact checklist to `AGENTS.md` before the development workflow:

- Run `git status --short` before editing.
- Identify whether the change touches public compatibility surfaces.
- Avoid generated/runtime paths unless explicitly working on fixtures.
- Read the nearest relevant package tests before changing behavior.
- Preserve user changes in a dirty tree.

Acceptance criteria:

- The checklist is under 10 bullets.
- It reinforces existing invariants without replacing them.

### 3. Clarify Verification Tiers

Replace the single all-or-nothing completion gate with tiered guidance:

- Documentation-only: run `git diff --check`; run package tests only when docs
  are generated, embedded, or validated by tests.
- Focused code change: run the nearest package tests plus formatting checks.
- Public API/CLI/server/deck-format change: run focused tests, relevant
  integration tests, update docs, then run the full gate.
- Release-risk or broad refactor: run the full gate including race tests.

Keep the current full gate as the top tier.

Acceptance criteria:

- The current full gate remains documented.
- Agents have explicit guidance for small changes and unavailable optional
  dependencies.

### 4. Make Skill Language Agent-Neutral

Update `plugin/skills/*/SKILL.md` and `plugin/docs/conventions.md` to avoid
tool-specific assumptions:

- Replace `AskUserQuestion` with "ask the user".
- Replace "separate subagent" with "independent review pass, using a subagent if
  the environment supports it".
- Replace "needs Matt's call" with "needs user/owner decision".

Acceptance criteria:

- No embedded workflow requires a named agent product or tool to understand the
  instruction.
- Existing behavior remains unchanged.

### 5. Tighten Server, Screenshot, and Source Fallbacks

Add fallback rules to conventions:

- Only provide a localhost deck URL after confirming the server is running.
- If the engine is not running, report the built artifact path or the command to
  serve it instead of implying a live preview.
- If Playwright/Chromium is unavailable, perform static checks, run build/tests,
  and record that visual QA was not completed.
- If PDF/PPT rendering is unavailable for source comparison, preserve the source
  attachment, update the companion log, and report the missing dependency.

Acceptance criteria:

- `deck-new`, `slide-add`, `slide-edit`, and `deck-review` all have a clear
  completion path when optional visual tooling is absent.
- The critic loop still treats real screenshots as the preferred path.

### 6. Surface Linked-Slide Protection in Edit Workflows

Add a short instruction to `slide-edit`, `slide-add`, `deck-fork`, and
`deck-review`:

- Before mutating a slide, check for a matching `.link.yaml` sidecar.
- Do not edit linked target fragments, companions, or attachments directly.
- Refresh from the source or detach before editing.

Acceptance criteria:

- Linked-slide safety is visible in the workflows where mutation can happen.
- The conventions document remains the detailed source of truth.

### 7. Add Skill/Convention Consistency Tests

Extend `plugin/plugin_test.go` or add a focused test file that checks:

- Every `plugin/skills/*/SKILL.md` has frontmatter `name` and `description`.
- Every skill name is exposed by `vstd skill <name>` or the plugin registry.
- Every skill that requires conventions references the same canonical
  conventions path or embedded command.
- `plugin/docs/conventions.md` mentions all commands used as canonical workflow
  surface, including `vstd asset add-video`.
- `codex/AGENTS.md` lists the same public skill names as the embedded plugin.

Acceptance criteria:

- A workflow rename or missing packaged skill fails tests.
- Tests assert public artifacts and packaging parity, not incidental prose.

### 8. Normalize Skill Descriptions

Update each skill frontmatter description to include:

- The workflow object: deck, slide, review, fork, or market/data refresh.
- The Vessica/vstd context.
- Common user verbs that should trigger the skill.

Acceptance criteria:

- Descriptions remain one paragraph each.
- Skill selection works for both product-specific requests and generic deck
  authoring requests.

## Suggested Implementation Order

1. Update `AGENTS.md` with the workflow router, baseline checklist, and
   verification tiers.
2. Apply agent-neutral wording changes across skills and conventions.
3. Add linked-slide and fallback reminders to the relevant skill files.
4. Update command-surface wording in conventions.
5. Add or extend plugin parity tests.
6. Run focused tests: `go test ./plugin`.
7. Run final checks appropriate to the actual change scope.
