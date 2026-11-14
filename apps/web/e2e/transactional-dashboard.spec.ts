import { expect, test, type APIRequestContext } from "@playwright/test";

const MAILPIT_API = process.env.MAILPIT_API_URL ?? "http://localhost:8025";

async function findVerificationLink(request: APIRequestContext, email: string): Promise<string> {
  let verificationLink = "";
  await expect.poll(async () => {
    const search = await request.get(`${MAILPIT_API}/api/v1/search`, {
      params: { query: `to:${email}` },
    });
    if (!search.ok()) return "";
    const result = await search.json() as {
      messages?: Array<{ ID?: string; id?: string; Subject?: string; subject?: string }>;
    };
    const message = (result.messages ?? []).find((item) =>
      (item.Subject ?? item.subject ?? "").toLowerCase().includes("verify your email"),
    );
    const id = message?.ID ?? message?.id;
    if (!id) return "";
    const detail = await request.get(`${MAILPIT_API}/api/v1/message/${encodeURIComponent(id)}`);
    if (!detail.ok()) return "";
    const content = JSON.stringify(await detail.json());
    verificationLink =
      content.match(/https?:\/\/[^\s"'<>\\]+/g)?.find((url) => url.includes("/auth/verify?")) ?? "";
    return verificationLink;
  }, { timeout: 45_000 }).not.toBe("");
  return verificationLink.replaceAll("\\u0026", "&").replaceAll("&amp;", "&");
}

test("owner manages transactional SMTP credentials in the dashboard", async ({ page, request }) => {
  test.setTimeout(120_000);
  const stamp = Date.now();
  const email = `transactional-dashboard-${stamp}@example.test`;
  const password = `Transactional-${stamp}-Password!`;

  await page.goto("/register");
  await page.getByPlaceholder("Your name").fill("Transactional Dashboard Owner");
  await page.getByPlaceholder("you@example.com").fill(email);
  await page.getByPlaceholder("Workspace name").fill(`Transactional Dashboard ${stamp}`);
  await page.getByPlaceholder("••••••••••").fill(password);
  await page.getByRole("button", { name: "Create account" }).click();
  await page.waitForURL("**/auth/verify?email=**");

  const verificationLink = await findVerificationLink(request, email);
  const verificationToken = new URL(verificationLink).searchParams.get("token");
  expect(verificationToken).toBeTruthy();
  await page.goto(`/auth/verify?token=${encodeURIComponent(verificationToken!)}`);
  await expect(page.getByText("Verified", { exact: true })).toBeVisible();
  await page.waitForURL("**/login");

  await page.getByPlaceholder("you@example.com").fill(email);
  await page.getByPlaceholder("••••••••").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/dashboard");
  await page.goto("/transactional");
  await expect(page.getByRole("heading", { name: "Transactional email" })).toBeVisible();
  await expect(page.getByText("Accepted today", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "SMTP submission credentials" })).toBeVisible();

  const credentialName = `Browser SMTP ${stamp}`;
  await page.getByLabel("SMTP credential name").fill(credentialName);
  await page.getByRole("button", { name: "Create SMTP credential" }).click();
  const oneTimeCredential = page.getByTestId("smtp-one-time-credential");
  await expect(oneTimeCredential).toBeVisible();
  const secretValues = await oneTimeCredential.locator("code").allTextContents();
  expect(secretValues).toHaveLength(2);
  expect(secretValues[0]).toMatch(/^sw_/);
  expect(secretValues[1]).toMatch(/^sw_smtp_/);
  const [username, oneTimePassword] = secretValues;

  const credentialRow = page.getByText(credentialName, { exact: true }).locator("xpath=../..");
  await expect(credentialRow).toContainText(username);
  await oneTimeCredential.getByRole("button", { name: "Dismiss" }).click();
  await expect(page.getByText(oneTimePassword, { exact: true })).toHaveCount(0);

  await credentialRow.getByRole("button", { name: "Revoke" }).click();
  const revokedRow = page.getByText(credentialName, { exact: true }).locator("xpath=../..");
  await expect(revokedRow).toContainText("revoked");
  await expect(revokedRow.getByRole("button", { name: "Revoke" }))
    .toHaveCount(0);
});
