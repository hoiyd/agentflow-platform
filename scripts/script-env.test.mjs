import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const scripts = path.dirname(fileURLToPath(import.meta.url));

// Failure inventory: missing files, override/empty precedence, literal values,
// wrong-scope secrets, unknown settings, argv, exit status and cancellation.
test("script configuration process boundary", async (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "agentflow-script-env-"));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  for (const name of ["run-with-env.mjs", "script-env.sh"]) {
    fs.copyFileSync(path.join(scripts, name), path.join(directory, name));
  }
  fs.writeFileSync(path.join(directory, ".env.example"), "API_PORT=8080\nWEB_PORT=3000\nNEXT_PUBLIC_API_BASE_URL=\nOPENAI_API_KEY=\nAGENTFLOW_WORKSPACE_ID=\nTEST_DATABASE_URL=\n");
  const source = 'source "$(dirname "${BASH_SOURCE[0]}")/script-env.sh"\nexec node "$(dirname "${BASH_SOURCE[0]}")/probe.mjs" "$@"\n';
  for (const name of ["dev.sh", "agentflow.sh", "test-browser.sh"]) {
    fs.writeFileSync(path.join(directory, name), source);
  }
  fs.writeFileSync(path.join(directory, "probe.mjs"), `
import fs from "node:fs";
if (process.argv.includes("--wait")) {
  process.on("SIGTERM", () => { fs.writeFileSync(process.argv.at(-1), "stopped"); process.exit(143); });
  setTimeout(() => process.exit(99), 5000);
  console.log("READY");
} else if (process.argv.includes("--fail")) process.exit(23);
else console.log(JSON.stringify({port:process.env.API_PORT??null,webPort:process.env.WEB_PORT??null,apiBase:process.env.NEXT_PUBLIC_API_BASE_URL??null,key:process.env.OPENAI_API_KEY??null,workspace:process.env.AGENTFLOW_WORKSPACE_ID??null,database:process.env.TEST_DATABASE_URL??null,args:process.argv.slice(2),cwd:process.cwd()}));
`);
  const envFile = path.join(directory, ".env");
  const writeEnv = (value) => fs.writeFileSync(envFile, value, { mode: 0o600 });
  const env = { PATH: process.env.PATH, HOME: directory };
  const run = (entry, extra = {}, args = []) => spawnSync("bash", [path.join(directory, entry), ...args], {
    env: { ...env, ...extra }, cwd: os.tmpdir(), encoding: "utf8", timeout: 10000,
  });
  const observations = [];
  const check = (name, result, expected) => {
    assert.equal(result.status, 0, name);
    const value = JSON.parse(result.stdout);
    for (const [key, expectedValue] of Object.entries(expected)) assert.deepEqual(value[key], expectedValue, `${name}: ${key}`);
    observations.push({ name, status: "passed", expected });
  };

  await t.test("missing optional file preserves the process environment", () => {
    check("missing_file", run("dev.sh", { API_PORT: "9191" }), { port: "9191", key: null });
  });
  await t.test("imports test settings without evaluating shell text", () => {
    const marker = path.join(directory, "must-not-exist");
    const literal = `$(touch ${marker})`;
    writeEnv(`OPENAI_API_KEY=${literal}\nTEST_DATABASE_URL="synthetic # test database"\n`);
    check("literal_values", run("test-browser.sh"), { key: literal, database: "synthetic # test database" });
    assert.equal(fs.existsSync(marker), false);
  });
  await t.test("external values, including empty values, win", () => {
    writeEnv("OPENAI_API_KEY=file-value\nTEST_DATABASE_URL=file-database\n");
    check("external_precedence", run("test-browser.sh", { OPENAI_API_KEY: "external-value", TEST_DATABASE_URL: "" }), { key: "external-value", database: "" });
  });
  await t.test("development cannot import test credentials or database", () => {
    writeEnv("API_PORT=9192\nOPENAI_API_KEY=test-only\nTEST_DATABASE_URL=test-only-database\n");
    check("development_scope", run("dev.sh"), { port: "9192", key: null, database: null });
  });
  await t.test("test commands do not inherit development ports or frontend URL from the file", () => {
    writeEnv("API_PORT=9192\nWEB_PORT=9193\nNEXT_PUBLIC_API_BASE_URL=http://development-only\nTEST_DATABASE_URL=synthetic-test-db\n");
    check("test_scope", run("test-browser.sh"), { port: null, webPort: null, apiBase: null, database: "synthetic-test-db" });
    check("explicit_test_override", run("test-browser.sh", { NEXT_PUBLIC_API_BASE_URL: "http://explicit-override" }), { apiBase: "http://explicit-override" });
  });
  await t.test("client imports only its own configuration", () => {
    writeEnv("AGENTFLOW_WORKSPACE_ID=owned-space\nOPENAI_API_KEY=test-only\nAPI_PORT=9193\n");
    check("client_scope", run("agentflow.sh", {}, ["argument with spaces", "--flag"]), { workspace: "owned-space", key: null, port: null, args: ["argument with spaces", "--flag"], cwd: fs.realpathSync(os.tmpdir()) });
  });
  await t.test("multiline dotenv values are preserved", () => {
    writeEnv('OPENAI_API_KEY="first\nsecond"\n');
    check("multiline", run("test-browser.sh"), { key: "first\nsecond" });
  });
  await t.test("unknown business settings fail without printing values", () => {
    writeEnv("DATABASE_URL=synthetic-do-not-print\n");
    const result = run("test-browser.sh");
    assert.equal(result.status, 1);
    assert.equal(result.stdout, "");
    assert(!result.stderr.includes("synthetic-do-not-print"));
    observations.push({ name: "unknown_setting_rejected", status: "passed" });
  });
  await t.test("the child exit status is retained", () => {
    writeEnv("");
    assert.equal(run("test-browser.sh", {}, ["--fail"]).status, 23);
    observations.push({ name: "exit_status", status: "passed", exit: 23 });
  });
  await t.test("termination reaches the child", async () => {
    writeEnv("");
    const marker = path.join(directory, "stopped");
    const child = spawn("bash", [path.join(directory, "test-browser.sh"), "--wait", marker], { env, stdio: ["ignore", "pipe", "pipe"] });
    const exited = new Promise((resolve, reject) => { child.once("error", reject); child.once("exit", (code, signal) => resolve({ code, signal })); });
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("fixture did not become ready")), 5000);
      child.stdout.on("data", (data) => { if (data.toString().includes("READY")) { clearTimeout(timer); resolve(); } });
      child.once("error", reject);
    });
    child.kill("SIGTERM");
    assert.deepEqual(await exited, { code: 143, signal: null });
    assert.equal(fs.readFileSync(marker, "utf8"), "stopped");
    observations.push({ name: "signal_forwarding", status: "passed", exit: 143 });
  });

  const report = path.resolve(scripts, "../.cache/evidence/script-env-loader.json");
  fs.mkdirSync(path.dirname(report), { recursive: true });
  fs.writeFileSync(report, JSON.stringify({
    schema: "script-env-functional-v1", command: "node --test scripts/script-env.test.mjs",
    loaderHash: createHash("sha256").update(fs.readFileSync(path.join(scripts, "run-with-env.mjs"))).digest("hex"),
    configuration: "synthetic, isolated temporary dotenv; real local .env files never read",
    observations, limitations: ["No database, model, browser or live service was started"],
  }, null, 2) + "\n");
});
