import { expect, test } from "@playwright/test";

// The E2E account is created by `go run ./services/identity/cmd/devseed`
// (see docs/STATUS.md). Override with DEPLOY_SMOKE_EMAIL / DEPLOY_SMOKE_PASSWORD.
const EMAIL = process.env.DEPLOY_SMOKE_EMAIL ?? "smoke@sendwise.test";
const PASSWORD = process.env.DEPLOY_SMOKE_PASSWORD ?? "SmokeTest2026!";

test("CSV import: upload a 3-row file, see Import complete and 2 contacts", async ({
  page,
}) => {
  const stamp = Date.now();
  const first = `e2e-${stamp}-one@example.test`;
  const second = `e2e-${stamp}-two@example.test`;
  const csv = [
    "email,first_name",
    `${first},One`,
    `${second},Two`,
    "not-an-email,Broken",
    "",
  ].join("\n");

  // Log in.
  await page.goto("/login");
  await page.getByPlaceholder("you@example.com").fill(EMAIL);
  await page.getByPlaceholder("••••••••").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/dashboard");

  // Open Contacts and start an import.
  await page.goto("/audience/contacts");
  await page.getByRole("button", { name: "Import", exact: true }).click();
  await page.locator("#file-input").setInputFiles({
    name: "contacts.csv",
    mimeType: "text/csv",
    buffer: Buffer.from(csv),
  });

  // Columns are auto-mapped from the header; review, then start.
  await page.getByRole("button", { name: "Review import" }).click();
  await page.getByRole("button", { name: "Start Import" }).click();

  // The worker processes the job; the completion screen must appear.
  await expect(
    page.getByRole("heading", { name: "Import Complete" }),
  ).toBeVisible({ timeout: 90_000 });
  await expect(
    page.getByText("Imported", { exact: true }).locator("xpath=following-sibling::p[1]"),
  ).toHaveText("2");

  await page.getByTestId("import-close").click();

  // Both contacts are listed in the table.
  await expect(page.getByText(first)).toBeVisible();
  await expect(page.getByText(second)).toBeVisible();
});
