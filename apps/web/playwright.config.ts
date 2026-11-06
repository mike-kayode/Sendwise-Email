import { defineConfig, devices } from "@playwright/test";

// E2E tests run against a stack that is already up:
//   docker compose up -d   (or ./scripts/deploy.sh)
// The default browser target is the local web app on the host; override via
// E2E_BASE_URL for containerized runs or alternate endpoints.
const baseURL = process.env.E2E_BASE_URL ?? "http://localhost:3000";

export default defineConfig({
  testDir: "./e2e",
  timeout: 120_000,
  expect: { timeout: 20_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [["list"]],
  use: {
    baseURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
