import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { expect, test } from "@playwright/test";

const repoRoot = resolve(process.cwd(), "..");
const composeFiles = ["-f", "docker-compose.yml", "-f", "docker-compose.microservices.yml"];

function compose(project: string, ...args: string[]) {
  execFileSync(
    "docker",
    ["compose", "--project-name", project, ...composeFiles, ...args],
    { cwd: repoRoot, stdio: "ignore" },
  );
}

async function stalwartHasMessage(to: string) {
  const baseURL = process.env.STALWART_URL;
  const username = process.env.STALWART_SUBMIT_USERNAME;
  const password = process.env.STALWART_SUBMIT_PASSWORD;
  const domain = process.env.STALWART_DOMAIN;
  if (!baseURL || !username || !password || !domain) {
    throw new Error("Stalwart URL, domain, and submission credentials are required for this browser proof");
  }

  const authorization = `Basic ${Buffer.from(`${username}@${domain}:${password}`).toString("base64")}`;
  const sessionResponse = await fetch(`${baseURL}/jmap/session`, {
    headers: { authorization },
  });
  if (!sessionResponse.ok) return false;
  const session = (await sessionResponse.json()) as {
    apiUrl: string;
    primaryAccounts: Record<string, string>;
  };
  const accountId = session.primaryAccounts["urn:ietf:params:jmap:mail"];
  if (!accountId) return false;

  const jmapURL = new URL(session.apiUrl, baseURL);
  const base = new URL(baseURL);
  jmapURL.protocol = base.protocol;
  jmapURL.host = base.host;

  async function call(method: string, args: Record<string, unknown>) {
    const response = await fetch(jmapURL, {
      method: "POST",
      headers: {
        authorization,
        "content-type": "application/json",
      },
      body: JSON.stringify({
        using: ["urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"],
        methodCalls: [[method, { ...args, accountId }, "sendwise"]],
      }),
    });
    if (!response.ok) return null;
    const result = (await response.json()) as {
      methodResponses?: Array<[string, Record<string, unknown>]>;
    };
    if (result.methodResponses?.[0]?.[0] !== method) return null;
    return result.methodResponses[0][1];
  }

  const mailboxResponse = await call("Mailbox/get", { ids: null });
  const mailboxes = mailboxResponse?.list as Array<{ id: string; role: string }> | undefined;
  const inbox = mailboxes?.find((mailbox) => mailbox.role === "inbox");
  if (!inbox) return false;

  const queryResponse = await call("Email/query", {
    filter: { inMailbox: inbox.id },
    limit: 50,
  });
  const ids = queryResponse?.ids as string[] | undefined;
  if (!ids?.length) return false;

  const emailResponse = await call("Email/get", {
    ids,
    properties: ["subject", "to"],
  });
  const emails = emailResponse?.list as Array<{
    subject: string;
    to: Array<{ email: string }>;
  }> | undefined;
  return Boolean(
    emails?.some(
      (email) =>
        email.subject === "Verify your email address" &&
        email.to?.some((address) => address.email.toLowerCase() === to.toLowerCase()),
    ),
  );
}

test("real Stalwart outage queues verification and recovers after restart", async ({ page }) => {
  test.skip(process.env.RUN_STALWART_RECOVERY_E2E !== "1", "requires the isolated Stalwart recovery stack");
  const project = process.env.STALWART_COMPOSE_PROJECT;
  if (project !== "sendwise-e2e-stalwart") {
    throw new Error("STALWART_COMPOSE_PROJECT must be the isolated sendwise-e2e-stalwart project");
  }

  let stopped = false;
  try {
    compose(project, "stop", "stalwart");
    stopped = true;

    await page.goto("/register");
    await page.getByPlaceholder("Your name").fill("Stalwart Recovery User");
    await page.getByPlaceholder("you@example.com").fill("noreply@sendwise.test");
    await page.getByPlaceholder("Workspace name").fill("Stalwart Recovery Workspace");
    await page.getByPlaceholder("••••••••••").fill("local-Stalwart-2026!");
    await page.getByRole("button", { name: "Create workspace" }).click();

    await expect(page).toHaveURL(/\/auth\/verify\?email=.*&delivery=retrying/);
    await expect(page.getByText(/The mail relay is temporarily unavailable/)).toBeVisible();

    compose(project, "start", "stalwart");
    stopped = false;

    await page.getByRole("button", { name: "Resend verification email" }).click();
    await expect(page.getByRole("button", { name: "Email requested" })).toBeVisible();
    await expect.poll(() => stalwartHasMessage("noreply@sendwise.test"), {
      timeout: 60_000,
      intervals: [1_000, 2_000, 5_000],
    }).toBe(true);
  } finally {
    if (stopped) compose(project, "start", "stalwart");
  }
});
