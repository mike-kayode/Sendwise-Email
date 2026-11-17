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

test("campaign managers can cancel active campaigns and delete past campaigns", async ({ page }) => {
  const draftID = "9b2df23a-6c47-4e56-8648-927b1645d408";
  const activeID = "2f5fc9d3-50ee-42fa-8a5d-bd1f6b005439";
  const completedID = "655ed351-8f4c-4a46-8df9-4bf8ffcb9fb4";
  let campaigns = [
    campaign(draftID, "Test campaign", "draft"),
    campaign(activeID, "Active campaign", "sending"),
    campaign(completedID, "Completed campaign", "completed"),
  ];
  let deleteRequests = 0;
  let cancelRequests = 0;

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
    const url = new URL(route.request().url());
    if (route.request().method() === "POST" && url.pathname.endsWith("/cancel")) {
      cancelRequests++;
      campaigns = campaigns.map((item) => item.id === activeID ? { ...item, status: "cancelled" } : item);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ status: "cancelled" }),
      });
      return;
    }
    if (route.request().method() === "DELETE") {
      deleteRequests++;
      const campaignID = url.pathname.split("/").at(-1);
      campaigns = campaigns.filter((item) => item.id !== campaignID);
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
  page.on("dialog", (dialog) => void dialog.accept());

  await page.goto("/campaigns");
  await expect(page.getByRole("button", { name: "Delete campaign Test campaign" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Cancel campaign Active campaign" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Delete campaign Active campaign" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Cancel campaign Completed campaign" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Delete campaign Completed campaign" })).toBeVisible();
  await page.getByRole("button", { name: "Cancel campaign Active campaign" }).click();
  await expect(page.getByRole("button", { name: "Delete campaign Active campaign" })).toBeVisible();
  await expect(page.getByText("cancelled", { exact: true })).toBeVisible();
  expect(cancelRequests).toBe(1);

  await page.getByRole("button", { name: "Delete campaign Completed campaign" }).click();
  await expect(page.getByRole("link", { name: "Completed campaign" })).toHaveCount(0);
  await page.getByRole("button", { name: "Delete campaign Active campaign" }).click();
  await expect(page.getByRole("link", { name: "Active campaign" })).toHaveCount(0);
  expect(deleteRequests).toBe(2);
});
