import { expect, test } from "@playwright/test";

const EMAIL = process.env.DEPLOY_SMOKE_EMAIL ?? "smoke@sendwise.test";
const PASSWORD = process.env.DEPLOY_SMOKE_PASSWORD ?? "SmokeTest2026!";
const API_BASE = process.env.E2E_API_ORIGIN ?? new URL(process.env.E2E_BASE_URL ?? "http://localhost:3000").origin.replace(/:3000$/, "");

test("signup form: publish, accept a logged-out submission, and record its audience", async ({ page }) => {
  const stamp = Date.now();
  const listName = `Form audience ${stamp}`;
  const tagName = `Form tag ${stamp}`;
  const formName = `Hosted form ${stamp}`;
  const contactEmail = `form-${stamp}@example.test`;

  await page.goto("/login");
  await page.getByPlaceholder("you@example.com").fill(EMAIL);
  await page.getByPlaceholder("••••••••").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/dashboard");

  const audienceCreated = await page.evaluate(async ({ listName, tagName, apiBase }) => {
    const token = localStorage.getItem("access_token");
    const headers = { "Content-Type": "application/json", Authorization: `Bearer ${token}` };
    const listResponse = await fetch(`${apiBase}/api/v1/lists`, {
      method: "POST",
      headers,
      body: JSON.stringify({ name: listName, description: "Created by the form E2E test" }),
    });
    const tagResponse = await fetch(`${apiBase}/api/v1/tags`, {
      method: "POST",
      headers,
      body: JSON.stringify({ name: tagName, color: "#2563eb" }),
    });
    return {
      listStatus: listResponse.status,
      listBody: await listResponse.text(),
      tagStatus: tagResponse.status,
      tagBody: await tagResponse.text(),
    };
  }, { listName, tagName, apiBase: API_BASE });
  expect(audienceCreated.listStatus, audienceCreated.listBody).toBe(201);
  expect(audienceCreated.tagStatus, audienceCreated.tagBody).toBe(201);

  await page.goto("/forms");
  await page.getByRole("button", { name: "New form" }).click();
  await page.waitForURL(/\/forms\/[0-9a-f-]+$/);
  const formID = page.url().split("/").at(-1)!;
  await page.getByLabel("Name", { exact: true }).fill(formName);
  await page.getByLabel("Add confirmed contacts to").selectOption({ label: listName });
  await page.getByLabel(tagName).check();
  await Promise.all([
    page.waitForResponse((response) => response.url().includes(`/api/v1/forms/${formID}`) && response.request().method() === "PATCH" && response.status() === 200),
    page.getByRole("button", { name: "Save" }).click(),
  ]);
  await expect(page.getByRole("button", { name: "Save" })).toBeVisible();
  await Promise.all([
    page.waitForResponse((response) => response.url().includes(`/api/v1/forms/${formID}/publish`) && response.request().method() === "POST" && response.status() === 200),
    page.getByRole("button", { name: "Publish" }).click(),
  ]);
  await expect(page.getByRole("button", { name: "Unpublish" })).toBeVisible();

  const hostedHref = await page.getByRole("link", { name: "Open hosted form" }).getAttribute("href");
  expect(hostedHref).toBeTruthy();
  await page.evaluate(() => localStorage.removeItem("access_token"));
  await page.goto(hostedHref!);
  await page.getByRole("textbox", { name: "Email", exact: true }).fill(contactEmail);
  await page.getByLabel("First name").fill("Form");
  await page.getByLabel("I agree to receive email updates.").check();
  await page.getByRole("button", { name: "Subscribe" }).click();
  await expect(page.getByRole("status")).toHaveText("Thank you for subscribing!");

  await page.goto("/login");
  await page.getByPlaceholder("you@example.com").fill(EMAIL);
  await page.getByPlaceholder("••••••••").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/dashboard");

  const audienceCounts = await page.evaluate(async ({ listName, tagName, apiBase }) => {
    const token = localStorage.getItem("access_token");
    const headers = { Authorization: `Bearer ${token}` };
    const [listsResponse, tagsResponse] = await Promise.all([
      fetch(`${apiBase}/api/v1/lists`, { headers }),
      fetch(`${apiBase}/api/v1/tags`, { headers }),
    ]);
    const [lists, tags] = await Promise.all([listsResponse.json(), tagsResponse.json()]);
    return {
      list: lists.data.find((item: { name: string }) => item.name === listName)?.contact_count,
      tag: tags.data.find((item: { name: string }) => item.name === tagName)?.contact_count,
    };
  }, { listName, tagName, apiBase: API_BASE });
  expect(audienceCounts).toEqual({ list: 1, tag: 1 });

  await page.goto(`/forms/${formID}/submissions`);
  await expect(page.getByText(contactEmail)).toBeVisible();
});
