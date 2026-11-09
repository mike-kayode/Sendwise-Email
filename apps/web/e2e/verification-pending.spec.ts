import { expect, test } from "@playwright/test";

test("queued verification delivery offers a resend action", async ({ page }, testInfo) => {
  const email = `retry-${Date.now()}@example.test`;
  let resentEmail = "";

  await page.route("**/api/v1/auth/register", async (route) => {
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({
        message: "Account created. Your verification email is queued; if delivery fails, we'll keep retrying.",
        email,
        delivery_status: "queued",
      }),
    });
  });
  await page.route("**/api/v1/auth/resend-verification", async (route) => {
    resentEmail = (route.request().postDataJSON() as { email: string }).email;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ message: "If that address needs verification, a new link will be sent." }),
    });
  });

  await page.goto("/register");
  await page.getByPlaceholder("Your name").fill("Retry User");
  await page.getByPlaceholder("you@example.com").fill(email);
  await page.getByPlaceholder("Workspace name").fill("Retry Workspace");
  await page.getByPlaceholder("••••••••••").fill("a-strong-password");
  await page.getByRole("button", { name: "Create workspace" }).click();

  await expect(page).toHaveURL(/\/auth\/verify\?email=/);
  await expect(page.getByText(/Your verification email is queued/)).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("pending-verification.png"), fullPage: true });
  await page.getByRole("button", { name: "Resend verification email" }).click();
  await expect.poll(() => resentEmail).toBe(email);
  await expect(page.getByRole("button", { name: "Email requested" })).toBeVisible();
});

test("expired or already-used verification link offers a helpful recovery", async ({ page }, testInfo) => {
  await page.route("**/api/v1/auth/verify", async (route) => {
    await route.fulfill({
      status: 400,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "invalid_token",
          message: "This verification link is invalid or has expired.",
        },
      }),
    });
  });

  await page.goto("/auth/verify?token=expired-or-used");
  await expect(page.getByText("This verification link is invalid or has expired.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Resend verification email" })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("expired-verification.png"), fullPage: true });
});

test("successful verification displays a confirmation", async ({ page }, testInfo) => {
  await page.route("**/api/v1/auth/verify", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ message: "Email verified. You can now sign in." }),
    });
  });

  await page.goto("/auth/verify?token=valid-test-token");
  await expect(page.getByText("Verified", { exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("verified-email.png"), fullPage: true });
});
