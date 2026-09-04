# Vessica Studio Deployment and Release

- Status: `Tag-driven public release process`
- Owner: `Matthew Kropp`
- Last verified: `2026-09-03`
- Scope: `Public vstd binaries/module/plugin artifacts; no private SaaS deployment`

## Environments

| Environment | Purpose | Trigger | Authority |
| --- | --- | --- | --- |
| Local checkout | Development and verification | Developer command | Contributor |
| GitHub release | Public source/binary/plugin distribution | Approved version tag/release workflow | Repository owner |
| Downstream Railway service | Optional user-managed server deployment | Downstream repository configuration | Downstream owner |

The private Vessica Studio Cloud control plane is deployed from its own private
repository and is outside this repository's deployment authority.

## Build Artifact

The repository builds the `vstd` Go executable and packages the canonical plugin
from source. Releases are identified by semantic-version tags and immutable Git
revisions; consumers such as Vessica Studio Cloud pin both.

## Configuration and Secrets

Core builds require no production secret. Optional OpenAI, S3, Git, Railway, and
cloud-client credentials are resolved at runtime from approved environment or OS
credential stores and never baked into artifacts.

The Cloud endpoint defaults to `https://cloud.vessica.studio` and may be
overridden at runtime with `VSTD_CLOUD_ENDPOINT`. Cloud use requires a supported
OS credential store; this does not affect installation or local-only commands.

## Deployment Preconditions

- Clean, reviewed source on the intended release commit.
- Full repository gate and packaging/parity tests pass.
- Public CLI help, README, plugin workflows, and compatibility notes agree.
- The public client protocol and fixtures are aligned with the private service's
  approved auth, capability, revision, and publication contracts.
- No credentials, generated deck builds, runtime state, or local media bytes are
  included.
- Explicit owner approval exists for tagging or publishing.

## Deployment Procedure

Use the repository's existing tag-driven GitHub release workflow and documented
release commands. Do not publish from an Agent Harness feature run; its PR must
be reviewed and merged first. Record the tag, source commit, checks, and artifact
identities.

## Database and State Changes

This public engine owns no SaaS production database. File-format or local-state
migrations must be backward-compatible or explicitly versioned, tested with real
fixtures, and documented before release.

## Post-Deployment Verification

Confirm the GitHub release is terminal-successful, artifacts resolve from a clean
environment, `vstd version` maps to the intended tag/commit, plugin packaging is
intact, and local-only commands work without cloud credentials.

## Rollback and Recovery

Do not move or overwrite an immutable published tag. Fix forward with a new
release, or direct consumers back to a previously verified tag/revision. Preserve
the failed artifact and logs for diagnosis.

## Deployment Authority

Only the repository owner may approve a public release, tag, or downstream
production deployment. Harness execution authorizes scoped code, commits, push,
and PR creation, but not merge, release publication, or product deployment.
