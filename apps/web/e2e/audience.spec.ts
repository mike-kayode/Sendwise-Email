import { expect, test } from "@playwright/test";

const EMAIL = process.env.DEPLOY_SMOKE_EMAIL ?? "smoke@sendwise.test";
const PASSWORD = process.env.DEPLOY_SMOKE_PASSWORD ?? "SmokeTest2026!";

test("P3 list import, tag assignment, and segment preview", async ({ page }) => {
  const stamp = Date.now();
  let listName = `P3 List ${stamp}`;
  const tagName = `P3 Tag ${stamp}`;
  const segmentName = `P3 Segment ${stamp}`;
  const contactEmail = `p3-${stamp}@example.test`;

  await page.goto("/login");
  await page.getByPlaceholder("you@example.com").fill(EMAIL);
  await page.getByPlaceholder("••••••••").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/dashboard");

  await page.goto("/audience/lists");
  await page.getByRole("button", { name: "Create list", exact: true }).click();
  await page.getByLabel("List name").fill(listName);
  await page.getByLabel("Description (optional)").fill("P3 audience proof");
  await page.getByRole("button", { name: "Create list", exact: true }).last().click();
  await expect(page.getByRole("row").filter({ hasText: listName })).toBeVisible();

  await page.goto("/audience/tags");
  await page.locator("#new-tag-name").fill(tagName);
  await page.getByRole("button", { name: "Create tag" }).click();
  await expect(page.getByText(tagName, { exact: true })).toBeVisible();

  await page.goto("/audience/contacts");
  await page.getByRole("button", { name: "Import", exact: true }).click();
  const csv = [
    "email,first_name",
    `${contactEmail},P3`,
    `p3-${stamp}-two@example.test,Second`,
    `p3-${stamp}-three@example.test,Third`,
    "",
  ].join("\n");
  await page.locator("#file-input").setInputFiles({
    name: "p3-contacts.csv",
    mimeType: "text/csv",
    buffer: Buffer.from(csv),
  });
  await page.getByLabel("Add imported contacts to a list (optional)").selectOption({ label: listName });
  await page.getByRole("button", { name: "Review import" }).click();
  await page.getByRole("button", { name: "Start Import" }).click();
  await expect(page.getByRole("heading", { name: "Import Complete" })).toBeVisible({ timeout: 90_000 });
  await expect(page.getByText("Imported", { exact: true }).locator("xpath=following-sibling::p[1]")).toHaveText("3");
  await page.getByTestId("import-close").click();

  await page.goto("/audience/lists");
  await page.getByRole("row").filter({ hasText: listName }).getByRole("link", { name: "Manage" }).click();
  await expect(page.getByRole("heading", { name: listName })).toBeVisible();
  await expect(page.getByText(contactEmail, { exact: true })).toBeVisible();
  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("button", { name: "Export CSV" }).click();
  expect((await downloadPromise).suggestedFilename()).toBe("list-contacts.csv");
  const updatedListName = `${listName} Updated`;
  await page.getByLabel("List name").fill(updatedListName);
  await page.getByRole("button", { name: "Save details" }).click();
  await expect(page.getByRole("heading", { name: updatedListName })).toBeVisible();
  listName = updatedListName;

  await page.goto("/audience/contacts");
  const contactRow = page.getByRole("row").filter({ hasText: contactEmail });
  await expect(contactRow).toBeVisible();
  await contactRow.getByRole("button", { name: "View" }).click();
  await page.getByLabel("Tag to assign to contact").selectOption({ label: tagName });
  await page.getByRole("button", { name: "Assign", exact: true }).click();
  await expect(page.getByRole("button", { name: `Remove ${tagName} tag` })).toBeVisible();
  await page.keyboard.press("Escape");

  await page.goto("/audience/segments");
  await page.locator("#segment-name").fill(segmentName);
  await page.getByLabel("Condition value").selectOption({ label: tagName });
  await page.getByRole("button", { name: "Add condition" }).click();
  await page.getByLabel("Condition field").selectOption("list");
  await page.getByLabel("Condition value").selectOption({ label: listName });
  await page.getByRole("button", { name: "Add condition" }).click();
  await page.getByRole("button", { name: "Preview" }).click();
  await expect(page.getByText("Estimated 1 contacts")).toBeVisible();
  await expect(page.getByText(contactEmail, { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Save segment" }).click();
  const segmentRow = page.getByRole("row").filter({ hasText: segmentName });
  await expect(segmentRow).toBeVisible();
  await expect(segmentRow).toContainText("1");

  await page.goto("/audience/lists");
  const listRow = page.getByRole("row").filter({ hasText: listName });
  page.once("dialog", (dialog) => dialog.accept());
  await listRow.getByRole("button", { name: "Delete" }).click();
  await expect(listRow).toHaveCount(0);
});
