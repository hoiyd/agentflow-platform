import { spawn, execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const profiles = [
  { name: "default", files: [], environment: {} },
  { name: "project-guidance", files: ["project-guidance.spec.ts", "project-guidance-workbench.spec.ts", "project-guidance-boundaries.spec.ts"], environment: { AGENTFLOW_PROJECT_GUIDANCE_TEST: "1" } },
  { name: "tool-discovery", files: ["tool-discovery.spec.ts"], environment: { AGENTFLOW_TOOL_DISCOVERY_TEST: "1" } },
  { name: "mcp", files: ["mcp.spec.ts"], environment: { AGENTFLOW_MCP_TEST: "1" } },
  { name: "mcp-connections", files: ["mcp-connections.spec.ts"], environment: { AGENTFLOW_MCP_CONNECTION_TEST: "1" } },
  { name: "filesystem-read", files: ["filesystem-read.spec.ts"], environment: { AGENTFLOW_FILESYSTEM_TEST: "1" } },
  { name: "skill-policy", files: ["skill-policy.spec.ts"], environment: { AGENTFLOW_SKILL_POLICY_TEST: "1" } },
  { name: "structured-output", files: ["structured-output.spec.ts"], environment: { AGENTFLOW_STRUCTURED_OUTPUT_TEST: "1" } },
  // These fixtures both write report.md. Equal grants do not make their data compatible.
  { name: "filesystem-write", files: ["filesystem-write.spec.ts"], environment: { AGENTFLOW_FILESYSTEM_WRITE_TEST: "1" } },
  { name: "planning", files: ["planning.spec.ts"], environment: { AGENTFLOW_PLANNING_TEST: "1" } },
  { name: "tool-approval", files: ["tool-approval.spec.ts"], environment: { AGENTFLOW_FILESYSTEM_WRITE_TEST: "1", AGENTFLOW_TOOL_APPROVAL_TEST: "1" } },
  { name: "identity", files: ["identity.spec.ts", "resource-authorization.spec.ts", "workspace-agent-config.spec.ts", "agent-selection.spec.ts"], environment: { AGENTFLOW_IDENTITY_TEST: "1" } },
  { name: "execution-boundary", files: ["execution-boundaries.spec.ts"], environment: { AGENTFLOW_EXECUTION_BOUNDARY_TEST: "1" } },
  { name: "sandbox", files: ["sandbox-command.spec.ts", "tool-progress.spec.ts", "run-inbox.spec.ts"], environment: { AGENTFLOW_SANDBOX_BROWSER_TEST: "1" } },
  { name: "inbox-boundaries", files: ["run-inbox-boundaries.spec.ts"], environment: { AGENTFLOW_INBOX_EDGE_TEST: "1" } },
  { name: "owner-admission", files: ["owner-admission.spec.ts"], environment: { AGENTFLOW_OWNER_ADMISSION_TEST: "1", AGENTFLOW_IDENTITY_TEST: "1" } },
  // Pricing and display-format capabilities are independent route fields;
  // these scenarios use distinct tasks and do not change operator permissions.
  { name: "inference-evidence", files: ["usage-breakdown.spec.ts", "reasoning.spec.ts", "partial-output.spec.ts"], environment: { AGENTFLOW_USAGE_TEST: "1", AGENTFLOW_REASONING_TEST: "1" } },
  { name: "keycloak", files: ["keycloak-onboarding.spec.ts"], environment: {}, script: "test-keycloak-auth.sh" }
];

export function profileEnvironment(profile, base) {
  const environment = { ...base };
  const flags = new Set([...Object.keys(base), ...profiles.flatMap(p => Object.keys(p.environment))]);
  for (const key of flags) if (/^AGENTFLOW_.*_TEST$/.test(key)) environment[key] = "0";
  return { ...environment, ...profile.environment };
}

async function execute(command, args, options) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { ...options, stdio: "inherit" });
    const forward = signal => child.kill(signal);
    const stop = () => forward("SIGTERM");
    const interrupt = () => forward("SIGINT");
    process.on("SIGTERM", stop);
    process.on("SIGINT", interrupt);
    const cleanup = () => { process.off("SIGTERM", stop); process.off("SIGINT", interrupt); };
    child.once("error", error => { cleanup(); reject(error); });
    child.once("exit", (code, signal) => { cleanup(); resolve(code ?? (signal === "SIGINT" ? 130 : 143)); });
  });
}

