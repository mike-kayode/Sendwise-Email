import { expect, test, type APIRequestContext } from "@playwright/test";

const API_ORIGIN = process.env.E2E_API_ORIGIN ?? "http://localhost:8084";
const MAILPIT_API = process.env.MAILPIT_API_URL ?? "http://localhost:8025";

async function findMailLink(
  request: APIRequestContext,
  email: string,
  subject: string,
): Promise<string> {
  let foundLink = "";
  await expect.poll(async () => {
    const search = await request.get(`${MAILPIT_API}/api/v1/search`, {
      params: { query: `to:${email}` },
    });
    if (!search.ok()) return "";
    const result = (await search.json()) as {
      messages?: Array<{ ID?: string; id?: string; Subject?: string; subject?: string }>;
    };
    const message = (result.messages ?? []).find((item) =>
      (item.Subject ?? item.subject ?? "").toLowerCase().includes(subject.toLowerCase()),
    );
    const id = message?.ID ?? message?.id;
    if (!id) return "";
    const detailResponse = await request.get(`${MAILPIT_API}/api/v1/message/${encodeURIComponent(id)}`);
    if (!detailResponse.ok()) return "";
    const content = JSON.stringify(await detailResponse.json());
    const lowerSubject = subject.toLowerCase();
    const expectedPath = lowerSubject.includes("verify")
      ? "/auth/verify?"
      : lowerSubject.includes("invited")
        ? "/auth/accept-invite?"
        : "/auth/reset-password?";
    const link = content.match(/https?:\/\/[^\s"'<>\\]+/g)?.find((item) => item.includes(expectedPath));
    if (!link) return "";
    foundLink = link.replaceAll("\\u0026", "&").replaceAll("&amp;", "&");
    return foundLink;
  }, { timeout: 45_000 }).not.toBe("");
  return foundLink;
}

function tokenFrom(link: string): string {
  const token = new URL(link).searchParams.get("token");
  if (!token) throw new Error("The email link did not contain a token");
  return token;
}

test("P2 account email, password reset, and logout journey", async ({ page, request }) => {
  const stamp = Date.now();
  const email = `p2-${stamp}@example.test`;
  const workspaceName = `P2 Workspace ${stamp}`;
  const initialPassword = `Initial-${stamp}-Password!`;
  const newPassword = `Updated-${stamp}-Password!`;

  await page.goto("/register");
  await page.getByPlaceholder("Your name").fill("P2 Integration User");
  await page.getByPlaceholder("you@example.com").fill(email);
  await page.getByPlaceholder("Workspace name").fill(workspaceName);
  await page.getByPlaceholder("••••••••••").fill(initialPassword);
  await page.getByRole("button", { name: "Create workspace" }).click();
  await page.waitForURL("**/auth/verify?email=**");
  await expect(page.getByRole("heading", { name: "Check your inbox" })).toBeVisible();

  const verificationToken = tokenFrom(await findMailLink(request, email, "Verify your email address"));
  await page.goto(`/auth/verify?token=${encodeURIComponent(verificationToken)}`);
  await expect(page.getByText("Verified", { exact: true })).toBeVisible();
  await page.waitForURL("**/login");

  await page.getByPlaceholder("you@example.com").fill(email);
  await page.getByPlaceholder("••••••••").fill(initialPassword);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/dashboard");
  const firstAccessToken = await page.evaluate(() => localStorage.getItem("access_token"));
  expect(firstAccessToken).toBeTruthy();

  await page.goto("/forgot-password");
  await page.getByPlaceholder("you@example.com").fill(email);
  await page.getByRole("button", { name: "Send reset link" }).click();
  await page.waitForURL("**/login");

  const resetToken = tokenFrom(await findMailLink(request, email, "Reset your password"));
  await page.goto(`/auth/reset-password?token=${encodeURIComponent(resetToken)}`);
  const resetPasswordFields = page.locator('input[autocomplete="new-password"]');
  await resetPasswordFields.nth(0).fill(newPassword);
  await resetPasswordFields.nth(1).fill(newPassword);
  await page.getByRole("button", { name: "Reset password" }).click();
  await page.waitForURL("**/login");

  await page.getByPlaceholder("you@example.com").fill(email);
  await page.getByPlaceholder("••••••••").fill(newPassword);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/dashboard");
  let accessToken = await page.evaluate(() => localStorage.getItem("access_token"));
  expect(accessToken).toBeTruthy();

  let refreshRequests = 0;
  const countRefreshRequest = (request: import("@playwright/test").Request) => {
    if (new URL(request.url()).pathname === "/api/v1/auth/refresh") refreshRequests++;
  };
  page.on("request", countRefreshRequest);
  const settingsReady = page.waitForResponse((response) =>
    response.url().includes("/api/v1/invitations") && response.status() === 200,
  );
  // Corrupt only localStorage, without dispatching the sync event: notifying
  // the still-mounted dashboard wakes providers.tsx -> me() -> a refresh POST
  // that page.goto then aborts. The server rotates on that aborted request, so
  // the settings page's own refresh presents a stale cookie and identity's
  // reuse detection revokes the whole session family. On the fresh settings
  // load exactly one refresh must recover the session.
  await page.evaluate(() => {
    localStorage.setItem("access_token", "expired-test-token");
  });
  await page.goto("/settings");
  await settingsReady;
  await expect(page.getByText("Workspace name")).toBeVisible();
  await expect.poll(() => refreshRequests).toBe(1);
  await page.waitForLoadState("networkidle");
  expect(refreshRequests).toBe(1);
  accessToken = await page.evaluate(() => localStorage.getItem("access_token"));
  expect(accessToken).toBeTruthy();
  expect(accessToken).not.toBe("expired-test-token");
  page.off("request", countRefreshRequest);

  const inviteEmail = `p2-invite-${stamp}@example.test`;
  const invitationResponse = await request.post(`${API_ORIGIN}/api/v1/invitations`, {
    headers: { Authorization: `Bearer ${accessToken}` },
    data: { email: inviteEmail, role: "viewer" },
  });
  expect(invitationResponse.status()).toBe(201);
  const invitationLink = await findMailLink(request, inviteEmail, `You are invited to join ${workspaceName}`);
  expect(new URL(invitationLink).pathname).toBe("/auth/accept-invite");

  await page.getByRole("button", { name: "Sign out" }).click();
  await page.waitForURL("**/login");

  const oldTokenResponse = await request.get(`${API_ORIGIN}/api/v1/auth/me`, {
    headers: { Authorization: `Bearer ${accessToken}` },
  });
  expect(oldTokenResponse.status()).toBe(401);
  const error = (await oldTokenResponse.json()) as { error?: { code?: string } };
  expect(error.error?.code).toBe("session_revoked");
});
