import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";

const publicPages = [
  "/",
  "/platform",
  "/developers",
  "/pricing",
  "/about",
  "/contact",
  "/login",
  "/register",
  "/forgot-password",
  "/auth/verify",
  "/auth/reset-password",
];

test("public and auth pages render without the removed footer credit and have no accessibility violations", async ({ page }, testInfo) => {
  const browserErrors: string[] = [];
  page.on("pageerror", (error) => browserErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") browserErrors.push(message.text());
  });

  for (const path of publicPages) {
    const slug = path === "/" ? "home" : path.slice(1).replaceAll("/", "-");
    for (const width of [390, 1440]) {
      await page.setViewportSize({ width, height: 900 });
      await page.goto(path);
      await expect(page.locator("main h1")).toHaveCount(1);
      await expect(page.locator(".marketing-footer")).toBeVisible();
      await expect(page.getByText("Developed by")).toHaveCount(0);

      if (width === 390) {
        const documentWidth = await page.evaluate(() => document.documentElement.scrollWidth);
        expect(documentWidth, `${path} horizontal overflow at 390px`).toBeLessThanOrEqual(width);
      }

      if (width === 1440) {
        const accessibility = await new AxeBuilder({ page })
          .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
          .analyze();
        expect(accessibility.violations, `${path} accessibility violations`).toEqual([]);
      }

      await page.screenshot({
        path: testInfo.outputPath(`${slug}-${width}.png`),
        fullPage: true,
      });
    }
  }
  expect(browserErrors).toEqual([]);
});

test("marketing typography, full-width layout, and auth imagery match the updated design", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");
  const styles = await page.locator("main").evaluate((main) => {
    const heading = main.querySelector("h1");
    const paragraph = main.querySelector("p");
    const wrap = main.querySelector(".wrap");
    if (!heading || !paragraph || !wrap) throw new Error("The home hero is missing required content");
    return {
      headingSize: getComputedStyle(heading).fontSize,
      paragraphSize: getComputedStyle(paragraph).fontSize,
      containerMaxWidth: getComputedStyle(wrap).maxWidth,
    };
  });
  expect(styles).toEqual({
    headingSize: "58px",
    paragraphSize: "13px",
    containerMaxWidth: "none",
  });

  await page.goto("/login");
  const loginImage = page.getByRole("img", { name: "Sendwise email marketing dashboard illustration" });
  await expect(loginImage).toBeVisible();
  await expect(loginImage).toHaveAttribute("src", /sendwise-dashboard/);

  await page.goto("/register");
  const registerImage = page.getByRole("img", { name: "Sendwise email marketing dashboard illustration" });
  await expect(registerImage).toBeVisible();
  await expect(registerImage).toHaveAttribute("src", /sendwise-dashboard/);
});

test("obsolete auth slogans are absent from sign-in and create-account pages", async ({ page }) => {
  const removedCopy = [
    "Your campaigns and delivery logs are waiting",
    "Create your workspace and send your first email",
  ];

  for (const [path, heading] of [["/login", "Sign in"], ["/register", "Create account"]]) {
    await page.goto(path);
    await expect(page.locator("main h1")).toHaveText(heading);
    for (const text of removedCopy) {
      await expect(page.getByText(text, { exact: false })).toHaveCount(0);
    }
  }
});

test("footer has clear spacing after the page content on desktop and mobile", async ({ page }) => {
  for (const path of ["/login", "/register", "/pricing"]) {
    for (const width of [390, 1440]) {
      await page.setViewportSize({ width, height: 900 });
      await page.goto(path);
      const layout = await page.evaluate(() => {
        const main = document.querySelector("main");
        const footer = document.querySelector("footer");
        const content = main?.lastElementChild;
        const footerContent = footer?.firstElementChild;
        const marketing = document.querySelector(".marketing");
        if (!main || !footer || !content || !footerContent || !marketing) throw new Error("Page content or footer is missing");
        return {
          gap: footer.getBoundingClientRect().top - content.getBoundingClientRect().bottom,
          layout: getComputedStyle(marketing).display,
          footerTopPadding: Number.parseFloat(getComputedStyle(footerContent).paddingTop),
        };
      });
      expect(layout.layout).toBe("flex");
      expect(layout.gap, `${path} footer gap at ${width}px`).toBeGreaterThanOrEqual(40);
      expect(layout.footerTopPadding, `${path} footer top padding at ${width}px`).toBeGreaterThanOrEqual(40);
    }
  }
});

