# Skill Checker

Skill Checker is a read-only operator check, not installation, runtime trust,
binding, execution permission, or a safety certification. It reuses the runtime
Loader and immediate-directory discovery; it never downloads dependencies,
executes scripts, changes configuration, or repairs packages.

## Commands and Report

From the repository root:

```bash
./scripts/skill-check.sh --root "$PWD/.agents/skills"
./scripts/skill-check.sh --dir "$PWD/.agents/skills/<skill-name>" --tools 'knowledge_search,knowledge_read'
./scripts/skill-check.sh --help
```

The shell activates the repository's Go version and forwards every argument
unchanged through `"$@"` to `go -C apps/api run ./cmd/skill check`. It must run
from the repository root, like `skill-install.sh`; an incorrect cwd is rejected
before Go activation. `--dir`, `--root` (including repeated flags and `--flag=value`),
`--tools` (including `--tools ''`), `--help` and `-h` retain the Go CLI's behavior.
No default input is added. Go activation or command failures stop the script.

`--dir` checks one package; `--root` discovers immediate packages using the same
path as API startup. Both are repeatable and may be mixed, with at most eight
inputs and eight discovered packages. There is no implicit root or environment
configuration read. Both entry points can check explicit directories outside
this project. Unlike installation, the underlying Go `check` command also does
not require an AgentFlow project cwd.
Relative paths still use the Go process cwd (`apps/api`), not the shell's root
cwd; the examples use shell-expanded absolute inputs to avoid ambiguity. The
wrapper neither rewrites paths nor adds installation/trust behavior.

stdout is deterministic `skill-check-v1` JSON. `compatible` is true only if
all inspected packages and roots have no blocking diagnostics. Each package
has `name`, `status`, a Loader `package_hash` when format-valid,
`dependency_status`, and diagnostics. Status is `compatible`, `incompatible`
(semantics/resource/dependency errors), or `rejected` (core format/discovery or
duplicate-name errors). A format-valid package can be incompatible.

