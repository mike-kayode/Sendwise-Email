import { expect, test } from "@playwright/test";

const EMAIL = process.env.DEPLOY_SMOKE_EMAIL ?? "smoke@sendwise.test";
const PASSWORD = process.env.DEPLOY_SMOKE_PASSWORD ?? "SmokeTest2026!";

test("sending domains: generated records, verification, and no browser errors", async ({ page }) => {
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(`${error.message}\n${error.stack ?? ""}`));
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });

  await page.goto("/login");
  await page.getByPlaceholder("you@example.com").fill(EMAIL);
  await page.getByPlaceholder("••••••••").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/dashboard");
  await page.goto("/domains");
  await expect(page.getByRole("heading", { name: "Sending domains" })).toBeVisible();

  const domainName = `domain-e2e-${Date.now()}.example.com`;
  const createResponse = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/v1/domains") &&
      response.request().method() === "POST" &&
      response.status() === 201,
  );
  await page.getByPlaceholder("mail.yourdomain.com").fill(domainName);
  await page.getByRole("button", { name: "Add domain" }).click();
  const created = await (await createResponse).json();
  await expect(page.getByText(domainName, { exact: true }).first()).toBeVisible();

  const domainCard = page.locator("div.cursor-pointer").filter({ hasText: domainName }).first();
  await domainCard.click();
  const recordsTable = page.getByRole("table");
  await expect(
    recordsTable.getByText(`_sendwise-verification.${domainName}`, { exact: true }),
  ).toBeVisible();
  await expect(recordsTable.locator("tbody tr")).toHaveCount(4);
  await expect(
    recordsTable.getByText(`v=DKIM1; k=rsa; p=${created.dkim_public_key}`, { exact: true }),
  ).toBeVisible();

  const dnsRecords = [
    {
      type: "TXT",
      name: domainName,
      value: "v=spf1 mx ~all",
      purpose: "SPF",
      status: "verified",
    },
    {
      type: "TXT",
      name: `${created.dkim_selector}._domainkey.${domainName}`,
      value: `v=DKIM1; k=rsa; p=${created.dkim_public_key}`,
      purpose: "DKIM",
      status: "verified",
    },
    {
      type: "TXT",
      name: `_dmarc.${domainName}`,
      value: "v=DMARC1; p=none",
      purpose: "DMARC",
      status: "verified",
    },
    {
      type: "TXT",
      name: `_sendwise-verification.${domainName}`,
      value: `v=sendwise-verification ${domainName}`,
      purpose: "Domain ownership",
      status: "verified",
    },
  ];
  const verifiedDomain = {
    ...created,
    status: "verified",
    spf_status: "verified",
    dkim_status: "verified",
    dmarc_status: "verified",
    verification_status: "verified",
    last_checked_at: new Date().toISOString(),
  };

  await page.route(`**/api/v1/domains/${created.id}/verify`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ domain: verifiedDomain, dns_records: dnsRecords }),
    });
  });
  await page.route(`**/api/v1/domains/${created.id}/dns-records`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ domain: verifiedDomain, dns_records: dnsRecords }),
    });
  });
  await page.route("**/api/v1/domains", async (route) => {
    if (route.request().method() !== "GET") {
      await route.continue();
      return;
    }
    const response = await route.fetch();
    const body = await response.json();
    const domain = body.data?.find((item: { id: string }) => item.id === created.id);
    if (domain) Object.assign(domain, verifiedDomain);
    await route.fulfill({ response, body: JSON.stringify(body) });
  });

  await domainCard.getByRole("button", { name: "Verify now" }).click();
  await expect(page.getByText("This domain is ready to send.")).toBeVisible();
  await expect(domainCard.getByText("Ready to send")).toBeVisible();
  await expect(page.getByText(`_sendwise-verification.${domainName}`, { exact: true })).toBeVisible();

  expect(pageErrors, pageErrors.join("\n")).toEqual([]);
  expect(consoleErrors, consoleErrors.join("\n")).toEqual([]);
});
