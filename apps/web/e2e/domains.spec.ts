import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { expect, test, type APIRequestContext } from "@playwright/test";

const MAILPIT_API = process.env.MAILPIT_API_URL ?? "http://localhost:8025";
const API_ORIGIN = process.env.E2E_API_ORIGIN ?? "http://localhost:8084";

async function findVerificationLink(
  request: APIRequestContext,
  email: string,
): Promise<string> {
  let foundLink = "";
  await expect.poll(async () => {
    const response = await request.get(`${MAILPIT_API}/api/v1/search`, {
      params: { query: `to:${email}` },
    });
    if (!response.ok()) return "";
    const result = (await response.json()) as {
      messages?: Array<{ ID?: string; id?: string; Subject?: string; subject?: string }>;
    };
    const message = (result.messages ?? []).find((item) =>
      (item.Subject ?? item.subject ?? "").toLowerCase().includes("verify your email"),
    );
    const id = message?.ID ?? message?.id;
    if (!id) return "";
    const detail = await request.get(`${MAILPIT_API}/api/v1/message/${encodeURIComponent(id)}`);
    if (!detail.ok()) return "";
    const content = JSON.stringify(await detail.json());
    const link = content.match(/https?:\/\/[^\s"'<>\\]+/g)?.find((item) =>
      item.includes("/auth/verify?"),
    );
    if (!link) return "";
    foundLink = link.replaceAll("\\u0026", "&").replaceAll("&amp;", "&");
    return foundLink;
  }, { timeout: 45_000 }).not.toBe("");
  return foundLink;
}

function runLocalSQL(sql: string): void {
  const composeFiles = [
    resolve(process.cwd(), "../../docker-compose.yml"),
    resolve(process.cwd(), "../../docker-compose.microservices.yml"),
  ];
  const args = [
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
  ];
  execFileSync("docker", args, { stdio: "pipe" });
}

function seedDomains(organizationID: string, stamp: number): void {
  if (!/^[0-9a-f-]{36}$/i.test(organizationID)) {
    throw new Error("The login token did not contain a valid organization ID");
  }
  const longDKIMPublicKey = "A".repeat(344);
  runLocalSQL(`
    INSERT INTO sending_domains (
      organization_id, domain, status, dkim_selector, dkim_public_key,
      spf_status, dkim_status, dmarc_status, verification_status,
      last_checked_at, verified_at
    ) VALUES
      ('${organizationID}', 'pending-${stamp}.example.test', 'pending', 'mail', '${longDKIMPublicKey}',
       'pending', 'missing', 'pending', 'pending', NULL, NULL),
      ('${organizationID}', 'verified-${stamp}.example.test', 'verified', 'mail', '',
       'verified', 'verified', 'verified', 'verified', now(), now())
  `);
}

function seedMisconfiguredDomain(organizationID: string, stamp: number): void {
  runLocalSQL(`
    INSERT INTO sending_domains (
      organization_id, domain, status, dkim_selector, dkim_public_key,
      spf_status, dkim_status, dmarc_status, verification_status,
      last_checked_at, verified_at
    ) VALUES (
      '${organizationID}', 'misconfigured-${stamp}.example.test',
      'misconfigured', 'mail', NULL, 'mismatched', 'missing', 'verified',
      'mismatched', NULL, NULL
    )
  `);
}

function seedConflictedDomain(organizationID: string, stamp: number): void {
  runLocalSQL(`
    INSERT INTO sending_domains (
      organization_id, domain, status, dkim_selector, dkim_public_key,
      spf_status, dkim_status, dmarc_status, verification_status,
      stalwart_authenticated, owner_conflict, last_checked_at, verified_at
    ) VALUES (
      '${organizationID}', 'conflicted-${stamp}.example.test',
      'verified', 'mail', '', 'verified', 'verified', 'verified', 'verified',
      true, true, now(), now()
    )
  `);
}

test("sending domains load from the real API without browser errors", async ({
  page,
  request,
}, testInfo) => {
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  const domainAPIResponses: number[] = [];
  page.on("pageerror", (error) => pageErrors.push(`${error.message}\n${error.stack ?? ""}`));
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  page.on("response", (response) => {
    if (new URL(response.url()).pathname.includes("/api/v1/domains")) {
      domainAPIResponses.push(response.status());
    }
  });

  const stamp = Date.now();
  const email = `domain-page-${stamp}@example.test`;
  const password = `Domain-${stamp}-Page-Test!`;

  await page.goto("/register");
  await page.getByPlaceholder("Your name").fill("Domain Page Test");
  await page.getByPlaceholder("you@example.com").fill(email);
  await page.getByPlaceholder("Workspace name").fill(`Domain Page ${stamp}`);
  await page.getByPlaceholder("••••••••••").fill(password);
  const registrationResponse = page.waitForResponse(
    (response) => new URL(response.url()).pathname.endsWith("/auth/register"),
  );
  await page.getByRole("button", { name: "Create workspace" }).click();
  const registered = await registrationResponse;
  expect(registered.status(), await registered.text()).toBe(201);
  await page.waitForURL(/\/auth\/verify\?email=/);

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
  await page.goto("/domains");
  await expect(page.getByRole("heading", { name: "Sending domains" })).toBeVisible();
  await expect(page.getByText("No sending domains yet.")).toBeVisible();
  expect(domainAPIResponses).toContain(200);
  await page.screenshot({ path: testInfo.outputPath("domains-empty.png"), fullPage: true });

  const accessToken = await page.evaluate(() => localStorage.getItem("access_token"));
  expect(accessToken).toBeTruthy();
  const payload = accessToken!.split(".")[1].replaceAll("-", "+").replaceAll("_", "/");
  const claims = JSON.parse(
    atob(payload.padEnd(Math.ceil(payload.length / 4) * 4, "=")),
  ) as { org?: string };
  expect(claims.org).toBeTruthy();
  seedDomains(claims.org!, stamp);

  await page.reload();
  await expect(page.getByText("1 of 2 verified", { exact: true })).toBeVisible();
  await expect(
    page.getByText(`pending-${stamp}.example.test`, { exact: true }).first(),
  ).toBeVisible();
  await expect(
    page.getByText(`verified-${stamp}.example.test`, { exact: true }).first(),
  ).toBeVisible();
  await expect(page.getByText("Verification needed", { exact: true })).toBeVisible();
  await expect(page.getByText("Ready to send", { exact: true })).toBeVisible();
  await page.screenshot({
    path: testInfo.outputPath("domains-one-unverified-one-verified.png"),
    fullPage: true,
  });

  await page.context().grantPermissions(["clipboard-read", "clipboard-write"], {
    origin: "http://localhost:3000",
  });
  const domainResponse = await request.get(`${API_ORIGIN}/api/v1/domains`, {
    headers: { Authorization: ["Bearer", accessToken].join(" ") },
  });
  const domainList = (await domainResponse.json()) as {
    data: Array<{ id: string; domain: string }>;
  };
  const pendingDomain = domainList.data.find((item) => item.domain === `pending-${stamp}.example.test`);
  expect(pendingDomain?.id).toBeTruthy();
  const dnsResponse = await request.get(
    `${API_ORIGIN}/api/v1/domains/${pendingDomain!.id}/dns-records`,
    { headers: { Authorization: ["Bearer", accessToken].join(" ") } },
  );
  const dnsRecords = (await dnsResponse.json()) as {
    dns_records: Array<{ name: string; value: string; type: string; ttl: number; purpose: string }>;
  };
  const copyRecord = dnsRecords.dns_records.find((record) =>
    record.name.includes("._domainkey") && record.value.length > 300,
  );
  expect(copyRecord).toBeTruthy();
  await page.getByRole("button", { name: "Show full names" }).click();
  const copyRow = page
    .getByRole("row")
    .filter({ hasText: copyRecord!.name })
    .filter({ hasText: copyRecord!.value });
  const copyName = copyRow.getByRole("button", { name: `Copy DNS name ${copyRecord!.name}` });
  const copyValue = copyRow.getByRole("button", { name: `Copy DNS value ${copyRecord!.name}` });
  await copyName.click();
  await expect(page.getByText("Copied", { exact: true })).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(copyRecord!.name);
  await copyValue.click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(copyRecord!.value);
  await page.evaluate(() => {
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined });
    const windowWithCapture = window as Window & { copiedFallbackText?: string };
    document.execCommand = (command) => {
      if (command !== "copy" || !(document.activeElement instanceof HTMLTextAreaElement)) {
        return false;
      }
      const textarea = document.activeElement;
      windowWithCapture.copiedFallbackText = textarea.value;
      return textarea.selectionStart === 0 && textarea.selectionEnd === textarea.value.length;
    };
  });
  await copyValue.click();
  await expect(page.getByText("Copied", { exact: true }).first()).toBeVisible();
  expect(
    await page.evaluate(
      () => (window as Window & { copiedFallbackText?: string }).copiedFallbackText,
    ),
  ).toBe(copyRecord!.value);
  await expect(page.getByText(copyRecord!.name, { exact: true }).first()).toBeVisible();
  await page.getByRole("button", { name: "Show relative names" }).click();
  await expect(page.getByText("@", { exact: true }).first()).toBeVisible();

  const fakeDNSRecords = dnsRecords.dns_records.map((record, index) => ({
    ...record,
    status: index === 1 ? "wrong_value" : index === 2 ? "missing" : "found",
    found_value: index === 2 ? "" : index === 1 ? "v=spf1 -all" : record.value,
    last_checked: new Date().toISOString(),
    resolvers: [{
      resolver: "fake DNS resolver",
      status: index === 1 ? "wrong_value" : index === 2 ? "missing" : "found",
      found_value: index === 2 ? "" : index === 1 ? "v=spf1 -all" : record.value,
    }],
  }));
  await page.route("**/api/v1/domains/*/dns-records", async (route) => {
    if (!route.request().url().includes(pendingDomain!.id)) {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ dns_records: fakeDNSRecords }),
    });
  });
  await page.route(`**/api/v1/domains/${pendingDomain!.id}/verify`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        domain: { id: pendingDomain!.id, domain: pendingDomain!.domain, status: "misconfigured" },
        dns_records: fakeDNSRecords,
      }),
    });
  });
  const verifyResponse = page.waitForResponse((response) =>
    response.url().includes(`/api/v1/domains/${pendingDomain!.id}/verify`) &&
    response.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Check now" }).first().click();
  expect((await verifyResponse).status()).toBe(200);
  await expect(page.getByText("DNS check complete", { exact: true })).toBeVisible();
  await page.getByText("Resolver results", { exact: true }).first().click();
  await expect(page.getByText("fake DNS resolver", { exact: false }).first()).toBeVisible();
  await expect(page.getByText("wrong value", { exact: true }).first()).toBeVisible();
  await expect(page.getByText("Found: v=spf1 -all", { exact: true })).toBeVisible();
  await page.unrouteAll();

  seedMisconfiguredDomain(claims.org!, stamp);
  await page.reload();
  await expect(
    page.getByText(`misconfigured-${stamp}.example.test`, { exact: true }).first(),
  ).toBeVisible();
  await expect(page.getByText("misconfigured", { exact: true }).first()).toBeVisible();
  await expect(page.getByText("mismatched", { exact: true }).first()).toBeVisible();
  await expect(page.getByText("missing", { exact: true }).first()).toBeVisible();

  seedConflictedDomain(claims.org!, stamp);
  await page.reload();
  await expect(
    page.getByText(`conflicted-${stamp}.example.test`, { exact: true }).first(),
  ).toBeVisible();
  await expect(
    page.getByText("Domain already owned by another workspace", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Ownership conflict", { exact: true })).toBeVisible();

  expect(pageErrors, pageErrors.join("\n")).toEqual([]);
  expect(consoleErrors, consoleErrors.join("\n")).toEqual([]);
});
