import { spawn } from "node:child_process";
import fs from "node:fs";
import { constants } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseEnv } from "node:util";

const directory = path.dirname(fileURLToPath(import.meta.url));
const scopes = {
  "dev.sh": new Set(["API_PORT", "WEB_PORT", "NEXT_PUBLIC_API_BASE_URL"]),
  "agentflow.sh": new Set(["AGENTFLOW_API_URL", "AGENTFLOW_WEB_URL", "AGENTFLOW_WORKSPACE_ID", "AGENTFLOW_SESSION_FILE"]),
  "demo-preflight.sh": new Set(["BENCHMARK_REPORT_DIR", "LIVE_BENCHMARK_DIR", "DEMO_MODE", "API_BASE_URL", "WEB_BASE_URL"]),
  "release-recovery-drill.sh": new Set(["TEST_DATABASE_URL", "RELEASE_DRILL_DATABASE_URL", "RELEASE_DRILL_REPORT_PATH", "RELEASE_DRILL_REPO_ROOT"]),
  "test.sh": null,
  "test-browser.sh": null,
  "test-keycloak-auth.sh": null,
  "evaluation/eval.sh": null,
  "evaluation/benchmark-evidence.sh": null,
  "evaluation/load-evidence.sh": null,
  "evaluation/llama-compatibility-evidence.sh": null,
  "evaluation/tokenization-calibration.sh": null,
};

try {
  const entry = fs.realpathSync(path.resolve(process.argv[2] || ""));
  const relative = path.relative(fs.realpathSync(directory), entry);
  if (!Object.hasOwn(scopes, relative)) throw new Error("unsupported entry");
  const declared = parseEnv(fs.readFileSync(path.join(directory, ".env.example"), "utf8"));
  let local = {};
  try {
    local = parseEnv(fs.readFileSync(path.join(directory, ".env"), "utf8"));
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
  }
  if (Object.keys(local).some((key) => !Object.hasOwn(declared, key))) throw new Error("undeclared setting");
  const environment = { ...process.env, AGENTFLOW_SCRIPT_ENV_READY: "1" };
  for (const [key, value] of Object.entries(local)) {
    const allowed = scopes[relative]?.has(key) ?? !scopes["dev.sh"].has(key);
    if (allowed && !Object.hasOwn(process.env, key)) {
      environment[key] = value;
    }
  }
  const child = spawn("bash", [entry, ...process.argv.slice(3)], { env: environment, stdio: "inherit" });
  const forward = (signal) => { if (child.exitCode === null) child.kill(signal); };
  process.on("SIGINT", forward);
  process.on("SIGTERM", forward);
  const cleanup = () => {
    process.off("SIGINT", forward);
    process.off("SIGTERM", forward);
  };
  child.once("error", () => {
    cleanup();
    console.error("Cannot start the configured script.");
    process.exitCode = 1;
  });
  child.once("exit", (code, signal) => {
    cleanup();
    process.exitCode = code ?? 128 + (constants.signals[signal] ?? 1);
  });
} catch {
  console.error("Cannot load scripts/.env; check its readability and declared variable names in scripts/.env.example.");
  process.exitCode = 1;
}