Diagnostics include stable `code`, `category`, `severity`, package-relative
`path`, safe field/resource labels where available, `message` and remediation
`hint`. Directory-level or unidentified errors use `.`. Inputs, host paths,
frontmatter values, instructions and resources are not echoed. Information and
warnings do not block. Reports stop after 64 diagnostics per package with a
blocking limit diagnostic; Markdown is capped at 64 recognizable links per file.
The existing [Loader limits](trusted-skills.md#package-format-and-boundaries) still apply.

Compiled CLI exit codes: **0** compatible/help, **1** incompatible/rejected or
output failure, **2** command syntax/missing input. `go run` may remap child exit
codes. Duplicate names invalidate both entries; any root failure invalidates
the report even if other packages pass. An empty existing root reports an empty
compatible catalog, not successful installation.

## Support Matrix

| Package declaration | Current behavior / Skill Checker |
| --- | --- |
| `name`, `description`, UTF-8 instructions | Validated and loaded using the actual runtime Loader |
| `metadata.agentflow-required-tools` | Runtime Freeze checks effective Tools; check reuses this against optional `--tools` |
| Other string-valued `metadata`, `license`, `compatibility` | Descriptive only; no grants, installation, license or environment enforcement. License/compatibility produces information |
| `allowed-tools` | Unsupported; does not pre-approve Tools, constrain runtime permissions or enable dependencies |
| `disable-model-invocation`, `user-invocable`, `context`, `agent`, `model`, `hooks`, other top-level extensions | Unsupported; check blocks rather than claiming invocation, delegation or lifecycle semantics |
| `$ARGUMENTS`, `$ARGUMENTS[n]`, `$n`, `${CLAUDE_*}` | Recognizable substitution syntax is unsupported; never replaced with arguments or host values |
| Dynamic command injection (`!` followed by a backtick command) | Unsupported; never executes or injects output |
| `scripts/` | Presence warns; not traversed/executed. Recognizable local links to scripts block because they are not frozen resources |
| Local Markdown links/images | Goldmark parses inline and reference links, including titles; verifies existence and membership in supported frozen references/assets text resources |
| Remote HTTP(S), mail and protocol-relative links; local anchors | Ignored and never fetched; not proof of remote availability |
| Plain prose/code-block paths, shell dependencies, arbitrary substitutions | Not interpreted or resolved; require manual review |

Links in a reference Markdown file resolve relative to that file; encoded paths
are decoded before root-bound checks. Traversal, absolute/file URLs, links to
nonregular files, unsupported types or unfrozen files block. Diagnostics identify
the referring file and, when safe, the missing target. Goldmark does not treat
example links inside fenced code as dependencies. Unsupported resources already
in `references/` or `assets/` remain strict Loader format errors.

`--tools` is an **operator-supplied effective, ready Tool set**, not a live API
probe. Omitted means `not_checked` with a warning when dependencies exist;
`--tools ''` explicitly checks against none. Otherwise the status is `available`,
`missing`, or `not_declared`. It cannot establish credentials, availability,
Scope or approval by itself; Agent binding and Run creation still perform their
existing checks, and execution Policy can still deny a Tool.

Skill Checker is not an automatic startup/binding gate. The Loader remains
fail-closed on core format/content errors; it does not implement foreign
behavioral frontmatter. Review and adapt incompatible semantics before trusting
a package. Existing Skills, bindings, snapshots, API DTOs and frontend behavior
are unchanged, except that direct package loading now rejects package symlinks
just like immediate discovery. Trusted roots must remain operator-owned and not
concurrently modified during checking/loading.

Description advice (>512 characters or broad phrases such as "any task") is
nonblocking and heuristic. It is not a malicious-content detector, quality
score, or proof that prose dependencies are complete. The runtime hard limit
remains 1024 characters. No content is automatically rewritten.

The portability boundary follows the [Agent Skills specification](https://agentskills.io/specification)
and distinguishes extensions documented by [Claude Code Skills](https://code.claude.com/docs/en/skills)
from capabilities implemented by AgentFlow; upstream validity does not imply
runtime compatibility.

## Failure Inventory

| Input / failure | Expected outcome |
| --- | --- |
| Valid text-only package | Compatible; identity agrees with runtime Loader |
| Invalid YAML, identity, UTF-8, sensitive content, size/count limits | Rejected with the same core Loader error code |
| Duplicate names across packages/roots | Rejected; no partial catalog claimed valid |
| Symlink or escaping resource | Rejected/denied; never read outside package |
| Missing Markdown resource | Incompatible with a package-relative diagnostic |
| Existing file outside frozen resources / unsupported type | Incompatible; no new read permission |
| Behavioral frontmatter, argument substitution, command injection | Incompatible; unsupported semantics are not implemented |
| Scripts present without a recognizable dependency | Warning; contents neither traversed nor executed |
| Descriptive metadata or broad/long description | Information/advice only, not an automatic security decision |
| Declared Tool dependencies | Check an explicitly supplied effective Tool set, or report not checked |
| Empty installation root | Valid empty discovery, not proof that a package was installed |
| Missing directory / output failure | Nonzero exit; no host path or content in diagnostics |

Tests must compare CLI JSON with the actual Loader, repeat checks for stable
results, retain fixture identities, and prove package/config bytes are unchanged.

```bash
bash scripts/test-skill-install.sh
go -C apps/api test -race ./internal/skill ./internal/skill/install/fallback ./cmd/skill -count=1
```

The shared shell checks cover both wrappers' argument preservation, help,
working-directory guards, Go activation failure and command failure propagation.

`TestCheckCommandUsesRealLoaderOutsideProject` exercises the real command,
serialized JSON, dependency failures and filesystem preservation.
`TestSkillCheckerMatchesLoaderAndDoesNotMutate` logs fixture identity and repeatable
evidence. These are deterministic CLI/Loader checks, not browser E2E, real
third-party script audits or live-model task-quality measurements.
