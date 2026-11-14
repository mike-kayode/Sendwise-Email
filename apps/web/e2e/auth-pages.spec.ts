import { expect, test } from "@playwright/test";

test("login and registration pages show one clear account-switch link", async ({ page }) => {
  await page.goto("/login");
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await expect(page.getByText("New to Sendwise?")).toHaveCount(0);
  await expect(page.locator(".auth-form").getByRole("link", { name: "Create account", exact: true })).toHaveCount(1);
  await expect(page.locator("form")).toBeVisible();

  await page.goto("/register");
  await expect(page.getByRole("heading", { name: "Create account" })).toBeVisible();
  await expect(page.locator(".auth-form").getByRole("link", { name: "Sign in", exact: true })).toHaveCount(1);
  await expect(page.locator(".auth-form form > .auth-submit + .auth-bottom-link")).toBeVisible();
  await expect(page.locator("form")).toBeVisible();
});
