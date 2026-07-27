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
  await expect(page.getByText("Message Cursor")).toBeVisible();
  await expect(page.getByText("page 1")).toBeVisible();
  await expect(page.getByRole("button", { name: "Back" })).toBeDisabled();
  await gotoNav(page, "Audit", "Audit Trail");
  await expect(page.getByText("Audit Cursor")).toBeVisible();
  await expect(page.getByText("page 1")).toBeVisible();
  await expect(page.getByRole("button", { name: "Back" })).toBeDisabled();
  await gotoNav(page, "Checks");
  await gotoNav(page, "Diagnostics");
  await gotoNav(page, "Overview", "Operations Overview");
});

test("renders sanitized discovery runtime diagnostics", async ({ page }) => {
  await login(page);
  await page.route("**/internal/admin/diagnostics", async (route) => {
    const response = await route.fetch();
    const diagnostics = await response.json();
    diagnostics.upstream.http_route_states = [
      {
        name: "orders-dns",
        target_type: "http",
        status: "degraded",
        consecutive_failures: 1,
        last_reason: "transport",
        updated_at: "2026-07-28T08:30:00Z",
        discovery: {
          type: "dns",
          resolved_endpoints: 3,
          unhealthy_endpoints: 1,
          last_refresh_result: "success",
          last_refresh_duration: "2.1ms",
          last_refresh_at: "2026-07-28T08:29:58Z",
          last_selection_result: "selected",
          last_selection_at: "2026-07-28T08:30:00Z",
          cooldown_skipped_total: 4,
          last_cooldown_skipped_at: "2026-07-28T08:30:00Z",
          last_endpoint_failure_class: "transport",
          last_endpoint_failure_at: "2026-07-28T08:30:00Z",
          last_forward_result: "success",
          last_forward_attempts: 2,
          last_forward_at: "2026-07-28T08:30:00Z",
          last_failover_decision: "succeeded",
          last_failover_at: "2026-07-28T08:30:00Z",
        },
      },
    ];
    await route.fulfill({ response, json: diagnostics });
  });

  await gotoNav(page, "Diagnostics");
  const routeCard = page.locator("article").filter({ has: page.getByRole("heading", { name: "orders-dns" }) });
  await expect(routeCard.getByText("Endpoint Discovery")).toBeVisible();
  await expect(routeCard.getByText("dns runtime")).toBeVisible();
  await expect(routeCard.getByLabel("Resolved endpoints: 3")).toBeVisible();
  await expect(routeCard.getByLabel("Unhealthy endpoints: 1")).toBeVisible();
  await expect(routeCard.locator("dl").getByText("transport", { exact: true })).toBeVisible();
  await expect(routeCard.locator("dl").getByText("succeeded", { exact: true })).toBeVisible();
  await expect(routeCard).not.toContainText("10.0.0.1");
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

test("bulk requeue confirms selection and reports partial results", async ({ page }) => {
  test.skip(expectedRole === "readonly", "readonly role has a dedicated disabled-actions smoke test");

  await login(page);
  await page.route("**/internal/messages?**", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      json: {
        code: "ok",
        status: "failed",
        limit: 100,
        has_more: false,
        total: 2,
        messages: [
          { code: "ok", message_id: "failed-console-1", client_id: "client-1", device_id: "device-1", status: "failed" },
          { code: "ok", message_id: "failed-console-2", client_id: "client-2", device_id: "device-2", status: "failed" },
        ],
      },
      status: 200,
    });
  });
  await page.route("**/internal/messages/requeue", async (route) => {
    expect(route.request().postDataJSON()).toEqual({ message_ids: ["failed-console-1", "failed-console-2"] });
    await route.fulfill({
      contentType: "application/json",
      json: {
        code: "partial_failure",
        total: 2,
        success: 1,
        failed: 1,
        results: [
          { code: "ok", message_id: "failed-console-1", status: "pending" },
          {
            code: "queue_capacity_exceeded",
            message_id: "failed-console-2",
            status: "failed",
            reason: "downlink queue capacity exceeded",
          },
        ],
      },
      status: 207,
    });
  });

  await gotoNav(page, "Messages");
  await expect(page.getByLabel("Select message failed-console-1")).toBeVisible();
  await page.getByLabel("Select eligible failed messages").check();
  await expect(page.getByText("2 / 100")).toBeVisible();
  await page.getByRole("button", { name: "Requeue selected" }).click();

  const confirmation = page.getByRole("dialog", { name: "2 failed messages" });
  await expect(confirmation).toBeVisible();
  await confirmation.getByRole("button", { name: "Requeue selected" }).click();

  const result = page.getByRole("dialog", { name: "1 of 2 requeued" });
  await expect(result).toBeVisible();
  await expect(result.getByText("queue_capacity_exceeded")).toBeVisible();
  await result.getByRole("button", { name: "Done" }).click();
  await expect(result).toHaveCount(0);
});

test("readonly sessions keep mutation actions disabled", async ({ page }) => {
  test.skip(expectedRole !== "readonly", "operator/admin role has a dedicated mutation-dialog smoke test");

  await login(page);
  await gotoNav(page, "Messages");

  await expect(page.getByText(/read-only for one or more message operations/i)).toBeVisible();
  await expect(page.getByRole("button", { name: "Run Retry Scan" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Send Test Push" })).toBeDisabled();
  await expect(page.getByLabel("Select eligible failed messages")).toBeDisabled();
  await expect(page.getByRole("button", { name: "Requeue selected" })).toBeDisabled();
});
