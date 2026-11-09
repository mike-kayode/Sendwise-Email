import { expect, test } from "@playwright/test";

test("queued verification delivery offers a resend action", async ({ page }) => {
  const email = `retry-${Date.now()}@example.test`;
  let resentEmail = "";

  await page.route("**/api/v1/auth/register", async (route) => {
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({
        message: "Account created. Verification email delivery is retrying.",
        email,
        delivery_status: "retry_queued",
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

  await expect(page).toHaveURL(/\/auth\/verify\?email=.*&delivery=retrying/);
  await expect(page.getByText(/The mail relay is temporarily unavailable/)).toBeVisible();
  await page.getByRole("button", { name: "Resend verification email" }).click();
  await expect.poll(() => resentEmail).toBe(email);
  await expect(page.getByRole("button", { name: "Email requested" })).toBeVisible();
});