test("short public pages keep the footer at the bottom of the viewport", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1400 });
  await page.goto("/auth/verify");
  const layout = await page.evaluate(() => {
    const marketing = document.querySelector(".marketing");
    const footer = document.querySelector(".marketing-footer");
    if (!marketing || !footer) throw new Error("The public frame or footer is missing");
    return {
      direction: getComputedStyle(marketing).flexDirection,
      footerBottom: footer.getBoundingClientRect().bottom,
      viewportHeight: window.innerHeight,
      documentHeight: document.documentElement.scrollHeight,
    };
  });
  expect(layout.direction).toBe("column");
  expect(layout.documentHeight).toBeLessThanOrEqual(layout.viewportHeight + 1);
  expect(layout.footerBottom).toBeGreaterThanOrEqual(layout.viewportHeight - 1);
});

test("light and dark themes toggle and persist, with white default page surfaces", async ({ page }) => {
  await page.goto("/pricing");
  const marketing = page.locator(".marketing");
  await expect(marketing).toHaveAttribute("data-theme", "light");
  const lightSurfaces = await page.locator(".marketing-header, .marketing-page-hero, .marketing-footer").evaluateAll((elements) =>
    elements.map((element) => getComputedStyle(element).backgroundColor),
  );
  expect(lightSurfaces).toEqual(["rgb(255, 255, 255)", "rgb(255, 255, 255)", "rgb(255, 255, 255)"]);

  const toggle = page.getByRole("button", { name: "Switch to dark mode" });
  await toggle.click();
  await expect(marketing).toHaveAttribute("data-theme", "dark");
  await page.reload();
  await expect(marketing).toHaveAttribute("data-theme", "dark");
  await page.getByRole("button", { name: "Switch to light mode" }).click();
  await expect(marketing).toHaveAttribute("data-theme", "light");
});

test("desktop theme toggle sits beside sign-in and remains available on mobile", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");
  const desktopToggle = page.getByRole("button", { name: "Switch to dark mode" });
  const signIn = page.locator(".marketing-header-actions").getByRole("link", { name: "Sign in" });
  const toggleBox = await desktopToggle.boundingBox();
  const signInBox = await signIn.boundingBox();
  expect(toggleBox).not.toBeNull();
  expect(signInBox).not.toBeNull();
  expect(toggleBox!.x + toggleBox!.width).toBeLessThanOrEqual(signInBox!.x);
  expect(signInBox!.x - toggleBox!.x - toggleBox!.width).toBeLessThan(20);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  const mobileToggle = page.getByRole("button", { name: "Switch to dark mode" });
  await expect(mobileToggle).toBeVisible();
  await mobileToggle.click();
  await expect(page.locator(".marketing")).toHaveAttribute("data-theme", "dark");
});

test("mobile navigation opens, receives focus, and closes on Escape", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  const menuButton = page.locator(".marketing-menu-button");
  await menuButton.click();
  await expect(menuButton).toHaveAttribute("aria-expanded", "true");
  await expect(page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Platform" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Platform" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(menuButton).toHaveAttribute("aria-expanded", "false");
  await expect(menuButton).toBeFocused();
});

test("code tabs copy the selected example and FAQ accordions toggle", async ({ page }) => {
  await page.goto("/");
  const codeTab = page.getByRole("tab", { name: "Node" });
  await codeTab.click();
  await expect(codeTab).toHaveAttribute("aria-selected", "true");
  await codeTab.focus();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("tab", { name: "Python" })).toHaveAttribute("aria-selected", "true");
  await page.evaluate(() => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: async () => undefined },
    });
  });
  await page.getByRole("button", { name: "Copy" }).first().click();
  await expect(page.getByRole("button", { name: "Copied" }).first()).toBeVisible();

  const question = page.getByRole("button", { name: "What happens to bounces and complaints?" });
  await question.click();
  await expect(question).toHaveAttribute("aria-expanded", "true");
  await question.click();
  await expect(question).toHaveAttribute("aria-expanded", "false");
});

