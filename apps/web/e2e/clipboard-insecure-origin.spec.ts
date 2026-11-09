import { expect, test } from "@playwright/test";

const insecureOrigin = process.env.E2E_INSECURE_ORIGIN;

test("DNS copy fallback works from a real insecure HTTP origin", async ({ page }) => {
  test.skip(!insecureOrigin, "set E2E_INSECURE_ORIGIN to a non-loopback HTTP origin");

  const domainID = "00000000-0000-4000-8000-000000000001";
  const domain = "clipboard.example.test";
  const recordName = `mail._domainkey.${domain}`;
  const recordValue = `v=DKIM1; k=rsa; p=${"A".repeat(344)}`;

  await page.addInitScript(() => {
    localStorage.setItem("access_token", "clipboard-insecure-origin-test-token");
    const windowWithCopyCapture = window as Window & { copiedFallbackText?: string };
    const nativeExecCommand = document.execCommand.bind(document);
    document.execCommand = (command, ...args) => {
      if (command === "copy" && document.activeElement instanceof HTMLTextAreaElement) {
        windowWithCopyCapture.copiedFallbackText = document.activeElement.value;
      }
      return nativeExecCommand(command, ...args);
    };
  });
  await page.route("**/api/v1/domains", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: [{
          id: domainID,
          domain,
          status: "pending",
          owner_conflict: false,
          created_at: new Date().toISOString(),
        }],
      }),
    }),
  );
  await page.route(`**/api/v1/domains/${domainID}/dns-records`, (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        dns_records: [{
          type: "TXT",
          name: recordName,
          value: recordValue,
          ttl: "Auto",
          purpose: "DKIM",
          status: "missing",
        }],
      }),
    }),
  );

  await page.goto(`${insecureOrigin}/domains`);
  expect(await page.evaluate(() => window.isSecureContext)).toBe(false);
  expect(await page.evaluate(() => navigator.clipboard)).toBeUndefined();
  await expect(page.getByRole("heading", { name: "Sending domains" })).toBeVisible();
  await page.getByRole("button", { name: `Copy DNS value ${recordName}` }).click();
  await expect(page.getByText("Copied", { exact: true })).toBeVisible();
  expect(
    await page.evaluate(
      () => (window as Window & { copiedFallbackText?: string }).copiedFallbackText,
    ),
  ).toBe(recordValue);
});
