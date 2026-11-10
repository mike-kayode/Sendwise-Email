import { expect, test, type APIRequestContext } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { resolve } from "node:path";

const API_ORIGIN = process.env.E2E_API_ORIGIN ?? "http://localhost:8084";
const MAILPIT_API = process.env.MAILPIT_API_URL ?? "http://localhost:8025";

function runLocalSQL(sql: string): void {
  const composeFiles = [
    resolve(process.cwd(), "../../docker-compose.yml"),
    resolve(process.cwd(), "../../docker-compose.microservices.yml"),
  ];
  execFileSync("docker", [
    "compose",
    "--project-name",
    process.env.E2E_COMPOSE_PROJECT ?? "sendwise-e2e-stalwart",
    ...composeFiles.flatMap((file) => ["-f", file]),
    "exec",
    "-T",
    "postgres",
    "sh",
    "-lc",
    `psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -c "${sql}"`,
  ], { stdio: "pipe" });
}

function seedVerifiedDomain(organizationID: string, domain: string): void {
  if (!/^[0-9a-f-]{36}$/i.test(organizationID) || !/^[a-z0-9.-]+$/.test(domain)) {
    throw new Error("The test organization or domain is invalid");
  }
  runLocalSQL(`
    UPDATE sending_domains
    SET status = 'verified',
        spf_status = 'verified',
        dkim_status = 'verified',
        verification_status = 'verified',
        stalwart_authenticated = true,
        last_checked_at = now(),
        verified_at = now()
    WHERE organization_id = '${organizationID}' AND domain = '${domain}'
  `);
}

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
  test.setTimeout(180_000);
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
  await page.getByPlaceholder("Workspace name").fill(`Task Five Workspace ${stamp}`);
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
  const claimsPayload = accessToken!.split(".")[1].replaceAll("-", "+").replaceAll("_", "/");
  const claims = JSON.parse(
    atob(claimsPayload.padEnd(Math.ceil(claimsPayload.length / 4) * 4, "=")),
  ) as { org?: string };
  expect(claims.org).toBeTruthy();
  const sendingDomain = `task5-${stamp}.example.test`;
  const domainResponse = await request.post(`${API_ORIGIN}/api/v1/domains`, {
    headers: { Authorization: ["Bearer", accessToken].join(" ") },
    data: { domain: sendingDomain },
  });
  expect(domainResponse.status(), await domainResponse.text()).toBe(201);
  seedVerifiedDomain(claims.org!, sendingDomain);

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

  await page.goto("/settings");
  await expect(page.getByRole("heading", { name: "Sender identities" })).toBeVisible();
  const addSender = async (localPart: string, displayName: string) => {
    await page.getByLabel("Verified sending domain").selectOption(sendingDomain);
    await page.getByLabel("Local part").fill(localPart);
    await page.getByLabel("Display name").fill(displayName);
    await page.getByLabel("Reply-to").fill(`support@${sendingDomain}`);
    await page.getByRole("button", { name: "Add sender" }).click();
    await expect(page.getByRole("row").filter({ hasText: `${localPart}@${sendingDomain}` })).toBeVisible();
  };
  await addSender("newsletter", "Newsletter");
  await addSender("sales", "Sales");
  const salesRow = page.getByRole("row").filter({ hasText: `sales@${sendingDomain}` });
  await salesRow.getByRole("button", { name: "Set default" }).click();
  await expect(salesRow.getByText("Default", { exact: true })).toBeVisible();

  const identitiesResponse = await request.get(`${API_ORIGIN}/api/v1/sender-identities`, {
    headers: { Authorization: ["Bearer", accessToken].join(" ") },
  });
  expect(identitiesResponse.ok()).toBeTruthy();
  const identities = (await identitiesResponse.json()) as {
    data: Array<{ id: string; email: string; is_default: boolean }>;
  };
  const salesIdentity = identities.data.find((item) => item.email === `sales@${sendingDomain}`);
  expect(salesIdentity?.id).toBeTruthy();
  expect(salesIdentity?.is_default).toBe(true);

  const listsResponse = await request.get(`${API_ORIGIN}/api/v1/lists`, {
    headers: { Authorization: ["Bearer", accessToken].join(" ") },
  });
  const lists = (await listsResponse.json()) as {
    data: Array<{ id: string; name: string }>;
  };
  const audience = lists.data.find((item) => item.name === listName);
  expect(audience?.id).toBeTruthy();
  const blockedCampaignResponse = await request.post(`${API_ORIGIN}/api/v1/campaigns`, {
    headers: { Authorization: ["Bearer", accessToken].join(" ") },
    data: {
      name: `${campaignName} without sender profile`,
      subject: "Blocked profile proof",
      sender_identity_id: salesIdentity!.id,
      list_id: audience!.id,
      content: { version: 1, blocks: [{ type: "paragraph", content: "Profile enforcement proof." }] },
    },
  });
  expect(blockedCampaignResponse.status(), await blockedCampaignResponse.text()).toBe(201);
  const blockedCampaign = (await blockedCampaignResponse.json()) as { id: string };
  const blockedSendResponse = await request.post(
    `${API_ORIGIN}/api/v1/campaigns/${blockedCampaign.id}/send`,
    { headers: { Authorization: ["Bearer", accessToken].join(" ") } },
  );
  expect(blockedSendResponse.status()).toBe(400);
  expect(await blockedSendResponse.text()).toContain("sender_profile_incomplete");

  await page.goto(`/campaigns/${blockedCampaign.id}/editor`);
  await expect(page.getByRole("heading", { name: "Complete your sender profile" })).toBeVisible();
  await page.getByRole("link", { name: "Set up sender profile" }).click();
  await expect(page).toHaveURL(/\/settings#sender-profile$/);
  await page.getByLabel("Company name").fill("Task Five E2E");
  await page.getByLabel("Support email").fill(`support@${sendingDomain}`);
  await page.getByLabel("Street address").fill("1 Test Road");
  await page.getByLabel("City").fill("Test City");
  await page.getByLabel("Region / state").fill("Test Region");
  await page.getByLabel("Postal code").fill("10001");
  await page.getByLabel("Country").fill("Test Country");
  await page.getByLabel("Website").fill("https://example.test");
  await page.getByLabel("Optional footer text").fill("A useful update from our team.");
  const senderProfileSave = page.waitForResponse((response) =>
    new URL(response.url()).pathname.endsWith("/sender-profile") &&
    response.request().method() === "PUT",
  );
  await page.getByRole("button", { name: "Save sender profile" }).click();
  const savedProfileResponse = await senderProfileSave;
  expect(savedProfileResponse.status(), await savedProfileResponse.text()).toBe(200);
  await expect(page.getByText("Profile complete. Campaign sends are enabled.")).toBeVisible();
  const foreignFromCampaignResponse = await request.post(`${API_ORIGIN}/api/v1/campaigns`, {
    headers: { Authorization: ["Bearer", accessToken].join(" ") },
    data: {
      name: `${campaignName} foreign From`,
      subject: "Foreign sender rejection proof",
      sender_name: "Unowned sender",
      sender_email: "newsletter@foreign-workspace.example.test",
      list_id: audience!.id,
      content: { version: 1, blocks: [{ type: "paragraph", content: "This must not send." }] },
    },
  });
  expect(foreignFromCampaignResponse.status(), await foreignFromCampaignResponse.text()).toBe(201);
  const foreignFromCampaign = await foreignFromCampaignResponse.json() as { id: string };
  const foreignFromSend = await request.post(
    `${API_ORIGIN}/api/v1/campaigns/${foreignFromCampaign.id}/send`,
    { headers: { Authorization: ["Bearer", accessToken].join(" ") } },
  );
  expect(foreignFromSend.status()).toBe(400);
  expect(await foreignFromSend.text()).toContain("sender_identity_required");

  await page.goto("/templates");
  await page.getByRole("button", { name: "New template" }).click();
  await page.getByLabel("Template name").fill(templateName);
  await page.getByLabel("Category").selectOption("newsletter");
  await page.getByRole("button", { name: "Create template" }).click();
  await page.waitForURL(/\/templates\/[0-9a-f-]+$/);
  await expect(page.getByRole("heading", { name: "Design template" })).toBeVisible();
  const templateMode = page.getByLabel("Template mode");
  page.once("dialog", (dialog) => dialog.accept());
  await templateMode.selectOption("plain");
  await expect(page.getByLabel("Plain-text message")).toBeVisible();
  await page.getByLabel("Plain-text message").fill("A useful update in plain text.");
  await templateMode.selectOption("html");
  await expect(page.getByTestId("grapes-email-editor")).toBeVisible();
  await expect(page.getByTestId("email-html-import-input")).toBeEnabled();
  await page.getByTestId("email-html-import-input").setInputFiles({
    name: "beefree-export.html",
    mimeType: "text/html",
    buffer: Buffer.from(
      '<table role="presentation" width="100%"><tr><td><h1>Welcome {{organization}}</h1><p>This message started from an imported email design.</p><a href="https://example.test">Read more</a></td></tr></table>',
    ),
  });
  await expect(page.frameLocator("iframe.gjs-frame").locator("h1").filter({ hasText: "Welcome {{organization}}" })).toBeVisible();
  await page.getByRole("button", { name: "Save template" }).click();
  await expect(page.getByText("Your design is ready to use in a campaign.")).toBeVisible();
  const templateID = page.url().split("/").at(-1);
  expect(templateID).toBeTruthy();
  const savedTemplateResponse = await request.get(`${API_ORIGIN}/api/v1/templates/${templateID}`, {
    headers: { Authorization: `Bearer ${accessToken}` },
  });
  expect(savedTemplateResponse.status(), await savedTemplateResponse.text()).toBe(200);
  const savedTemplate = await savedTemplateResponse.json() as { design: unknown };
  const savedDesign = typeof savedTemplate.design === "string"
    ? JSON.parse(savedTemplate.design) as Record<string, unknown>
    : savedTemplate.design as Record<string, unknown>;
  expect(savedDesign.editor).toBe("grapesjs");
  expect(savedDesign.editor_project).toMatchObject({ pages: expect.any(Array) });
  expect(savedDesign.html).toContain("Welcome {{organization}}");

  const testRecipient = `task5-test-${stamp}@example.test`;
  await page.getByLabel("Test recipient").fill(testRecipient);
  await page.getByRole("button", { name: "Send test email" }).click();
  await expect(page.getByText("accepted by SMTP relay", { exact: true })).toBeVisible();
  await expect.poll(async () => {
    const response = await request.get(`${MAILPIT_API}/api/v1/search`, {
      params: { query: `to:${testRecipient}` },
    });
    if (!response.ok()) return 0;
    const result = await response.json() as { total?: number; messages?: unknown[] };
    return result.total ?? result.messages?.length ?? 0;
  }, { timeout: 30_000 }).toBeGreaterThan(0);
  await page.goto(`/campaigns/new?template_id=${encodeURIComponent(templateID!)}`);
  await expect(page.getByText(`Using template: ${templateName}`)).toBeVisible();
  await page.locator("#subject").fill("Task 5 delivery proof");
  await page.locator("#preheader").fill("A locally delivered campaign report proof.");
  await page.locator("#sender-identity").selectOption(salesIdentity!.id);
  await page.locator("label").filter({ hasText: listName }).locator('input[type="radio"]').check();
  await page.getByRole("button", { name: "Continue to editor" }).click();
  await page.waitForURL(/\/campaigns\/[0-9a-f-]+\/editor$/);
  const campaignID = page.url().match(/\/campaigns\/([0-9a-f-]+)\/editor$/)?.[1];
  expect(campaignID).toBeTruthy();
  await expect(page.getByTestId("grapes-email-editor")).toBeVisible();
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByText("All changes saved.")).toBeVisible();
  await page.getByRole("link", { name: "Preview & send" }).click();
  await expect(page.frameLocator('iframe[title="Email HTML preview"]').locator("h1").filter({ hasText: "Welcome {{organization}}" })).toBeVisible();
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
  await expect(
    page.getByRole("row").filter({ hasText: campaignName }).filter({ hasText: "completed" }),
  ).toContainText("3");
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
