import { expect, test, type Page } from "@playwright/test";

const consoleURL = process.env.ZCOURIER_CONSOLE_BASE_URL ?? "http://127.0.0.1:18084/console/";
const internalToken = process.env.ZCOURIER_CONSOLE_INTERNAL_TOKEN ?? "dev-internal-token";
const expectedRole = process.env.ZCOURIER_CONSOLE_EXPECTED_ROLE ?? "admin";

async function openConsole(page: Page) {
  await page.goto(consoleURL);
  await expect(page.getByText("Z-Courier").first()).toBeVisible();
}

async function login(page: Page) {
  await openConsole(page);
  await page.getByLabel("Internal Token").fill(internalToken);
  await page.getByRole("button", { name: "Sign In" }).click();
  await expect(page.getByRole("button", { name: "Sign Out" })).toBeVisible();
  await expect(page.getByText(expectedRole).first()).toBeVisible();
}

async function gotoNav(page: Page, name: string, heading: string | RegExp = name) {
  await page.getByRole("button", { name }).click();
  const pageHeading =
    typeof heading === "string"
      ? page.getByRole("heading", { name: heading, exact: true })
      : page.getByRole("heading", { name: heading });
  await expect(pageHeading).toBeVisible();
}

test("shows the login shell before authentication", async ({ page }) => {
  await openConsole(page);

  await expect(page.getByLabel("Internal Token")).toBeVisible();
  await expect(page.getByRole("button", { name: "Sign In" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Operations Overview" })).toBeVisible();
});

test("logs in and navigates core console pages", async ({ page }) => {
  await login(page);

  await expect(page.getByRole("heading", { name: "Operations Overview" })).toBeVisible();
  await gotoNav(page, "Routes");
  await gotoNav(page, "Sessions");
  await gotoNav(page, "Messages");
  await gotoNav(page, "Audit", "Audit Trail");
  await gotoNav(page, "Checks");
  await gotoNav(page, "Diagnostics");
  await gotoNav(page, "Overview", "Operations Overview");
});

test("operator mutation actions are guarded by confirmation dialogs", async ({ page }) => {
  test.skip(expectedRole === "readonly", "readonly role has a dedicated disabled-actions smoke test");

  await login(page);
  await gotoNav(page, "Messages");

  await page.getByRole("button", { name: "Run Retry Scan" }).click();
  const retryDialog = page.getByRole("dialog", { name: "Run Retry Scan" });
  await expect(retryDialog).toBeVisible();
  await page.getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByRole("dialog", { name: "Run Retry Scan" })).toHaveCount(0);
  await page.getByRole("button", { name: "Run Retry Scan" }).click();
  await page.getByRole("dialog", { name: "Run Retry Scan" }).getByRole("button", { name: "Run Retry Scan" }).click();
  await expect(page.getByText("Scan completed")).toBeVisible();

  const pushPanel = page.locator("article").filter({ has: page.getByRole("heading", { name: "Push Playground" }) });
  await pushPanel.getByLabel("ClientID").fill("console-smoke-client");
  await pushPanel.getByLabel("DeviceID").fill("console-smoke-device");
  await pushPanel.getByLabel("MsgID").fill("2001");
  await pushPanel.getByLabel("Body").fill("console smoke body");
  await pushPanel.getByRole("button", { name: "Send Test Push" }).click();

  const testPushDialog = page.getByRole("dialog", { name: /console-smoke-client \/ console-smoke-device/ });
  await expect(testPushDialog).toBeVisible();
  await expect(page.getByText("Body Preview")).toBeVisible();
  await testPushDialog.getByRole("button", { name: "Send Test Push" }).click();
  await expect(page.getByText("Push Result")).toBeVisible();
  await expect(page.getByText(/queued|sent|delivered|accepted/i).first()).toBeVisible();
});

test("readonly sessions keep mutation actions disabled", async ({ page }) => {
  test.skip(expectedRole !== "readonly", "operator/admin role has a dedicated mutation-dialog smoke test");

  await login(page);
  await gotoNav(page, "Messages");

  await expect(page.getByText(/read-only for one or more message operations/i)).toBeVisible();
  await expect(page.getByRole("button", { name: "Run Retry Scan" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Send Test Push" })).toBeDisabled();
});