test("pricing shows the published monthly plans without an unpriced annual option", async ({ page }) => {
  await page.goto("/pricing");
  const plans = page.locator(".pricing-plans .plan");
  await expect(plans).toHaveCount(4);
  const expectedPlans = [
    ["Free", "₦0", "3,000 emails per month", "100 emails per day"],
    ["Starter", "₦5,000", "10,000 emails per month"],
    ["Business", "₦15,000", "50,000 emails per month"],
    ["Enterprise", "₦50,000", "200,000 emails per month"],
  ];
  for (const [index, expected] of expectedPlans.entries()) {
    const plan = plans.nth(index);
    for (const text of expected) await expect(plan).toContainText(text);
  }
  await expect(page.getByRole("heading", { name: "Questions about plan setup?" })).toBeVisible();
  await expect(page.getByRole("group", { name: "Billing period" })).toHaveCount(0);
  await expect(page.getByText("billed yearly", { exact: false })).toHaveCount(0);
  const question = page.getByRole("button", { name: "What is included in the Free plan?" });
  await expect(question).toHaveAttribute("aria-expanded", "true");
  await question.click();
  await expect(question).toHaveAttribute("aria-expanded", "false");
  await question.click();
  await expect(question).toHaveAttribute("aria-expanded", "true");
});

test("unapproved legal placeholder pages are not published", async ({ page }) => {
  for (const path of ["/legal", "/legal/privacy", "/legal/terms", "/legal/acceptable-use", "/legal/data-protection"]) {
    const response = await page.goto(path);
    expect(response?.status(), path).toBe(404);
  }
});

test("About, Contact, and Developer pages contain no template copy or unsupported API paths", async ({ page }) => {
  await page.goto("/about");
  await expect(page.getByText(/\[(YOUR STORY|YOUR MISSION|CITY, COUNTRY|NAME|ROLE)\]/)).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "The people behind Sendwise." })).toHaveCount(0);

  await page.goto("/contact");
  await expect(page.getByText(/\[(EMAIL ADDRESS|SUPPORT EMAIL|RESPONSE TIME|ABUSE EMAIL)\]/)).toHaveCount(0);
  await expect(page.getByLabel("What is this about?")).toContainText("Report abuse or safety concern");

  await page.goto("/developers");
  await expect(page.getByText("/api/v1/transactional/send", { exact: false })).toHaveCount(0);
  await expect(page.getByText("/api/v1/transactional/messages/batch", { exact: false })).toHaveCount(0);
  await expect(page.getByText("/api/v1/transactional/suppressions", { exact: false })).toHaveCount(0);
  await expect(page.getByText("POST /api/v1/transactional/messages", { exact: false })).toBeVisible();
});

test("contact form shows success and error states", async ({ page }) => {
  await page.goto("/contact");
  await page.route("**/api/v1/public/contact", (route) =>
    route.fulfill({ status: 202, contentType: "application/json", body: JSON.stringify({ message: "ok" }) }),
  );
  await page.getByLabel("Full name").fill("Ada Lovelace");
  await page.getByLabel("Work email").fill("ada@example.com");
  await page.getByLabel("Message").fill("Please tell me about your platform and available plans.");
  await page.getByRole("button", { name: "Send message" }).click();
  await expect(page.getByRole("heading", { name: "Message sent" })).toBeVisible();

  await page.getByRole("button", { name: "Send another message" }).click();
  await page.unrouteAll();
  await page.route("**/api/v1/public/contact", (route) =>
    route.fulfill({
      status: 429,
      contentType: "application/json",
      body: JSON.stringify({ error: { message: "Too many requests." } }),
    }),
  );
  await page.getByLabel("Full name").fill("Ada Lovelace");
  await page.getByLabel("Work email").fill("ada@example.com");
  await page.getByLabel("Message").fill("Please tell me about your platform and available plans.");
  await page.getByRole("button", { name: "Send message" }).click();
  await expect(page.locator(".form-error[role='alert']")).toContainText("Too many requests.");
});
