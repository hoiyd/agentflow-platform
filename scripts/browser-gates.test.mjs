import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { profiles, profileEnvironment, runGates } from "./browser-gates.mjs";

// Failure inventory: missing old gates, incompatible fixture flags, repeated
// builds, stale/missing artifacts, all-skipped runs, build failure and cancellation.
test("the original gates remain assigned to isolated compatible profiles", () => {
  const expected = [
    "project-guidance.spec.ts", "project-guidance-workbench.spec.ts", "project-guidance-boundaries.spec.ts",
    "tool-discovery.spec.ts", "mcp.spec.ts", "mcp-connections.spec.ts",
    "filesystem-read.spec.ts", "skill-policy.spec.ts", "structured-output.spec.ts",
    "planning.spec.ts", "filesystem-write.spec.ts", "tool-approval.spec.ts", "identity.spec.ts",
    "resource-authorization.spec.ts", "workspace-agent-config.spec.ts", "agent-selection.spec.ts",
    "execution-boundaries.spec.ts", "sandbox-command.spec.ts", "tool-progress.spec.ts",
    "run-inbox.spec.ts", "run-inbox-boundaries.spec.ts", "owner-admission.spec.ts",
    "usage-breakdown.spec.ts", "reasoning.spec.ts", "partial-output.spec.ts", "keycloak-onboarding.spec.ts"
  ];
  assert.deepEqual(profiles.flatMap(p => p.files).sort(), expected.sort());
  assert.equal(profiles.filter(p => p.files.length === 0).length, 1);
  assert.equal(new Set(profiles.map(p => p.name)).size, profiles.length);
  assert.deepEqual(profiles.find(p => p.name === "filesystem-write").files, ["filesystem-write.spec.ts"]);
  assert.deepEqual(profiles.find(p => p.name === "planning").files, ["planning.spec.ts"]);
  const inference = profiles.find(p => p.name === "inference-evidence");
  assert.deepEqual(inference.files, ["usage-breakdown.spec.ts", "reasoning.spec.ts", "partial-output.spec.ts"]);
  assert.deepEqual(inference.environment, { AGENTFLOW_USAGE_TEST: "1", AGENTFLOW_REASONING_TEST: "1" });
  assert.equal(profiles.find(p => p.name === "tool-approval").environment.AGENTFLOW_TOOL_APPROVAL_TEST, "1");
});

test("profile flags do not inherit another composition or operator overrides", () => {
  const environment = profileEnvironment(profiles.find(p => p.name === "identity"), {
    TEST_DATABASE_URL: "dedicated-test-db", AGENTFLOW_REASONING_TEST: "1",
    AGENTFLOW_TOOL_APPROVAL_TEST: "1", AGENTFLOW_UNEXPECTED_TEST: "1"
  });
  assert.equal(environment.TEST_DATABASE_URL, "dedicated-test-db");
  assert.equal(environment.AGENTFLOW_IDENTITY_TEST, "1");
  assert.equal(environment.AGENTFLOW_REASONING_TEST, "0");
  assert.equal(environment.AGENTFLOW_TOOL_APPROVAL_TEST, "0");
  assert.equal(environment.AGENTFLOW_UNEXPECTED_TEST, "0");
});

function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "agentflow-browser-gates-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  fs.mkdirSync(path.join(root, "apps/web/.next-functional"), { recursive: true });
  return root;
}

test("one build serves fresh profile outputs; failed and skipped gates cannot pass", async t => {
  const root = fixture(t);
  const calls = [];
  const execute = async (command, args, options) => {
    calls.push({ command, args, ...options });
    if (command === "npm") {
      fs.writeFileSync(path.join(root, "apps/web/.next-functional/BUILD_ID"), "fresh-build");
      return 0;
    }
    const directory = options.env.AGENTFLOW_BROWSER_OUTPUT_DIR;
    if (calls.length === 3) assert.match(args[1], /^\(\?:\^\|\/\)identity\\\.spec\\\.ts\$$/);
    assert.equal(options.env.AGENTFLOW_BROWSER_BUILD_ID, "fresh-build");
    fs.mkdirSync(directory, { recursive: true });
    const stats = calls.length === 2 ? { expected: 1, unexpected: 1, skipped: 0 } : { expected: 0, unexpected: 0, skipped: 2 };
    fs.writeFileSync(path.join(directory, "results.json"), JSON.stringify({ stats }));
    return calls.length === 2 ? 1 : 0;
  };
  const report = await runGates(root, ["default", "identity"], { execute, environment: {} });
  assert.equal(calls.filter(c => c.command === "npm").length, 1);
  assert.notEqual(calls[1].env.AGENTFLOW_BROWSER_OUTPUT_DIR, calls[2].env.AGENTFLOW_BROWSER_OUTPUT_DIR);
  assert.deepEqual(report.profiles.map(p => p.status), ["failed", "failed"]);
  assert.equal(report.profiles[0].stats.unexpected, 1);
  assert.equal(report.profiles[1].stats.skipped, 2);
  assert.equal(report.build.id, "fresh-build");
  assert(!JSON.stringify(report).includes("dedicated-test-db"));
});

test("stale evidence is removed before a failed launch", async t => {
  const root = fixture(t);
  const directory = path.join(root, "apps/web/test-results/gates/default");
  fs.mkdirSync(directory, { recursive: true });
  fs.writeFileSync(path.join(directory, "results.json"), JSON.stringify({ stats: { expected: 9 } }));
  const report = await runGates(root, ["default"], { environment: {}, execute: async command => {
    if (command === "npm") {
      fs.writeFileSync(path.join(root, "apps/web/.next-functional/BUILD_ID"), "build");
      return 0;
    }
    return 1;
  } });
  assert.equal(report.profiles[0].status, "failed");
  assert.equal(report.profiles[0].stats, undefined);
  assert(!fs.existsSync(path.join(directory, "results.json")));
});

test("build failure prevents server launches; cancellation leaves later gates not run", async t => {
  for (const buildExit of [1, 0]) {
    const root = fixture(t);
    const stale = path.join(root, "apps/web/test-results/gates/identity");
    fs.mkdirSync(stale, { recursive: true });
    fs.writeFileSync(path.join(stale, "results.json"), "old evidence");
    const calls = [];
    const report = await runGates(root, ["default", "identity"], { environment: {}, execute: async command => {
      calls.push(command);
      if (command === "npm") {
        if (!buildExit) fs.writeFileSync(path.join(root, "apps/web/.next-functional/BUILD_ID"), "build");
        return buildExit;
      }
      return 143;
    } });
    assert.equal(calls.length, buildExit ? 1 : 2);
    assert.equal(report.profiles.at(-1).status, "not_run");
    assert(!fs.existsSync(path.join(stale, "results.json")));
  }
});

test("unknown and duplicate profile names fail before building", async t => {
  const root = fixture(t);
  for (const names of [["missing"], ["default", "default"]]) {
    await assert.rejects(runGates(root, names, { execute: () => assert.fail("must not build") }), /profile/i);
  }
});
