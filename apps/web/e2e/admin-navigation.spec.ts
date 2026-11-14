import { expect, test, type Page } from "@playwright/test";

async function openAdminConsole(page: Page, platformRole: "support_admin" | "super_admin") {
  await page.addInitScript(() => {
    const token = "admin-navigation-test-token";
    localStorage.setItem("access_token", token);
    localStorage.setItem("auth-storage", JSON.stringify({
      state: {
        token,
        user: { id: "admin-user", name: "Platform Admin", email: "admin@example.test" },
        organization: { id: "workspace", name: "Test Workspace", slug: "test", role: "owner" },
        isLoading: false,
      },
      version: 0,
    }));
  });
  await page.route("**/api/v1/admin/session", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ platform_role: platformRole, mfa_verified: false }),
    }),
  );
  await page.goto("/admin");
}

test("platform admins see system navigation instead of workspace navigation", async ({ page }) => {
  await openAdminConsole(page, "super_admin");

  const sidebar = page.locator("aside").first();
  await expect(sidebar.getByText("System management")).toBeVisible();
  await expect(sidebar.getByText("super admin")).toBeVisible();
  for (const label of ["Overview", "Search", "Users", "Workspaces", "Domains", "Quota defaults", "Campaigns", "Suppressions", "Audit log"]) {
    await expect(sidebar.getByRole("link", { name: label, exact: true })).toBeVisible();
  }
  for (const label of ["Marketing workspace", "Dashboard", "Audience", "Automations", "Templates", "Forms", "Reports", "Settings"]) {
    await expect(sidebar.getByText(label, { exact: true })).toHaveCount(0);
  }
  await expect(page.getByRole("link", { name: "Search users, workspaces, domains..." })).toBeVisible();
  await expect(page.getByRole("link", { name: /New campaign/ })).toHaveCount(0);
});

test("super admin can raise a domain quota and sees the refreshed effective limit", async ({ page }) => {
  const domainID = "9b2df23a-6c47-4e56-8648-927b1645d408";
  let dailyLimit = 100;
  await page.addInitScript(() => {
    const token = "admin-quota-test-token";
    localStorage.setItem("access_token", token);
    localStorage.setItem("auth-storage", JSON.stringify({
      state: {
        token,
        user: { id: "admin-user", name: "Platform Admin", email: "admin@example.test" },
        organization: { id: "workspace", name: "Test Workspace", slug: "test", role: "owner" },
        isLoading: false,
      },
      version: 0,
    }));
  });
  await page.route("**/api/v1/admin/session", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ platform_role: "super_admin", mfa_verified: true }),
    }),
  );
  await page.route(`**/api/v1/admin/domains/${domainID}`, (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: {
        id: domainID,
        workspace_id: "workspace",
        workspace_name: "Test Workspace",
        workspace_slug: "test",
        domain: "mailer.example.test",
        status: "verified",
        spf_status: "verified",
        dkim_status: "verified",
        dmarc_status: "verified",
        verification_status: "verified",
        owner_conflict: false,
        stalwart_authenticated: true,
        transactional_enabled: true,
        created_at: "2026-01-01T00:00:00Z",
        dns_check_details: { checked_at: "2026-01-01T00:00:00Z", records: {} },
      } }),
    }),
  );
  await page.route(`**/api/v1/admin/domains/${domainID}/quotas`, async (route) => {
    if (route.request().method() === "PUT") {
      const request = route.request().postDataJSON() as {
        streams: Array<{ stream: string; daily_limit: number | null }>;
        reason: string;
      };
      expect(request.reason).toBe("Increase approved for verified sender");
      dailyLimit = request.streams.find((stream) => stream.stream === "marketing")?.daily_limit ?? dailyLimit;
    }
    const streamSnapshot = (stream: "marketing" | "transactional") => ({
      stream,
      daily_limit: dailyLimit,
      monthly_limit: 1000,
      per_second_limit: 10,
      daily_override: dailyLimit,
      monthly_override: 1000,
      per_second_override: 10,
      unlimited: false,
      daily_used: 80,
      monthly_used: 200,
      per_second_used: 1,
      daily_reset_at: "2026-12-01T00:00:00Z",
      monthly_reset_at: "2027-01-01T00:00:00Z",
      per_second_reset_at: "2026-11-14T23:26:00Z",
      daily_usage_percent: 100 * 80 / dailyLimit,
      monthly_usage_percent: 20,
    });
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: {
        domain_id: domainID,
        streams: {
          marketing: streamSnapshot("marketing"),
          transactional: streamSnapshot("transactional"),
        },
        risk: { young_domain: false, verified_days: 100, high_bounce_rate: false, bounce_rate_7_days: 0 },
        requests: [],
      }, platform_role: "super_admin" }),
    });
  });
  await page.route("**/api/v1/admin/mfa/verify", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ verified: true }) }),
  );
  page.on("dialog", (dialog) => dialog.accept());

  await page.goto(`/admin/domains/${domainID}`);
  const quotaPanel = page.getByRole("region", { name: "Domain quotas" });
  await expect(quotaPanel.getByText("80 / 100").first()).toBeVisible();
  await quotaPanel.getByLabel("Daily override").first().fill("200");
  await quotaPanel.getByLabel("Required reason").fill("Increase approved for verified sender");
  await quotaPanel.getByLabel("Authenticator or recovery code").fill("123456");
  await quotaPanel.getByRole("button", { name: "Review and save quotas" }).click();
  await expect(quotaPanel.getByText("80 / 200").first()).toBeVisible();
});

test("support admins get the admin menu without super-admin-only links", async ({ page }) => {
  await openAdminConsole(page, "support_admin");

  const sidebar = page.locator("aside").first();
  await expect(sidebar.getByText("support admin")).toBeVisible();
  await expect(sidebar.getByRole("link", { name: "Workspaces", exact: true })).toBeVisible();
  await expect(sidebar.getByRole("link", { name: "Suppressions", exact: true })).toHaveCount(0);
  await expect(sidebar.getByText("Marketing workspace", { exact: true })).toHaveCount(0);
});
