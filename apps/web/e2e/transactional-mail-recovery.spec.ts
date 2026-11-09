import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { expect, test } from "@playwright/test";

const currentDirectory = process.cwd();
const repoRoot = existsSync(resolve(currentDirectory, "docker-compose.yml"))
  ? currentDirectory
  : resolve(currentDirectory, "../..");
const composeFiles = ["-f", "docker-compose.yml", "-f", "docker-compose.microservices.yml"];

function compose(project: string, ...args: string[]) {
  execFileSync(
    "docker",
    ["compose", "--project-name", project, ...composeFiles, ...args],
    { cwd: repoRoot, stdio: "inherit" },
  );
}

async function mailpitHasVerificationMessage(to: string) {
  const mailpitURL = process.env.MAILPIT_API_URL;
  if (!mailpitURL) throw new Error("MAILPIT_API_URL is required for the relay recovery proof");
  let response: Response;
  try {
    response = await fetch(
      `${mailpitURL}/api/v1/search?query=${encodeURIComponent(`to:${to}`)}`,
    );
  } catch {
    return false;
  }
  if (!response.ok) return false;
  const result = (await response.json()) as {
    messages?: Array<{ Subject?: string; subject?: string }>;
  };
  return Boolean(
    result.messages?.some((message) =>
      (message.Subject ?? message.subject ?? "").includes("Verify your email address"),
    ),
  );
}

test("mail worker retries queued verification after the SMTP relay recovers", async ({ page }, testInfo) => {
  test.skip(process.env.RUN_MAILPIT_RECOVERY_E2E !== "1", "requires the isolated Mailpit recovery stack");
  const project = process.env.E2E_COMPOSE_PROJECT;
  if (!project) throw new Error("E2E_COMPOSE_PROJECT is required for the relay recovery proof");

  const email = `recovery-${Date.now()}@sendwise.test`;
  let relayStopped = false;
  page.on("requestfailed", (request) => {
    console.log(`request failed: ${request.url()} (${request.failure()?.errorText})`);
  });
  page.on("response", (response) => {
    if (response.url().includes("/auth/register")) {
      console.log(`registration response: ${response.status()} ${response.url()}`);
    }
  });
  try {
    compose(project, "stop", "mailpit");
    relayStopped = true;

    await page.goto("/register");
    await page.getByPlaceholder("Your name").fill("Relay Recovery User");
    await page.getByPlaceholder("you@example.com").fill(email);
    await page.getByPlaceholder("Workspace name").fill("Relay Recovery Workspace");
    await page.getByPlaceholder("••••••••••").fill("local-relay-recovery-2026!");
    await page.getByRole("button", { name: "Create workspace" }).click();

    await expect(page).toHaveURL(/\/auth\/verify\?email=/);
    await expect(page.getByText(/Your verification email is queued/)).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath("relay-recovery-pending.png"), fullPage: true });

    compose(project, "start", "mailpit");
    relayStopped = false;
    await expect.poll(() => mailpitHasVerificationMessage(email), {
      timeout: 60_000,
      intervals: [1_000, 2_000, 5_000],
    }).toBe(true);
  } finally {
    if (relayStopped) compose(project, "start", "mailpit");
  }
});
