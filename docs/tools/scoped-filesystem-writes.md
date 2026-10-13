# Scoped Filesystem Writes

`file_write` and `file_patch` save bounded UTF-8 reports in operator-owned,
dedicated output roots. They share the read Tools' path boundary and the existing
Executor, Effect Journal and Reconciliation surface; sbx still owns untrusted
command execution. No source-tree, deletion or execution permission is implied.

## Failure Inventory

| Failure | Required outcome |
| --- | --- |
| Read-only/unmapped root, foreign/inactive Workspace, missing execution owner or policy | Reject before publication; never widen permissions |
| Traversal, sensitive filename, executable/binary file, links or nonregular target | Reject without changing target bytes |
| Existing create target, stale hash or ambiguous patch | Typed conflict; preserve existing content |
| Concurrent managed writers | Serialize under the root lock; only one stale-hash update succeeds |
| Short write, disk full, temp sync or intent persistence failure | No target publication; confirmed failure where durable settlement is available |
| Cancellation before publication | No publication; uncertain if execution ownership/settlement was already lost |
| Publication succeeds but directory sync, journal/event persistence or worker fails | Keep evidence; require reconciliation, never blindly rewrite |
| Restart, duplicate call or repeated recovery | Committed calls replay receipts; recovery checks file identity/hash and does not reapply changes |
| Root revoked/replaced or target changed before recovery | Fail closed; preserve uncertain status for operator disposition |

## Initial Boundary

Only flat text reports in a root explicitly marked `write_access: managed_output`
are writable. That root must be private to the API OS user and exclusively managed
by cooperating AgentFlow writers. Root-level advisory locking protects cooperating
processes, not editors or privileged/noncooperating host processes. Shared project
roots are read-only; this is not cross-process filesystem CAS or exactly-once.

The root must have no group/other permission bits (normally `0700`); created files
are `0600`. Only flat, non-hidden `.md`, `.txt`, `.json`, and `.csv` names are
accepted. Existing executable, binary, linked, sensitive, or oversized targets
cannot be updated. Subdirectories, shared source-tree updates, undo and deletion
are excluded. Writable roots cannot overlap another Workspace's configured roots.
Filenames cannot contain control characters or recognized credential values.
Existing read-only configurations do not gain write access.
Keep operational configuration and credentials outside output roots; the root-map
file itself is explicitly rejected there at startup.

## Enable and Use

