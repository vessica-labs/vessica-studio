# Isolated browser editing transport (protocol 1)

`vstd editor-session --root DIR --deck NAME --port 8080 --lifetime 30m`
serves the existing engine-owned player, HUD and structured editing API on
127.0.0.1. The materialized studio must contain exactly the selected deck and
pass the canonical Cloud content validator. The maximum lifetime is one hour;
the process closes its listener and active connections when it expires.

The supervisor supplies `VSTD_EDITOR_TOKEN` (32–256 non-whitespace characters)
in the process environment. Every HTTP request, including documents, assets,
events and snapshots, requires `Authorization: Bearer TOKEN`. The token belongs
only to the trusted gateway; never send it to the browser, put it in command
arguments, or store it in the content directory. Authentication failures return
401 without exposing content. The gateway strips caller-supplied authentication
headers before adding its credential.

This transport is not a tenant authorization service. The SaaS host must run it
in a disposable isolated sandbox with no provider, account, Git, database or
object-storage credentials and must authorize each browser operation against
current identity, workspace membership and presentation access. Expose the
browser player on a credential-isolated origin, never the application origin.
Do not forward the app session cookie or native credentials to presentation HTML.

The player entry is `/d/NAME/`. `/api/me` reports `start_editing: true`, so the
existing HUD enters edit mode without query parameters. Local and read-only
players are unchanged. The allowlist exposes structured slide/companion/title,
new-slide/move, attachments, media, export, status and event routes. Account,
sharing, Git collaboration, agent and external-provider routes are excluded;
those require separate authorized host integrations. This command starts no
Git sync, collaboration store or agent worker.

`GET /api/app/decks/NAME/thumbnail.png` exposes only the selected deck's cached
first-slide PNG through the existing renderer. It requires the same session
credential, rejects other decks, and grants no catalog mutation access. Hosts
can generate and retain a small derivative asynchronously without loading a
full presentation document in each catalog card.

`GET /api/editor/snapshot` returns JSON:

```json
{"protocol":1,"digest":"<canonical SHA-256>","files":[{"path":"studio.yaml","content":"<base64>","mode":420}]}
```

Files and the digest come directly from the existing `studio.CloudContent`
contract. Generated builds, runtime state, association files and credentials are
excluded. Snapshot reads and normal requests are serialized inside the engine;
SSE stays independent and is cancelled at expiry. Requests have a 16 MiB body
limit. Snapshot validation failure returns 500 without a partial snapshot.

A successful mutation means the **sandbox files** changed. The gateway MUST
serialize writes for the session, fetch and validate its snapshot, and commit
through the authoritative optimistic revision service against the session's
recorded base before forwarding a successful save response to the browser.
On a stale base, failed commit or uncertain result, retain the recoverable
snapshot and show an explicit unsaved/conflict state. Never overwrite another
revision or publish implicitly. A browser reload must use the last committed
revision. Terminate/revoke the runtime on signout, membership loss, expiry or
explicit close; provider teardown failures require durable reconciliation.

Cloud must consume a released immutable engine pin that includes this command.
The public engine does not create Cloud sessions or authorize publication.