export async function runGates(root, names = [], { execute: run = execute, environment = process.env } = {}) {
  const selected = names.length ? names.map(name => {
    const profile = profiles.find(p => p.name === name);
    if (!profile) throw new Error(`Unknown browser profile: ${name}`);
    return profile;
  }) : profiles;
  if (new Set(selected).size !== selected.length) throw new Error("Duplicate browser profile");
  const web = path.join(root, "apps/web");
  const output = path.join(web, "test-results/gates");
  fs.rmSync(output, { recursive: true, force: true });
  fs.mkdirSync(output, { recursive: true });
  let revision = "unknown";
  try { revision = execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim(); } catch { /* Isolated driver tests have no Git repository. */ }
  const report = {
    schema: "browser-gates-v1", revision,
    build: { status: "not_run", count: 0 },
    profiles: selected.map(p => ({ name: p.name, files: p.files, flags: p.environment, status: "not_run" })),
    limitations: ["Controlled Provider and disposable Postgres; not live model quality or VM isolation", "Build reuse is bounded to this invocation; each profile restarts isolated servers"]
  };
  const save = () => fs.writeFileSync(path.join(output, "summary.json"), JSON.stringify(report, null, 2) + "\n");
  save();
  const buildStarted = Date.now();
  report.build.count = 1;
  const buildEnvironment = {
    ...profileEnvironment({ environment: {} }, environment),
    NEXT_PUBLIC_API_BASE_URL: "http://127.0.0.1:18080", AGENTFLOW_TEST_DIST_DIR: ".next-functional"
  };
  try {
    const code = await run("npm", ["run", "build", "--", "--webpack"], { cwd: web, env: buildEnvironment });
    report.build.status = code === 0 ? "passed" : "failed";
    report.build.exit_code = code;
    if (code !== 0) return report;
    report.build.id = fs.readFileSync(path.join(web, ".next-functional/BUILD_ID"), "utf8").trim();
    if (!report.build.id) throw new Error("Functional build has no BUILD_ID");
    report.build.duration_ms = Date.now() - buildStarted;
    for (const [index, profile] of selected.entries()) {
      const entry = report.profiles[index];
      const directory = path.join(output, profile.name);
      fs.mkdirSync(directory, { recursive: true });
      entry.output_directory = path.relative(root, directory);
      const started = Date.now();
      try {
        console.log(`\nBrowser profile: ${profile.name}`);
        // Playwright file arguments are regexes: an unanchored planning.spec.ts
        // also runs run-inbox-planning.spec.ts under the wrong composition.
        const args = profile.script ? [] : profile.files.map(file => `(?:^|/)${file.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`);
        entry.exit_code = await run("bash", [path.join(root, "scripts", profile.script ?? "test-browser.sh"), ...args], {
          cwd: root, env: { ...profileEnvironment(profile, environment), AGENTFLOW_BROWSER_BUILD_ID: report.build.id, AGENTFLOW_BROWSER_OUTPUT_DIR: directory }
        });
        try { entry.stats = JSON.parse(fs.readFileSync(path.join(directory, "results.json"), "utf8")).stats; } catch { /* A launch failure has no Playwright report. */ }
        entry.status = entry.exit_code === 0 && entry.stats?.expected > 0 && entry.stats?.unexpected === 0 ? "passed" : "failed";
      } catch {
        entry.status = "failed";
      }
      entry.duration_ms = Date.now() - started;
      save();
      if (entry.exit_code === 130 || entry.exit_code === 143) break;
    }
  } catch {
    report.build.status = "failed";
  } finally {
    report.build.duration_ms ??= Date.now() - buildStarted;
    save();
  }
  return report;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try {
    const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
    if (!process.env.TEST_DATABASE_URL) throw new Error("TEST_DATABASE_URL is required for isolated browser gates");
    const report = await runGates(root, process.argv.slice(2));
    process.exitCode = report.build.status === "passed" && report.profiles.every(p => p.status === "passed") ? 0 : 1;
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
