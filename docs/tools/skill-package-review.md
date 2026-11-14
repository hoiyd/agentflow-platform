# Reviewed Skill Packages

`SKILL_REVIEW_MANIFEST_PATH` opts into exact-content review. Empty preserves
the existing trusted-directory behavior. Installation receipts and Vercel lockfiles
record provenance, not approval; the API never downloads or approves packages.

## Operator Setup

1. Inspect the package and run `bash scripts/skill-check.sh --root .agents/skills`
   from the repository root. Use its `package_hash`, not a Git commit or lockfile hash.
2. Create an operator-owned JSON file outside **all** configured Skill roots:

   ```json
   {"schema_version":"skill-reviews-v1","approvals":[
     {"name":"reviewed-method","hash":"<64 lowercase hexadecimal characters>"}
   ]}
   ```

3. Set `SKILL_REVIEW_MANIFEST_PATH=.data/skill-reviews.json` in the API environment
   and restart. Keep the local manifest out of source control and model-writable
   directories. Production composition rejects manifests inside managed output
   roots and rechecks this location guard on access. The example name/hash must
   be replaced with the inspected values.

Only one hash per name is allowed. An empty `approvals: []` denies every package;
missing/null approvals, unknown fields, duplicate names, invalid hashes, symlink
files, unsupported schema and files over 64 KiB are rejected. Startup fails if
strict configuration is unavailable; it never falls back to directory trust.

## Updates and Revocation

Strict mode rereads packages and approvals for new bindings and Run snapshots.
New packages need approval; changed hashes return `skill_review_changed` and
unlisted packages return `skill_review_required`. The existing Agent configuration
and Skill picker show the blocking reason. Saved bindings remain intact, but
cannot authorize a new execution of blocked content.

Review an update, obtain its current checker hash, and replace the matching
manifest entry atomically. No restart is needed for manifest/content changes in
strict mode. Removing an entry revokes it for new Runs. Missing/malformed runtime
configuration blocks new Skill snapshots and exposes unavailable readiness.
Duplicate package names or invalid resources fail closed through the same Loader.

Old Runs keep their immutable Skill text and hashes. Replay and supported
Continue/Resume paths do not substitute current disk content or apply a new
review retroactively. To revoke actual Tool capability, change the live Tool
policy/Workspace grant; a package review is not an execution permission.

## Boundary and Evidence

The existing package hash covers canonical **loaded runtime content**: description,
instructions, supported text resources, required Tools and invocation policy.
It is not a whole-tree digest; ignored scripts, decorative metadata and installer
receipts are not executable capabilities and are not part of this identity.
This gate is not a signature, malware scanner, prompt-safety proof, approval UI,
or defense against an attacker who can rewrite operator files/API memory.

Run `go test ./internal/skill -run 'TestReviewedPackage|TestReviewManifest'`
from `apps/api`. The opt-in browser gate uses production composition, real
package files and disposable Postgres:

```bash
AGENTFLOW_SKILL_POLICY_TEST=1 bash scripts/test-browser.sh skill-review.spec.ts
```

It retains synthetic readiness screenshots and JSON Run/Snapshot evidence;
the provider is deterministic, not live-model quality evidence. New API metadata
is additive; Snapshot hashing/serialization and installation formats are unchanged.
