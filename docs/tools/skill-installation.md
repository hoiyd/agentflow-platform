# Skill Installation

Installation, operator review, runtime trust, Agent binding, and execution
permission are separate steps. This page owns both installer workflows;
[Trusted Skills](trusted-skills.md) owns package format, Loader limits,
progressive loading, and frozen content.

## Preferred Installer: Vercel Skills CLI

Run [Vercel Skills CLI](https://github.com/vercel-labs/skills) directly from the
**AgentFlow repository root**, not `apps/api` or another project:

```bash
npx skills add vercel-labs/agent-skills
```

This is the upstream example, not an endorsement of every package. Select only
reviewed packages, **project** scope and the **Universal** target; confirm
`.agents/skills/` before installing. Do not use global scope. `--copy` avoids
agent-directory symlinks but is not a sandbox. Consult the
[official source formats](https://github.com/vercel-labs/skills#install-a-skill).

The verified CLI version is 1.7.0 (Node >=22.20.0, npm/npx, Git for repository
sources). Its project path comes from cwd and has no arbitrary `--dest` option;
the basic command uses the currently published CLI. Use native list/update/remove
from this same repository root. Vercel uses OS permissions, Git credentials,
and network configuration; installs/updates can replace existing files. Review
instructions, resources, changes, and License before API restart.

## Shared Directory and Runtime Trust

```text
agentflow-platform/
  skills-lock.json                 Vercel-managed provenance, ignored by Git
  .agents/skills/<skill-name>/
    SKILL.md
    install-receipt.json            Go fallback only
  apps/api/.env                    TRUSTED_SKILL_DIRS=../../.agents/skills
```

`TRUSTED_SKILL_DIRS` is a CSV of reviewed **installation roots**. Relative paths
use API cwd (`apps/api` under `make dev`). Discover only immediate non-hidden
`SKILL.md` package directories: no recursive scan, symlinks, or trust inferred
from locks/receipts. Invalid packages, duplicate names, or more than eight fail
the catalog; empty roots are valid, and empty configuration disables bindings.
`make setup` creates the normal empty root. Individual-package paths are invalid.

Installing a valid package in a configured root makes it discoverable after
restart, not automatically bound or executed. Neither installer edits `.env`,
Agent bindings, Tool grants, or old Runs. Bind reviewed names in Single's
**Configure > Skills**; invoke explicitly or let the model select a bound method.
Old Runs use frozen content, not updated disk files.

| Directory mistake | Check / outcome |
| --- | --- |
| Shell fallback outside repository root | Reject before Go activation/download |
| Underlying Go CLI outside repository root or API module | Reject using project markers; root `go -C apps/api` is supported |
| Native Vercel outside root | Cannot be intercepted; first confirm `apps/api/go.mod` and `apps/web/package.json` in cwd |
| Missing/non-directory configured root | Startup fails with resolved path and install/cwd hint |
| Config points to one package | Startup requires the parent installation root |
| Empty root / installation elsewhere | Empty or old catalog may load; startup success does not prove the new install location |

Layout compatibility is not full Skill capability compatibility. AgentFlow
loads bounded UTF-8 instructions/resources, not binary assets, scripts, or
arbitrary metadata. Native Vercel can copy/dereference files unsupported by that
Loader. The fallback validates the restricted subset and records omissions.

## Restricted Go Fallback

Choose the complete Go installer explicitly when native Vercel cannot be used;
a Vercel failure never triggers it automatically. It lives in
`internal/skill/install/fallback`, not the Loader or a Vercel adapter.

From the repository root:

```bash
mkdir -p .agents/skills
./scripts/skill-install.sh --repo '<owner>/<repository>' --path '<package-subdirectory>/<skill-name>'
./scripts/skill-install.sh --repo '<owner>/<repository>' --path '<package-subdirectory>/<skill-name>' --apply
```

The first command previews via temporary downloads and leaves the destination
unchanged; the second publishes. The shell activates the repository's Go version
and forwards every argument through `"$@"` to
`go -C apps/api run ./cmd/skill install`. Plain root `go run ./cmd/skill` is not
valid because there is no root Go module. Quote placeholders/paths as needed.

| Flag | Contract |
| --- | --- |
| `--repo` | Required public GitHub owner/repository; not a SkillsMP URL |
| `--path` | Required repository-relative package subdirectory |
| `--ref` | Optional branch, tag, or full SHA for preview/apply; omitted resolves latest default-branch commit |
| `--dest` | Optional existing installation root; default project `.agents/skills`. Relative override resolves from Go cwd (`apps/api`); prefer absolute staging paths |
| `--timeout` | Whole-operation deadline; default 30s, positive and <=2m |
| `--apply` | Publish; omission or false means preview |
| `--help` / `-h` | Usage without download |

Resolve the revision once per operation, then fetch immutable tree/blob objects;
do not assume main/master or choose a release automatically. Separate previews
and installs may resolve different commits. To reproduce a preview exactly,
pass `--ref '<resolved-commit-sha>' --apply`. Existing target directories, files,
and symlinks are never overwritten; there is no force or self-update option.
Use a separate staging root to review another version; it is not automatically
trusted by the API. The shell creates no directories, grants, or bindings itself.

## Content and Provenance

GitHub tree/blob API reads avoid archives, checkouts, hooks, and script execution.
Retain unmodified `SKILL.md` and supported text under references/assets;
recognized package License files, or first matching root License, are retained.
Missing License warns without inventing redistribution permission. Other paths
are reported as omissions; scripts/agents and unsupported directories are not
traversed or claimed audited. Selected files must be non-executable regular
blobs and their Git identities must match bytes.

Reports/receipts retain repo/path, resolved commit/tree, file identities/bytes,
omissions, warnings, time, install directory, and Loader `package_hash`.
`files_hash` covers the sorted retained files including License, excluding the
receipt; `source_tree` includes omitted content, not proof it was inspected.
Compare Loader identity with `/api/skills`, Snapshot, and Manifest references.
Hashes are provenance, not signatures, safety judgments, or execution permission.

Vercel and fallback share files/Loader, not provenance management: fallback does
not edit `skills-lock.json`, and Vercel restore/update does not record Go receipts.
Installed files and locks are ignored by Git. Reports contain no Skill body or
credentials, but local paths/provenance should remain local evidence.
stdout is JSON, stderr diagnostics; compiled CLI exit codes are 0 success/help,
1 install/output failure, 2 syntax failure. `go run` may remap child exit codes;
the shell propagates its wrapped status. A post-publication stdout failure does
not remove the installed package/receipt.

## Fallback Safety and Failure Boundaries

| Boundary | Limit / failure outcome |
| --- | --- |
| Network | Public HTTPS api.github.com only; no credentials, proxy inheritance, arbitrary hosts, private repositories, auth headers, retries, or model calls |
| Requests / redirects | 48 total requests; fewer than five redirects per chain, each revalidated |
| Response / aggregate JSON | 512 KiB / 4 MiB; reject compressed, malformed, truncated, or duplicate data |
| Blob / tree entries | 32 KiB per blob; 2,048 entries total; Loader imposes tighter content limits |
| Paths | 256-byte package/resource bound, limited depth, no traversal/backslash, inspected symlink/submodule/executable rejection |
| Publication | Existing operator-owned root, `os.Root`-bounded exclusive staging writes, owner-only modes, root-wide lock, same-filesystem rename |
| Errors/cancel/deadline | Stop within bounds, clean staging/lock, preserve old targets, no partial trusted package |
| Resolution/identity failure | Explicit error; no guessed ref, ignored hash mismatch, or destination mutation |
| GitHub 403/429 | Explicit failure without printing remote body or falling back to authenticated download |

The root must not be concurrently mutable by untrusted processes. The lock only
coordinates cooperating installers; it is not an OS sandbox. Targets are checked
before download and again before publication. Preview stages outside the root
and takes no publication lock.

SIGINT/SIGTERM cancel cleanly. A forced kill may leave `.skill-install.lock` and
`.skill-stage-*`; it never exposes an incomplete published Skill. Publication
may already have finished: inspect receipt before retrying and verify no active
installer before removing stale state. No automatic lock stealing, interrupted
resume, or power-failure durability guarantee is provided.

After installation, review instructions/resources/License, configure the parent
root if needed, restart API, then bind the Skill. No install can enable Tool
dependencies or alter old Snapshots. Stars and package listings are not safety evidence.

## Verification

From root, with configured Go:

```bash
bash scripts/test-skill-install.sh
go -C apps/api test ./internal/skill ./internal/skill/install/fallback ./cmd/skill -count=1
go -C apps/api test -race ./internal/skill ./internal/skill/install/fallback ./cmd/skill -count=1
```

Deterministic fixtures cover discovery, cwd/destination checks, supported content,
limits, deadlines, redirect/blob validation, concurrent installs, no-overwrite,
receipt, and production Loader compatibility. No default CI downloads packages.

`TestPreviewAndInstallThroughProductionLoader` logs installation evidence for
HTTP tree/blob -> staging -> receipt -> actual Loader, with unchanged trust.
The optional Vercel-output smoke reads already installed packages; it does not
execute npx, install, grant permissions, or call a model. An empty catalog fails:

```bash
TEST_VERCEL_SKILL_PROJECT="$PWD" \
  go -C apps/api test ./internal/skill -run TestLiveVercelInstallationCompatibility -count=1 -v
```

Retain CLI version, resolved source, logs, and identities locally. Live download
success proves only that revision's compatibility, not writing quality, future
revision safety, or authorized Tool dependencies.
