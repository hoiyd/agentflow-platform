# Scoped Filesystem Reads

`file_list`, `file_search` and `file_read` read operator-approved project roots.
They use fixed Go operations, not sbx, Shell or model-generated code. sbx remains
the separate boundary for untrusted commands. Both use the existing Tool
Catalog, Executor, Workspace grants, Budget, Artifact and Trace mechanisms.

Writes are a separate opt-in grant in [Scoped filesystem writes](scoped-filesystem-writes.md),
limited to dedicated managed output roots. A read grant never implies write access.

## Failure Inventory

| Failure | Required outcome |
| --- | --- |
| No root configuration or operator Tool grant | Unavailable/denied; no ambient working-directory fallback |
| Missing Run, mismatched Conversation, other Workspace or inactive Workspace | Denied before file access |
| Absolute path, traversal, symlink, hard link or special file | Denied at the actual open boundary; no target bytes returned |
| Credential filename/directory or recognized secrets in ordinary text | Deny sensitive paths; redact recognized content before paging/returning |
| Binary, invalid UTF-8 or oversized file | Explicit unsupported/limit error, not partial successful text |
| Large directory/search | Bounded traversal and scan; report incomplete coverage and request a narrower path |
| Invalid page boundary or changed content hash | Reject; do not concatenate pages of different file versions |
| Cancellation/deadline | Stop bounded work and close opened descriptors |
| Root removed, reassigned, replaced or configuration changed before Resume | Fail closed; never silently substitute a new directory |

## Boundary

Only operator-configured Workspace/root pairs are readable. A model selects a
logical root ID and relative path; a Workspace ID is not a disk path. Current
Workspace lifecycle and grants still apply to frozen Runs. Files are untrusted
data, not instructions, and do not automatically acquire Knowledge/web citation
IDs. Root configuration is frozen through the existing Tool definition revision,
not a new Snapshot schema. File contents themselves are not frozen.

The supported host platforms are Linux and macOS. Root-relative opening rejects
symlinks at every component, nonregular files, multiply-linked files and device
changes. This is not an OS sandbox: privileged mount changes and copies of secrets
into otherwise permitted files remain operator concerns. Content redaction is
pattern-based, not complete secret detection. No host Shell, writes, arbitrary
directories, filesystem checkpoint or indexing service is provided.
Default path exclusions include credential directories/files, `.data` application
state/backups and `docs/private` notes; an operator grant cannot override them.

## Enable and Use

1. Create an operator-owned JSON file outside the served project root. Use the
   owning Workspace's decimal database ID, not its display name:

   ```json
   {"roots":[{"id":"project","workspace_id":"42","path":"/srv/agentflow/review-project"}]}
   ```

2. Set `FILESYSTEM_ROOTS_PATH=.data/filesystem-roots.json` in the API environment.
   Empty disables the feature. Relative config paths resolve from the API process
   working directory; each configured root path must be canonical and absolute.
   Whole-filesystem roots and sensitive directory components are rejected.
3. Add `file_list`, `file_search`, `file_read` to the operator Tool configuration's
   `enabled_tools`. Preserve other entries. Add an exact rule for **each** Tool
   with this capability (`tool` and `id` differ for each rule):

   ```json
   {
     "id":"operator-file-list","tool":"file_list","action":"allow_and_log",
     "capability":{
       "source":"local",
       "scope":{"resources":[{"kind":"filesystem","name":"project_files","access":"read"}],"network":{"mode":"none"}},
       "side_effect_class":"none","rate":"run_budgeted","reversibility":"reversible",
       "visibility":"user","approval_mode":"none","audit_level":"full"
     }
   }
   ```

4. Restart the API, allow these Tools in the owning Workspace's Tools page, and
   bind them to an owned Agent in Configure. The owner cannot grant a directory
   merely by enabling a Tool; the operator root map is a separate ceiling.

`file_list {}` discovers only roots mapped to the current Run's Workspace.
Then use `file_list {"root":"project","path":"."}`, literal `file_search`
with `query`, and `file_read` with the returned relative `path`. All three modes
use the same Binding; Multi-Agent Workers inherit only their effective grants.
Existing Tool events/Replay expose results and typed failures; no new frontend
page, endpoint or generated DTO is needed. Filesystem results mark private data,
so existing Run-scoped external-Tool egress restrictions continue to apply.

## Limits and Evidence

| Control | First-release bound |
| --- | --- |
| Config | 64 KiB; 64 Workspace/root pairs |
| Read | 1 MiB raw UTF-8 file; default 4 KiB page, max 8 KiB |
| Traversal | 500 visited entries, including skipped ones; depth 1-3 |
| Search | Literal substring; depth 3; 2 MiB raw scan budget plus one overflow-detection byte; max 500 retained matches |
| Result pages | Up to 50 list entries or 20 matches; UTF-8 previews bounded to 240 bytes |
| Execution | 5-second Executor timeout; cooperative cancellation between filesystem operations |

Pagination offsets refer to **redacted UTF-8 bytes**, not original file bytes.
`content_hash` covers the complete observed raw file, `observed_bytes` its raw
size, and `total_bytes` the redacted size. A continued read must supply
`expected_hash=content_hash`; changing files requires restarting at offset 0.
Offsets must start at UTF-8 boundaries; the schema permits up to 4 MiB to account
for expansion after redaction, while each raw file remains limited to 1 MiB.
Empty files return an empty complete page. Definitions also capture the named
redaction strategy so a strategy-version change cannot silently reuse a frozen Tool.
Search previews show the match neighborhood in already-redacted content, with
file identity/hash and the exact redacted byte `offset`. Supply that offset and
hash to `file_read` to reach late matches without paging through an entire file.
`line` counts redacted lines; search retains the first occurrence per matching
line, not every occurrence or original-editor line coordinates.

`truncated` means more pages exist within retained results. `scan_truncated`
means traversal depth, entry/byte/match limits or an oversized file prevented full
coverage; narrow the directory instead of claiming all files were searched.
Directory and match pagination rescans the bounded live tree, so concurrent edits
can shift those pages. Read hashes detect file-page drift, not whole-tree drift.

Tool definition revisions include a digest of the complete root map and root
device/inode identities, without exposing host paths in model schemas or Snapshots.
Catalog availability checks detect drift before Run restoration/model exposure;
Bindings recheck current roots and Workspace lifecycle at each open. A root-map
change conservatively invalidates **all three file Tool definitions**, including
Runs using another entry. Restart the API and start a new Run after an intentional
change. A missing/invalid configured root prevents startup rather than silently
falling back. Unchanged roots survive restart; unchanged file contents are not
promised. OS calls on a stalled filesystem cannot be forcibly interrupted by Go
context cancellation; use trusted local roots, not untrusted/network mounts.

## Validation

From `apps/api`, with the repository's Go version activated:

```bash
TEST_DATABASE_URL='<disposable-test-postgres-url>' go test ./internal/tool/filesystem ./internal/agent -run 'TestFilesystem' -race -count=1
```

The desktop functional gate runs production Next/Go composition, real local files
and disposable Postgres with a strict deterministic provider. It exercises root
discovery, list/search/read, escape/sensitive-path denial, all three modes and
Replay reload. Its `filesystem-read-evidence.json` attachment retains Run IDs,
frozen definitions and Tool evidence; it does not measure live-model quality:

```bash
TEST_DATABASE_URL='<disposable-test-postgres-url>' AGENTFLOW_FILESYSTEM_TEST=1 \
  bash scripts/test-browser.sh filesystem-read.spec.ts
```

Disable the Tools to roll back access; retain historical events/Artifacts.
No database migration, sbx mount or default Agent permission expansion is made.
