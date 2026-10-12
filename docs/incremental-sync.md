# Incremental native sync (Protocol 1)

The optional `workspace.sync.incremental` capability extends Protocol 1 without
changing its existing full-snapshot endpoints. Clients must negotiate it before
using these routes. Servers without it continue to receive full snapshots.

- `GET /v1/workspaces/:workspace/revisions/:revision/manifest` returns
  `{id, workspace_id, files: [{path, sha256}]}` without file contents.
- `POST /v1/workspaces/:workspace/revisions/incremental` accepts the normal sync
  attribution (`writer_kind`, `base_revision_id`, `operation_id`, `message`) and
  a **complete** `files` manifest. Every entry has `path` and lowercase hex
  SHA-256. New or changed files include base64 `content`; unchanged entries omit
  it. Empty files include `content: ""`. Modes retain the legacy adapter behavior.
- References resolve only by matching path and SHA-256 in the authorized exact
  base revision. There is no global digest lookup. Omitted paths are deletions;
  this is a complete snapshot represented compactly, not a partial file patch.
- The server reconstructs the snapshot before using the existing validator,
  durable operation identity, reconciliation and canonical commit path. A stale
  base still participates in the existing three-way reconciliation.
- Successful sync returns the canonical revision with the same transfer-file
  representation. Bytes matching the submitted manifest are omitted. Concurrent
  server edits include their bytes. Clients verify all resulting hashes, missing
  references, duplicate paths and reconstructed size bounds before applying.
- `POST /v1/workspaces/:workspace/revisions/:revision/transfer` accepts
  `{files: [{path, sha256}]}` describing a captured local snapshot and returns the
  requested complete revision, omitting bytes that match those references.
  A known file is only a transfer hint, never authority to read other resources.

Read routes retain `workspace.read` bearer authorization; sync retains
`workspace.sync`. Resource membership and permissions are revalidated. The
4096-file, 16 MiB/file, 128 MiB decoded-snapshot and 192 MiB JSON bounds remain.
A changed large file transfers in full; unchanged files require hashing locally
and on the server but no asset bytes cross the client network.

Sync captures the local bytes before networking. Edits made during transfer
remain subject to the existing checkpoint/merge contract. Transport or integrity
failure must leave the association and local edits intact. Once a mutation was
attempted, clients must not silently retry with another protocol or operation ID.
Identical retries reconstruct the same snapshot from the immutable base and use
existing durable replay handling.

## Rollout

Release the public client from this repository, then consume its immutable
version and checksums through Cloud's normal engine dependency process. Deploy
Cloud's optional routes/capability before upgrading clients to avoid a gap;
older clients keep using the existing routes. The server transport adapter itself
uses the existing pinned engine validation/reconciliation contracts and does not
require a new engine command. No schema migration is required. Verify the actual
presentation's sync before claiming it is Cloud-confirmed.

For publication workflows that intentionally leave Cloud-only media off the
local device, incremental sync also accepts `response_mode: "manifest"`. Its
response contains no file bytes. `Client.SyncManifest` returns this immutable
canonical manifest for verification and explicit-revision publication. It must
not be applied locally or used to mark the local workspace clean. The default
`files` response and ordinary workspace sync behavior are unchanged.

Native publication requests can explicitly include `visibility: "public"` to
publish a Public release. Omitting it retains the existing Unlisted default.
Clients must request the intended visibility explicitly when republishing a
Public deck; older servers reject the additional field rather than silently
changing the requested audience policy. A completed Public publication returns
a stable URL without an unlisted bearer selector.
