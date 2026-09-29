# Restricted Go Skill Installer (Fallback)

**Prefer [Vercel Skills CLI](skill-installation.md).** The unchanged
`go run ./cmd/skill install` backend command is the fallback when Node/npm, Git, or the
Vercel distribution route cannot be used. Its implementation and tests live in
`apps/api/internal/skill/install/fallback/`; it is not the runtime Loader or a
wrapper around Vercel. Choose it explicitly; Vercel failures never auto-invoke it.

## Scope

The operator CLI downloads a selected public GitHub package, validates it with
the existing Skill Loader, and records its provenance. Installation is not
runtime trust, an Agent binding, a Tool grant, or a security review.

Git [trees](https://docs.github.com/en/rest/git/trees) and
[blobs](https://docs.github.com/en/rest/git/blobs) are read individually; repository archives, Git checkout,
package hooks, and scripts are not used. This avoids downloading unrelated
repository content and introduces no extraction or process-execution surface.

## Preview, Install, Review, Activate

Run from the repository root using the repository's Go version. `go -C apps/api`
selects the existing backend module; the installer checks the project location
and defaults to the same root `.agents/skills` directory used by Vercel:

```bash
mkdir -p .agents/skills
go -C apps/api run ./cmd/skill install \
  --repo affaan-m/ECC \
  --path .agents/skills/article-writing \
  --ref d3b8a3e908904e242ed2dbe66af62cca71131419
```

The default is a network-backed preview, not an installation. It downloads and
validates the supported content in a temporary directory, prints a JSON report,
then removes that temporary directory. The destination remains unchanged.
`--ref` is required: previews accept a branch, tag or complete commit SHA, and
return the resolved `commit`. Apply the exact resolved revision:

```bash
go -C apps/api run ./cmd/skill install \
  --repo affaan-m/ECC \
  --path .agents/skills/article-writing \
  --ref d3b8a3e908904e242ed2dbe66af62cca71131419 \
  --apply
```

`--apply` rejects branch/tag names rather than resolving them again. Installation
also refuses any existing target, including an empty directory, file or symlink;
there is no force, overwrite or self-update mode. Use a different download root
to review another revision without disturbing an active package.

The default final directory is `<repository-root>/.agents/skills/<name>`, the
same package location as Vercel. `--dest` remains available for an explicit
existing staging root; relative overrides resolve from the Go process cwd
(`apps/api` with `go -C`), so prefer an absolute path. A custom staging root is
not automatically discovered by the API. The Go installer never overwrites
existing Vercel packages or changes Vercel's lockfile.

Inspect the installed `SKILL.md` and resource text. Only after operator review,
configure its parent installation root in `TRUSTED_SKILL_DIRS` if not already
configured, and restart the API. The standard API setting is
`TRUSTED_SKILL_DIRS=../../.agents/skills` from `apps/api`. Then bind the package
in **Configure > Skills** and invoke it explicitly
or let the model select it. See [Trusted Skills](trusted-skills.md) for invocation,
dependencies, Manifest identities and Frozen Snapshot behavior.

This example's pinned source was used for an installation smoke test, not a
blanket endorsement of the repository or future revisions. SkillsMP can help
discover a package, but the CLI takes its actual GitHub repo/path/revision, not
a SkillsMP URL. Stars and directory listings do not establish safety.

## Supported Content and Receipts

- Retain unmodified `SKILL.md` and supported UTF-8 text under `references/` and
  `assets/`, using the Loader's shared path contract and existing package limits.
- Retain recognized package License files (`LICENSE`, `LICENSE.md`, `LICENSE.txt`,
  `COPYING`); if absent, retain the first matching repository-root License.
  A missing License produces a warning, not an invented licensing decision.
- Omit other content and report its path. Unsupported directories such as
  `scripts/` and `agents/` are not traversed. Link rejection applies to inspected
  tree entries; unvisited directories are not claimed to have been audited.
- Selected files must be non-executable regular blobs. Git blob identity is
  verified against downloaded bytes; no source file is rewritten or executed.

The `skill-install-v1` JSON report and installed `install-receipt.json` contain
the repo, source subdirectory, complete commit SHA, package tree identity,
source/installed file paths, byte counts, Git blob IDs, per-file SHA-256 values,
omissions, warnings, timestamp and exact installation directory.

`package_hash` is the existing Loader identity and can be compared to
`GET /api/skills`, `runtime_snapshot.skills`, or the Manifest's
`skill:<name>@<hash>` reference. `files_hash` hashes the sorted retained-file
manifest, including License identities; it excludes the receipt itself.
`source_tree` identifies the selected Git subtree, including omitted content,
but is not a claim that all of that content was downloaded or inspected.
SHA-1 is used only for Git object identity; neither it nor SHA-256 is a signature,
malicious-instruction detector, or proof of permission to redistribute content.

Receipt text does not contain the Skill body or credentials. stdout is JSON;
stderr contains next steps/errors. Exit codes are `0` for success/help, `1` for
installation/output failure and `2` for invalid CLI syntax. If stdout fails after
publication, the complete installed package and receipt remain available.

## Resource and Filesystem Limits

| Boundary | Limit |
| --- | --- |
| Complete operation deadline | Default 30s; positive and at most 2m through `--timeout` |
| Network | Public `https://api.github.com` only; no auth header, URL credentials, proxy inheritance, arbitrary hosts or HTTP |
| HTTP requests | At most 48, including redirects; no retry or paid model access |
| Redirects | Fewer than 5 per request chain; every destination revalidated |
| One response / aggregate JSON bytes | 512 KiB / 4 MiB; compressed responses rejected |
| One downloaded blob | 32 KiB; selected Skill content must also pass tighter Loader limits |
| Enumerated tree entries | 2,048 total; truncated and duplicate trees rejected |
| Package path / resource traversal | 256 bytes; bounded directory depth, no traversal/backslash paths |
| Package publication | Existing operator-owned root, `os.Root`-bounded writes, owner-only files/directories and same-filesystem rename |

The install root must not be writable or concurrently mutated by untrusted
users/processes. One root-wide lock serializes cooperating installers; this is
not an OS sandbox or protection against a hostile process with access to the
same root. Existing targets are checked before download and again before
publication. Supported files are exclusively created in staging, then the
fully validated directory is renamed into place. Previews stage outside the
installation root and never take its publication lock.

Ordinary errors/cancellation remove staging and release the lock. `SIGINT` and
`SIGTERM` cancel the operation; process termination before publication may leave
a `.skill-install.lock` and `.skill-stage-*` directory, but does not expose staged
files as a published Skill. Publication after a forced kill may already be
complete; inspect its receipt before retrying. Filesystem durability across
machine/power failure is not guaranteed by this CLI. Before removing a stale
lock/stage, confirm no installer owns it. There is no automatic lock-stealing or
unsafe interrupted-install resume.

GitHub's unauthenticated API rate limits still apply; HTTP 403/429 is reported
without printing the remote response body. Private repositories, authentication,
remote dependency installation, automatic trust and runtime hot reload are not
supported. The CLI never reads `.env`, modifies bindings, enables Tools, creates
Run events or changes old Frozen Snapshots. No HTTP/client/frontend contract or
database migration is needed.

## Failure Checks

| Failure | Required outcome |
| --- | --- |
| Invalid repo, package path, ref, timeout or destination | Reject before downloading or modifying the destination. |
| Apply with a mutable branch/tag | Reject; use the complete commit SHA returned by preview. |
| Missing package, invalid metadata, duplicate names or unsupported resource identity | Fail validation; no installed package. |
| Unsafe tree path, symlink, submodule or executable selected file | Reject; never follow or execute it. |
| Unsupported directories/resources | Report omissions; do not traverse scripts or silently claim full-package compatibility. |
| HTTP failure, rate limit, invalid/truncated JSON, blob identity mismatch | Fail explicitly; do not consume remote error bodies as instructions. |
| Unapproved redirect, credentials in a URL or non-HTTPS destination | Deny before the request is sent. |
| Too many requests/entries, oversized responses/files or total download | Stop within fixed bounds; no partial install. |
| Deadline or cancellation | Stop and clean temporary files; do not publish an incomplete package. |
| Existing destination, conflicting install or write failure | Preserve existing content; publish only a fully validated directory. |
| Missing License | Retain an explicit warning; do not infer redistribution permission. |
| Installer output/receipt | No credentials; retain source revision, file identities and Loader package hash. |
| Successful installation | Do not edit `.env`, restart the API, bind an Agent or enable dependencies. |

Deterministic HTTP fixtures prove these installation boundaries; live downloads
only prove compatibility with the selected source, not the safety or writing
quality of its instructions.

```bash
go -C apps/api test ./internal/skill/install/fallback ./cmd/skill ./internal/skill -count=1
go -C apps/api test -race ./internal/skill/install/fallback ./cmd/skill ./internal/skill -count=1
```

`TestPreviewAndInstallThroughProductionLoader` logs `skill_install_evidence`
with commit, package/files hashes, retained/omitted counts and unchanged trust.
It drives real HTTP tree/blob responses, staging, publication, receipt loading
and the production Loader with local fixtures. Other tests cover the inventory
above, including concurrent installers and preservation of existing targets.
Default CI never downloads public packages or calls a model.

An opt-in live smoke can install both the example above and
`.agents/skills/brand-voice` from the same pinned revision into a fresh temporary
root. Retain their JSON reports locally and compare Loader hashes to reviewed
packages. Do not publish package bodies or user-specific paths as test artifacts.
