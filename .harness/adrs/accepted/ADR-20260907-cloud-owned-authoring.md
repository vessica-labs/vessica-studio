# Cloud-owned authoring and isolated writer reconciliation

- Status: Accepted by owner request, 2026-09-07
- Applies to: reconciliation, file identity/order, browser persistence, worktrees, native sync, plugin workflows
- Supersedes: ADR-VES-13's stale-head rejection and manual conflict-resolution workflow only

The owner explicitly authorized a clean break, removal of GitHub presentation
storage, implementation, public release, and Cloud deployment. Existing
presentations do not require migration or backward compatibility.

The public engine owns a deterministic three-way merger over the existing
HTML/Markdown/YAML file contract. HTML objects have persistent IDs; new objects
receive new IDs. Slide filenames stay fixed during reordering; `slide_order` in
deck metadata records order. The merger combines independent properties,
sections, log appends, and order operations. Direct human edits take precedence
over overlapping agent edits. A conflict cannot preserve two different visible
values in one property: both original snapshots are retained, while the policy
selects the visible value. Paired-slide deletion/edit decisions are atomic.

Cloud calls the pinned public `vstd reconcile` CLI; it must not copy engine
semantics. It owns Postgres journals and head advancement, private immutable
objects, authorization, integration recovery, and disposable hosted worktrees.
GitHub remains a source-code/release provider, not a presentation data provider.

The browser records mutations in an IndexedDB outbox before transmitting them.
Hosted outboxes live on the stable application origin and are scoped to a
workspace/presentation. Cloud acknowledgement follows durable persistence;
device-only persistence is labelled separately. Stable operation IDs make
retries safe across sessions. The canvas refreshes when idle without forcing a
reload or replacing active edits.

Native sync preserves local checkpoints before network calls and applies the
canonical reconciled result. `vstd serve` synchronizes connected workspaces in
the background. Plugin workflows sync before reading/opening Cloud content and
use `vstd worktree begin/finish` for edits. Local agents run in isolated worktrees
and reconcile their delta against the current parent. Local-only workflows
remain account-free and network-free. Kernel file locks serialize local engine
writes and projection replacement; direct third-party filesystem writers must
use an isolated worktree to participate safely.

Version 0.6.0 introduces the new contract. Cloud rejects older native clients.
Publication remains an explicit, separate action on an immutable revision.

Verification includes semantic merge tests, real-file worktree tests, browser
offline/refresh tests, and Cloud PostgreSQL concurrency/replay/recovery tests.
