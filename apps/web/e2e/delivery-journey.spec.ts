import { expect, test } from "@playwright/test";

test("recipient drawer shows delivery journey without engagement activity", async ({ page }) => {
  const campaignID = "9b2df23a-6c47-4e56-8648-927b1645d408";
  const recipientID = "6a2a3a6e-7359-49e1-9c53-eef9f182a2fc";
  await page.addInitScript(() => {
    const token = "delivery-journey-ui-test-token";
    localStorage.setItem("access_token", token);
    localStorage.setItem("auth-storage", JSON.stringify({
      state: {
        token,
        user: { id: "owner-user", name: "Workspace Owner", email: "owner@example.test" },
        organization: { id: "workspace", name: "Test Workspace", slug: "test", role: "owner" },
        isLoading: false,
      },
      version: 0,
    }));
  });
  await page.route(`**/api/v1/analytics/campaigns/${campaignID}`, (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        campaign: {
          id: campaignID,
          name: "Delivery journey proof",
          subject: "Journey",
          sender_name: "Sendwise",
          sender_email: "sender@example.test",
          content: {},
          status: "completed",
          recipient_count: 1,
          track_opens: true,
          track_clicks: true,
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        },
        recipients: { total: 1, by_status: { submitted: 1 } },
        events: { by_type: {} },
        journey: { funnel: { submitted_to_mail_server: 1 }, stuck: 0 },
        rates: { open_rate: 0, click_rate: 0, bounce_rate: 0, delivered_rate: 0 },
        top_links: [],
      }),
    }),
  );
  await page.route(`**/api/v1/campaigns/${campaignID}/recipients?*`, (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: [{
          recipient_id: recipientID,
          name: "Test Recipient",
          email: "recipient@example.test",
          delivery_status: "submitted",
          accepted_at: "2026-01-01T00:00:00Z",
          delivered_at: null,
          open_count: 0,
          first_opened_at: null,
          last_opened_at: null,
          click_count: 0,
          last_clicked_at: null,
          last_activity_at: null,
          engagement_classification: "unknown",
          journey_status: "submitted_to_mail_server",
        }],
        pagination: { total: 1, limit: 25, offset: 0 },
      }),
    }),
  );
  await page.route(`**/api/v1/campaigns/${campaignID}/recipients/${recipientID}/activity`, (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ data: [] }) }),
  );
  await page.route(`**/api/v1/campaigns/${campaignID}/recipients/${recipientID}/journey`, (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        status: "submitted_to_mail_server",
        events: [{ stage: "submitted_to_mail_server", timestamp: "2026-01-01T00:00:00Z" }],
      }),
    }),
  );

  await page.goto(`/campaigns/${campaignID}/report`);
  await page.getByRole("button", { name: "View journey" }).click();
  await expect(page.getByText("No activity has been recorded for this recipient yet.")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Delivery journey" })).toBeVisible();
  await expect(page.getByRole("dialog").getByText("Submitted to the mail server")).toBeVisible();
  await expect(page.getByText(/not that it reached the inbox/)).toBeVisible();
});