Follow the [read configuration and grants](scoped-filesystem.md#enable-and-use),
adding a dedicated private output directory to the same operator root map:

```json
{"roots":[{"id":"output","workspace_id":"42","path":"/srv/agentflow/reports","write_access":"managed_output"}]}
```

Add `file_write` and `file_patch` to operator `enabled_tools`, with an exact
`allow_and_log` rule for each. Their capability differs from the read Tools:

```json
{
  "id":"operator-file-write","tool":"file_write","action":"allow_and_log",
  "capability":{
    "source":"local",
    "scope":{"resources":[{"kind":"filesystem","name":"project_outputs","access":"write"}],"network":{"mode":"none"}},
    "side_effect_class":"external_write","rate":"run_budgeted","reversibility":"irreversible",
    "visibility":"user","approval_mode":"none","audit_level":"full"
  }
}
```

Restart the API, allow the Tools in the owning Workspace and bind them to an
owned Agent. Enable `file_read` separately for verification. Neither Agent prompts,
Skills nor owner toggles can expand the operator root map. `ask` still returns an
approval requirement; this feature does not implement interactive approval.

- `file_write` with `root`, `path`, `content` **creates only**. Existing targets
  return `filesystem_write_conflict` without changing bytes.
- To replace, read first and pass `expected_hash=content_hash` with new `content`.
- `file_patch` requires `expected_hash`, `old_text`, `new_text`; exactly one literal
  match is replaced. Missing/ambiguous text or a stale hash is a typed conflict.
- Results contain operation, relative identity, root revision, file ID, old/new
  raw hashes and bytes. Use `file_read` to verify; filesystem text remains untrusted
  data, not automatic citation evidence or instructions.

Each submitted/resulting file is bounded to **64 KiB UTF-8 bytes**; existing files
larger than that cannot be updated even if readable. Execution uses the existing
5-second Tool timeout and Run Budget. Recognized credentials are rejected, not
silently rewritten; detection is pattern-based, not complete secret prevention.
Cancellation is cooperative and cannot forcibly interrupt a stalled OS syscall.

## Publication and Recovery

1. Executor authorization/audit and journal preparation require Run, real Turn
   or Stage, and Tool Call identity. Single never fabricates a Stage.
2. Under the output-root lock, inspect target hash/object identity, stage the
   complete new text in a same-directory private file and sync it.
3. Persist bounded hash/identity evidence in the existing `tool_effects.result`
   column **before** publication. No source text or host path is in this receipt.
4. Recheck current root authorization, target identity/hash and cancellation.
   Publish create-only with kernel no-replace rename; updates use atomic rename
   under the exclusive managed-writer contract. Sync the directory, then let the
   Executor commit its normal result to the journal.

Pre-publication errors settle as `failed` only while durable execution ownership
is retained. The Tool loop permits argument correction only after reading that
durable failed settlement, not based on a validation-looking error. Cancellation,
publication I/O errors and failed post-publication settlement remain uncertain.

Duplicate committed calls replay the receipt, not the write. An uncertain call
cannot run again. In Replay's existing Tool Effects controls, `retry_with_same_key`
is deliberately a **query-only verification** for these bindings: check the saved
target identity/hash and directory durability; do not recreate missing files or
reapply a patch. Matching evidence commits the receipt. Missing/changed files,
revoked roots, incompatible definitions or missing intent stay fail-closed for
manual `confirm_committed`/`confirm_failed` disposition with actor/reason/version.
Recovery claims and failed checks retain evidence for subsequent attempts.

Committed file output alone does not prove an unfinished Stage completed; existing
checkpoint/Resume restrictions remain in force. Atomic rename is not a transaction
with Postgres and does not establish exactly-once. Kernel advisory locks require
cooperating writers ([flock contract](https://man7.org/linux/man-pages/man2/flock.2.html));
do not point an output root at a concurrently edited/shared source tree.

Unpublished staging files are cleaned up on ordinary completion/cancellation and
excluded from read Tools. A hard process kill before publication may leave hidden
staging files; they are not exposed or automatically replayed. After settling the
related receipts, an operator may remove orphan `.agentflow-*.tmp` files offline.
This is not a filesystem retention/quota service or automatic undo.

The standard redacted/bounded Tool argument trace can include submitted text;
hash-only receipts do not promise that prompts/arguments are never retained.
Existing private-data egress restrictions apply after filesystem Tools. Root-map
changes invalidate frozen definitions as described in the read contract.

## Validation and Rollback

```bash
# From apps/api with the repository Go version activated:
TEST_DATABASE_URL='<disposable-test-postgres-url>' \
  go test ./internal/tool/filesystem -run 'TestFilesystemWrite' -race -count=1

# From repository root:
TEST_DATABASE_URL='<disposable-test-postgres-url>' AGENTFLOW_FILESYSTEM_WRITE_TEST=1 \
  bash scripts/test-browser.sh filesystem-write.spec.ts
```

The focused browser gate uses production composition, real files, disposable
Postgres and a strict deterministic provider in Single/Multi-Agent/Autonomous.
It retains `filesystem-write-evidence.json` with tasks, Run/Workspace IDs, frozen
configuration, Tool events/effects, checks and limitations, plus a desktop screenshot.
A Skill-bound reader must still have no write schema. Fault integrations cover
the inventory above, including an actual worker kill at the publication/settlement
boundary; they invoke the existing orphan-marking Store operation explicitly,
not a full deployment recovery drill. This does not measure live-model quality.

No DB schema migration, new endpoint, DTO or second recovery state machine is
introduced. Disable write Tools to revoke access; retain historical receipts,
events and files. Settle pending effects before removing roots/bindings needed to
verify them. Removing `write_access` changes definitions; old Runs must not silently
resume against new grants.
