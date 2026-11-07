import { expect, test } from "@playwright/test";

const EMAIL = process.env.DEPLOY_SMOKE_EMAIL ?? "smoke@sendwise.test";
const PASSWORD = process.env.DEPLOY_SMOKE_PASSWORD ?? "SmokeTest2026!";
const API_BASE =
  process.env.E2E_API_ORIGIN ??
  new URL(process.env.E2E_BASE_URL ?? "http://localhost:3000").origin.replace(/:3000$/, "");

test("campaign send plan enforces warm-up cap and requires postal address", async ({ page }) => {
  const stamp = Date.now();
  const listName = `Bulk warm-up ${stamp}`;
  const senderEmail = `sender@bulk-${stamp}.example.test`;

  await page.goto("/login");
  await page.getByPlaceholder("you@example.com").fill(EMAIL);
  await page.getByPlaceholder("••••••••").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/dashboard");

  const setup = await page.evaluate(async ({ apiBase, listName, stamp, senderEmail }) => {
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
  }, { apiBase: API_BASE, listName, stamp, senderEmail });

  await page.goto(`/campaigns/${setup.campaignId}/preview`);
  await expect(page.getByText("All required checks passed.")).toBeVisible();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Send now" }).click();
  await expect(page.getByText("50 will go out today; 1 are queued for later warm-up days.")).toBeVisible();
});
