# Skill Installation

## Preferred Installer: Vercel Skills CLI

Use [Vercel Skills CLI](https://github.com/vercel-labs/skills) directly from the
**AgentFlow repository root**, not `apps/api`, `apps/web`, or a temporary download
project. There is no adapter script. The complete restricted Go installer is
retained as an [explicit fallback](skill-installation-fallback.md).
Run that fallback from the root with
`./scripts/skill-install.sh --repo ... --path ...`; the script forwards
native Go CLI flags without invoking Vercel.

Follow the official [Install a Skill and source-format examples](https://github.com/vercel-labs/skills#install-a-skill).
From the AgentFlow repository root, the official basic example is:

```bash
npx skills add vercel-labs/agent-skills
```

Select only reviewed packages, use **project** scope and the **Universal** target
when prompted, and confirm the destination is `.agents/skills/` before installing.
This source is the upstream command example, not a claim that every package is
compatible with AgentFlow's restricted Loader.

Vercel's [project installation paths](https://github.com/vercel-labs/skills/blob/main/src/installer.ts)
are based on its working directory. `universal` selects `.agents/skills/`;
`--copy` avoids agent-directory symlinks. Do not use `--global` for AgentFlow.
Compatibility was verified with `skills@1.7.0` (Node.js `>=22.20.0`, npm/npx and
Git for repository sources); the basic command uses the currently published CLI.
That verified version has no native arbitrary `--dest` option.

```text
agentflow-platform/
  skills-lock.json                 Vercel-managed source identities
  .agents/skills/                  shared operator-trusted installation root
    <skill-name>/
      SKILL.md
      install-receipt.json        only if installed by the Go fallback
    <another-skill-name>/
      SKILL.md
  apps/api/
    .env                          TRUSTED_SKILL_DIRS=../../.agents/skills
```

Installed packages and the local lockfile are ignored by Git. Both installers
use the same package layout and production Loader. Go fallback code remains
separate under `apps/api/internal/skill/install/fallback/`; it does not wrap
Vercel or edit `skills-lock.json`. Vercel can load fallback-installed files,
but its lockfile-based restore/update does not record the fallback's provenance.

## Directory Checks and Runtime Trust

`TRUSTED_SKILL_DIRS` now names **installation roots**, not individual package
directories. In `apps/api/.env`, for the usual API process cwd of `apps/api`:

```bash
TRUSTED_SKILL_DIRS=../../.agents/skills
```

An absolute repository-root `.agents/skills` path also works. CSV allows multiple
operator-owned roots, but the normal Vercel/Go workflow uses just this one.
Existing exact-package entries must be replaced by their parent installation
root; they fail explicitly rather than silently finding zero packages.

The Loader discovers only immediate, non-hidden subdirectories containing
`SKILL.md`. It skips non-package directories, notes and installer staging/lock
entries; it does not search recursively, follow package symlinks or interpret a
lockfile/receipt as permission. Invalid packages, duplicate names across roots
or more than eight discovered packages fail the whole catalog, without partial
trust. Empty roots are valid; an empty environment value disables new bindings.
`make setup` creates the empty shared root for a clean checkout.

The configured root is a trust boundary: new valid packages placed there become
available after an API restart without changing the environment list. **Review
new instructions/resources and their License before restarting the API.** Agent
bindings, Tool permissions and credentials are still separate; discovering a
package does not automatically bind or execute it. Old Runs keep frozen content.

Neither CLI edits `.env`, bindings or Tool permissions. See
[Trusted Skills](trusted-skills.md) for invocation, Snapshot and Replay behavior.

| Situation | Check / outcome |
| --- | --- |
| Shell fallback run outside this repository root | Stop with a repository-root hint before Go activation or downloading. |
| Go installer run outside this repository root or its `apps/api` module | Check project markers and stop before downloading. Root-level `go -C apps/api` is supported. |
| Vercel run from the wrong directory | Native Vercel cannot know AgentFlow's root; confirm `apps/api/go.mod` and `apps/web/package.json` exist in the current directory first. No wrapper is installed to intercept it. |
| Configured root is missing or not a directory | API startup fails with the resolved path and a working-directory/install hint. |
| Configured path points to a single package | API startup reports that the parent installation root is required. |
| Configured root is empty | API logs zero loaded packages; inspect the installation location rather than expecting auto-discovery elsewhere. |

The Shell shortcut requires the repository root. The underlying Go check accepts
both the repository root and its `apps/api` module because
`go -C` changes the child process working directory; it does not require the
original shell to be at the root. Native `npx skills` alone has no AgentFlow
directory guard. If the configured root already exists, startup can succeed
even when a new package was mistakenly installed elsewhere: logging an empty
catalog or loading old packages is not proof of a correct installation location.

Start AgentFlow with `make dev` from the repository root. The script runs the
API in `apps/api`, matching `.env` and its relative-path conventions. Direct
backend commands still use that module; from the repository root, prefer
`./scripts/skill-install.sh --repo ... --path ...` for the fallback.
It activates the configured Go version and invokes
`go -C apps/api run ./cmd/skill install ...`. Plain
`go run ./cmd/skill install` at the repository root is not valid because this
repository has no root Go module or `cmd/skill` package. No module restructuring
is needed for Skill installation.

## Compatibility and Security Boundaries

Directory layout is compatible with native Vercel, not every possible Skill
capability. The existing text-only Loader limits, credential checks, frozen
hashes and progressive loading still apply. Unsupported binary resources under
`references/` or `assets/` fail validation; `scripts/`, `agents/` and unrelated
top-level files are not loaded or executed. Some community metadata shapes
remain outside this restricted subset.

Vercel runs with the operator's OS permissions, Git authentication and network
environment. `--copy` is not a sandbox: files can be copied or source links
dereferenced, and installs/updates may replace existing packages. Installation
is not a safety or licensing review. Do not update active trusted files blindly;
inspect changes before API restart. Run Vercel `list`, `update` and `remove`
from the repository root, not from a different installation project.

If Vercel cannot be used, invoke the Go fallback explicitly. Its bounded
HTTPS/API-only downloads, preview/apply flow, no-overwrite checks, receipt and
atomic publication are retained; these guarantees are not attributed to
Vercel. A Vercel error never automatically starts another installer.

## Verification

Deterministic discovery tests cover empty roots, immediate-only discovery,
extra files/staging, exact-path mistakes, root absence, duplicate names, links,
content/resource limits and frozen identities. CLI tests check project cwd,
the shared default destination, explicit overrides and unchanged fallback
failure behavior. No network or model is needed in default CI:

```bash
# From the repository root, with the repository's Go version.
go -C apps/api test ./internal/skill ./internal/skill/install/fallback ./cmd/skill -count=1
```

The opt-in test in `vercel_compatibility_test.go` reads the valid packages already
installed under the selected project's `.agents/skills`, then checks discovery
and frozen-content validity. An empty catalog fails rather than passing without
checking a package. It does not install files, execute npx, call a model or modify
trust:

```bash
TEST_VERCEL_SKILL_PROJECT="$PWD" \
  go -C apps/api test ./internal/skill -run TestLiveVercelInstallationCompatibility -count=1 -v
```

This is **Vercel output -> AgentFlow Loader/Snapshot compatibility**, not a
direct comparison between installers. The separate fallback integration test,
`TestPreviewAndInstallThroughProductionLoader`, checks the Go download,
preview/apply, receipt and root-discovery path with deterministic HTTP fixtures.
Tool dependencies in the live smoke are only snapshot-format inputs, not
evidence that the corresponding Bindings are configured or authorized.

Retain the CLI version, pinned source, installation log and emitted package
hashes locally. Compare them to the Go receipt's `package_hash`, `/api/skills`
and Snapshot/Manifest identities. Vercel's lockfile hash and the Go receipt's
file hash describe provenance, not the Loader's runtime package identity. These
example packages demonstrate compatibility, not writing quality or future
revision safety.
