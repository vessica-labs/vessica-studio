# Architecture Decision Index

Read this index before opening ADR bodies. Apply only records whose status is `Accepted`, that are not superseded, and whose applicability intersects the paths, components, interfaces, or concerns affected by the current change.

| ADR | Status | Applies to | Supersedes | Summary |
| --- | --- | --- | --- | --- |
| [ADR-20260907](accepted/ADR-20260907-cloud-owned-authoring.md) | Accepted | Reconciliation, persistent identity/order, browser saves, worktrees, native sync, plugin workflows | Manual conflict-resolution portions of ADR-VES-13 | Cloud-owned journals and snapshots, automatic reconciliation, and isolated local authoring |
| [ADR-VES-13-native-cloud-client](accepted/ADR-VES-13-native-cloud-client.md) | Accepted | `cmd/vstd` cloud commands; `internal/cloud`, `internal/cloudauth`, `internal/cloudworkspace`, `internal/cloudpublish`; studio cloud content projection; canonical plugin workflows | None | Defines the versioned, local-first native Cloud boundary, secure sessions, explicit-base synchronization, and revision publication. |

Runtime-injected ADR context may appear elsewhere in this directory and remains ignored by Git. Durable records produced by the documentation stage belong in `accepted/` and must be added to this index.
