import { expect, test } from "@playwright/test";
import type { Campaign } from "../lib/auth";

function campaign(id: string, name: string, status: string): Campaign {
  return {
    id,
    name,
    subject: `${name} subject`,
    sender_name: "Sendwise",
    sender_email: "hello@example.test",
    content: {},
    status,
    recipient_count: 0,
    track_opens: true,
    track_clicks: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

test("campaign managers can delete drafts but not active or completed campaigns", async ({ page }) => {
  const draftID = "9b2df23a-6c47-4e56-8648-927b1645d408";
  let campaigns = [
    campaign(draftID, "Test campaign", "draft"),
    campaign("2f5fc9d3-50ee-42fa-8a5d-bd1f6b005439", "Active campaign", "sending"),
    campaign("655ed351-8f4c-4a46-8df9-4bf8ffcb9fb4", "Completed campaign", "completed"),
  ];
  let deleteRequests = 0;

  await page.addInitScript(() => {
    const token = "campaign-delete-test-token";
    localStorage.setItem("access_token", token);
    localStorage.setItem("auth-storage", JSON.stringify({
      state: {
        token,
        user: { id: "campaign-manager", name: "Campaign Manager", email: "manager@example.test" },
        organization: { id: "workspace", name: "Test Workspace", slug: "test", role: "manager" },
        isLoading: false,
      },
      version: 0,
    }));
  });
  await page.route("**/api/v1/campaigns**", async (route) => {
    if (route.request().method() === "DELETE") {
      deleteRequests++;
      campaigns = campaigns.filter((item) => item.id !== draftID);
      await route.fulfill({ status: 204 });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: campaigns, pagination: { total: campaigns.length, limit: 20, offset: 0 } }),
    });
  });
  await page.route("**/api/v1/analytics/overview", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        contacts: 0,
        subscribed: 0,
        campaigns_sent: 0,
        emails_submitted: 0,
        opens: 0,
        clicks: 0,
        bounces: 0,
        complaints: 0,
        unsubscribes: 0,
        open_rate: 0,
        click_rate: 0,
        bounce_rate: 0,
      }),
    }),
  );
  page.once("dialog", (dialog) => void dialog.accept());

  await page.goto("/campaigns");
  await expect(page.getByRole("button", { name: "Delete campaign Test campaign" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Delete campaign Active campaign" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Delete campaign Completed campaign" })).toHaveCount(0);
  await page.getByRole("button", { name: "Delete campaign Test campaign" }).click();
  await expect(page.getByRole("link", { name: "Test campaign" })).toHaveCount(0);
  expect(deleteRequests).toBe(1);
});
