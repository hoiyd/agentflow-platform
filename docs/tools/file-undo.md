# Conflict-safe File Undo

Undo restores one existing report changed by `file_write` replacement or
`file_patch`. It is an explicit owner action in **Run Replay -> File changes**,
not a model Tool or Run Resume. New-file deletion, source trees, directories,
batch rewind and external API/database rollback are outside this boundary.

## Enable

Undo retention is **off by default**. In the existing
[filesystem root map](scoped-filesystem-writes.md#enable-and-use), explicitly
authorize both a retention period and a per-root byte budget:

```json
{"roots":[{
  "id":"reports","workspace_id":"42","path":"/srv/agentflow/reports",
  "write_access":"managed_output",
  "undo_retention_seconds":86400,"undo_max_bytes":1048576
}]}
```

Retention accepts 1 second to 30 days; budget accepts 1 byte to 16 MiB, with at
most 256 retained changes per root. The budget includes serialized metadata and
reserved compensation metadata; each file remains bounded to 64 KiB UTF-8 and
each retained JSON record to 128 KiB. A full budget rejects replacement **before
publication**, rather than writing without the promised before-image.

With an Undo-enabled root, the `file_write`/`file_patch` capability declares
`reversibility: compensatable`. Update their operator rules to that capability,
retain explicit Workspace grants and Agent bindings, then restart. Root/grant
changes invalidate affected frozen definitions; existing changes without a
before-image do not gain Undo retroactively. Read-only/unconfigured roots gain
neither retention nor compensation authority.

## Storage and Retention

Before publication, save the necessary old text, hashes, original file identity,
Run/Turn/Stage/call identity and fixed expiry in a `0600` record under the root's
private `0700` `.agentflow-undo/` directory. Ordinary filesystem Tools cannot list,
read or search it. Effect receipts and previews contain metadata, not that copy
of the text; existing Chat/Tool argument/read tracing retains its own policies.

Expiry is checked before use and never extended by previews or Undo. Expired
records are deleted lazily during retained writes, previews and compensation;
an inactive root may retain expired bytes until next access. There is no periodic
erasure guarantee. To discard images immediately, stop managed writers and remove
the reviewed private records offline; historical receipts remain, but their Undo
becomes unavailable. Settle uncertain compensation before removing its evidence.
Crash-left staging files fail closed for operator inspection, not automatic replay.

This storage is local to the authorized output root, not encrypted by the
application. Operator consent includes retaining a second plaintext copy; OS
access controls and volume protection remain deployment responsibilities.

## Preview and Execute

Only completed, failed or canceled Runs can be undone; finish or cancel active,
waiting or recoverable execution first. The preview shows relative resource,
written/restore hashes and sizes, expiry and any blocking reason. It never returns
before-image text. Confirm with a reason; the API records the authenticated owner
as actor, regardless of the client-supplied actor label.

`GET /api/runs/{id}/tool-effects/{key}/compensation-preview` returns this metadata.
Execution reuses `POST .../reconcile` with `action: compensate`, a unique
`command_id`, the current `expected_version`, actor and reason. Existing owner /
Workspace authorization, service/Workspace Tool grants and matching definition
revision apply to both requests. Preview success is not a durable write grant.

Under the existing output-root lock, recheck root authorization, current file
hash **and object identity**, and before-image integrity. A later writer, even
one producing identical bytes in a different inode, prevents overwriting. Stage
the restore, durably record its identity, then atomically rename and sync the
directory. Success settles the original Effect as `compensated`; its old Tool
Call cannot replay a now-undone result. Run status and conversation stay unchanged.

## Uncertain Outcomes

Undo uses the existing versioned Effect claim and typed reconciliation events.
Duplicate commands return recorded outcomes; competing commands cannot both
enter the write callback. Proven pre-publication rejection preserves the original
`committed` receipt. After compensation intent/publication may have occurred,
failure stays uncertain and does not certify success.

In **Tool effect reconciliation**, `verify_compensation` / **Verify restored file**
only checks the retained restore inode/hash and directory durability, then settles
`compensated`. It never reapplies old text, even after restart or failed event
settlement. Missing/expired/drifted evidence requires operator disposition; a
timeout does not release the claim for another write. This is compensation, not
a transaction with Postgres, filesystem CAS against noncooperating editors, or
exactly-once execution. Dedicated roots require cooperating writers.

## Failure Inventory

| Failure | Required outcome |
| --- | --- |
| No explicit retention grant, create-only operation, expired/missing before-image | Undo unavailable; never infer deletion or retention permission |
| Incomplete/out-of-range retention settings or read-only root | Reject configuration; never infer usable retention defaults |
| Human edit, another Run's write, replaced inode, symlink, changed/revoked root | Conflict; preserve current bytes |
| Foreign owner, archived Workspace, revoked Tool or changed definition | Reject through existing resource/policy checks |
| Full retention budget, short write, disk full, sync/intent failure | Reject before publication; leave target unchanged |
| Cancellation before publication | No write; do not automatically reclaim an uncertain callback |
| Compensation publishes but journal/event settlement fails | Retain compensation identity; query-only verification, never blindly write again |
| Duplicate/concurrent Undo | One durable claim; repeated command returns its recorded outcome |

## Verification

From `apps/api`, using the repository Go version and a dedicated disposable
`TEST_DATABASE_URL`:

```bash
go test -race ./internal/tool/filesystem ./internal/httpapi \
  -run 'TestFilesystemUndo|TestFileUndoHTTP' -count=1
```

From the repository root:

```bash
AGENTFLOW_FILESYSTEM_WRITE_TEST=1 bash scripts/test-browser.sh filesystem-write.spec.ts
```

The focused production-composition gate checks all three modes, owner-scoped
HTTP, Undo confirmation, reload, real `file_read` after restoration and refusal
after another Run writes. It retains synthetic desktop screenshots and JSON
identity/configuration/effect evidence. Backend integrations cover retention,
tampering, races, disk/sync/cancellation faults and compensation-settlement loss.
These are deterministic provider fixtures, not live-model quality evidence or a
full deployment recovery drill. No database schema change is needed.
