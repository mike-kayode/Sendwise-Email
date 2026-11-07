import { expect, test, type APIRequestContext } from "@playwright/test";

const API_ORIGIN = process.env.E2E_API_ORIGIN ?? "http://localhost:8084";
const MAILPIT_API = process.env.MAILPIT_API_URL ?? "http://localhost:8025";

async function findVerificationLink(
  request: APIRequestContext,
  email: string,
): Promise<string> {
  await expect.poll(async () => {
    const response = await request.get(`${MAILPIT_API}/api/v1/search`, {
      params: { query: `to:${email}` },
    });
    if (!response.ok()) return "";
    const body = await response.json() as {
      messages?: Array<{ ID?: string; id?: string; Subject?: string; subject?: string }>;
    };
    const message = (body.messages ?? []).find((item) =>
      (item.Subject ?? item.subject ?? "").toLowerCase().includes("verify your email"),
    );
    const id = message?.ID ?? message?.id;
    if (!id) return "";
    const detail = await request.get(`${MAILPIT_API}/api/v1/message/${encodeURIComponent(id)}`);
    if (!detail.ok()) return "";
    const content = JSON.stringify(await detail.json());
    return content.match(/https?:\/\/[^\s"'<>\\]+/g)?.find((url) => url.includes("/auth/verify?")) ?? "";
  }, { timeout: 45_000 }).not.toBe("");

  const search = await request.get(`${MAILPIT_API}/api/v1/search`, {
    params: { query: `to:${email}` },
  });
  const result = await search.json() as {
    messages?: Array<{ ID?: string; id?: string; Subject?: string; subject?: string }>;
  };
  const message = (result.messages ?? []).find((item) =>
    (item.Subject ?? item.subject ?? "").toLowerCase().includes("verify your email"),
  );
  const id = message?.ID ?? message?.id;
  if (!id) throw new Error("Mailpit verification message disappeared after it was found");
  const detail = await request.get(`${MAILPIT_API}/api/v1/message/${encodeURIComponent(id)}`);
  const content = JSON.stringify(await detail.json());
  const link = content.match(/https?:\/\/[^\s"'<>\\]+/g)?.find((url) => url.includes("/auth/verify?"));
  if (!link) throw new Error("Mailpit verification message had no verification URL");
  return link.replaceAll("\\u0026", "&").replaceAll("&amp;", "&");
}

test("register, import an audience, edit a template, send, and verify campaign reports", async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const email = `task5-${stamp}@example.test`;
  const password = `Task5-${stamp}-Password!`;
  const listName = `Task 5 audience ${stamp}`;
  const templateName = `Task 5 template ${stamp}`;
  const campaignName = `${templateName} campaign`;
  const addresses = [1, 2, 3].map((index) => `task5-${stamp}-${index}@example.test`);

  await page.goto("/register");
  await page.getByPlaceholder("Your name").fill("Task Five E2E");
  await page.getByPlaceholder("you@example.com").fill(email);
  await page.getByPlaceholder("Acme Studio").fill(`Task Five Workspace ${stamp}`);
  await page.getByPlaceholder("••••••••••").fill(password);
  await page.getByRole("button", { name: "Create workspace" }).click();
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
  const accessToken = await page.evaluate(() => localStorage.getItem("access_token"));
  expect(accessToken).toBeTruthy();

  await page.goto("/audience/lists");
  await page.getByRole("button", { name: "Create list", exact: true }).click();
  await page.getByLabel("List name").fill(listName);
  await page.getByLabel("Description (optional)").fill("Three-address delivery proof");
  await page.getByRole("button", { name: "Create list", exact: true }).last().click();
  await expect(page.getByRole("row").filter({ hasText: listName })).toBeVisible();

  await page.goto("/audience/contacts");
  await page.getByRole("button", { name: "Import", exact: true }).click();
  const csv = [
    "email,first_name",
    `${addresses[0]},One`,
    `${addresses[1]},Two`,
    `${addresses[2]},Three`,
    "",
  ].join("\n");
  await page.locator("#file-input").setInputFiles({
    name: "task5-audience.csv",
    mimeType: "text/csv",
    buffer: Buffer.from(csv),
  });
  await page.getByLabel("Add imported contacts to a list (optional)").selectOption({ label: listName });
  await page.getByRole("button", { name: "Review import" }).click();
  await page.getByRole("button", { name: "Start Import" }).click();
  await expect(page.getByRole("heading", { name: "Import Complete" })).toBeVisible({ timeout: 90_000 });
  await expect(page.getByText("Imported", { exact: true }).locator("xpath=following-sibling::p[1]")).toHaveText("3");
  await page.getByTestId("import-close").click();

  const contactResponse = await request.get(`${API_ORIGIN}/api/v1/contacts?limit=100`, {
    headers: { Authorization: `Bearer ${accessToken}` },
  });
  expect(contactResponse.ok()).toBeTruthy();
  const contactList = await contactResponse.json() as {
    data: Array<{ id: string; email: string; status: string }>;
  };
  const importedContacts = addresses.map((address) => {
    const contact = contactList.data.find((item) => item.email === address);
    if (!contact) throw new Error(`Imported contact ${address} was not returned by the contacts API`);
    return contact;
  });
  for (const contact of importedContacts) {
    const response = await request.patch(`${API_ORIGIN}/api/v1/contacts/${contact.id}`, {
      headers: { Authorization: `Bearer ${accessToken}` },
      data: {
        status: "subscribed",
        consent: {
          source: "task5-e2e",
          purpose: "marketing",
          evidence: { test: "synthetic address consent", confirmed: true },
        },
      },
    });
    expect(response.status(), await response.text()).toBe(200);
  }

  await page.goto("/templates");
  await page.getByRole("button", { name: "New template" }).click();
  await page.getByLabel("Template name").fill(templateName);
  await page.getByLabel("Category").selectOption("newsletter");
  await page.getByRole("button", { name: "Create template" }).click();
  await page.waitForURL(/\/templates\/[0-9a-f-]+$/);
  await expect(page.getByRole("heading", { name: "Design template" })).toBeVisible();
  await page.getByRole("button", { name: "Add heading" }).click();
  await page.getByLabel("Block content 1").fill("A useful update");
  await page.getByRole("button", { name: "Add paragraph" }).click();
  await page.getByLabel("Block content 2").fill("This message started from a saved template.");
  await page.getByRole("button", { name: "Save template" }).click();
  await expect(page.getByText("Your design is ready to use in a campaign.")).toBeVisible();
  const templateID = page.url().split("/").at(-1);
  expect(templateID).toBeTruthy();

  await page.goto(`/campaigns/new?template_id=${encodeURIComponent(templateID!)}`);
  await expect(page.getByText(`Using template: ${templateName}`)).toBeVisible();
  await page.locator("#subject").fill("Task 5 delivery proof");
  await page.locator("#preheader").fill("A locally delivered campaign report proof.");
  await page.locator("#sender-name").fill("Sendwise Task 5");
  const senderField = page.locator("#sender-email");
  if (await senderField.evaluate((element) => element.tagName) === "SELECT") {
    await senderField.selectOption({ index: 0 });
  } else {
    await senderField.fill("sender@task5.example.test");
  }
  await page.locator("#sender-postal-address").fill("1 Test Road, Test City");
  await page.locator("label").filter({ hasText: listName }).locator('input[type="radio"]').check();
  await page.getByRole("button", { name: "Continue to editor" }).click();
  await page.waitForURL(/\/campaigns\/[0-9a-f-]+\/editor$/);
  const campaignID = page.url().match(/\/campaigns\/([0-9a-f-]+)\/editor$/)?.[1];
  expect(campaignID).toBeTruthy();
  await expect(page.getByLabel("Block content 1")).toHaveValue("A useful update");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByText("All changes saved.")).toBeVisible();
  await page.getByRole("link", { name: "Preview & send" }).click();
  await expect(page.getByText("All required checks passed.")).toBeVisible();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Send now" }).click();
  await expect(page.getByText("3 will go out today; 0 are queued for later warm-up days.")).toBeVisible();

  const reportURL = `${API_ORIGIN}/api/v1/analytics/campaigns/${campaignID}`;
  await expect.poll(async () => {
    const response = await request.get(reportURL, {
      headers: { Authorization: `Bearer ${accessToken}` },
    });
    if (!response.ok()) return 0;
    const report = await response.json() as {
      recipients?: { by_status?: Record<string, number> };
    };
    return report.recipients?.by_status?.submitted ?? 0;
  }, { timeout: 90_000 }).toBe(3);

  await page.goto("/reports");
  await expect(page.getByRole("row").filter({ hasText: campaignName })).toContainText("3");
  await expect(page.getByText("Emails sent (30 days)")).toBeVisible();
  await page.goto(`/campaigns/${campaignID}/report`);
  await expect(page.getByRole("heading", { name: "Campaign report" })).toBeVisible();
  await expect(page.getByText("Submitted", { exact: true }).first()).toBeVisible();
  await expect(page.getByText("3", { exact: true }).first()).toBeVisible();

  for (const address of addresses) {
    await expect.poll(async () => {
      const response = await request.get(`${MAILPIT_API}/api/v1/search`, {
        params: { query: `to:${address}` },
      });
      if (!response.ok()) return 0;
      const result = await response.json() as { total?: number; messages?: unknown[] };
      return result.total ?? result.messages?.length ?? 0;
    }, { timeout: 30_000 }).toBeGreaterThan(0);
  }
});
