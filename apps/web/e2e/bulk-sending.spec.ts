import { expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { resolve } from "node:path";

const EMAIL = process.env.DEPLOY_SMOKE_EMAIL ?? "smoke@sendwise.test";
const PASSWORD = process.env.DEPLOY_SMOKE_PASSWORD ?? "SmokeTest2026!";
const API_BASE =
  process.env.E2E_API_ORIGIN ??
  new URL(process.env.E2E_BASE_URL ?? "http://localhost:3000").origin.replace(/:3000$/, "");

function runLocalSQL(sql: string): void {
  const composeFiles = [
    resolve(process.cwd(), "../../docker-compose.yml"),
    resolve(process.cwd(), "../../docker-compose.microservices.yml"),
  ];
  execFileSync(
    "docker",
    [
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
    ],
    { stdio: "pipe" },
  );
}

test("campaign send plan enforces warm-up cap with a verified sender profile", async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const listName = `Bulk warm-up ${stamp}`;
  const senderEmail = `sender@bulk-${stamp}.example.test`;

  await page.goto("/login");
  await page.getByPlaceholder("you@example.com").fill(EMAIL);
  await page.getByPlaceholder("••••••••").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/dashboard");

  const { accessToken, organizationID } = await page.evaluate(() => {
    const token = localStorage.getItem("access_token");
    if (!token) throw new Error("The E2E account did not receive an access token");
    const payload = token.split(".")[1].replaceAll("-", "+").replaceAll("_", "/");
    const claims = JSON.parse(atob(payload.padEnd(Math.ceil(payload.length / 4) * 4, "="))) as {
      org?: string;
    };
    if (!claims.org) throw new Error("The E2E account token has no organization claim");
    return { accessToken: token, organizationID: claims.org };
  });
  const headers = { Authorization: `Bearer ${accessToken}` };
  const domain = `bulk-${stamp}.example.test`;
  const domainResponse = await request.post(`${API_BASE}/api/v1/domains`, {
    headers,
    data: { domain },
  });
  expect(domainResponse.status(), await domainResponse.text()).toBe(201);
  runLocalSQL(`
    UPDATE sending_domains
    SET status = 'verified', spf_status = 'verified', dkim_status = 'verified',
        verification_status = 'verified', stalwart_authenticated = true,
        last_checked_at = now(), verified_at = now()
    WHERE organization_id = '${organizationID}' AND domain = '${domain}'
  `);
  const senderResponse = await request.post(`${API_BASE}/api/v1/sender-identities`, {
    headers,
    data: {
      domain,
      local_part: "sender",
      display_name: "Sendwise E2E",
      is_default: true,
    },
  });
  expect(senderResponse.status(), await senderResponse.text()).toBe(201);
  const senderIdentity = (await senderResponse.json()) as { id: string };
  const profileResponse = await request.put(`${API_BASE}/api/v1/sender-profile`, {
    headers,
    data: {
      company_name: "Sendwise E2E",
      street_address: "1 Test Street",
      city: "Test City",
      region: "Test Region",
      postal_code: "10001",
      country: "Test Country",
      website: "https://example.test",
      support_email: `support@${domain}`,
    },
  });
  expect(profileResponse.status(), await profileResponse.text()).toBe(200);

  const setup = await page.evaluate(async ({ apiBase, listName, stamp, senderEmail, senderIdentityID }) => {
    const token = localStorage.getItem("access_token");
    const headers = {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
    };
    const listResponse = await fetch(`${apiBase}/api/v1/lists`, {
      method: "POST",
      headers,
      body: JSON.stringify({ name: listName, description: "Warm-up proof audience" }),
    });
    const list = await listResponse.json();
    if (!listResponse.ok) throw new Error(`Create list failed (${listResponse.status}): ${JSON.stringify(list)}`);

    const contacts = await Promise.all(
      Array.from({ length: 51 }, async (_, index) => {
        const contactResponse = await fetch(`${apiBase}/api/v1/contacts`, {
          method: "POST",
          headers,
          body: JSON.stringify({
            email: `bulk-${stamp}-${index}@example.test`,
            status: "subscribed",
            source: "bulk-warmup-e2e",
            consent: {
              source: "bulk-warmup-e2e",
              purpose: "marketing",
              evidence: { form: "bulk-warmup-e2e", confirmed: true },
            },
          }),
        });
        const contact = await contactResponse.json();
        if (!contactResponse.ok) {
          throw new Error(`Create contact failed (${contactResponse.status}): ${JSON.stringify(contact)}`);
        }
        return contact.id as string;
      }),
    );
    const memberResponse = await fetch(`${apiBase}/api/v1/lists/${list.id}/contacts`, {
      method: "POST",
      headers,
      body: JSON.stringify({ contact_ids: contacts }),
    });
    if (!memberResponse.ok) throw new Error(`Add list members failed (${memberResponse.status}): ${await memberResponse.text()}`);

    const campaignResponse = await fetch(`${apiBase}/api/v1/campaigns`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        name: `Warm-up campaign ${stamp}`,
        subject: "Warm-up send plan",
        sender_name: "Sendwise E2E",
        sender_email: senderEmail,
        sender_identity_id: senderIdentityID,
        sender_postal_address: "1 Test Street, Test City",
        list_id: list.id,
        content: { version: 1, blocks: [{ type: "paragraph", content: "Warm-up proof" }] },
      }),
    });
    const campaign = await campaignResponse.json();
    if (!campaignResponse.ok) {
      throw new Error(`Create campaign failed (${campaignResponse.status}): ${JSON.stringify(campaign)}`);
    }
    return { campaignId: campaign.id as string };
  }, { apiBase: API_BASE, listName, stamp, senderEmail, senderIdentityID: senderIdentity.id });

  await page.goto(`/campaigns/${setup.campaignId}/preview`);
  await expect(page.getByText("All required checks passed.")).toBeVisible();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Send now" }).click();
  await expect(page.getByText("50 will go out today; 1 are queued for later warm-up days.")).toBeVisible();
});
