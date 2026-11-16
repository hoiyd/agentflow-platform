import { defineConfig } from "@playwright/test";
import path from "node:path";
import fs from "node:fs";

if (!process.env.TEST_DATABASE_URL) {
  throw new Error("TEST_DATABASE_URL is required; browser tests never use the application database");
}

const buildId = process.env.AGENTFLOW_BROWSER_BUILD_ID;
if (buildId && fs.readFileSync(path.resolve(__dirname, ".next-functional/BUILD_ID"), "utf8").trim() !== buildId) {
  throw new Error("The functional frontend does not match this browser gate's build");
}
const output = process.env.AGENTFLOW_BROWSER_OUTPUT_DIR;

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 15_000 },
  outputDir: output ? path.join(output, "artifacts") : "test-results",
  reporter: [["list"], ["html", { open: "never", ...(output ? { outputFolder: path.join(output, "report") } : {}) }], ["json", { outputFile: output ? path.join(output, "results.json") : "test-results/results.json" }]],
  use: {
    actionTimeout: 15_000,
    baseURL: "http://127.0.0.1:13000",
    viewport: { width: 1440, height: 1000 },
    trace: { mode: "retain-on-failure", screenshots: false },
    screenshot: "off",
    video: "off"
  },
  webServer: [
    {
      name: "isolated-go-api",
      cwd: path.resolve(__dirname, "../api"),
      command: "bash -c 'source ../../scripts/go-env.sh && activate_agentflow_go && AGENTFLOW_BROWSER_TEST=1 go test ./app -run ^TestBrowserServer$ -count=1 -timeout=10m -v'",
      url: "http://127.0.0.1:18080/health",
      timeout: 120_000,
      reuseExistingServer: false,
      gracefulShutdown: { signal: "SIGTERM", timeout: 15_000 }
    },
    {
      name: "next-production",
      command: `${buildId ? "" : "npm run build -- --webpack && "}npm run start -- --hostname 127.0.0.1 --port 13000`,
      env: {
        NEXT_PUBLIC_API_BASE_URL: "http://127.0.0.1:18080",
        AGENTFLOW_TEST_DIST_DIR: ".next-functional"
      },
      url: "http://127.0.0.1:13000/workspace",
      timeout: 180_000,
      reuseExistingServer: false,
      gracefulShutdown: { signal: "SIGTERM", timeout: 5_000 }
    }
  ]
});
